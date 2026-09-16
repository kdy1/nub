package yarn

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/manifest"
)

func TestClassicGraphAndPrivateURLs(t *testing.T) {
	data := `a@^1:
  version "1.2.0"
  resolved "https://private/a.tgz#sha"
  integrity ""
  dependencies:
    alias "npm:@real/b@2"
  optionalDependencies:
    ignored "1"
"alias@npm:@real/b@2":
  version "2.0.0"
  resolved "https://registry.yarnpkg.com/@real/b/-/b.tgz"
ignored@1:
  version "1.0.0"
local@file:dir:
  version "0.0.0"
`
	pj, _ := manifest.ParsePackage([]byte(`{"dependencies":{"a":"^1"},"devDependencies":{"local":"file:dir"}}`))
	g, warnings, err := ParseClassic(filepath.Join(t.TempDir(), "yarn.lock"), []byte(data), pj, Options{})
	if err != nil || len(warnings) != 0 {
		t.Fatal(err, warnings)
	}
	a := g.Packages["a@1.2.0"]
	if *a.TarballURL != "https://private/a.tgz" || a.Integrity == nil || *a.Integrity != "" || a.Dependencies["alias"] != "2.0.0" || len(a.Dependencies) != 1 || a.ExtraMeta["__aube_preserve_tarball_url"] == nil {
		t.Fatal(a)
	}
	if *g.Packages["alias@2.0.0"].AliasOf != "@real/b" || g.Packages["alias@2.0.0"].TarballURL != nil {
		t.Fatal(g)
	}
	deps := g.RootDeps()
	if len(deps) != 2 || deps[1].Type != lockfile.Dev || g.Packages[deps[1].DepPath].Source.Kind != lockfile.Directory {
		t.Fatal(deps)
	}
}
func TestClassicUnsupportedRequiredAndOptional(t *testing.T) {
	data := []byte("x@exotic:value:\n  version 1\n")
	for _, optional := range []bool{false, true} {
		field := "dependencies"
		if optional {
			field = "optionalDependencies"
		}
		pj, _ := manifest.ParsePackage([]byte(`{"` + field + `":{"x":"exotic:value"}}`))
		g, warnings, err := ParseClassic("yarn.lock", data, pj, Options{})
		if !optional {
			var issue *UnsupportedSource
			if !errors.As(err, &issue) || issue.Protocol != "exotic" {
				t.Fatal(err)
			}
			continue
		}
		if err != nil || len(warnings) != 1 || len(g.RootDeps()) != 0 || g.SkippedOptionalDependencies["."]["x"] != "exotic:value" {
			t.Fatal(g, warnings, err)
		}
		g, _, err = ParseClassic("yarn.lock", data, pj, Options{AllowUnsupportedSources: true})
		if err != nil || len(g.RootDeps()) != 1 || g.Packages["x@1"] == nil {
			t.Fatal(g, err)
		}
	}
}
func TestClassicWorkspaceDiscoveryAndSiblingLinks(t *testing.T) {
	dir := t.TempDir()
	for path, data := range map[string]string{
		"packages/a/package.json":       `{"name":"a","version":"1.2.0","dependencies":{"b":"^2","remote":"^1"}}`,
		"packages/b/package.json":       `{"name":"b","version":"2.3.0"}`,
		"packages/deep/c/package.json":  `{"name":"c","dependencies":{"a":"workspace:^"}}`,
		"packages/bad/package.json":     `{invalid`,
		"packages/.hidden/package.json": `{"name":"hidden"}`,
		"outside/package.json":          `{"name":"outside"}`,
	} {
		p := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	members := discoverMembers(dir, []string{"packages/*", "packages/**", "!packages/b", "."})
	want := []string{"packages/.hidden", "packages/a", "packages/b", "packages/bad", "packages/deep/c"}
	if !reflect.DeepEqual(members, want) {
		t.Fatal(members)
	}
	pj, _ := manifest.ParsePackage([]byte(`{"workspaces":["packages/**","!packages/b"],"dependencies":{"a":"^1","outside":"*"}}`))
	g, _, err := ParseClassic(filepath.Join(dir, "yarn.lock"), []byte("remote@^1:\n  version 1.9.0\n"), pj, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.RootDeps()) != 1 || g.RootDeps()[0].DepPath != "a@1.2.0" || g.Packages["a@1.2.0"] != nil {
		t.Fatal(g.RootDeps())
	}
	if got := g.Importers["packages/a"]; len(got) != 2 || got[0].DepPath != "b@2.3.0" || got[1].DepPath != "remote@1.9.0" {
		t.Fatal(got)
	}
	if got := g.Importers["packages/deep/c"]; len(got) != 1 || got[0].DepPath != "a@1.2.0" {
		t.Fatal(got)
	}
	if _, ok := g.Importers["packages/bad"]; ok {
		t.Fatal("invalid member read")
	}
}
