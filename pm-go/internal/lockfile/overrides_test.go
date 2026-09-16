package lockfile

import "testing"

func TestDirectOverrideSpecificityAndChains(t *testing.T) {
	rules := CompileDirectOverrides(map[string]string{"plist": "9.9.9", "plist@<3": "2.0.0", "parent/plist": "never", "@scope/pkg@>=4": "5", "parent>only": "never", "**/only": "never", "only@": "never"})
	for _, tc := range []struct{ name, spec, want string }{
		{"plist", "^2.0.0", "2.0.0"}, {"plist", "^3.0.0", "9.9.9"}, {"@scope/pkg", "4.1.0", "5"}, {"@scope/pkg", "3.0.0", ""}, {"only", "1", ""},
	} {
		got := rules.Apply(tc.name, tc.spec)
		if got == nil && tc.want != "" || got != nil && *got != tc.want {
			t.Errorf("%s@%s = %v, want %s", tc.name, tc.spec, got, tc.want)
		}
	}
	for _, tc := range []struct{ key, want string }{
		{"parent/lodash@>=4", "lodash"}, {"parent@1>@scope/pkg@>1", "@scope/pkg"}, {"@scope/parent/@other/pkg", "@other/pkg"}, {"**/foo", "foo"}, {"pkg@>=1 <2", "pkg"}, {"pkg@>1.0.0", "pkg"}, {"parent>>child", ""}, {"parent/", ""}, {">foo", ""}, {"@scope/", ""},
	} {
		got, ok := OverrideTarget(tc.key)
		if got != tc.want || ok != (tc.want != "") {
			t.Errorf("target(%q) = %q, %v", tc.key, got, ok)
		}
	}
}
func TestOverrideLowerBoundProbe(t *testing.T) {
	for _, tc := range []struct {
		spec, req string
		want      bool
	}{
		{">3.0.5", "<3.0.5", false}, {">3.0.5", ">=3.0.6", true}, {"^4.0.0", "<3", false}, {"~2.1.0", "<3", true}, {"latest", "<3", true}, {"workspace:*", "<3", true}, {"1.x", "<0", true}, {"^1.0.0", "bad-range", true}, {">1.0.0-beta.1", ">=1.0.1-beta.1 <1.0.1", true},
	} {
		if got := overrideRangeCouldSatisfy(tc.spec, tc.req); got != tc.want {
			t.Errorf("%s vs %s = %v", tc.spec, tc.req, got)
		}
	}
}
