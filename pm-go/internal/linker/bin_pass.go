package linker

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/manifest"
)

// HoistedPlacements retains every concrete site for a graph key, in placement
// order. The first site is the canonical location used by manifest consumers.
type HoistedPlacements map[string][]string

type BinPlan struct {
	ProjectDir, ModulesDirName, VirtualStoreDir string
	Graph                                       *lockfile.Graph
	MaxFilenameLength                           int
	Placements                                  HoistedPlacements // nil means isolated
	WorkspaceDirs                               map[string]string
	Manifests                                   map[string]*manifest.Package // "." is root
	HasWorkspace, Hoisted, VirtualStoreOnly     bool
	IgnoreScripts, HasAllowRule, FloorMayAllow  bool
	Options                                     BinOptions
}

// DependencyBuildsMayRun is shared by bin setup and lifecycle orchestration.
// The default-trust floor can authorize builds without an explicit allow rule.
func DependencyBuildsMayRun(ignoreScripts, hasAllowRule, floorMayAllow bool) bool {
	return !ignoreScripts && (hasAllowRule || floorMayAllow)
}

// LinkAllBins runs shared-directory passes in precedence order: hoisted
// placements, direct dependencies, then importer self-bins. Per-dependency
// bins use disjoint isolated directories and are linked last when builds may run.
func LinkAllBins(ctx context.Context, plan BinPlan, preserved PreservedBinLinks) (*BinLinks, error) {
	builds := DependencyBuildsMayRun(plan.IgnoreScripts, plan.HasAllowRule, plan.FloorMayAllow)
	managed := NewBinLinks(builds)
	if plan.VirtualStoreOnly {
		return NewBinLinks(false), nil
	}
	if !filepath.IsAbs(plan.ProjectDir) || !filepath.IsAbs(plan.VirtualStoreDir) || plan.Graph == nil {
		return nil, fmt.Errorf("bin linking requires absolute project/store paths and a graph")
	}
	if plan.ModulesDirName == "" {
		plan.ModulesDirName = "node_modules"
	}
	if plan.MaxFilenameLength == 0 {
		plan.MaxFilenameLength = 120
	}
	if plan.Options.PreferSymlinked == nil && !plan.Hoisted {
		prefer := false
		plan.Options.PreferSymlinked = &prefer
	}
	plan.Options.HiddenModulesDir = ""
	if !plan.Hoisted {
		plan.Options.HiddenModulesDir = filepath.Join(plan.VirtualStoreDir, "node_modules")
	}
	pass := binPass{plan: plan, ctx: ctx, managed: managed, preserved: preserved,
		packages: map[string]*jsonvalue.Value{}, workspaces: map[string]*jsonvalue.Value{}}
	for _, key := range slices.Sorted(maps.Keys(plan.Placements)) {
		pkg := plan.Graph.Packages[key]
		if pkg == nil {
			continue
		}
		value, err := pass.readPackage(key, pkg.Name)
		if err != nil {
			return nil, err
		}
		for _, dir := range plan.Placements[key] {
			binDir := filepath.Join(DepModulesDir(dir, pkg.Name), ".bin")
			if err := pass.linkPackage(binDir, dir, pkg.Name, key, value); err != nil {
				return nil, err
			}
		}
	}
	if err := pass.linkImporter(".", plan.Graph.RootDeps()); err != nil {
		return nil, err
	}
	if plan.HasWorkspace {
		for _, importer := range slices.Sorted(maps.Keys(plan.Graph.Importers)) {
			if importer != "." && IsPhysicalImporter(importer) {
				if err := pass.linkImporter(importer, plan.Graph.Importers[importer]); err != nil {
					return nil, err
				}
			}
		}
	}
	if builds && plan.Placements == nil {
		if err := pass.linkDependencies(); err != nil {
			return nil, err
		}
	}
	return managed, nil
}

func IsPhysicalImporter(path string) bool {
	return path == "." || !strings.Contains(path, "/node_modules/")
}

func DepModulesDir(packageDir, name string) string {
	parent := filepath.Dir(packageDir)
	if strings.HasPrefix(name, "@") {
		parent = filepath.Dir(parent)
	}
	return parent
}

func MaterializedPackageDir(virtualStore, depPath, name string, maxLength int, placements HoistedPlacements) (string, error) {
	if paths := placements[depPath]; len(paths) > 0 {
		return paths[0], nil
	}
	filename, err := lockfile.DepPathFilename(depPath, maxLength)
	if err != nil {
		return "", err
	}
	return filepath.Join(virtualStore, filename, "node_modules", filepath.FromSlash(name)), nil
}

type binPass struct {
	plan                 BinPlan
	ctx                  context.Context
	managed              *BinLinks
	preserved            PreservedBinLinks
	packages, workspaces map[string]*jsonvalue.Value
}

func (p *binPass) packageDir(key, name string) (string, error) {
	return MaterializedPackageDir(p.plan.VirtualStoreDir, key, name, p.plan.MaxFilenameLength, p.plan.Placements)
}

func (p *binPass) readPackage(key, name string) (*jsonvalue.Value, error) {
	if value, ok := p.packages[key]; ok {
		return value, nil
	}
	dir, err := p.packageDir(key, name)
	if err != nil {
		return nil, err
	}
	value, err := readBinManifest(dir, name, false)
	if err == nil {
		p.packages[key] = value
	}
	return value, err
}

func readBinManifest(dir, name string, local bool) (*jsonvalue.Value, error) {
	path := filepath.Join(dir, "package.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	label := name
	if local {
		label = "local dep " + name
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read package.json for %s at %s: %w", label, path, err)
	}
	value, err := jsonvalue.Parse(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}))
	if err != nil {
		return nil, fmt.Errorf("failed to parse package.json for %s: %w", label, err)
	}
	return value, nil
}

func (p *binPass) linkPackage(binDir, pkgDir, name, key string, value *jsonvalue.Value) error {
	if err := p.ctx.Err(); err != nil {
		return err
	}
	if err := p.managed.LinkManifest(binDir, pkgDir, name, value, p.plan.Options, p.preserved); err != nil {
		return err
	}
	if pkg := p.plan.Graph.Packages[key]; pkg != nil {
		for _, bundled := range pkg.BundledDependencies {
			dir := filepath.Join(pkgDir, "node_modules", filepath.FromSlash(bundled))
			data, err := os.ReadFile(filepath.Join(dir, "package.json"))
			if err != nil {
				continue
			}
			value, err := jsonvalue.Parse(data)
			if err != nil {
				continue
			}
			if err := p.managed.LinkManifest(binDir, dir, bundled, value, p.plan.Options, p.preserved); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *binPass) linkDep(binDir, key, name string) error {
	dir, err := p.packageDir(key, name)
	if err != nil {
		return err
	}
	value, err := p.readPackage(key, name)
	if err != nil {
		return err
	}
	return p.linkPackage(binDir, dir, name, key, value)
}

func (p *binPass) linkLocal(binDir, dir, name string) error {
	value, ok := p.workspaces[dir]
	if !ok {
		var err error
		value, err = readBinManifest(dir, name, true)
		if err != nil {
			return err
		}
		p.workspaces[dir] = value
	}
	return p.managed.LinkManifest(binDir, dir, name, value, p.plan.Options, p.preserved)
}

func (p *binPass) linkImporter(importer string, deps []lockfile.DirectDep) error {
	if err := p.ctx.Err(); err != nil {
		return err
	}
	dir := filepath.Join(p.plan.ProjectDir, filepath.FromSlash(importer))
	binDir := filepath.Join(dir, p.plan.ModulesDirName, ".bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return err
	}
	for _, dep := range deps {
		local := ""
		if p.plan.HasWorkspace {
			local = p.plan.WorkspaceDirs[dep.Name]
		}
		if local == "" {
			if pkg := p.plan.Graph.Packages[dep.DepPath]; pkg != nil && pkg.Source != nil && (pkg.Source.Kind == lockfile.Link || pkg.Source.Kind == lockfile.Portal) {
				local = filepath.FromSlash(pkg.Source.Path)
				if !filepath.IsAbs(local) {
					local = filepath.Join(p.plan.ProjectDir, local)
				}
			}
		}
		var err error
		if local != "" {
			err = p.linkLocal(binDir, local, dep.Name)
		} else {
			err = p.linkDep(binDir, dep.DepPath, dep.Name)
		}
		if err != nil {
			return err
		}
	}
	if pkg := p.plan.Manifests[importer]; pkg != nil {
		prefer := false
		opts := p.plan.Options
		opts.PreferSymlinked = &prefer
		name := ""
		if pkg.Name != nil {
			name = *pkg.Name
		}
		return p.managed.LinkEntries(binDir, dir, name, pkg.Raw.Get("bin"), opts, p.preserved)
	}
	return nil
}

func (p *binPass) linkDependencies() error {
	for _, key := range slices.Sorted(maps.Keys(p.plan.Graph.Packages)) {
		if err := p.ctx.Err(); err != nil {
			return err
		}
		pkg := p.plan.Graph.Packages[key]
		if len(pkg.Dependencies) == 0 {
			continue
		}
		dir, err := p.packageDir(key, pkg.Name)
		if err != nil {
			return err
		}
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		binDir := filepath.Join(DepModulesDir(dir, pkg.Name), ".bin")
		for _, name := range slices.Sorted(maps.Keys(pkg.Dependencies)) {
			child, ok := p.plan.Graph.Child(name, pkg.Dependencies[name])
			if !ok || child == key && name == pkg.Name {
				continue
			}
			if err := p.linkDep(binDir, child, name); err != nil {
				return err
			}
		}
	}
	return nil
}
