package resolver

import (
	"fmt"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/semver"
)

// The zero policy has no exemptions, as required for minimumReleaseAgeExclude.
// Trust defaults are explicit so an age gate cannot inherit them accidentally.
type PackageVersionPolicy struct{ rules []versionPolicyRule }
type versionPolicyRule struct {
	name   string
	ranges []*semver.EngineRange
}
type VersionPolicyParseError struct {
	Pattern  string
	NameGlob bool
}

func (e *VersionPolicyParseError) Error() string {
	if e.NameGlob {
		return fmt.Sprintf("invalid exclude pattern `%s`: name patterns (`*`) cannot be combined with version unions", e.Pattern)
	}
	return fmt.Sprintf("invalid exclude pattern `%s`: version selectors must be valid semver ranges", e.Pattern)
}
func (e *VersionPolicyParseError) Code() string {
	if e.NameGlob {
		return "ERR_AUBE_TRUST_EXCLUDE_NAME_GLOB_WITH_VERSIONS"
	}
	return "ERR_AUBE_TRUST_EXCLUDE_INVALID_VERSION_UNION"
}
func ParseVersionPolicy(patterns []string) (PackageVersionPolicy, error) {
	policy := PackageVersionPolicy{}
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}
		rule, err := parseVersionPolicyRule(pattern)
		if err != nil {
			return PackageVersionPolicy{}, err
		}
		policy.rules = append(policy.rules, rule)
	}
	return policy, nil
}
func ParseVersionPolicyLossy(patterns []string) (PackageVersionPolicy, []error) {
	policy := PackageVersionPolicy{}
	var errs []error
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}
		rule, err := parseVersionPolicyRule(pattern)
		if err != nil {
			errs = append(errs, err)
		} else {
			policy.rules = append(policy.rules, rule)
		}
	}
	return policy, errs
}
func parseVersionPolicyRule(pattern string) (versionPolicyRule, error) {
	start := 0
	if strings.HasPrefix(pattern, "@") {
		start = 1
	}
	at := strings.IndexByte(pattern[start:], '@')
	if at < 0 {
		return versionPolicyRule{name: pattern}, nil
	}
	at += start
	rule := versionPolicyRule{name: pattern[:at]}
	if strings.Contains(rule.name, "*") {
		return rule, &VersionPolicyParseError{Pattern: pattern, NameGlob: true}
	}
	for _, chunk := range strings.Split(pattern[at+1:], "||") {
		trimmed := strings.TrimSpace(chunk)
		r, err := semver.ParseEngineRange(trimmed)
		if trimmed == "" || err != nil {
			return rule, &VersionPolicyParseError{Pattern: pattern}
		}
		rule.ranges = append(rule.ranges, r)
	}
	return rule, nil
}
func (p PackageVersionPolicy) Len() int { return len(p.rules) }
func (p PackageVersionPolicy) MatchesNameOnly(name string) bool {
	for _, rule := range p.rules {
		if rule.ranges == nil && policyNameMatches(rule.name, name) {
			return true
		}
	}
	return false
}
func (p PackageVersionPolicy) HasVersionedMatch(name string) bool {
	for _, rule := range p.rules {
		if rule.ranges != nil && policyNameMatches(rule.name, name) {
			return true
		}
	}
	return false
}
func (p PackageVersionPolicy) Excludes(name, version string) bool {
	v, err := semver.ParseEngineVersion(version)
	if err != nil {
		return p.MatchesNameOnly(name)
	}
	for _, rule := range p.rules {
		if !policyNameMatches(rule.name, name) {
			continue
		}
		if rule.ranges == nil {
			return true
		}
		for _, r := range rule.ranges {
			if r.Contains(v) {
				return true
			}
		}
	}
	return false
}
func policyNameMatches(pattern, name string) bool {
	if !strings.Contains(pattern, "*") {
		return pattern == name
	}
	var parts []string
	for _, part := range strings.Split(pattern, "*") {
		if part != "" {
			parts = append(parts, part)
		}
	}
	leading, trailing := strings.HasPrefix(pattern, "*"), strings.HasSuffix(pattern, "*")
	cursor := 0
	for i, part := range parts {
		window := name[cursor:]
		if i == 0 && !leading {
			if !strings.HasPrefix(window, part) {
				return false
			}
			cursor += len(part)
		} else if i == len(parts)-1 && !trailing {
			if !strings.HasSuffix(window, part) {
				return false
			}
			cursor = len(name)
		} else {
			index := strings.Index(window, part)
			if index < 0 {
				return false
			}
			cursor += index + len(part)
		}
	}
	return true
}

func DefaultTrustExcludes() PackageVersionPolicy {
	p, err := ParseVersionPolicy([]string{"@octokit/endpoint", "@hono/node-server@1.19.15", "chokidar", "eslint-config-prettier", "eslint-import-resolver-typescript", "nanoid", "react-redux", "reselect", "semver", "ua-parser-js", "undici", "undici-types", "vite"})
	if err != nil {
		panic(err)
	}
	return p
}
func TrustExcludesWithUserRules(user PackageVersionPolicy) PackageVersionPolicy {
	p := DefaultTrustExcludes()
	p.rules = append(p.rules, user.rules...)
	return p
}
