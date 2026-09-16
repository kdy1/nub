package lockfile

import (
	"fmt"
	"maps"
	"slices"
	"strings"

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
		parts, ok := overrideSegments(key)
		if !ok || len(parts) != 1 {
			continue
		}
		name, requirement, ok := overrideSegment(parts[0])
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
	parts, ok := overrideSegments(key)
	if !ok {
		return "", false
	}
	name, _, ok := overrideSegment(parts[len(parts)-1])
	return name, ok
}

func overrideSegments(key string) ([]string, bool) {
	var pnpm, out []string
	start := 0
	for i := 0; i < len(key); i++ {
		if key[i] != '>' {
			continue
		}
		if i == 0 {
			return nil, false
		}
		if !strings.ContainsRune(" |@", rune(key[i-1])) {
			if start == i {
				return nil, false
			}
			pnpm = append(pnpm, key[start:i])
			start = i + 1
		}
	}
	if start >= len(key) {
		return nil, false
	}
	pnpm = append(pnpm, key[start:])
	for _, part := range pnpm {
		start = 0
		for i := 0; i < len(part); i++ {
			if part[i] != '/' {
				continue
			}
			current := part[start:i]
			scope := strings.HasPrefix(current, "@") && !strings.Contains(current[1:], "/")
			if !scope {
				if current == "" {
					return nil, false
				}
				out = append(out, current)
				start = i + 1
			}
		}
		if start == len(part) {
			return nil, false
		}
		out = append(out, part[start:])
	}
	return out, true
}
func overrideSegment(s string) (string, *string, bool) {
	if s == "**" {
		return "", nil, false
	}
	start := 0
	if strings.HasPrefix(s, "@") {
		slash := strings.IndexByte(s, '/')
		if slash < 0 || slash == len(s)-1 {
			return "", nil, false
		}
		start = slash + 1
	}
	if at := strings.IndexByte(s[start:], '@'); at >= 0 {
		if at == 0 || start+at == len(s)-1 {
			return "", nil, false
		}
		req := s[start+at+1:]
		return s[:start+at], &req, true
	}
	return s, nil, true
}

// This is the reference lower-bound probe, not a general range intersection.
// Unparseable ranges deliberately keep a user override eligible.
func overrideRangeCouldSatisfy(taskRange, requirement string) bool {
	r, err := semver.ParseRange(requirement)
	if err != nil {
		return true
	}
	if v, err := semver.ParseVersion(taskRange); err == nil && r.Contains(v) {
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
	v, err := semver.ParseVersion(candidate)
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
		v, err = semver.ParseVersion(text)
		if err != nil {
			return true
		}
	}
	return r.Contains(v)
}
