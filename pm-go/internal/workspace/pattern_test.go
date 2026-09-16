package workspace

import (
	"reflect"
	"testing"
)

func TestMemberPatternDiscoverySemantics(t *testing.T) {
	for _, test := range []struct {
		path     string
		patterns []string
		want     bool
	}{
		{"packages/app", []string{"packages/*"}, true}, {"packages/a/b", []string{"packages/*"}, false},
		{"packages/app", []string{"./packages/*/"}, true}, {"apps/app", []string{"packages/../apps/*"}, true},
		{"packages/.hidden", []string{"packages/*"}, true}, {"Packages/app", []string{"packages/*"}, false},
		{"packages/app", []string{"./packages/**"}, false}, {"packages", []string{"packages/**"}, false},
		{"packages/a/b/leaf/x", []string{"packages/*/leaf/**"}, true},
		{"packages/a/b", []string{"packages/**"}, true}, {"packages/app/node_modules/a", []string{"**"}, false},
		{"packages/app", []string{"packages/*", "!./packages/app"}, true},
		{"packages/app", []string{"packages/*", "!packages/app"}, false},
		{"packages", []string{"packages", "!packages/**"}, false},
		{"packages/a/b", []string{"packages/**", "!packages/*"}, false},
		{"packages/a", []string{"!packages/a", "packages/*"}, false},
		{"apps/a", []string{"{packages,apps}/*"}, true},
		{"../sibling", []string{"../**"}, true}, {"packages/a", []string{"!other"}, false},
		{"packages/a", []string{"packages/**a"}, false},
	} {
		if got := MatchesMember(test.path, test.patterns); got != test.want {
			t.Errorf("%s %v: %t want %t", test.path, test.patterns, got, test.want)
		}
	}
}
func TestBraceExpansion(t *testing.T) {
	for input, want := range map[string][]string{"{a,b}/{c,d}": {"a/c", "a/d", "b/c", "b/d"}, "{a,{b,c}}/*": {"a/*", "b/*", "c/*"}, "{literal}": {"{literal}"}, "unclosed{a,b": {"unclosed{a,b"}, "{,a}": {"", "a"}} {
		if got := ExpandBraces(input); !reflect.DeepEqual(got, want) {
			t.Fatal(input, got, want)
		}
	}
}
func TestGlobCharacters(t *testing.T) {
	for _, test := range []struct {
		pattern, path   string
		separator, want bool
	}{
		{"a?c", "abc", true, true}, {"a?c", "a/c", true, false}, {"a?c", "a/c", false, true},
		{"[a-c]", "b", true, true}, {"[!a-c]", "z", true, true}, {"[!a-c]", "b", true, false},
		{"[]]", "]", true, true}, {"[[]", "[", true, true}, {"[z-a]", "b", false, false},
		{"**/x", "x", true, true}, {"**/x", "a/b/x", true, true}, {"**/x", "abx", false, false},
		{"a/**/x", "a/x", true, true}, {"***", "x", false, false}, {"a**", "abc", false, false},
		{"a/*/c", "a/b/d/c", false, true}, {"a/*/c", "a/b/d/c", true, false}, {"[abc", "a", false, false},
	} {
		if got := Match(test.pattern, test.path, test.separator); got != test.want {
			t.Fatal(test, got)
		}
	}
}
