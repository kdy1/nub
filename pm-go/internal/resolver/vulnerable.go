package resolver

import (
	"maps"
	"slices"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/registry"
	"github.com/nubjs/nub/pm-go/internal/semver"
)

func IsVulnerable(name, version string, ranges map[string][]string) bool {
	v, err := semver.ParseVersion(version)
	if err != nil {
		return false
	}
	for _, raw := range ranges[name] {
		// The advisory matcher parses raw ranges; unlike dependency ranges,
		// an empty advisory is not normalized to a wildcard by the reference.
		if strings.TrimSpace(raw) == "" {
			continue
		}
		r, err := semver.ParseRange(raw)
		if err == nil && r.Contains(v) {
			return true
		}
	}
	return false
}

// PreferNonVulnerable ranks dated acceptable versions before undated versions.
// A known-too-new version remains excluded even when the fallback is vulnerable.
func PreferNonVulnerable(name string, p *registry.Packument, requested string, fallback *registry.Version, opts PickOptions, ranges map[string][]string) *registry.Version {
	if fallback == nil || !IsVulnerable(name, fallback.Version, ranges) {
		return fallback
	}
	r, err := semver.ParseRange(requested)
	if err != nil {
		return fallback
	}
	var best, undated *registry.Version
	var bestVersion, undatedVersion *semver.Version
	for _, raw := range slices.Sorted(maps.Keys(p.Versions)) {
		v, err := semver.ParseVersion(raw)
		if err != nil || !r.Contains(v) || IsVulnerable(name, raw, ranges) {
			continue
		}
		cutoff := opts.Cutoff
		if opts.AgeExempt != nil && opts.AgeExempt(raw, v) {
			cutoff = opts.ExemptCutoff
		}
		meta := p.Versions[raw]
		if ClassifyAge(p, raw, cutoff, true) == Clears {
			if outranks(v, meta, bestVersion, best, opts.Lowest) {
				best, bestVersion = meta, v
			}
		} else if _, dated := p.Time[raw]; !dated {
			if outranks(v, meta, undatedVersion, undated, opts.Lowest) {
				undated, undatedVersion = meta, v
			}
		}
	}
	if best != nil {
		return best
	}
	if undated != nil {
		return undated
	}
	return fallback
}
