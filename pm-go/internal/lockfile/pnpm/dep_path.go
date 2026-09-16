// Package pnpm contains pnpm and nub.lock graph encoding primitives.
package pnpm

import (
	"maps"
	"slices"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/semver"
)

const PatchHashMarker = "(patch_hash="

func SplitDepPath(path string) (name, version string, ok bool) {
	s := strings.TrimPrefix(path, "/")
	start := 0
	if strings.HasPrefix(s, "@") {
		i := strings.IndexByte(s, '/')
		if i < 0 {
			return "", "", false
		}
		start = i + 1
	}
	i := strings.IndexByte(s[start:], '@')
	if i < 0 {
		return "", "", false
	}
	i += start
	version, _, _ = strings.Cut(s[i+1:], "(")
	return s[:i], version, true
}
func DepPathTail(path, name string) string { return strings.TrimPrefix(path, name+"@") }
func PeerlessPath(name, version string) string {
	head, _, _ := strings.Cut(version, "(")
	return name + "@" + head
}
func PeerlessAliasTarget(packages map[string]*lockfile.Package, path string) *lockfile.Package {
	name, version, ok := SplitDepPath(path)
	if !ok {
		return nil
	}
	return packages[name+"@"+version]
}
func StripPatchHash(s string) string {
	for {
		start := strings.Index(s, PatchHashMarker)
		if start < 0 {
			return s
		}
		end := strings.IndexByte(s[start:], ')')
		if end < 0 {
			return s
		}
		s = s[:start] + s[start+end+1:]
	}
}

func outerParens(s string) []string {
	var segments []string
	i := strings.IndexByte(s, '(')
	if i < 0 {
		return segments
	}
	for i < len(s) {
		if s[i] != '(' {
			i++
			continue
		}
		start, depth := i, 0
		for i < len(s) {
			switch s[i] {
			case '(':
				depth++
			case ')':
				depth--
			}
			i++
			if depth == 0 {
				segments = append(segments, s[start:i])
				break
			}
		}
		if depth != 0 {
			break
		}
	}
	return segments
}

// RewritePeerSuffix translates flat peer references, recursively preserving
// nested peer heads. A suffix with no balanced segment is kept verbatim and
// reported to warn; callers attach the reference malformed-peer warning code.
func RewritePeerSuffix(s string, translate func(string) (string, bool), warn func(string)) string {
	return rewrite(s, translate, warn, false)
}
func rewrite(s string, translate func(string) (string, bool), warn func(string), inner bool) string {
	i := strings.IndexByte(s, '(')
	if i < 0 {
		if inner {
			if replacement, ok := translate(s); ok {
				return replacement
			}
		}
		return s
	}
	segments := outerParens(s[i:])
	if len(segments) == 0 {
		if warn != nil {
			warn(s)
		}
		return s
	}
	var out strings.Builder
	out.WriteString(s[:i])
	for _, segment := range segments {
		out.WriteByte('(')
		out.WriteString(rewrite(segment[1:len(segment)-1], translate, warn, true))
		out.WriteByte(')')
	}
	return out.String()
}

// RegistryAlias identifies the unsupported registry-qualified version syntax,
// without confusing protocol selectors with registry aliases.
func RegistryAlias(version string) (string, bool) {
	name, v, ok := strings.Cut(version, ":")
	if !ok || name == "" || !asciiAlpha(name[0]) {
		return "", false
	}
	for i := 1; i < len(name); i++ {
		c := name[i]
		if !asciiAlpha(c) && !(c >= '0' && c <= '9') && c != '_' && c != '.' && c != '-' {
			return "", false
		}
	}
	if slices.Contains([]string{"bitbucket", "catalog", "custom", "file", "git", "github", "gitlab", "http", "https", "jsr", "link", "npm", "runtime", "ssh", "workspace"}, name) {
		return "", false
	}
	if _, err := semver.ParseVersion(v); err != nil {
		return "", false
	}
	return name, true
}
func asciiAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

type AliasRemap struct{ AliasPath, RealPath, AliasName, RealName string }

// RewriteAliases keeps peer suffixes while turning pnpm's real-name-prefixed
// alias edges into name-relative values, returning the packages to synthesize.
func RewriteAliases(deps map[string]string) []AliasRemap {
	var remaps []AliasRemap
	for _, name := range slices.Sorted(maps.Keys(deps)) {
		value := deps[name]
		bare, _, _ := strings.Cut(value, "(")
		real, version, ok := SplitDepPath(bare)
		if !ok || real == name {
			continue
		}
		suffix := ""
		if i := strings.IndexByte(value, '('); i >= 0 {
			suffix = value[i:]
		}
		remaps = append(remaps, AliasRemap{name + "@" + version + suffix, value, name, real})
		deps[name] = version + suffix
	}
	return remaps
}

func HostedGitTarball(raw string) bool {
	rest, ok := strings.CutPrefix(raw, "https://")
	if !ok {
		rest, ok = strings.CutPrefix(raw, "http://")
	}
	if !ok {
		return false
	}
	rest, _, _ = strings.Cut(rest, "?")
	rest, _, _ = strings.Cut(rest, "#")
	authority, path, _ := strings.Cut(rest, "/")
	if i := strings.LastIndexByte(authority, '@'); i >= 0 {
		authority = authority[i+1:]
	}
	host, _, _ := strings.Cut(authority, ":")
	switch strings.ToLower(host) {
	case "codeload.github.com", "npm.pkg.github.com":
		return true
	case "gitlab.com":
		return strings.Contains(path, "/-/archive/")
	case "bitbucket.org":
		return strings.Contains(path, "/get/")
	}
	return false
}
