package bun

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/manifest"
)

func TestNativeBunByteRoundTrip(t *testing.T) {
	original, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "vendor/aube/crates/aube-lockfile/tests/fixtures/bun-native.lock"))
	if err != nil {
		t.Fatal(err)
	}
	// Match the reference fixture test on Windows autocrlf checkouts.
	original = bytes.ReplaceAll(original, []byte("\r\n"), []byte("\n"))
	g, _, err := Parse(original, Options{})
	if err != nil {
		t.Fatal(err)
	}
	pj, err := manifest.ParsePackage([]byte(`{"name":"aube-lockfile-stability","version":"1.0.0","dependencies":{"chalk":"^4.1.2","picocolors":"^1.1.1","semver":"^7.6.3"}}`))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "bun.lock")
	if err := Write(path, g, pj); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("native bytes differ\n%s", got)
	}
}
func TestWorkspaceWriterAuthorityAndMetadata(t *testing.T) {
	g, _, err := Parse([]byte(`{"lockfileVersion":2,"configVersion":5,"workspaces":{"":{"name":"old","peerDependencies":{"x":"*"}},"packages/app":{"name":"old-app","version":"0","future":true}},"packages":{"app":["app@workspace:packages/app",{"dependencies":{"lib":"workspace:*","a":"^1"}}],"lib":["lib@workspace:packages/lib"],"a":["a@1"]}}`), Options{})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "packages/app"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "packages/app/package.json"), []byte(`{"name":"app","version":"2","bin":{"app":"cli.js"},"dependencies":{"a":"^1"},"peerDependenciesMeta":{"z":{"optional":true},"b":{"optional":true},"a":{"optional":false}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	pj, _ := manifest.ParsePackage([]byte(`{"name":"root","version":"8","bin":"hidden.js"}`))
	data, err := Encode(filepath.Join(dir, "bun.lock"), g, pj)
	if err != nil {
		t.Fatal(err)
	}
	r, err := parseRaw(data)
	if err != nil {
		t.Fatal(err)
	}
	if r.version != 1 || r.configVersion != 5 || r.workspaces[""].extra["name"].Text() != "root" || r.workspaces[""].extra["version"] != nil || r.workspaces[""].extra["bin"] != nil {
		t.Fatal(string(data))
	}
	ws := r.workspaces["packages/app"]
	if ws.extra["name"].Text() != "app" || ws.extra["version"].Text() != "2" || ws.extra["future"] == nil || inline(ws.extra["optionalPeers"]) != `["b", "z"]` {
		t.Fatal(string(data))
	}
	app, err := decodeEntry("app", r.packages["app"])
	if err != nil || app.meta.dependencies["lib"] != "workspace:*" || app.meta.dependencies["a"] != "^1" || len(r.packages["lib"]) != 1 {
		t.Fatal(string(data), err)
	}
}
func TestGitTupleFromResolvedAndRoundTrippedSources(t *testing.T) {
	sha := strings.Repeat("a", 40)
	for _, hosted := range []bool{false, true} {
		p := lockfile.NewPackage("alias", "1.0.0")
		real := "real"
		p.AliasOf = &real
		sri := "sha512-" + strings.Repeat("b", 88)
		p.Integrity = &sri
		p.Source = &lockfile.Source{Kind: lockfile.Git, URL: "https://github.com/owner/repo.git", Resolved: sha}
		if hosted {
			p.Source = &lockfile.Source{Kind: lockfile.RemoteTarball, URL: "https://codeload.github.com/owner/repo/tar.gz/" + sha, GitHosted: true, Integrity: &sri}
		}
		v := packageTuple(p, jsonvalue.Object())
		if len(v.Array) != 3 || v.Array[0].Text() != "real@github:owner/repo#aaaaaaa" || v.Array[2].Text() != "owner-repo-aaaaaaa" {
			t.Fatal(inline(v))
		}
		p.Version = "github:owner/repo#tag"
		v = packageTuple(p, jsonvalue.Object())
		if len(v.Array) != 4 || v.Array[0].Text() != "real@github:owner/repo#tag" || v.Array[2].Text() != "owner-repo-tag" || v.Array[3].Text() != sri {
			t.Fatal(inline(v))
		}
	}
}
func TestMetadataOrderAndCanonicalSourceKeys(t *testing.T) {
	g := lockfile.NewGraph()
	p := lockfile.NewPackage("gitpkg", "1.0.0")
	p.Source = &lockfile.Source{Kind: lockfile.Git, URL: "https://example/repo.git", Resolved: "tag"}
	p.DepPath = p.Source.DepPath(p.Name)
	p.PeerDependencies = map[string]string{"peer": "*"}
	p.PeerDependenciesMeta = map[string]lockfile.PeerMeta{"z": {Optional: true}}
	p.OS = []string{"linux"}
	p.CPU = []string{"x64"}
	p.Libc = []string{"glibc"}
	p.Bin = map[string]string{"": "", "bin": "cli.js"}
	p.ExtraMeta = map[string]*jsonvalue.Value{"future": jsonvalue.String("yes"), "__aube_preserve_tarball_url": jsonvalue.String("hidden")}
	g.Packages[p.DepPath] = p
	g.Importers["."] = []lockfile.DirectDep{{Name: p.Name, DepPath: p.DepPath}}
	g.TrustedDependencies = []string{"z", "a"}
	g.PatchedDependencies = map[string]string{"a@1": "patch"}
	g.Overrides = map[string]string{"a": "1"}
	g.Catalogs = map[string]map[string]lockfile.CatalogEntry{"default": {"a": {Specifier: "^1"}}, "named": {"b": {Specifier: "^2"}}, "empty": {}}
	g.ExtraFields = map[string]*jsonvalue.Value{"future": jsonvalue.String("ok"), "packages": jsonvalue.Null()}
	pj, _ := manifest.ParsePackage([]byte(`{}`))
	data, err := Encode("bun.lock", g, pj)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	last := -1
	for _, field := range []string{"trustedDependencies", "patchedDependencies", "overrides", "catalog", "catalogs", "future", "packages"} {
		idx := strings.Index(text, quote(field)+":")
		if idx <= last {
			t.Fatal(text)
		}
		last = idx
	}
	if !strings.Contains(text, `["gitpkg@git+https://example/repo.git#tag", { "peerDependencies": { "peer": "*" }, "optionalPeers": ["z"], "os": ["linux"], "cpu": ["x64"], "libc": ["glibc"], "bin": { "bin": "cli.js" }, "future": "yes" }, "repo-tag"]`) || strings.Contains(text, "__aube") {
		t.Fatal(text)
	}
}
func TestEmptyWriter(t *testing.T) {
	pj, _ := manifest.ParsePackage([]byte(`{}`))
	data, err := Encode("bun.lock", lockfile.NewGraph(), pj)
	want := "{\n  \"lockfileVersion\": 1,\n  \"configVersion\": 1,\n  \"workspaces\": {\n    \"\": {\n    },\n  },\n  \"packages\": {}\n}\n"
	if err != nil || string(data) != want {
		t.Fatal(string(data), err)
	}
}
