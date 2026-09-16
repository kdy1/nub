package installstate

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/nubjs/nub/pm-go/internal/linker"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
)

type LayoutInput struct {
	Graph                        *lockfile.Graph
	Linker                       string
	HoistingLimits               linker.HoistingLimits
	ModulesDirName, VirtualStore string
	MaxFilenameLength            int
	Placements                   linker.HoistedPlacements
	UseGlobalVirtualStore        bool
}

// CaptureLayout records installed locations, not newly resolved settings.
// Only root-direct packages carry manifest proofs, matching the reference;
// every importer's direct slot and every shared-store edge is recorded.
func CaptureLayout(ctx context.Context, project string, input LayoutInput) (*Layout, error) {
	if input.Graph == nil || !filepath.IsAbs(project) || !filepath.IsAbs(input.VirtualStore) {
		return nil, fmt.Errorf("install layout requires a graph and absolute project/store paths")
	}
	if input.Linker != "isolated" && input.Linker != "hoisted" {
		return nil, fmt.Errorf("invalid install layout linker %q", input.Linker)
	}
	if input.ModulesDirName == "" {
		input.ModulesDirName = "node_modules"
	}
	if input.MaxFilenameLength == 0 {
		input.MaxFilenameLength = lockfile.DefaultVirtualStoreMaxLength
	}
	maxLength := uint64(input.MaxFilenameLength)
	l := &Layout{Linker: input.Linker, ModulesDirName: input.ModulesDirName, VirtualStoreDirMaxLength: &maxLength, DirectEntries: map[string][]string{}, Packages: map[string]InstalledPackage{}}
	if input.Linker == "hoisted" {
		if input.HoistingLimits > linker.HoistDependencies {
			return nil, fmt.Errorf("invalid hoisting limits")
		}
		limit := []string{"none", "workspaces", "dependencies"}[input.HoistingLimits]
		l.HoistingLimits = &limit
	}
	for _, importer := range keys(input.Graph.Importers) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		dir := join(project, importer)
		entries := []string{}
		for _, dep := range input.Graph.Importers[importer] {
			entry := filepath.Join(dir, input.ModulesDirName, filepath.FromSlash(dep.Name))
			if input.Linker == "hoisted" && input.Placements != nil {
				for ancestor := dir; ; ancestor = filepath.Dir(ancestor) {
					candidate := filepath.Join(ancestor, input.ModulesDirName, filepath.FromSlash(dep.Name))
					if slices.Contains(input.Placements[dep.DepPath], candidate) {
						entry = candidate
						break
					}
					if filepath.Dir(ancestor) == ancestor {
						break
					}
				}
			}
			entries = append(entries, relative(entry, project))
		}
		l.DirectEntries[importer] = entries
	}
	direct := lockfile.Set{}
	for _, dep := range input.Graph.RootDeps() {
		direct.Add(dep.DepPath)
	}
	for _, key := range keys(input.Graph.Packages) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !direct.Has(key) {
			continue
		}
		pkg := input.Graph.Packages[key]
		dir, err := input.packageDir(project, key, pkg)
		if err != nil {
			return nil, err
		}
		path := filepath.Join(dir, "package.json")
		hash := ""
		if h := hashFileIfExists(path); h != nil {
			hash = *h
		}
		l.Packages[key] = InstalledPackage{Name: pkg.Name, Version: pkg.Version, PackageJSONPath: relative(path, project), PackageJSONHash: hash, Link: pkg.Source != nil && pkg.Source.Kind == lockfile.Link}
	}
	if input.UseGlobalVirtualStore && input.Linker == "isolated" {
		nested, err := input.collectNested(ctx, project)
		if err != nil {
			return nil, err
		}
		l.GVSNestedLinks = nested
	}
	return l, nil
}

func (input LayoutInput) packageDir(project, key string, pkg *lockfile.Package) (string, error) {
	if pkg.Source != nil && pkg.Source.Kind == lockfile.Link {
		return join(project, pkg.Source.Path), nil
	}
	return linker.MaterializedPackageDir(input.VirtualStore, key, pkg.Name, input.MaxFilenameLength, input.Placements)
}

func (input LayoutInput) collectNested(ctx context.Context, project string) (*map[string]string, error) {
	links := map[string]string{}
	for _, key := range keys(input.Graph.Packages) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pkg := input.Graph.Packages[key]
		if pkg.Source != nil && !pkg.Source.GloballyShareable() {
			continue
		}
		filename, err := lockfile.DepPathFilename(key, input.MaxFilenameLength)
		if err != nil {
			return nil, err
		}
		if _, err := os.Readlink(filepath.Join(input.VirtualStore, filename)); err != nil {
			continue
		}
		dir, err := input.packageDir(project, key, pkg)
		if err != nil {
			return nil, err
		}
		nm := linker.DepModulesDir(dir, pkg.Name)
		for _, name := range keys(pkg.Dependencies) {
			if name == pkg.Name {
				continue
			}
			path := filepath.Join(nm, filepath.FromSlash(name))
			target, err := os.Readlink(path)
			if err != nil || !utf8.ValidString(target) {
				return nil, nil
			}
			links[relative(path, project)] = target
		}
	}
	return &links, nil
}

// VerifyLayout checks lstat for direct links (dangling link: is installed),
// package identity after a content-hash miss, and exact shared edge targets.
func VerifyLayout(project string, l *Layout) string {
	if l == nil {
		return ""
	}
	for _, importer := range keys(l.DirectEntries) {
		for _, rel := range l.DirectEntries[importer] {
			if _, err := os.Lstat(join(project, rel)); err != nil {
				return "installed entry missing: " + rel
			}
		}
	}
	for _, key := range keys(l.Packages) {
		pkg := l.Packages[key]
		if pkg.Link {
			continue
		}
		path := join(project, pkg.PackageJSONPath)
		if h := hashFileIfExists(path); h != nil && pkg.PackageJSONHash != "" && pkg.PackageJSONHash != HashBytes(nil) && *h == pkg.PackageJSONHash {
			continue
		}
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return "installed package metadata missing: " + pkg.PackageJSONPath
		}
		var manifest struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		if err != nil || decode(data, &manifest) != nil {
			return "installed package metadata unreadable: " + pkg.PackageJSONPath
		}
		if manifest.Name != pkg.Name || manifest.Version != pkg.Version {
			return "installed package metadata changed: " + pkg.PackageJSONPath
		}
	}
	return staleNestedLink(project, l)
}

func GVSNestedLinksCurrent(project string, l *Layout) bool {
	return l != nil && l.GVSNestedLinks != nil && staleNestedLink(project, l) == ""
}
func staleNestedLink(project string, l *Layout) string {
	if l.GVSNestedLinks == nil {
		return ""
	}
	for _, rel := range keys(*l.GVSNestedLinks) {
		actual, err := os.Readlink(join(project, rel))
		if err != nil {
			return "global virtual store link missing: " + rel
		}
		if pathComponents(actual) != pathComponents((*l.GVSNestedLinks)[rel]) {
			return "global virtual store link changed: " + rel
		}
	}
	return ""
}

// Rust Path equality removes interior '.' and redundant separators but retains
// '..' and a leading relative '.'. Clean would erase meaningful '..' segments.
func pathComponents(path string) string {
	if runtime.GOOS == "windows" {
		path = strings.ReplaceAll(path, "\\", "/")
	}
	parts := strings.Split(path, "/")
	out := []string{}
	for i, p := range parts {
		if p != "" && (p != "." || i == 0) {
			out = append(out, p)
		}
	}
	prefix := ""
	if strings.HasPrefix(path, "/") {
		prefix = "/"
	}
	return prefix + strings.Join(out, "/")
}
func keys[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }
