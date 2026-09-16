package linker

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/store"
)

type HoistedPlan struct {
	IsolatedPlan
	Limits   HoistingLimits
	Reusable lockfile.Set // Driver-vouched complete placements with unchanged content.
}

// LinkHoistedProject writes real package directories in shallow-to-deep order.
// Children replace bundled copies only after their parents finish. The caller
// holds install/store leases and separately runs bin and lifecycle passes.
func LinkHoistedProject(ctx context.Context, plan HoistedPlan) (LinkStats, HoistedPlacements, error) {
	stats, placements := LinkStats{}, HoistedPlacements{}
	plans, err := hoistedPlans(plan.ProjectDir, plan.ModulesDirName, plan.Graph, plan.HasWorkspace, plan.Limits)
	if err != nil {
		return stats, placements, err
	}
	for _, tree := range plans {
		for _, idx := range tree.importers {
			node := tree.nodes[idx]
			if err := mkdirLinkDir(ctx, node.modulesDir); err != nil {
				return stats, placements, err
			}
			keep := map[string]bool{}
			for name := range node.children {
				keep[name] = true
			}
			sweepTopLevel(ctx, node.modulesDir, keep, "")
		}
		for _, level := range tree.byDepth() {
			for _, idx := range level {
				node := tree.nodes[idx]
				if err := plan.materializeHoisted(ctx, node.key, node.pkgDir, &stats); err != nil {
					return stats, placements, err
				}
				placements[node.key] = append(placements[node.key], node.pkgDir)
			}
		}
		for _, idx := range tree.importers {
			stats.TopLevelLinked += len(tree.nodes[idx].children)
		}
	}
	if plan.HasWorkspace && enabled(plan.HoistWorkspacePackages) {
		modules := plan.ModulesDirName
		if modules == "" {
			modules = "node_modules"
		}
		for _, key := range slices.Sorted(maps.Keys(plan.Graph.Importers)) {
			if !IsPhysicalImporter(key) {
				continue
			}
			nm, err := CheckedModulesDir(sourcePath(plan.ProjectDir, key), modules)
			if err != nil {
				return stats, placements, err
			}
			for _, dep := range plan.Graph.Importers[key] {
				if err := ValidatePackageLinkName(dep.Name); err != nil {
					return stats, placements, err
				}
				target, ok := plan.WorkspaceDirs[dep.Name]
				if !ok || plan.Graph.Packages[dep.DepPath] != nil {
					continue
				}
				if !filepath.IsAbs(target) {
					return stats, placements, fmt.Errorf("workspace linking requires absolute member directories")
				}
				link := filepath.Join(nm, filepath.FromSlash(dep.Name))
				_ = removeEntry(ctx, link, 4)
				if _, err := ensureTopLink(ctx, link, target); err != nil {
					return stats, placements, err
				}
				stats.TopLevelLinked++
			}
		}
	}
	virtual := plan.VirtualStoreDir
	if virtual == "" {
		virtual = filepath.Join(plan.ProjectDir, "node_modules/.store")
	}
	if !filepath.IsAbs(virtual) {
		return stats, placements, fmt.Errorf("virtual store override must be absolute")
	}
	_ = removeEntry(ctx, filepath.Join(virtual, "node_modules"), 10)
	return stats, placements, ctx.Err()
}

func (p HoistedPlan) materializeHoisted(ctx context.Context, key, dir string, stats *LinkStats) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	pkg := p.Graph.Packages[key]
	if pkg.Source != nil && pkg.Source.Kind == lockfile.Link {
		_ = removeEntry(ctx, dir, 4)
		_, err := ensureTopLink(ctx, dir, sourcePath(p.ProjectDir, pkg.Source.Path))
		return err
	}
	if p.Reusable.Has(key) {
		if info, err := os.Stat(filepath.Join(dir, "package.json")); err == nil && info.Mode().IsRegular() {
			stats.PackagesCached++
			return nil
		}
	}
	index, ok := p.Indices[key]
	if !ok && p.Store != nil {
		index, ok = p.Store.LoadIndex(pkg.RegistryName(), pkg.Version, p.indexReadKey(pkg), false)
	}
	if !ok {
		return &MissingPackageIndex{key}
	}
	_ = removeEntry(ctx, dir, 4)
	if err := p.fillHoisted(ctx, key, pkg, index, dir, stats); err != nil {
		return err
	}
	if selector, patch, ok := lockfile.LookupPatch(pkg, p.Patches); ok {
		if err := ApplyPatch(ctx, dir, patch); err != nil {
			return &PatchError{selector, err.Error()}
		}
	}
	p.Quarantine.StripIndexed(dir, index)
	stats.PackagesLinked++
	return nil
}

// Hoisted replacement follows the reference's in-place fill. A failed pass
// leaves no reusable-state vouch; the next pass wipes and rebuilds that tree.
func (p HoistedPlan) fillHoisted(ctx context.Context, key string, pkg *lockfile.Package, index store.PackageIndex, dir string, stats *LinkStats) error {
	parents := map[string]bool{dir: true}
	keys := slices.Sorted(maps.Keys(index))
	for _, key := range keys {
		if err := validateIndexKey(key); err != nil {
			return err
		}
		parents[filepath.Dir(filepath.Join(dir, filepath.FromSlash(key)))] = true
	}
	for _, parent := range slices.Sorted(maps.Keys(parents)) {
		if err := mkdirLinkDir(ctx, parent); err != nil {
			return err
		}
	}
	for _, rel := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		file := index[rel]
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if err := placeFile(ctx, file, target, p.Strategy); err != nil {
			if _, statErr := os.Stat(file.Path); os.IsNotExist(statErr) {
				if p.Store != nil {
					_, _ = p.Store.InvalidateIndex(ctx, pkg.RegistryName(), pkg.Version, p.indexReadKey(pkg))
				}
				return &MissingStoreFile{file.Path, rel}
			}
			return err
		}
		stats.FilesLinked++
		if file.Executable && runtime.GOOS != "windows" {
			info, err := os.Stat(target)
			if err != nil {
				return err
			}
			if err := os.Chmod(target, info.Mode().Perm()|0111); err != nil {
				return err
			}
		}
	}
	return nil
}

// HoistedPlacementsFromGraph reconstructs existing placement sites for rebuild
// and bin consumers. Install-state callers should prefer their recorded map.
func HoistedPlacementsFromGraph(project string, graph *lockfile.Graph, modules string, limits HoistingLimits) (HoistedPlacements, error) {
	plans, err := hoistedPlans(project, modules, graph, true, limits)
	if err != nil {
		return nil, err
	}
	out := HoistedPlacements{}
	for _, tree := range plans {
		// Reference reconstruction walks arena order, unlike materialization's
		// depth order. Preserve it rather than sorting paths independently.
		for _, node := range tree.nodes {
			if node.key != "" && node.pkgDir != "" {
				if _, err := os.Stat(node.pkgDir); err == nil {
					out[node.key] = append(out[node.key], node.pkgDir)
				}
			}
		}
	}
	return out, nil
}
