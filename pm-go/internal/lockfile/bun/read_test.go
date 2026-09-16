package bun

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
)

func TestWorkspaceAndNestedResolution(t *testing.T) {
	data := `{"lockfileVersion":2,"workspaces":{"":{"dependencies":{"p":"1"}},"packages/app":{"name":"@ws/app","dependencies":{"b":"2","missing":"1"},"peerDependencies":{"peer":"*","optional":"*"},"optionalPeers":["optional"]}},"packages":{"p":["p@1","",{"dependencies":{"b":"1","@s/c":"1"},"optionalDependencies":{"missing":"1"}}],"p/b":["b@1"],"p/@s/c":["@s/c@1"],"b":["b@3"],"@ws/app/b":["real@2"],"packages/app/b":["b@9"],"packages/peer":["peer@9"],"peer":["peer@1"],"optional":["optional@1"]}}`
	g, warnings, err := Parse([]byte(data), Options{})
	if err != nil || len(warnings) != 0 {
		t.Fatal(err, warnings)
	}
	p := g.Packages["p@1"]
	if p.Dependencies["b"] != "1" || p.Dependencies["@s/c"] != "1" || len(p.OptionalDependencies) != 0 {
		t.Fatal(p)
	}
	deps := g.Importers["packages/app"]
	if len(deps) != 2 || deps[0].DepPath != "b@2" || deps[1].DepPath != "peer@1" || *g.Packages["b@2"].AliasOf != "real" {
		t.Fatal(deps)
	}
	if g.WorkspaceExtraFields["packages/app"]["peerDependencies"] == nil {
		t.Fatal("lost workspace peers")
	}
}

func TestUnsupportedShadowAndOptionalPolicy(t *testing.T) {
	prefix := `{"lockfileVersion":1,"workspaces":{"":{"dependencies":{"p":"1"},"optionalDependencies":{"x":"exotic:x"}}},"packages":{"x":["x@exotic:x"],"b":["b@2"],"p/b":["b@exotic:b"],"p":["p@1",`
	for _, optional := range []bool{false, true} {
		field := "dependencies"
		if optional {
			field = "optionalDependencies"
		}
		data := []byte(prefix + `{"` + field + `":{"b":"exotic:b"}}]}}`)
		g, warnings, err := Parse(data, Options{})
		if !optional {
			var issue *UnsupportedSource
			if !errors.As(err, &issue) || issue.Protocol != "exotic" {
				t.Fatal(err)
			}
			continue
		}
		if err != nil || len(warnings) != 2 || len(g.Packages["p@1"].Dependencies) != 0 || g.SkippedOptionalDependencies["."]["x"] != "exotic:x" {
			t.Fatal(g, warnings, err)
		}
		g, _, err = Parse(data, Options{AllowUnsupportedSources: true})
		if err != nil || g.Packages["p@1"].Dependencies["b"] != "exotic:b" {
			t.Fatal(g, err)
		}
	}
	if _, _, err := Parse([]byte(`{"lockfileVersion":1,"packages":{"unused":["unused@exotic:x"]}}`), Options{}); err != nil {
		t.Fatal("unreferenced unsupported entry", err)
	}
}

func TestSourceClassificationAndRebasing(t *testing.T) {
	data := `{"lockfileVersion":1,"workspaces":{"plugin":{"name":"plugin"},"packages/app":{"name":"@w/app"}},"packages":{"plugin":["plugin@workspace:plugin"],"selector":["selector@workspace:*"],"@w/app/a":["a@file:../../vendor/a.tgz"],"@w/app/b":["b@file:vendor/b"],"g":["g@github:o/r#abc"],"h":["h@git+https://example/r.git#tag=abc&path=src"],"remote":["remote@https://example/a.tgz"]}}`
	g, _, err := Parse([]byte(data), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"plugin@workspace:plugin": "plugin", "selector@workspace:*": ".", "a@file:../../vendor/a.tgz": filepath.FromSlash("vendor/a.tgz"), "b@file:vendor/b": "vendor/b"} {
		if g.Packages[key].Source.Path != want {
			t.Fatal(key, g.Packages[key].Source)
		}
	}
	git := g.Packages["g@github:o/r#abc"].Source
	if git.Kind != lockfile.Git || git.URL != "https://github.com/o/r.git" || git.Resolved != "abc" || *git.Committish != "abc" {
		t.Fatal(git)
	}
	if *g.Packages["h@git+https://example/r.git#tag=abc&path=src"].Source.Subpath != "src" {
		t.Fatal("lost git subpath")
	}
}

func TestPreservedMetadataAndFirstResolvedEntry(t *testing.T) {
	data := `{"lockfileVersion":1,"configVersion":7,"trustedDependencies":["z","a","z"],"catalog":{"a":"1"},"catalogs":{"default":{"b":"2"}},"future":{"v":true},"packages":{"a":["a@1","https://private/a",{"dependencies":{"b":"^1"},"optionalDependencies":{"b":"~1"},"peerDependencies":{"c":"*"},"optionalPeers":["c"],"bin":"cli.js","future":7,"os":"linux"}],"b":["b@1"],"z/a":["a@1",{"dependencies":{"b":"2"}}],"z/a/b":["b@2"]}}`
	g, _, err := Parse([]byte(data), Options{})
	if err != nil {
		t.Fatal(err)
	}
	p := g.Packages["a@1"]
	if p.Dependencies["b"] != "1" || p.DeclaredDependencies["b"] != "~1" || p.Bin["a"] != "cli.js" || !p.PeerDependenciesMeta["c"].Optional || p.ExtraMeta["future"] == nil || *p.TarballURL != "https://private/a" {
		t.Fatal(p)
	}
	if *g.BunConfigVersion != 7 || len(g.TrustedDependencies) != 2 || g.TrustedDependencies[0] != "z" || len(g.Catalogs["default"]) != 1 || g.Catalogs["default"]["b"].Version != "2" || g.ExtraFields["future"] == nil {
		t.Fatal(g)
	}
	if _, ok := g.Importers["."]; !ok {
		t.Fatal("missing root")
	}
}

func TestScopedKeyWalk(t *testing.T) {
	for key, want := range map[string]string{"@a/b": "@a/b", "parent/@a/b": "@a/b", "@a/b/c": "c", "parent/foo": "foo"} {
		if aliasName(key) != want {
			t.Fatal(key)
		}
	}
	set := map[string]bool{"parent/x": true, "@a/x": true, "x": true}
	got, ok := resolveNested("parent/@a/b", "x", func(s string) bool { return set[s] })
	if !ok || got != "parent/x" {
		t.Fatal(got)
	}
	got, ok = resolveNested("@a/b", "x", func(s string) bool { return set[s] })
	if !ok || got != "x" {
		t.Fatal(got)
	}
}
