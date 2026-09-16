package lockfile

import (
	"maps"
	"slices"
	"testing"
)

func placements(tree []Placement) map[string]string {
	out := map[string]string{}
	for _, item := range tree {
		out[item.InstallPath()] = item.Key
	}
	return out
}
func TestHoistShadowAndCycles(t *testing.T) {
	canonical := map[string]*Package{}
	for _, p := range []*Package{NewPackage("a", "1"), NewPackage("x", "1"), NewPackage("x", "2"), NewPackage("b", "1"), NewPackage("c", "1")} {
		canonical[p.SpecKey()] = p
	}
	canonical["a@1"].Dependencies = map[string]string{"x": "2"}
	canonical["x@2"].Dependencies = map[string]string{"b": "1"}
	canonical["b@1"].Dependencies = map[string]string{"x": "1", "c": "1"}
	canonical["c@1"].Dependencies = map[string]string{"b": "1"}
	// Reserving b at root forces x@2's b to resolve through the root copy;
	// its own x@1 then also resolves at root.
	roots := []DirectDep{{Name: "a", DepPath: "a@1"}, {Name: "x", DepPath: "x@1"}, {Name: "b", DepPath: "b@1"}}
	got := placements(HoistTree(canonical, roots, nil))
	want := map[string]string{"node_modules/a": "a@1", "node_modules/x": "x@1", "node_modules/b": "b@1", "node_modules/c": "c@1", "node_modules/a/node_modules/x": "x@2"}
	if !maps.Equal(got, want) {
		t.Fatal(got)
	}
	// Force b@2 below x@2, where x@2 shadows root x@1. b@2 needs a new
	// x@1 of its own instead of incorrectly relying on root's x@1.
	canonical["b@2"] = NewPackage("b", "2")
	canonical["b@2"].Dependencies["x"] = "1"
	canonical["x@2"].Dependencies["b"] = "2"
	got = placements(HoistTree(canonical, roots, nil))
	if got["node_modules/a/node_modules/x/node_modules/b/node_modules/x"] != "x@1" {
		t.Fatal(got)
	}
}
func TestPreferredHoistsAndCanonicalKeys(t *testing.T) {
	canonical := map[string]*Package{}
	for _, p := range []*Package{NewPackage("a", "1"), NewPackage("b", "1"), NewPackage("x", "1"), NewPackage("x", "2"), NewPackage("stale", "1")} {
		canonical[p.SpecKey()] = p
	}
	canonical["a@1"].Dependencies["x"] = "x@1(peer@3)"
	canonical["b@1"].Dependencies["x"] = "2"
	roots := []DirectDep{{Name: "a", DepPath: "a@1(peer@3)"}, {Name: "b", DepPath: "b@1"}}
	got := placements(HoistTree(canonical, roots, map[string]string{"x": "x@2", "a": "stale@1", "stale": "stale@1"}))
	if got["node_modules/x"] != "x@2" || got["node_modules/a/node_modules/x"] != "x@1" || got["node_modules/a"] != "a@1" {
		t.Fatal(got)
	}
	if _, exists := got["node_modules/stale"]; exists {
		t.Fatal("unreachable preferred entry retained")
	}
	if ChildCanonicalKey("@s/p", "@s/p@2(abc)") != "@s/p@2" || DependencyVersion("@s/p", "@s/p@2(abc)") != "2" || CanonicalKey("@s/p@2(react@3)") != "@s/p@2" {
		t.Fatal("canonical key mismatch")
	}
	g := NewGraph()
	first, second := NewPackage("a", "1"), NewPackage("a", "1")
	g.Packages["a@1(z@2)"], g.Packages["a@1(a@2)"] = second, first
	if g.CanonicalPackages()["a@1"] != first {
		t.Fatal("canonical choice isn't sorted-first")
	}
}
func TestCanonicalReachabilityFlags(t *testing.T) {
	canonical := map[string]*Package{}
	for _, name := range []string{"prod", "dev", "opt", "shared", "peer", "declaredpeer"} {
		canonical[name+"@1"] = NewPackage(name, "1")
	}
	canonical["prod@1"].Dependencies = map[string]string{"opt": "1", "peer": "1", "declaredpeer": "1"}
	canonical["prod@1"].OptionalDependencies["opt"] = "1"
	canonical["prod@1"].PeerDependencies = map[string]string{"peer": "*", "declaredpeer": "*"}
	canonical["prod@1"].DeclaredDependencies = map[string]string{"declaredpeer": "*"}
	canonical["dev@1"].Dependencies["shared"] = "1"
	canonical["opt@1"].Dependencies["shared"] = "1"
	roots := []DirectDep{{Name: "prod", DepPath: "prod@1", Type: Production}, {Name: "dev", DepPath: "dev@1", Type: Dev}, {Name: "opt", DepPath: "opt@1", Type: Optional}}
	for _, tc := range []struct {
		exclude []DepType
		peers   bool
		want    []string
	}{
		{nil, true, []string{"declaredpeer@1", "dev@1", "opt@1", "peer@1", "prod@1", "shared@1"}},
		{[]DepType{Dev, Optional}, true, []string{"declaredpeer@1", "peer@1", "prod@1"}},
		{nil, false, []string{"declaredpeer@1", "dev@1", "opt@1", "prod@1", "shared@1"}},
		{[]DepType{Optional}, true, []string{"declaredpeer@1", "dev@1", "peer@1", "prod@1", "shared@1"}},
	} {
		if got := ReachableCanonical(canonical, roots, tc.exclude, tc.peers).Sorted(); !slices.Equal(got, tc.want) {
			t.Fatalf("%v peers=%v: %v", tc.exclude, tc.peers, got)
		}
	}
}
