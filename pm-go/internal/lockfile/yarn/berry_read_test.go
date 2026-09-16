package yarn

import (
	"errors"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/manifest"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBerryDescriptorsAndPatches(t *testing.T) {
	if got := splitBerryHeader(" a@npm:1, b@npm:2,c@npm:3 "); !reflect.DeepEqual(got, []string{"a@npm:1", "b@npm:2,c@npm:3"}) {
		t.Fatal(got)
	}
	name, protocol, body, ok := parseBerrySpec("@s/a@git+ssh://example/repo#commit=one")
	if !ok || name != "@s/a" || protocol != "git+ssh" || body != "//example/repo#commit=one" {
		t.Fatal(name, protocol, body)
	}
	for _, tc := range []struct {
		body, path string
		count      int
	}{
		{"foo@npm%3A1#~builtin<compat/foo>", "", 0}, {"foo#optional!~/.yarn/a.patch&builtin<x>::hash=x&locator=y", ".yarn/a.patch", 1}, {"foo#a.patch&b.patch", "a.patch", 2}, {"foo#~a.patch", "a.patch", 1},
	} {
		if path, count := berryPatchPath(tc.body); path != tc.path || count != tc.count {
			t.Fatal(tc, path, count)
		}
	}
}

func TestBerryGraphResolutionPinsAndMetadata(t *testing.T) {
	data := []byte(`__metadata: {version: 8}
"a@npm:^1":
  version: 1.2.0
  resolution: "a@npm:1.2.0"
  checksum: abc
  dependencies: {b: "npm:^2"}
  optionalDependencies: {local: "file:./a.tgz#hash"}
  peerDependencies: {peer: 5}
  peerDependenciesMeta: {peer: {optional: true}, other: {optional: "true"}}
"b@npm:2.9.0":
  version: 2.9.0
  resolution: "b@npm:2.9.0"
"local@file:./a.tgz#hash":
  version: 0.0.0
  resolution: "local@file:./a.tgz#hash"
"patched@patch:patched@1#./patches/a.patch::locator=root":
  version: 1.0.0
  resolution: "patched@patch:patched@1#./patches/a.patch::hash=x"
`)
	pj, _ := manifest.ParsePackage([]byte(`{"dependencies":{"a":"^1","b":"^2","patched":"patch:patched@1#./patches/a.patch"},"resolutions":{"b":"2.9.0"}}`))
	g, warnings, err := Parse(filepath.Join(t.TempDir(), "yarn.lock"), data, pj, Options{})
	if err != nil || len(warnings) != 0 {
		t.Fatal(err, warnings)
	}
	a := g.Packages["a@1.2.0"]
	if a.Dependencies["b"] != "b@2.9.0" || len(a.Dependencies) != 1 || a.DeclaredDependencies["b"] != "npm:^2" || *a.YarnChecksum != "abc" || a.PeerDependencies["peer"] != "5" || !a.PeerDependenciesMeta["peer"].Optional || a.PeerDependenciesMeta["other"].Optional {
		t.Fatal(a)
	}
	if g.Packages[a.OptionalDependencies["local"]].Source.Kind != lockfile.Tarball || g.PatchedDependencies["patched@1.0.0"] != "./patches/a.patch" {
		t.Fatal(g)
	}
	if deps := g.RootDeps(); len(deps) != 3 || deps[1].DepPath != "b@2.9.0" || deps[0].Specifier != nil {
		t.Fatal(deps)
	}
	g, _, err = Parse("yarn.lock", data, pj, Options{Overrides: map[string]string{}})
	if err != nil || len(g.RootDeps()) != 2 || len(g.Packages["a@1.2.0"].Dependencies) != 0 {
		t.Fatal(g, err)
	}
}
func TestBerryUnsupportedSourcePolicy(t *testing.T) {
	data := []byte("__metadata: {version: 8}\nx@exotic:value: {version: 1, resolution: 'x@exotic:value'}\n")
	for _, optional := range []bool{false, true} {
		field := "dependencies"
		if optional {
			field = "optionalDependencies"
		}
		pj, _ := manifest.ParsePackage([]byte(`{"` + field + `":{"x":"exotic:value"}}`))
		g, warnings, err := Parse("yarn.lock", data, pj, Options{})
		if !optional {
			var issue *UnsupportedSource
			if !errors.As(err, &issue) || issue.Protocol != "exotic" {
				t.Fatal(err)
			}
		} else if err != nil || len(warnings) != 1 || g.SkippedOptionalDependencies["."]["x"] != "exotic:value" {
			t.Fatal(g, warnings, err)
		}
		g, warnings, err = Parse("yarn.lock", data, pj, Options{AllowUnsupportedSources: true})
		if err != nil || len(warnings) != 1 || len(g.Packages) != 0 || len(g.RootDeps()) != 0 {
			t.Fatal(g, warnings, err)
		}
	}
}
