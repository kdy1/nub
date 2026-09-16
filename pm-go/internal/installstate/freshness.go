package installstate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/manifest"
	"github.com/nubjs/nub/pm-go/internal/store"
)

type FreshnessInput struct {
	// Members are discovered with the current identity-scoped workspace rules.
	// Errors in reference discovery produce an empty list. Paths are absolute.
	Members []string
	// Optional per-member branch overrides cover workspaces spanning Git roots.
	// An absent entry inherits Paths.Branch; an explicit empty value disables it.
	MemberBranches map[string]string
	// Nil is the run/exec check, which inspects the existing tree independently
	// of install-shape settings. Install passes the resolved settings digest.
	SettingsHash *string
}

// Check returns the reference's first stale reason, or empty for a fresh tree.
// It may refresh metadata-only snapshots; failed cache writes are harmless.
// LinkCompletedCleanly is a separate prerequisite for hoisted entry reuse.
func (p Paths) Check(ctx context.Context, input FreshnessInput) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	for _, member := range input.Members {
		if !filepath.IsAbs(member) {
			return "", fmt.Errorf("workspace member must be absolute: %s", member)
		}
	}
	s := p.ReadFreshness()
	if s == nil {
		return "install state not found", nil
	}
	if _, err := os.Stat(p.Modules); err != nil {
		return filepath.Base(p.Modules) + " is missing", nil
	}
	name, path := p.ActiveLockfile()
	missing := false
	refreshed := false
	if path != "" {
		meta := CaptureMeta(path)
		if meta == nil || s.LockfileMeta == nil || *meta != *s.LockfileMeta {
			if HashFile(path) != s.LockfileHash {
				return name + " has changed", nil
			}
			if meta != nil {
				s.LockfileMeta = meta
				refreshed = true
			}
		}
	} else if len(s.MemberLockfileHashes) == 0 {
		missing = true
	}
	if len(s.MemberLockfileHashes) > 0 {
		if reason := p.memberLockfilesStale(s, input); reason != "" {
			return reason, nil
		}
	}
	if reason := packageJSONsStale(p.Project, s); reason != "" {
		return reason, nil
	}
	if len(s.MemberLockfileHashes) == 0 {
		for _, member := range input.Members {
			rel := relative(member, p.Project)
			key := manifestKey(p.Project, member)
			if _, ok := s.PackageJSONHashes[key]; !ok {
				return rel + " is a new workspace member", nil
			}
		}
	}
	if s.SectionFiltered {
		return "previous install omitted dependency sections; auto-installing full graph", nil
	}
	if reason := DeferredBuildsStale(s.DeferredDepBuilds); reason != "" {
		return reason, nil
	}
	if s.DepBuildPolicyHash == "" {
		return "dependency build policy state is missing", nil
	}
	if reason := VerifyLayout(p.Project, s.Layout); reason != "" {
		return reason, nil
	}
	if input.SettingsHash != nil && *input.SettingsHash != s.SettingsHash {
		return "install settings or the active Node version have changed", nil
	}
	if missing {
		return "no lockfile found", nil
	}
	if s.LocalDirectoryHashes == nil {
		return "local dependency fingerprints not recorded", nil
	}
	for _, rel := range keys(*s.LocalDirectoryHashes) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		stored := (*s.LocalDirectoryHashes)[rel]
		source := join(p.Project, rel)
		metadata, err := store.DirectoryMetadataFingerprint(ctx, source)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "local dependency " + rel + " is unreadable", nil
		}
		if metadata == stored.MetadataHash {
			continue
		}
		content, err := store.DirectoryContentFingerprint(ctx, source)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "local dependency " + rel + " is unreadable", nil
		}
		if content != stored.ContentHash {
			return "local dependency " + rel + " has changed", nil
		}
		stored.MetadataHash = metadata
		(*s.LocalDirectoryHashes)[rel] = stored
		refreshed = true
	}
	if refreshed {
		_ = p.WriteFreshness(s)
	}
	return "", ctx.Err()
}

func packageJSONsStale(project string, s *Freshness) string {
	for _, rel := range keys(s.PackageJSONHashes) {
		path := manifestPath(project, rel)
		if _, err := os.Stat(path); err != nil {
			return rel + " is missing"
		}
		if stored, ok := s.PackageJSONMeta[rel]; ok {
			if current := CaptureMeta(path); current != nil && *current == stored {
				continue
			}
		}
		if HashFile(path) == s.PackageJSONHashes[rel] {
			continue
		}
		reason := rel + " has changed"
		if rel == "." {
			reason = "package.json has changed"
		}
		shape, ok := s.PackageJSONShapeDigests[rel]
		if !ok {
			return reason
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return reason
		}
		v, err := jsonvalue.Parse(data)
		if err != nil || manifest.InstallShapeDigest(v) != shape {
			return reason
		}
	}
	return ""
}

func (p Paths) memberLockfilesStale(s *Freshness, input FreshnessInput) string {
	seen := map[string]bool{}
	for _, dir := range input.Members {
		key := relative(dir, p.Project)
		hash, ok := s.MemberLockfileHashes[key]
		if !ok {
			return key + " is a new workspace member"
		}
		seen[key] = true
		_, path := ActiveLockfile(dir, input.branch(dir, p.Branch))
		if path == "" {
			if hash != "" {
				return key + " lockfile is missing"
			}
			continue
		}
		if stored, ok := s.MemberLockfileMeta[key]; ok {
			if meta := CaptureMeta(path); meta != nil && *meta == stored {
				continue
			}
		}
		if HashFile(path) != hash {
			return key + " lockfile has changed"
		}
	}
	for _, key := range keys(s.MemberLockfileHashes) {
		if !seen[key] {
			return key + " was removed from the workspace"
		}
	}
	return ""
}

func DeferredBuildsStale(builds *[]string) string {
	if builds == nil {
		return "install state predates dependency-build completion tracking; re-checking builds"
	}
	if len(*builds) == 0 {
		return ""
	}
	preview := strings.Join((*builds)[:min(3, len(*builds))], ", ")
	if len(*builds) > 3 {
		preview += fmt.Sprintf(", and %d more", len(*builds)-3)
	}
	return "a dependency build did not run on the last install (" + preview + "); retrying"
}

func (p Paths) SettingsChanged(current string) bool {
	s := p.ReadFreshness()
	return s != nil && s.SettingsHash != current
}
func (p Paths) ReleasePolicyChanged(current string) bool {
	s := p.Read()
	return s != nil && s.ReleasePolicyHash != "" && s.ReleasePolicyHash != current
}

// ReusableHoisted consumes freshly computed package content hashes, never
// resolution identities. A prior successful install cannot vouch for a later
// interrupted link phase or a switch from another layout.
func (p Paths) ReusableHoisted(current map[string]string) lockfile.Set {
	result := lockfile.Set{}
	if !p.LinkCompletedCleanly() {
		return result
	}
	s := p.Read()
	if s == nil || s.Layout == nil || s.Layout.Linker != "hoisted" || len(s.PackageContentHashes) == 0 {
		return result
	}
	for key, hash := range current {
		if prior, ok := s.PackageContentHashes[key]; ok && prior == hash {
			result.Add(key)
		}
	}
	return result
}

func (i FreshnessInput) branch(member, fallback string) string {
	if b, ok := i.MemberBranches[member]; ok {
		return b
	}
	return fallback
}
func manifestKey(project, member string) string {
	if filepath.Clean(member) == filepath.Clean(project) {
		return "."
	}
	return relative(filepath.Join(member, "package.json"), project)
}
func manifestPath(project, key string) string {
	if key == "." {
		return filepath.Join(project, "package.json")
	}
	return join(project, key)
}
