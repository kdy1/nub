package linker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/store"
	"github.com/nubjs/nub/pm-go/internal/workspace"
)

func (p IsolatedPlan) populateGlobal(ctx context.Context, local Materializer, key string, pkg *lockfile.Package, nested map[string]string, stats *LinkStats) error {
	global := local
	global.Root, global.Hashes = p.GlobalVirtualStoreDir, p.Hashes
	localName, err := local.EntryName(key)
	if err != nil {
		return err
	}
	globalName, err := global.EntryName(key)
	if err != nil {
		return err
	}
	localEntry := filepath.Join(local.Root, localName)
	globalEntry := filepath.Join(global.Root, globalName)
	globalPackage := filepath.Join(globalEntry, "node_modules", filepath.FromSlash(pkg.Name))
	sharedSource := pkg.Source != nil
	if !sharedSource && packageNameMatchesAny(pkg.Name, p.DiskMaterialize) {
		// Store-resident importers still target the shared copy. Ejection
		// therefore keeps both placements, counting only the local one.
		localReady := realDirectory(localEntry)
		if _, err := os.Stat(globalPackage); err == nil && localReady {
			stats.PackagesCached++
			return nil
		}
		index, err := p.packageIndex(key, pkg)
		if err != nil {
			return err
		}
		if _, err := p.ensureIndexed(ctx, global, key, pkg, index, nested); err != nil {
			return err
		}
		if localReady {
			stats.PackagesCached++
			p.Quarantine.StripIndexed(filepath.Join(localEntry, "node_modules", filepath.FromSlash(pkg.Name)), index)
			return nil
		}
		if _, err := os.Readlink(localEntry); err == nil {
			_ = removeEntry(ctx, localEntry, 4)
			if _, err := os.Lstat(localEntry); err == nil {
				return fmt.Errorf("I/O error at %s: failed to remove stale shared-store link before disk-materializing the package", localEntry)
			}
		}
		result, err := p.ensureIndexed(ctx, local, key, pkg, index, nested)
		if err != nil {
			return err
		}
		addMaterializedStats(stats, result)
		return nil
	}
	projectLocal := !sharedSource && p.ProjectLocalDepPaths.Has(key)
	if projectLocal && realDirectory(localEntry) {
		stats.PackagesCached++
		return nil
	}
	if !projectLocal && freshGlobalLink(localEntry, globalEntry) {
		if err := global.linkDependencies(ctx, global.Root, globalName, p.Graph, pkg, nested, true); err != nil {
			return err
		}
		stats.PackagesCached++
		if sharedSource {
			p.Quarantine.StripIndexed(globalPackage, p.Indices[key])
		}
		return nil
	}
	index, err := p.packageIndex(key, pkg)
	if err != nil {
		return err
	}
	if projectLocal {
		_ = removeEntry(ctx, localEntry, 4)
		result, err := p.ensureIndexed(ctx, local, key, pkg, index, nested)
		if err != nil {
			return err
		}
		addMaterializedStats(stats, result)
		return nil
	}
	result, err := p.ensureIndexed(ctx, global, key, pkg, index, nested)
	if err != nil {
		return err
	}
	addMaterializedStats(stats, result)
	_ = removeEntry(ctx, localEntry, 4)
	if err := mkdirLinkDir(ctx, filepath.Dir(localEntry)); err != nil {
		return err
	}
	// Global references intentionally persist an absolute target. The local
	// project entry remains dep-path keyed while its target includes the hash.
	return CreateDirLink(ctx, globalEntry, localEntry)
}

func addMaterializedStats(stats *LinkStats, result Materialized) {
	if result.Cached {
		stats.PackagesCached++
	} else {
		stats.PackagesLinked++
		stats.FilesLinked += result.FilesLinked
	}
}

func realDirectory(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && !patchPathIsLink(info)
}

func freshGlobalLink(link, target string) bool {
	actual, err := os.Readlink(link)
	if err != nil || !sameStoredPath(actual, target) {
		return false
	}
	_, err = os.Stat(link)
	return err == nil
}

func packageNameMatchesAny(name string, patterns []string) bool {
	for _, pattern := range patterns {
		// A malformed glob falls back to its literal spelling. Literal
		// equality also implies a match for every valid package-name pattern.
		if name == pattern || workspace.Match(pattern, name, false) {
			return true
		}
	}
	return false
}

func (p IsolatedPlan) indexReadKey(pkg *lockfile.Package) *string {
	if pkg.Integrity != nil {
		return pkg.Integrity
	}
	if binding, ok := p.NoIntegrityReadKeys[pkg.RegistryName()+"@"+pkg.Version]; ok {
		return &binding
	}
	return nil
}

func (p IsolatedPlan) packageIndex(key string, pkg *lockfile.Package) (store.PackageIndex, error) {
	if index, ok := p.Indices[key]; ok {
		return index, nil
	}
	if p.Store != nil && pkg.Source == nil {
		if index, ok := p.Store.LoadIndex(pkg.RegistryName(), pkg.Version, p.indexReadKey(pkg), false); ok {
			return index, nil
		}
	}
	return nil, &MissingPackageIndex{key}
}

func (p IsolatedPlan) ensureIndexed(ctx context.Context, m Materializer, key string, pkg *lockfile.Package, index store.PackageIndex, nested map[string]string) (Materialized, error) {
	result, err := m.EnsurePackage(ctx, key, p.Graph, pkg, index, nested)
	var missing *MissingStoreFile
	if errors.As(err, &missing) && p.Store != nil {
		_, _ = p.Store.InvalidateIndex(ctx, pkg.RegistryName(), pkg.Version, p.indexReadKey(pkg))
	}
	return result, err
}
