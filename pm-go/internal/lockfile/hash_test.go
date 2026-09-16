package lockfile

import (
	"reflect"
	"testing"
)

func hashFixture() *Graph {
	g := NewGraph()
	a := NewPackage("a", "1.0.0")
	b := NewPackage("b", "2.0.0")
	c := NewPackage("c", "3.0.0")
	a.Dependencies["b"] = b.DepPath
	b.Dependencies["a"] = "1.0.0"
	a.Dependencies["missing"] = "1"
	g.Packages[a.DepPath] = a
	g.Packages[b.DepPath] = b
	g.Packages[c.DepPath] = c
	g.Importers["."] = []DirectDep{{Name: "a", DepPath: a.DepPath, Specifier: new("^1")}, {Name: "c", DepPath: c.DepPath, Type: Dev, Specifier: new("3")}}
	return g
}
func TestGraphHashIdentityAndCycles(t *testing.T) {
	g := hashFixture()
	before := g.IdentityHash(nil)
	other := g.Clone()
	other.Importers["."][0], other.Importers["."][1] = other.Importers["."][1], other.Importers["."][0]
	if other.IdentityHash(nil) != before {
		t.Fatal("importer order changed identity")
	}
	other.Packages["a@1.0.0"].Dependencies["b"] = "2.0.0"
	if other.IdentityHash(nil) != before {
		t.Fatal("equivalent edge representation changed identity")
	}
	other.Packages["b@2.0.0"].Integrity = new("sha512-changed")
	if other.IdentityHash(nil) == before {
		t.Fatal("cyclic child content ignored")
	}
	for _, change := range []func(*Graph){
		func(g *Graph) { g.Importers["."][0].Type = Optional }, func(g *Graph) { g.Importers["."][0].Specifier = new("~1") }, func(g *Graph) { g.Importers["."][0].Specifier = nil },
	} {
		other = g.Clone()
		change(other)
		if other.IdentityHash(nil) == before {
			t.Fatal("importer intent ignored")
		}
	}
}
func TestGraphHashEnginePatchAndContent(t *testing.T) {
	g := hashFixture()
	plain := g.ComputeHashes(HashOptions{})
	options := HashOptions{Engine: new("linux-x64-node22"), AllowBuild: func(p *Package) bool { return p.Name == "b" }}
	built := g.ComputeHashes(options)
	if plain["a@1.0.0"] == built["a@1.0.0"] || plain["b@2.0.0"] == built["b@2.0.0"] || plain["c@3.0.0"] != built["c@3.0.0"] {
		t.Fatal(plain, built)
	}
	options.Engine = nil
	if !reflect.DeepEqual(plain, g.ComputeHashes(options)) {
		t.Fatal("host-independent hashes changed with build permission")
	}
	options.Patch = func(name, version string) *string {
		if name == "b" {
			return new("hash")
		}
		return nil
	}
	patched := g.ComputeHashes(options)
	if plain["a@1.0.0"] == patched["a@1.0.0"] {
		t.Fatal("patch did not propagate")
	}
	options = HashOptions{Content: func(key string) *string {
		if key == "b@2.0.0" {
			return new("materialized")
		}
		return nil
	}}
	if g.ComputeHashes(options)["a@1.0.0"] == plain["a@1.0.0"] {
		t.Fatal("materialized content did not propagate")
	}
	if got := (GraphHashes{"@s/a@1": "0123456789abcdef1234"}).DepPath("@s/a@1"); got != "@s/a@1-0123456789abcdef" {
		t.Fatal(got)
	}
}
func TestContentAffectedIncludesCyclicAncestors(t *testing.T) {
	g := hashFixture()
	g.Packages["b@2.0.0"].Source = &Source{Kind: Git, URL: "https://example/repo.git", Resolved: "commit"}
	if got := g.ContentAffected().Sorted(); !reflect.DeepEqual(got, []string{"a@1.0.0", "b@2.0.0"}) {
		t.Fatal(got)
	}
}
