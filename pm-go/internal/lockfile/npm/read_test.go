package npm

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/manifest"
)

func read(t *testing.T, body, pkg string) (*lockfile.Graph, []Warning) {
	t.Helper()
	if pkg == "" {
		pkg = "{}"
	}
	m, err := manifest.ParsePackage([]byte(pkg))
	if err != nil {
		t.Fatal(err)
	}
	g, warnings, err := Parse([]byte(body), m)
	if err != nil {
		t.Fatal(err)
	}
	return g, warnings
}

func TestStrictLockfileShapes(t *testing.T) {
	for _, field := range []string{
		`"dependencies":null`, `"dependencies":{"a":1}`, `"peerDependencies":[]`,
		`"bin":"bin.js"`, `"link":null`, `"hasInstallScript":1`, `"version":2`,
		`"peerDependenciesMeta":null`, `"peerDependenciesMeta":{"a":null}`, `"peerDependenciesMeta":{"a":{"optional":null}}`,
		`"license":1`, `"license":["MIT",null]`, `"license":{"type":[]}`, `"funding":["x",false]`,
		`"bundleDependencies":null`, `"bundleDependencies":true`, `"bundleDependencies":[],"bundledDependencies":[]`,
		`"workspaces":{}`, `"engines":false`,
	} {
		t.Run(field, func(t *testing.T) {
			_, _, err := Parse([]byte(`{"lockfileVersion":3,"packages":{"node_modules/a":{"version":"1.0.0",`+field+`}}}`), nil)
			if err == nil {
				t.Fatal("accepted invalid shape")
			}
		})
	}
	for _, body := range []string{`[]`, `{"packages":null}`, `{"lockfileVersion":3.0}`, `{"lockfileVersion":-1}`, `{"lockfileVersion":4294967296}`, `{"lockfileVersion":"3"}`, `{"dependencies":null}`, `{"dependencies":{"a":{"version":"1","requires":null}}}`} {
		if _, _, err := Parse([]byte(body), nil); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	for _, body := range []string{`{}`, `{"lockfileVersion":null}`, `{"lockfileVersion":0}`, `{"lockfileVersion":1}`, `{"lockfileVersion":2}`, `{"lockfileVersion":3}`, `{"lockfileVersion":10}`} {
		g, warnings := read(t, body, "")
		if len(g.Packages) != 0 || len(g.Importers) != 1 || len(warnings) != 0 {
			t.Fatalf("empty lock: %#v %v", g, warnings)
		}
	}
}

func TestPackageMetadata(t *testing.T) {
	g, _ := read(t, `{"lockfileVersion":3,"packages":{
		"":{"dependencies":{"alias":"npm:real@^1"}},
		"node_modules/alias":{"name":"real","version":"1.0.0","resolved":"https://registry.test/real.tgz","integrity":"sha512-x",
		"os":"linux","cpu":["x64",null,1,"arm64"],"libc":false,"engines":["node >=8"],"bin":{"run":"bin.js"},
		"license":[{},[{"type":"MIT"}],"ignored"],"funding":[{},"https://sponsor.test",{"url":"ignored"}],
		"deprecated":"use another","hasInstallScript":true,"hasShrinkwrap":true,"inBundle":true,"bundledDependencies":["bundled"],
		"peerDependencies":{"peer":"^2"},"peerDependenciesMeta":{"peer":{"optional":true}}}
	}}`, "")
	p := g.Packages["alias@1.0.0"]
	if p == nil || p.RegistryName() != "real" || p.Source != nil || value(p.TarballURL, "") != "https://registry.test/real.tgz" || value(p.Integrity, "") != "sha512-x" {
		t.Fatalf("source metadata: %#v", p)
	}
	if !slices.Equal(p.OS, []string{"linux"}) || !slices.Equal(p.CPU, []string{"x64", "arm64"}) || len(p.Libc) != 0 || len(p.Engines) != 0 {
		t.Fatalf("platform metadata: %#v", p)
	}
	if value(p.License, "") != "MIT" || value(p.FundingURL, "") != "https://sponsor.test" || p.Bin["run"] != "bin.js" || !p.HasInstallScript || !p.HasShrinkwrap || !p.InBundle || value(p.Deprecated, "") != "use another" || !slices.Equal(p.BundledDependencies, []string{"bundled"}) {
		t.Fatalf("package metadata: %#v", p)
	}
	if !p.PeerDependenciesMeta["peer"].Optional || p.PeerDependencies["peer"] != "^2" || len(p.Dependencies) != 0 {
		t.Fatal(p)
	}
	if len(g.RootDeps()) != 1 || value(g.RootDeps()[0].Specifier, "") != "npm:real@^1" {
		t.Fatal(g.RootDeps())
	}
}

func TestNestedPeerAndOptionalPlacement(t *testing.T) {
	g, _ := read(t, `{"lockfileVersion":3,"packages":{
		"":{"dependencies":{"a":"^1"},"devDependencies":{"a":"^1","b":"^1"},"optionalDependencies":{"b":"^1","opt":"^1"},"peerDependencies":{"peer":"^1","optpeer":"^1"},"peerDependenciesMeta":{"optpeer":{"optional":true}}},
		"node_modules/a":{"version":"1.0.0","dependencies":{"child":"^2","peer":"^2"},"optionalDependencies":{"opt":"^1","absent":"*"},"peerDependencies":{"peer":"^2","optpeer":"^1"},"peerDependenciesMeta":{"peer":{"optional":true},"optpeer":{"optional":true}}},
		"node_modules/a/node_modules/child":{"version":"2.0.0","dependencies":{"a":"*"}},
		"node_modules/a/node_modules/peer":{"version":"2.0.0"},
		"node_modules/b":{"version":"1.0.0","dependencies":{"child":"^1"},"peerDependencies":{"peer":"^1"}},
		"node_modules/child":{"version":"1.0.0"},"node_modules/peer":{"version":"1.0.0"},
		"node_modules/opt":{"version":"1.0.0"},"node_modules/optpeer":{"version":"1.0.0"}
	}}`, "")
	a, b := g.Packages["a@1.0.0"], g.Packages["b@1.0.0"]
	if !maps.Equal(a.Dependencies, map[string]string{"child": "2.0.0", "peer": "2.0.0", "opt": "1.0.0", "optpeer": "1.0.0"}) {
		t.Fatal(a.Dependencies)
	}
	if !maps.Equal(a.OptionalDependencies, map[string]string{"opt": "1.0.0", "optpeer": "1.0.0"}) {
		t.Fatal(a.OptionalDependencies)
	}
	if b.Dependencies["child"] != "1.0.0" || b.Dependencies["peer"] != "1.0.0" || g.Packages["child@2.0.0"].Dependencies["a"] != "1.0.0" {
		t.Fatal("nested resolution")
	}
	if _, exists := a.DeclaredDependencies["optpeer"]; exists {
		t.Fatal("peer written as a declared dependency")
	}
	deps := g.RootDeps()
	if len(deps) != 4 {
		t.Fatal(deps)
	}
	for i, want := range []struct {
		name string
		kind lockfile.DepType
	}{{"a", lockfile.Production}, {"b", lockfile.Dev}, {"opt", lockfile.Optional}, {"peer", lockfile.Production}} {
		if deps[i].Name != want.name || deps[i].Type != want.kind {
			t.Fatal(deps)
		}
	}
}

func TestWorkspaceLinksAndParentLookup(t *testing.T) {
	lock := `{"lockfileVersion":3,"packages":{
		"":{"workspaces":["packages/**","!packages/excluded/**"]},
		"node_modules/parent":{"link":true,"resolved":"packages/parent"},
		"node_modules/@scope/app":{"link":true,"resolved":"packages/parent/app"},
		"node_modules/local":{"link":true,"resolved":"vendor/local"},
		"node_modules/excluded":{"link":true,"resolved":"packages/excluded"},
		"packages/parent":{"name":"parent","version":"1.0.0"},
		"packages/parent/app":{"name":"@scope/app","dependencies":{"dep":"^2"},"peerDependencies":{"peer":"*"}},
		"packages/parent/node_modules/dep":{"version":"2.0.0"},
		"node_modules/dep":{"version":"1.0.0"},"node_modules/peer":{"version":"1.0.0"},
		"vendor/local":{"name":"local","version":"1.0.0"},"packages/excluded":{"name":"excluded"}
	}}`
	g, _ := read(t, lock, `{"workspaces":["vendor/*"]}`)
	if !slices.Equal(sorted(g.Importers), []string{".", "packages/parent", "packages/parent/app"}) {
		t.Fatal(sorted(g.Importers))
	}
	if len(g.RootDeps()) != 4 {
		t.Fatal(g.RootDeps())
	}
	for _, dep := range g.RootDeps() {
		if dep.Specifier != nil {
			t.Fatal("implicit root link has a specifier")
		}
	}
	deps := g.Importers["packages/parent/app"]
	if len(deps) != 2 || deps[0].DepPath != "dep@2.0.0" || deps[1].DepPath != "peer@1.0.0" {
		t.Fatal(deps)
	}
	key := (lockfile.Source{Kind: lockfile.Link, Path: "packages/parent/app"}).DepPath("@scope/app")
	if p := g.Packages[key]; p == nil || p.Version != "0.0.0" || p.Dependencies["dep"] != "2.0.0" {
		t.Fatal(p)
	}
	without := strings.Replace(lock, `"workspaces":["packages/**","!packages/excluded/**"]`, `"name":"root"`, 1)
	fallback, _ := read(t, without, `{"workspaces":["vendor/*"]}`)
	if !slices.Equal(sorted(fallback.Importers), []string{".", "vendor/local"}) {
		t.Fatal(sorted(fallback.Importers))
	}
	conservative, _ := read(t, without, "")
	if len(conservative.Importers) != 5 {
		t.Fatal(sorted(conservative.Importers))
	}
}

func TestPointerLinksAndPrefixedPaths(t *testing.T) {
	g, _ := read(t, `{"lockfileVersion":3,"packages":{
		"":{"dependencies":{"browserslist":"^1"}},
		"../../../project/node_modules/browserslist":{"version":"1.0.0","resolved":"https://host/node_modules/archive.tgz","dependencies":{"update":"^1"}},
		"../../../project/node_modules/browserslist/node_modules/update":{"version":"1.0.0","peerDependencies":{"browserslist":"^1"}},
		"../../../project/node_modules/browserslist/node_modules/browserslist":{"link":true,"resolved":"../../../project/node_modules/browserslist"}
	}}`, "")
	if len(g.Packages) != 2 || len(g.Importers) != 1 || len(g.RootDeps()) != 1 {
		t.Fatal(g)
	}
	if g.Packages["update@1.0.0"].Dependencies["browserslist"] != "1.0.0" || value(g.Packages["browserslist@1.0.0"].TarballURL, "") != "https://host/node_modules/archive.tgz" {
		t.Fatal(g.Packages)
	}
	for _, p := range g.Packages {
		if p.Source != nil {
			t.Fatalf("pointer treated as source: %#v", p)
		}
	}
	// Sorted original keys win a canonical-key collision, independently of
	// source JSON field order.
	dup, _ := read(t, `{"lockfileVersion":3,"packages":{"node_modules/a":{"version":"2"},"../p/node_modules/a":{"version":"1"}}}`, "")
	if dup.Packages["a@1"] == nil || len(dup.Packages) != 1 {
		t.Fatal(dup.Packages)
	}
}

func TestNonRegistrySources(t *testing.T) {
	g, _ := read(t, `{"lockfileVersion":3,"packages":{
		"":{"dependencies":{"archive":"https://host/archive.tgz","git":"github:owner/repo#abcdef0","file":"file:../FILE.TGZ","dir":"file:../local"}},
		"node_modules/archive":{"version":"1","resolved":"https://host/archive.tgz","integrity":"sha512-archive"},
		"node_modules/git":{"version":"1","resolved":"git+https://github.com/owner/repo.git#abcdef0&path:/sub"},
		"node_modules/file":{"version":"1","resolved":"file:../FILE.TGZ"},"node_modules/dir":{"version":"1","resolved":"file:../local"},
		"node_modules/unpinned":{"version":"1","resolved":"git+https://host/repo.git"}
	}}`, "")
	want := map[string]lockfile.SourceKind{"archive": lockfile.RemoteTarball, "git": lockfile.Git, "file": lockfile.Tarball, "dir": lockfile.Directory}
	for _, p := range g.Packages {
		if kind, ok := want[p.Name]; ok {
			if p.Source == nil || p.Source.Kind != kind || p.DepPath != p.Source.DepPath(p.Name) || p.TarballURL != nil {
				t.Fatalf("%s: %#v", p.Name, p)
			}
			if p.Name == "git" && (p.Source.Resolved != "abcdef0" || value(p.Source.Subpath, "") != "sub") {
				t.Fatal(p.Source)
			}
		} else if p.Source != nil {
			t.Fatal("unpinned git treated as locked")
		}
	}
}

func TestLegacyLifting(t *testing.T) {
	legacy := `{"dependencies":{
		"a":{"version":"1.0.0","requires":{"dep":"^2"},"dependencies":{"dep":{"version":"2.0.0"},"bundle":{"version":"1.0.0","bundled":true}}},
		"dep":{"version":"1.0.0"},"orphan":{"version":"1.0.0"},"dev":{"version":"1.0.0"},"opt":{"version":"1.0.0"}
	}}`
	g, warnings := read(t, legacy, `{"dependencies":{"a":"^1"},"devDependencies":{"dev":"*"},"optionalDependencies":{"opt":"*"}}`)
	if len(warnings) != 1 || warnings[0].Code != "WARN_AUBE_LOCKFILE_LEGACY_INCOMPLETE_GRAPH" || !strings.Contains(warnings[0].Message, "(orphan)") {
		t.Fatal(warnings)
	}
	a := g.Packages["a@1.0.0"]
	if !maps.Equal(a.Dependencies, map[string]string{"dep": "2.0.0", "bundle": "1.0.0"}) || !maps.Equal(a.DeclaredDependencies, map[string]string{"dep": "^2", "bundle": "*"}) || !slices.Equal(a.BundledDependencies, []string{"bundle"}) || !g.Packages["bundle@1.0.0"].InBundle {
		t.Fatal(a)
	}
	if len(g.RootDeps()) != 3 || g.RootDeps()[1].Type != lockfile.Dev || g.RootDeps()[2].Type != lockfile.Optional {
		t.Fatal(g.RootDeps())
	}
	if g.Packages["orphan@1.0.0"] == nil {
		t.Fatal("reader prematurely pruned legacy orphan")
	}
	v1, w1 := read(t, strings.Replace(legacy, `{`, `{"lockfileVersion":1,`, 1), `{"dependencies":{"a":"^1"},"devDependencies":{"dev":"*"},"optionalDependencies":{"opt":"*"}}`)
	if !reflect.DeepEqual(g, v1) || !reflect.DeepEqual(warnings, w1) {
		t.Fatal("versionless shrinkwrap differs from v1")
	}
}

func TestMalformedGraphEntries(t *testing.T) {
	for _, item := range []struct{ body, message string }{
		{`"node_modules/a":{}`, "package 'a' has no version"},
		{`"node_modules/a":{"link":true}`, "linked package 'a' has no resolved target"},
		{`"node_modules/a":{"link":true,"resolved":"absent"}`, "linked package 'a' points to missing target 'absent'"},
		{`"unknown":{"version":"1"}`, "could not determine package name for 'unknown'"},
	} {
		_, _, err := Parse([]byte(`{"lockfileVersion":3,"packages":{`+item.body+`}}`), nil)
		if err == nil || !strings.Contains(err.Error(), item.message) {
			t.Fatalf("%s: %v", item.body, err)
		}
	}
}

func TestReuseRepositoryNPMFixtures(t *testing.T) {
	// These existing conversion/frontdoor fixtures need no Rust executable.
	root := filepath.Join("..", "..", "..", "..")
	if _, err := os.Stat(filepath.Join(root, "vendor", "aube", "fixtures")); os.IsNotExist(err) {
		t.Skip("standalone module checkout: repository corpus unavailable")
	}
	for _, path := range []string{"vendor/aube/fixtures/import-npm/package-lock.json", "vendor/aube/fixtures/import-shrinkwrap/npm-shrinkwrap.json", "tests/conformance/frontdoor/fixtures/npm/package-lock.json"} {
		t.Run(path, func(t *testing.T) {
			full := filepath.Join(root, filepath.FromSlash(path))
			project, err := manifest.ReadPackage(filepath.Join(filepath.Dir(full), "package.json"))
			if err != nil {
				t.Fatal(err)
			}
			g, warnings, err := Read(full, project)
			if err != nil {
				t.Fatal(err)
			}
			if len(warnings) != 0 {
				t.Fatalf("unexpected warnings: %v", warnings)
			}
			if strings.Contains(path, "import-") {
				if len(g.Packages) != 4 || len(g.RootDeps()) != 3 || g.Packages["is-odd@3.0.1"].Dependencies["is-number"] != "6.0.0" {
					t.Fatal("existing import fixture graph differs")
				}
			} else if len(g.Packages) != 0 || len(g.RootDeps()) != 0 || len(g.Importers) != 1 {
				t.Fatal("empty frontdoor fixture graph differs")
			}
		})
	}
}

func TestResolveNestedAndPackageNames(t *testing.T) {
	paths := map[string]installInfo{"node_modules/x": {"x", "root"}, "test/node_modules/x": {"x", "parent"}, "node_modules/a/node_modules/x": {"x", "nested"}}
	for base, want := range map[string]string{"": "root", "test/app": "parent", "test/app/deep": "parent", "node_modules/a": "nested", "node_modules/a/node_modules/b": "nested", "node_modules/b": "root"} {
		got, ok := resolveNested(base, "x", paths)
		if !ok || got.depPath != want {
			t.Fatalf("%s: %+v", base, got)
		}
	}
	for path, want := range map[string]string{"node_modules/foo": "foo", "node_modules/@scope/foo": "@scope/foo", "node_modules/a/node_modules/@s/p/bin": "@s/p", "not-a-package": "", "node_modules/": "", "node_modules/@scope": ""} {
		got, ok := packageName(path)
		if got != want || ok != (want != "") {
			t.Fatalf("%s: %s %v", path, got, ok)
		}
	}
}

func TestTolerantMetadataButStrictNestedFunding(t *testing.T) {
	for _, license := range []string{`null`, `""`, `{}`, `{"type":null,"unknown":1}`, `[]`, `[{"type":null},"MIT"]`} {
		for _, funding := range []string{`null`, `""`, `{}`, `{"url":null}`, `[]`, `[{},[{"url":"ok"}]]`} {
			v, err := jsonvalue.Parse([]byte(fmt.Sprintf(`{"license":%s,"funding":%s}`, license, funding)))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := parsePackage(v); err != nil {
				t.Fatalf("%s %s: %v", license, funding, err)
			}
		}
	}
}
