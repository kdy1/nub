package workspace

import "testing"

func TestHoistGlobASCIIFoldingAndRanges(t *testing.T) {
	for _, c := range []struct {
		pattern, name string
		want          bool
	}{
		{"@SCOPE/*", "@scope/pkg", true}, {"*", "@scope/pkg", true},
		{"[a-z]*", "Foo", true}, {"[A-Z]*", "foo", true},
		{"[A-z]", "_", true}, {"[Z-a]", "_", true},
		{"[a-Z]", "q", true}, {"[A-9]", "a", false},
		{"[!a-z]", "F", false}, {"Ä*", "äfoo", false},
		{"**/foo", "@scope/FOO", true}, {"bad***", "badpkg", false},
	} {
		if got := MatchFold(c.pattern, c.name); got != c.want {
			t.Errorf("%s %s = %v", c.pattern, c.name, got)
		}
	}
}
