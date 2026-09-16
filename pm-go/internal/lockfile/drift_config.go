package lockfile

import (
	"fmt"
	"maps"
	"slices"

	"github.com/nubjs/nub/pm-go/internal/identity"
)

type DriftStatus struct{ Reason string }

func (d DriftStatus) Fresh() bool { return d.Reason == "" }
func drift(reason string, args ...any) DriftStatus {
	return DriftStatus{Reason: fmt.Sprintf(reason, args...)}
}
func driftKeys[V any](values map[string]V) []string { return slices.Sorted(maps.Keys(values)) }
func recordsResolutionMetadata(kind identity.Kind) bool {
	return kind == identity.Nub || kind == identity.Pnpm || kind == identity.Bun
}

// CheckPatchedDependenciesDrift uses hashes for pnpm/nub.lock and paths for
// Bun. Formats without this configuration block cannot invalidate it.
func (g *Graph) CheckPatchedDependenciesDrift(kind identity.Kind, paths, hashes map[string]string) DriftStatus {
	if !recordsResolutionMetadata(kind) {
		return DriftStatus{}
	}
	if kind == identity.Pnpm || kind == identity.Nub {
		return g.patchHashesDrift(paths, hashes)
	}
	for _, key := range driftKeys(g.PatchedDependencies) {
		if _, ok := paths[key]; !ok {
			return drift("patchedDependencies.%s: recorded in the lockfile but no longer declared in the project", key)
		}
	}
	for _, key := range driftKeys(paths) {
		path := paths[key]
		locked, ok := g.PatchedDependencies[key]
		if !ok {
			return drift("patchedDependencies.%s: declared in the project but missing from the lockfile", key)
		}
		if locked != path {
			return drift("patchedDependencies.%s: project says %s, lockfile says %s", key, path, locked)
		}
		if hash, ok := hashes[key]; ok {
			if locked, ok := g.PatchedDependencyHashes[key]; ok && locked != hash {
				return drift("patchedDependencies.%s: patch file contents changed (hash mismatch)", key)
			}
		}
	}
	return DriftStatus{}
}
func (g *Graph) patchHashesDrift(paths, hashes map[string]string) DriftStatus {
	for _, keys := range [][]string{driftKeys(g.PatchedDependencyHashes), driftKeys(g.PatchedDependencies)} {
		for _, key := range keys {
			_, hash := hashes[key]
			_, path := paths[key]
			if !hash && !path {
				return drift("patchedDependencies.%s: recorded in the lockfile but no longer declared in the project", key)
			}
		}
	}
	for _, key := range driftKeys(hashes) {
		locked, ok := g.PatchedDependencyHashes[key]
		if ok {
			if locked != hashes[key] {
				return drift("patchedDependencies.%s: patch file contents changed (hash mismatch)", key)
			}
			continue
		}
		lockedPath, hasLocked := g.PatchedDependencies[key]
		path, hasPath := paths[key]
		if !hasLocked || !hasPath {
			return drift("patchedDependencies.%s: declared in the project but missing from the lockfile", key)
		}
		if lockedPath != path {
			return drift("patchedDependencies.%s: project says %s, lockfile says %s", key, path, lockedPath)
		}
	}
	return DriftStatus{}
}
func (g *Graph) CheckCatalogsDrift(catalogs map[string]map[string]string) DriftStatus {
	for _, name := range driftKeys(catalogs) {
		for _, pkg := range driftKeys(catalogs[name]) {
			if entry, ok := g.Catalogs[name][pkg]; ok && entry.Specifier != catalogs[name][pkg] {
				return drift("catalogs.%s.%s: workspace says %s, lockfile says %s", name, pkg, catalogs[name][pkg], entry.Specifier)
			}
		}
	}
	for _, name := range driftKeys(g.Catalogs) {
		for _, pkg := range driftKeys(g.Catalogs[name]) {
			if _, ok := catalogs[name][pkg]; !ok {
				return drift("catalogs.%s: workspace removed %s", name, pkg)
			}
		}
	}
	return DriftStatus{}
}
func (g *Graph) CheckPackageExtensionsDrift(checksum *string) DriftStatus {
	if checksum == nil && g.PackageExtensionsChecksum == nil || checksum != nil && g.PackageExtensionsChecksum != nil && *checksum == *g.PackageExtensionsChecksum {
		return DriftStatus{}
	}
	return drift("packageExtensions changed since the lockfile was written")
}
