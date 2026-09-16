// Package spec contains shared dependency-specifier grammar.
package spec

import (
	"strings"
	"unicode/utf8"

	"github.com/nubjs/nub/pm-go/internal/semver"
)

// Split separates a package coordinate without confusing the scope marker
// with the version separator. The boolean distinguishes no spec from an empty
// explicit spec such as "pkg@".
func Split(input string) (name, version string, explicit bool) {
	start := 0
	if strings.HasPrefix(input, "@") {
		slash := strings.IndexByte(input, '/')
		if slash < 0 {
			return input, "", false
		}
		start = slash + 1
	}
	if at := strings.IndexByte(input[start:], '@'); at >= 0 {
		return input[:start+at], input[start+at+1:], true
	}
	return input, "", false
}

type Workspace struct{ Kind, Name, Range, Path string }

func ParseWorkspace(input string) (Workspace, bool) {
	tail, ok := strings.CutPrefix(input, "workspace:")
	if !ok {
		return Workspace{}, false
	}
	if strings.HasPrefix(tail, ".") {
		return Workspace{Kind: "path", Path: tail}, true
	}
	if tail != "" {
		first, size := utf8.DecodeRuneInString(tail)
		if first != '_' && first != '/' {
			if at := strings.IndexByte(tail[size:], '@'); at >= 0 {
				at += size
				return Workspace{Kind: "alias", Name: tail[:at], Range: tail[at+1:]}, true
			}
		}
	}
	return Workspace{Kind: "range", Range: tail}, true
}

// WorkspaceRangeBinds is used only after a local member was identified. A
// non-range tail can be a member directory locator; it never means a registry
// fallback. Bare tracking sigils accept even prerelease workspace versions.
func WorkspaceRangeBinds(version, rangeText string) bool {
	switch rangeText {
	case "", "*", "^", "~":
		return true
	}
	r, err := semver.ParseDependencyRange(rangeText)
	if err != nil {
		return true
	}
	v, err := semver.ParseEngineVersion(version)
	return err == nil && r.Contains(v)
}
