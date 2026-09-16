package resolver

import (
	"reflect"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/registry"
)

func TestPackageExtensionSelectors(t *testing.T) {
	for _, tc := range []struct {
		selector, name, version string
		want                    bool
	}{
		{"@scope/pkg@^1", "@scope/pkg", "1.2.3", true},
		{" @scope/pkg@^2 ", "@scope/pkg", "1.2.3", false},
		{"host@*", "host", "not-a-semver", true},
		{"host", "host", "whatever-ref", true},
		{"host@", "host", "1.0.0", false},
		{"host@ ", "host", "1.0.0", false},
		{"@scope/pkg", "@scope/pkg", "1.0.0", true},
		{"host@^1", "host", "1.0.0-beta.1", false},
		{"*", "host", "1.0.0", false},
		{"other@*", "host", "1.0.0", false},
	} {
		if got := PackageSelectorMatches(tc.selector, tc.name, tc.version); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}
func TestPackageExtensionsPreserveDeclarationsAndFirstExtension(t *testing.T) {
	p := &registry.Version{Name: "host", Version: "1.0.0", Dependencies: map[string]string{"declared": "^1"}, OptionalDependencies: map[string]string{"declared": "^2"}, PeerDependencies: map[string]string{"declared": "^3"}, PeerOptional: map[string]bool{"declared": false}}
	extensions := []PackageExtension{
		{Selector: "host@^1", Dependencies: map[string]string{"declared": "9", "added": "4"}, OptionalDependencies: map[string]string{"declared": "9", "optional": "5"}, PeerDependencies: map[string]string{"declared": "9", "peer": "6"}, PeerOptional: map[string]bool{"declared": true, "peer": true}},
		{Selector: "host", Dependencies: map[string]string{"added": "9", "later": "7"}},
		{Selector: "other", Dependencies: map[string]string{"wrong": "9"}},
	}
	ApplyPackageExtensions(p, extensions)
	if !reflect.DeepEqual(p.Dependencies, map[string]string{"declared": "^1", "added": "4", "later": "7"}) || p.OptionalDependencies["declared"] != "^2" || p.OptionalDependencies["optional"] != "5" || p.PeerDependencies["declared"] != "^3" || p.PeerDependencies["peer"] != "6" || p.PeerOptional["declared"] || !p.PeerOptional["peer"] {
		t.Fatal(p)
	}
	deps := ApplyLocalExtensions("host", "non-semver", nil, []PackageExtension{{Selector: "host@*", Dependencies: map[string]string{"runtime": "github:org/pkg#abc"}, OptionalDependencies: map[string]string{"optional": "1"}, PeerDependencies: map[string]string{"peer": "2"}}})
	if !reflect.DeepEqual(deps, map[string]string{"runtime": "github:org/pkg#abc"}) {
		t.Fatal(deps)
	}
	allowed := map[string]string{"old": "<2"}
	if !IsDeprecationAllowed("old", "1.9.0", allowed) || IsDeprecationAllowed("old", "2.0.0", allowed) || IsDeprecationAllowed("other", "1.0.0", allowed) {
		t.Fatal("deprecation policy")
	}
}
