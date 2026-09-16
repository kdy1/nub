package resolver

import (
	"maps"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/semver"
)

// LockedIndex borrows an immutable existing graph. Buckets retain dep-path
// ordering, rather than sorting by semver, to preserve reference reuse picks.
type LockedIndex struct {
	byName map[string][]*lockfile.Package
}

func NewLockedIndex(existing *lockfile.Graph) *LockedIndex {
	index := &LockedIndex{byName: map[string][]*lockfile.Package{}}
	if existing != nil {
		for _, key := range slices.Sorted(maps.Keys(existing.Packages)) {
			p := existing.Packages[key]
			index.byName[p.Name] = append(index.byName[p.Name], p)
		}
	}
	return index
}
func (i *LockedIndex) FindSatisfying(name, requested, registryName string, vulnerable map[string][]string) *lockfile.Package {
	for _, p := range i.byName[name] {
		if !p.InBundle && semver.EngineSatisfies(p.Version, requested) && !IsVulnerable(registryName, p.Version, vulnerable) {
			return p
		}
	}
	return nil
}

// FindFirstInRange intentionally does not skip bundled or vulnerable entries.
// The version-hint caller filters the single result without searching again.
func (i *LockedIndex) FindFirstInRange(name, requested string) *lockfile.Package {
	for _, p := range i.byName[name] {
		if semver.EngineSatisfies(p.Version, requested) {
			return p
		}
	}
	return nil
}
func (i *LockedIndex) FindLocalIntegrity(name, version string, source *lockfile.Source) *string {
	for _, p := range i.byName[name] {
		if p.Source != nil && sourceIntegrityMatches(p.Source, source) && (p.Version == version || p.Source.Kind == lockfile.Git && source.Kind == lockfile.Git && p.Version == "0.0.0") {
			if p.Integrity == nil {
				return nil
			}
			return new(*p.Integrity)
		}
	}
	return nil
}
func sourceIntegrityMatches(a, b *lockfile.Source) bool {
	if a == nil || b == nil || a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case lockfile.Git:
		return lockfile.GitCommitsMatch(a.Resolved, b.Resolved) && equalString(a.Subpath, b.Subpath)
	case lockfile.RemoteTarball:
		return a.URL == b.URL && stringValue(a.Integrity) == stringValue(b.Integrity) && a.GitHosted == b.GitHosted
	default:
		return comparablePath(a.Path) == comparablePath(b.Path)
	}
}
func equalString(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
func stringValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Rust Path equality skips repeated separators and interior dots but keeps
// parent components and a leading relative dot. filepath.Clean folds parents.
func comparablePath(s string) string {
	if runtime.GOOS == "windows" {
		s = strings.ReplaceAll(s, "\\", "/")
	}
	volume := filepath.VolumeName(s)
	s = strings.TrimPrefix(s, volume)
	rooted := strings.HasPrefix(s, "/")
	var parts []string
	for _, part := range strings.Split(s, "/") {
		if part == "" {
			continue
		}
		if part == "." && (rooted || len(parts) > 0) {
			continue
		}
		parts = append(parts, part)
	}
	if rooted {
		volume += "/"
	}
	return volume + strings.Join(parts, "/")
}
