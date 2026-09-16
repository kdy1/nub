package resolver

import (
	"maps"
	"slices"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/overridesyntax"
	"github.com/nubjs/nub/pm-go/internal/semver"
)

type OverrideSegment struct {
	Name        string
	Requirement *string
}
type OverrideRule struct {
	Parents             []OverrideSegment
	Target              OverrideSegment
	Replacement, RawKey string
}
type OverrideRules []OverrideRule

func CompileOverrides(raw map[string]string) OverrideRules {
	var rules OverrideRules
	for _, key := range slices.Sorted(maps.Keys(raw)) {
		parts, ok := overridesyntax.Split(key)
		if !ok {
			continue
		}
		var segments []OverrideSegment
		for _, part := range parts {
			if part == "**" {
				segments = append(segments, OverrideSegment{})
				continue
			}
			name, req, valid := overridesyntax.ParseSegment(part)
			if !valid {
				ok = false
				break
			}
			segments = append(segments, OverrideSegment{Name: name, Requirement: req})
		}
		if !ok || len(segments) == 0 || segments[len(segments)-1].Name == "" {
			continue
		}
		rules = append(rules, OverrideRule{Parents: segments[:len(segments)-1], Target: segments[len(segments)-1], Replacement: raw[key], RawKey: key})
	}
	return rules
}
func (rule OverrideRule) Matches(name, requested string, ancestors []AncestorFrame) bool {
	if rule.Target.Name != name {
		return false
	}
	if rule.Target.Requirement != nil && !overrideRangesOverlap(requested, *rule.Target.Requirement) {
		return false
	}
	return matchAncestors(rule.Parents, ancestors)
}
func matchAncestors(parents []OverrideSegment, ancestors []AncestorFrame) bool {
	if len(parents) == 0 {
		return true
	}
	last := parents[len(parents)-1]
	rest := parents[:len(parents)-1]
	if last.Name == "" {
		for take := 0; take <= len(ancestors); take++ {
			if matchAncestors(rest, ancestors[:len(ancestors)-take]) {
				return true
			}
		}
		return false
	}
	if len(ancestors) == 0 {
		return false
	}
	frame := ancestors[len(ancestors)-1]
	if last.Name != frame.Name {
		return false
	}
	if last.Requirement != nil {
		v, ev := semver.ParseEngineVersion(frame.Version)
		r, er := semver.ParseEngineRange(*last.Requirement)
		if ev != nil || er != nil || !r.Contains(v) {
			return false
		}
	}
	return matchAncestors(rest, ancestors[:len(ancestors)-1])
}
func overrideRangesOverlap(requested, requirement string) bool {
	selector, err := semver.ParseEngineRange(requirement)
	if err != nil {
		return true
	}
	if v, err := semver.ParseEngineVersion(requested); err == nil {
		return selector.Contains(v)
	}
	declared, err := semver.ParseEngineRange(requested)
	return err != nil || declared.AllowsAny(selector)
}
func stripTaskAlias(requested string) string {
	for _, prefix := range []string{"npm:", "jsr:"} {
		if rest, ok := strings.CutPrefix(requested, prefix); ok {
			if at := strings.LastIndexByte(rest, '@'); at > 0 {
				return rest[at+1:]
			}
			return rest
		}
	}
	return requested
}
func (rules OverrideRules) Pick(name, requested string, ancestors []AncestorFrame) *string {
	requested = stripTaskAlias(requested)
	bestScore := -1
	var replacement *string
	for _, rule := range rules {
		if !rule.Matches(name, requested, ancestors) {
			continue
		}
		score := 0
		for _, parent := range rule.Parents {
			if parent.Name != "" {
				score += 2
			}
		}
		if rule.Target.Requirement != nil {
			score++
		}
		// Iterator::max_by_key retains the last equal maximum in the Rust
		// implementation; the compiled order is lexicographic by raw key.
		if score >= bestScore {
			bestScore = score
			replacement = new(rule.Replacement)
		}
	}
	return replacement
}
