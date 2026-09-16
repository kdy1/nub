package lockfile

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/overridesyntax"
	"github.com/nubjs/nub/pm-go/internal/semver"
)

// DirectOverrideRules matches importer pins. Ancestor selectors belong to the
// resolver and cannot match a direct dependency without an ancestor chain.
type DirectOverrideRules []directOverrideRule
type directOverrideRule struct {
	name        string
	requirement *string
	replacement string
}

func CompileDirectOverrides(raw map[string]string) DirectOverrideRules {
	var rules DirectOverrideRules
	for _, key := range slices.Sorted(maps.Keys(raw)) {
		parts, ok := overridesyntax.Split(key)
		if !ok || len(parts) != 1 {
			continue
		}
		name, requirement, ok := overridesyntax.ParseSegment(parts[0])
		if ok {
			rules = append(rules, directOverrideRule{name, requirement, raw[key]})
		}
	}
	slices.SortStableFunc(rules, func(a, b directOverrideRule) int {
		if (a.requirement == nil) == (b.requirement == nil) {
			return 0
		}
		if a.requirement != nil {
			return -1
		}
		return 1
	})
	return rules
}

func (rules DirectOverrideRules) Apply(name, rangeText string) *string {
	for _, rule := range rules {
		if rule.name == name && (rule.requirement == nil || overrideRangeCouldSatisfy(rangeText, *rule.requirement)) {
			return &rule.replacement
		}
	}
	return nil
}

// OverrideTarget also accepts ancestor chains when expanding catalog values.
func OverrideTarget(key string) (string, bool) {
	parts, ok := overridesyntax.Split(key)
	if !ok {
		return "", false
	}
	name, _, ok := overridesyntax.ParseSegment(parts[len(parts)-1])
	return name, ok
}

// This is the reference lower-bound probe, not a general range intersection.
// Unparseable ranges deliberately keep a user override eligible.
func overrideRangeCouldSatisfy(taskRange, requirement string) bool {
	r, err := semver.ParseEngineRange(requirement)
	if err != nil {
		return true
	}
	if v, err := semver.ParseEngineVersion(taskRange); err == nil && r.Contains(v) {
		return true
	}
	trimmed := strings.TrimSpace(taskRange)
	candidate := strings.TrimLeft(trimmed, "^~=>v ")
	if end := strings.IndexAny(candidate, " ,<|>"); end >= 0 {
		candidate = candidate[:end]
	}
	if candidate == "" || candidate[0] < '0' || candidate[0] > '9' {
		return true
	}
	v, err := semver.ParseEngineVersion(candidate)
	if err != nil {
		return true
	}
	if strings.HasPrefix(trimmed, ">") && !strings.HasPrefix(trimmed, ">=") {
		text := fmt.Sprintf("%d.%d.%d", v.Major(), v.Minor(), v.Patch()+1)
		if v.Prerelease() != "" {
			text += "-" + v.Prerelease()
		}
		if v.Metadata() != "" {
			text += "+" + v.Metadata()
		}
		v, err = semver.ParseEngineVersion(text)
		if err != nil {
			return true
		}
	}
	return r.Contains(v)
}
