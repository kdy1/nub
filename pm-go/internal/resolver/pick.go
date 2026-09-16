package resolver

import (
	"regexp"
	"sort"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/registry"
	"github.com/nubjs/nub/pm-go/internal/semver"
)

type AgeVerdict string

const (
	Clears         AgeVerdict = ""
	TooNew         AgeVerdict = "too-new"
	Undeterminable AgeVerdict = "undeterminable"
)

type PickOptions struct {
	Locked               string
	Lowest, Strict       bool
	Cutoff, ExemptCutoff string
	AgeExempt            func(string, *semver.Version) bool
}

type PickResult struct {
	Version *registry.Version
	AgeGate AgeVerdict
}

// ClassifyAge keeps unknown age separate from a known recent publication.
// Modified can prove maturity only when the document contains no version times.
func ClassifyAge(p *registry.Packument, version, cutoff string, strict bool) AgeVerdict {
	if cutoff == "" {
		return Clears
	}
	if date, ok := p.Time[version]; ok {
		if date <= cutoff {
			return Clears
		}
		return TooNew
	}
	hasTimes := false
	for key := range p.Time {
		if key != "created" && key != "modified" {
			hasTimes = true
			break
		}
	}
	if !strict || !hasTimes && p.Modified != nil && *p.Modified <= cutoff {
		return Clears
	}
	return Undeterminable
}

var protocolRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)

func AliasRange(spec string) string {
	if rest, ok := strings.CutPrefix(spec, "npm:"); ok {
		if i := strings.LastIndexByte(rest, '@'); i > 0 {
			return rest[i+1:]
		}
		return "latest"
	}
	return spec
}

func Pick(p *registry.Packument, requested string, opts PickOptions) PickResult {
	requested = AliasRange(requested)
	classify := func(raw string, v *semver.Version) AgeVerdict {
		cutoff := opts.Cutoff
		if opts.AgeExempt != nil {
			if v == nil {
				v, _ = semver.ParseEngineVersion(raw)
			}
			if opts.AgeExempt(raw, v) {
				cutoff = opts.ExemptCutoff
			}
		}
		return ClassifyAge(p, raw, cutoff, opts.Strict)
	}
	r, err := semver.ParseDependencyRange(requested)
	if err != nil {
		if protocolRE.MatchString(requested) {
			return PickResult{}
		}
		effective, ok := p.Tags[requested]
		if !ok {
			if requested != "latest" {
				return PickResult{}
			}
			var highest *semver.Version
			for raw := range p.Versions {
				v, e := semver.ParseEngineVersion(raw)
				if e == nil && v.Prerelease() == "" && (highest == nil || v.GreaterThan(highest)) {
					highest, effective = v, raw
				}
			}
			if highest == nil {
				return PickResult{}
			}
		}
		v, e := semver.ParseEngineVersion(effective)
		if requested == "latest" && !opts.Lowest && opts.Cutoff != "" && classify(effective, v) != Clears && e == nil && v.Prerelease() == "" {
			effective = "<=" + effective
		}
		r, err = semver.ParseDependencyRange(effective)
		if err != nil {
			return PickResult{}
		}
	}
	eligible := func(raw string) *registry.Version {
		v, e := semver.ParseEngineVersion(raw)
		if e == nil && r.Contains(v) && classify(raw, v) == Clears {
			return p.Versions[raw]
		}
		return nil
	}
	if opts.Locked != "" {
		if v := eligible(opts.Locked); v != nil {
			return PickResult{Version: v}
		}
	}
	if !opts.Lowest {
		if v := eligible(p.Tags["latest"]); v != nil {
			return PickResult{Version: v}
		}
	}
	var best, fallback *registry.Version
	var bestVersion, fallbackVersion *semver.Version
	var cause AgeVerdict
	keys := make([]string, 0, len(p.Versions))
	for key := range p.Versions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, raw := range keys {
		v, e := semver.ParseEngineVersion(raw)
		if e != nil || !r.Contains(v) {
			continue
		}
		meta := p.Versions[raw]
		if ClassifyAge(p, raw, opts.ExemptCutoff, opts.Strict) == Clears && outranks(v, meta, fallbackVersion, fallback, true) {
			fallback, fallbackVersion = meta, v
		}
		switch verdict := classify(raw, v); verdict {
		case Clears:
			if outranks(v, meta, bestVersion, best, opts.Lowest) {
				best, bestVersion = meta, v
			}
		case TooNew:
			cause = TooNew
		case Undeterminable:
			if cause == Clears {
				cause = Undeterminable
			}
		}
	}
	if best != nil {
		return PickResult{Version: best}
	}
	if !opts.Strict && opts.Cutoff != "" && fallback != nil {
		return PickResult{Version: fallback}
	}
	return PickResult{AgeGate: cause}
}

func outranks(v *semver.Version, m *registry.Version, oldVersion *semver.Version, old *registry.Version, lowest bool) bool {
	if old == nil {
		return true
	}
	if (m.Deprecated == nil) != (old.Deprecated == nil) {
		return m.Deprecated == nil
	}
	if lowest {
		return v.LessThan(oldVersion)
	}
	return v.GreaterThan(oldVersion)
}
