package linker

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/store"
)

type MissingPackageIndex struct{ DepPath string }

func (e *MissingPackageIndex) Error() string {
	return fmt.Sprintf("internal: missing package index for %s — caller skipped `load_index` but the package wasn't already materialized", e.DepPath)
}
func (*MissingPackageIndex) Code() string { return "ERR_AUBE_MISSING_PACKAGE_INDEX" }

type LinkStats struct {
	PackagesLinked, PackagesCached, FilesLinked, TopLevelLinked int
}

// IsolatedPlan describes a project-local isolated layout. The install driver
// owns the project/store leases and immutable graph and index inputs. Scripts
// and executable shims are separate passes after the package layout succeeds.
type IsolatedPlan struct {
	ProjectDir, ModulesDirName, VirtualStoreDir string
	Graph                                       *lockfile.Graph
	Indices                                     map[string]store.PackageIndex
	Store                                       *store.Store
	NoIntegrityReadKeys                         map[string]string
	Strategy                                    Strategy
	Patches                                     map[string]string
	Quarantine                                  *Quarantine
	MaxFilenameLength                           int
	HasWorkspace                                bool
	WorkspaceDirs                               map[string]string
	Hoist, HoistWorkspacePackages               *bool
	HoistPatterns, PublicHoistPatterns          []string
	ShamefullyHoist, DedupeDirectDeps           bool
	VirtualStoreOnly                            bool
	Report                                      func(code, message string)
}

func enabled(value *bool) bool { return value == nil || *value }

func LinkIsolatedProject(ctx context.Context, plan IsolatedPlan) (LinkStats, error) {
	stats := LinkStats{}
	if plan.Graph == nil || !filepath.IsAbs(plan.ProjectDir) {
		return stats, fmt.Errorf("isolated linking requires an absolute project and graph")
	}
	for _, pkg := range plan.Graph.Packages {
		if err := ValidatePackageLinkName(pkg.Name); err != nil {
			return stats, err
		}
	}
	plan.ProjectDir = filepath.Clean(plan.ProjectDir)
	if plan.ModulesDirName == "" {
		plan.ModulesDirName = "node_modules"
	}
	nm, err := CheckedModulesDir(plan.ProjectDir, plan.ModulesDirName)
	if err != nil {
		return stats, err
	}
	importers := map[string]string{".": nm}
	if plan.HasWorkspace {
		importers = map[string]string{}
		for key := range plan.Graph.Importers {
			if key == "." {
				importers[key] = nm
				continue
			}
			if !IsPhysicalImporter(key) {
				continue
			}
			dir, err := CheckedModulesDir(filepath.Join(plan.ProjectDir, filepath.FromSlash(key)), plan.ModulesDirName)
			if err != nil {
				return stats, err
			}
			importers[key] = dir
		}
		for _, dir := range plan.WorkspaceDirs {
			if !filepath.IsAbs(dir) {
				return stats, fmt.Errorf("workspace linking requires absolute member directories")
			}
		}
	}
	if plan.VirtualStoreDir == "" {
		// Nub's adapter default is independent of a custom modulesDir.
		plan.VirtualStoreDir = filepath.Join(plan.ProjectDir, "node_modules", ".store")
	} else if !filepath.IsAbs(plan.VirtualStoreDir) {
		return stats, fmt.Errorf("virtual store override must be absolute")
	}
	if plan.HoistPatterns == nil {
		plan.HoistPatterns = []string{"*"}
	}
	m := Materializer{Root: plan.VirtualStoreDir, Strategy: plan.Strategy, Patches: plan.Patches, Quarantine: plan.Quarantine, MaxFilenameLength: plan.MaxFilenameLength}
	if err := mkdirLinkDir(ctx, m.Root); err != nil {
		return stats, err
	}
	if plan.HasWorkspace {
		if err := mkdirLinkDir(ctx, nm); err != nil {
			return stats, err
		}
	}
	SweepStaleTemps(ctx, m.Root)
	leaf := ""
	if filepath.Dir(m.Root) == nm {
		leaf = filepath.Base(m.Root)
	}
	if !plan.HasWorkspace {
		sweepTopLevel(ctx, nm, plan.preserve(plan.Graph.RootDeps(), true), leaf)
	}
	current := CurrentPatchHashes(plan.Patches)
	if err := WipeChangedPatchedEntries(ctx, m.Root, plan.Graph, ReadAppliedPatches(nm), current, plan.MaxFilenameLength); err != nil {
		return stats, err
	}
	keys := slices.Sorted(maps.Keys(plan.Graph.Packages))
	nested := map[string]string{}
	for _, key := range keys {
		pkg := plan.Graph.Packages[key]
		if pkg.Source != nil && pkg.Source.Kind == lockfile.Link {
			nested[key] = sourcePath(plan.ProjectDir, pkg.Source.Path)
		}
	}
	// Mutable local trees are imported afresh, and take precedence over the
	// registry pass. A warm registry entry needs no index read.
	for _, localPass := range []bool{true, false} {
		for _, key := range keys {
			if err := ctx.Err(); err != nil {
				return stats, err
			}
			pkg := plan.Graph.Packages[key]
			if (pkg.Source != nil) != localPass || pkg.Source != nil && pkg.Source.Kind == lockfile.Link {
				continue
			}
			index, present := plan.Indices[key]
			if localPass && !present {
				continue
			}
			entry, err := m.EntryName(key)
			if err != nil {
				return stats, err
			}
			entryDir := filepath.Join(m.Root, entry)
			if localPass && (pkg.Source.Kind == lockfile.Directory || pkg.Source.Kind == lockfile.Portal) {
				_ = removeEntry(ctx, entryDir, 4)
				if _, err := os.Stat(entryDir); err == nil {
					return stats, fmt.Errorf("I/O error at %s: failed to remove stale local dependency materialization", entryDir)
				}
			}
			if _, err := os.Stat(entryDir); err == nil {
				stats.PackagesCached++
				if localPass {
					plan.Quarantine.StripIndexed(filepath.Join(entryDir, "node_modules", filepath.FromSlash(pkg.Name)), index)
				}
				continue
			}
			integrity := pkg.Integrity
			if integrity == nil {
				if binding, ok := plan.NoIntegrityReadKeys[pkg.RegistryName()+"@"+pkg.Version]; ok {
					integrity = &binding
				}
			}
			if !present && plan.Store != nil {
				index, present = plan.Store.LoadIndex(pkg.RegistryName(), pkg.Version, integrity, false)
			}
			if !present {
				return stats, &MissingPackageIndex{key}
			}
			result, err := m.EnsurePackage(ctx, key, plan.Graph, pkg, index, nested)
			if err != nil {
				var missing *MissingStoreFile
				if errors.As(err, &missing) && plan.Store != nil {
					_, _ = plan.Store.InvalidateIndex(ctx, pkg.RegistryName(), pkg.Version, integrity)
				}
				return stats, err
			}
			if result.Cached {
				stats.PackagesCached++
			} else {
				stats.PackagesLinked++
				stats.FilesLinked += result.FilesLinked
			}
		}
	}
	if plan.VirtualStoreOnly {
		if plan.HasWorkspace {
			sweepTopLevel(ctx, nm, nil, leaf)
		}
	} else {
		rootDeps := map[string]string{}
		for _, dep := range plan.Graph.RootDeps() {
			rootDeps[dep.Name] = dep.DepPath
		}
		for _, key := range slices.Sorted(maps.Keys(importers)) {
			dir, deps := importers[key], plan.Graph.Importers[key]
			if plan.HasWorkspace {
				if err := mkdirLinkDir(ctx, dir); err != nil {
					return stats, err
				}
				storeLeaf := ""
				if key == "." {
					storeLeaf = leaf
				}
				sweepTopLevel(ctx, dir, plan.preserve(deps, key == "."), storeLeaf)
			}
			for _, dep := range deps {
				if plan.HasWorkspace && plan.DedupeDirectDeps && key != "." && rootDeps[dep.Name] == dep.DepPath {
					continue
				}
				if err := ValidatePackageLinkName(dep.Name); err != nil {
					return stats, err
				}
				source, err := plan.directTarget(m, dep)
				if err != nil {
					return stats, err
				}
				if source == "" {
					continue
				}
				linked, err := ensureTopLink(ctx, filepath.Join(dir, filepath.FromSlash(dep.Name)), source)
				if err != nil {
					return stats, err
				}
				if linked {
					stats.TopLevelLinked++
				}
			}
		}
		if err := plan.publicHoist(ctx, m, nm, &stats); err != nil {
			return stats, err
		}
	}
	if err := plan.hiddenHoist(ctx, m); err != nil {
		return stats, err
	}
	if err := ctx.Err(); err != nil {
		return stats, err
	}
	if err := WriteAppliedPatches(nm, current); err != nil && plan.Report != nil {
		plan.Report("ERR_AUBE_PATCHES_TRACKING_WRITE", fmt.Sprintf("failed to write %s: %v. next install may miss stale patched entries", AppliedPatchesFilename, err))
	}
	return stats, nil
}

func sourcePath(root, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, filepath.FromSlash(path))
}

func (p IsolatedPlan) directTarget(m Materializer, dep lockfile.DirectDep) (string, error) {
	pkg := p.Graph.Packages[dep.DepPath]
	if p.HasWorkspace && pkg == nil {
		if dir, ok := p.WorkspaceDirs[dep.Name]; ok {
			if enabled(p.HoistWorkspacePackages) {
				return dir, nil
			}
			return "", nil
		}
	}
	if pkg != nil && pkg.Source != nil && pkg.Source.Kind == lockfile.Link {
		return sourcePath(p.ProjectDir, pkg.Source.Path), nil
	}
	return existingPackageDir(m, dep.DepPath, dep.Name)
}

func existingPackageDir(m Materializer, key, name string) (string, error) {
	entry, err := m.EntryName(key)
	if err != nil {
		return "", err
	}
	source := filepath.Join(m.Root, entry, "node_modules", filepath.FromSlash(name))
	if _, err := os.Stat(source); err != nil {
		return "", nil
	}
	return source, nil
}

func (p IsolatedPlan) preserve(deps []lockfile.DirectDep, root bool) map[string]bool {
	out := map[string]bool{}
	for _, dep := range deps {
		out[dep.Name] = true
	}
	if root {
		for _, pkg := range p.Graph.Packages {
			if p.ShamefullyHoist || pkg.Source == nil && hoistMatches(pkg.Name, p.PublicHoistPatterns) {
				out[pkg.Name] = true
			}
		}
	}
	return out
}
