package pnpm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
)

func graph(t *testing.T, s string) *lockfile.Graph {
	t.Helper()
	g, _, err := Parse([]byte(s), Options{})
	if err != nil {
		t.Fatal(err)
	}
	return g
}
func TestGraphImportersAndAliases(t *testing.T) {
	g := graph(t, `lockfileVersion: '9.0'
importers:
  '':
    dependencies:
      fork: {specifier: 'catalog:', version: 'real@1.0.0(peer@2.0.0)'}
    devDependencies:
      tool: {specifier: '*', version: 2.0.0}
    optionalDependencies:
      optional: {specifier: '^3', version: 3.0.0}
    skippedOptionalDependencies:
      skip: {specifier: '^4', version: 4.0.0}
  '.': {dependencies: {ignored: {specifier: '*', version: 1.0.0}}}
packages:
  real@1.0.0: {peerDependencies: {peer: '^2'}, hasBin: true}
  tool@2.0.0: {}
  optional@3.0.0: {}
snapshots:
  real@1.0.0(peer@2.0.0): {dependencies: {peer: 2.0.0}}
  tool@2.0.0: {}
  optional@3.0.0: {optional: true}
`)
	if len(g.RootDeps()) != 3 || g.RootDeps()[0].Name != "fork" || g.RootDeps()[1].Type != lockfile.Dev || g.RootDeps()[2].Type != lockfile.Optional {
		t.Fatalf("importers: %+v", g.Importers)
	}
	fork := g.Packages["fork@1.0.0(peer@2.0.0)"]
	if fork == nil || *fork.AliasOf != "real" || fork.Dependencies["peer"] != "2.0.0" || len(fork.Bin) != 1 {
		t.Fatalf("alias: %+v", fork)
	}
	if g.Packages["real@1.0.0(peer@2.0.0)"] != nil {
		t.Fatal("unreferenced canonical alias source survived")
	}
	if g.SkippedOptionalDependencies["."]["skip"] != "^4" {
		t.Fatal("skipped optional lost")
	}
}
func TestGraphAliasRetainsRealEdges(t *testing.T) {
	g := graph(t, `lockfileVersion: '9.0'
importers: {'.': {dependencies: {fork: {specifier: 'npm:real@1', version: 'real@1.0.0'}}}}
packages: {real@1.0.0: {}, parent@1.0.0: {}}
snapshots: {real@1.0.0: {}, parent@1.0.0: {optionalDependencies: {real: 1.0.0}}}
`)
	if g.Packages["real@1.0.0"] == nil || g.Packages["fork@1.0.0"] == nil {
		t.Fatal("live real entry dropped")
	}
}
func TestGraphLocalRebaseAndAlias(t *testing.T) {
	g := graph(t, `lockfileVersion: '9.0'
importers:
  packages/app:
    dependencies:
      lib: {specifier: '^1', version: 'link:../lib'}
      file: {specifier: 'file:../../vendor/file', version: 'file:../../vendor/file'}
      archive: {specifier: 'file:../../archive.tgz', version: 'file:../../archive.tgz'}
      alias: {specifier: 'npm:file@*', version: 'file@file:../../vendor/file'}
packages:
  file@file:vendor/file: {version: 1.2.3, resolution: {directory: ../../vendor/file}}
snapshots:
  file@file:../../vendor/file: {dependencies: {child: 2.0.0}, optionalDependencies: {opt: 3.0.0}}
  child@2.0.0: {}
  opt@3.0.0: {}
`)
	paths := map[string]string{"lib": "packages/lib", "file": "vendor/file", "archive": "archive.tgz", "alias": "vendor/file"}
	for _, d := range g.Importers["packages/app"] {
		p := g.Packages[d.DepPath]
		if p == nil || p.Source == nil || p.Source.PathPOSIX() != paths[d.Name] {
			t.Fatalf("%s: %+v", d.Name, p)
		}
		if d.Name == "file" || d.Name == "alias" {
			if p.Dependencies["child"] != "2.0.0" || p.OptionalDependencies["opt"] != "3.0.0" {
				t.Fatal("local snapshot lost")
			}
		}
		if d.Name == "archive" && p.Source.Kind != lockfile.Tarball {
			t.Fatal("archive classified as directory")
		}
	}
}
func TestGraphRemoteAndPeerCanonicalization(t *testing.T) {
	const url = "https://example.test/pkg.tgz"
	g := graph(t, `lockfileVersion: '9.0'
importers: {'.': {dependencies: {parent: {specifier: '*', version: '1.0.0(remote@https://example.test/pkg.tgz)'}}}}
packages:
  parent@1.0.0: {}
  remote@https://example.test/pkg.tgz: {version: 2.1.0, resolution: {tarball: https://example.test/pkg.tgz, integrity: sha512-test}}
snapshots:
  parent@1.0.0(remote@https://example.test/pkg.tgz): {dependencies: {remote: https://example.test/pkg.tgz}}
  remote@https://example.test/pkg.tgz: {}
`)
	key := (lockfile.Source{Kind: lockfile.RemoteTarball, URL: url}).DepPath("remote")
	p := g.Packages[key]
	if p == nil || p.Version != "2.1.0" || p.Source == nil || p.ExtraMeta["__aube_preserve_tarball_url"] == nil {
		t.Fatalf("remote package %+v", p)
	}
	parent := "parent@1.0.0(" + key + ")"
	if g.Packages[parent] == nil || g.RootDeps()[0].DepPath != parent {
		t.Fatal("peer was not rekeyed")
	}
	if child, ok := g.Child("remote", g.Packages[parent].Dependencies["remote"]); !ok || child != key {
		t.Fatal("remote edge mismatch")
	}
}
func TestGraphDirectRemoteUsesRecordedVersion(t *testing.T) {
	g := graph(t, `lockfileVersion: '9.0'
importers: {'.': {dependencies: {remote: {specifier: 'https://example.test/pkg.tgz', version: 'https://example.test/pkg.tgz(peer@1.0.0)'}}}}
packages:
  remote@https://example.test/pkg.tgz: {version: 2.0.0, resolution: {tarball: https://example.test/pkg.tgz, integrity: sha512-test}}
snapshots:
  remote@https://example.test/pkg.tgz(peer@1.0.0): {dependencies: {child: 1.0.0}}
  child@1.0.0: {}
`)
	p := g.Packages[g.RootDeps()[0].DepPath]
	if p == nil || p.Version != "2.0.0" || p.Source.URL != "https://example.test/pkg.tgz" || p.Dependencies["child"] != "1.0.0" || len(g.Packages) != 2 {
		t.Fatalf("direct remote: %+v", p)
	}
}
func TestGraphHeaderPatchesAndRuntimeMetadata(t *testing.T) {
	g := graph(t, `lockfileVersion: '9.0'
settings: {autoInstallPeers: false, excludeLinksFromLockfile: true, lockfileIncludeTarballUrl: true}
overrides: {a: '^1'}
packageExtensionsChecksum: sha256-extensions
pnpmfileChecksum: sha256-hook
ignoredOptionalDependencies: [skip, skip]
catalogs: {default: {a: {specifier: '^1', version: 1.0.0}}}
time: {a@1.0.0: '2026-01-01'}
patchedDependencies: {a@1.0.0: hash, b@1.0.0: {path: b.patch, hash: hash2}, c@1.0.0: {path: c.patch}}
importers:
  .:
    dependencies: {a: {specifier: '^1', version: '1.0.0(patch_hash=hash)'}}
    devDependencies: {node: {specifier: 'runtime:^24', version: 'runtime:24.4.1'}}
packages:
  a@1.0.0(patch_hash=hash): {}
  node@runtime:24.4.1:
    hasBin: true
    resolution:
      type: variations
      variants:
        - targets: [{os: linux, cpu: x64, libc: musl}]
          resolution: {type: binary, url: 'https://node.test/node.tgz', integrity: sha256-example, bin: bin/node}
snapshots: {a@1.0.0(patch_hash=hash): {}, node@runtime:24.4.1: {}}
`)
	if len(g.RootDeps()) != 1 || g.RootDeps()[0].DepPath != "a@1.0.0" || len(g.Packages) != 1 {
		t.Fatal("patch/runtime package keys")
	}
	if g.Settings.AutoInstallPeers || !g.Settings.ExcludeLinksFromLockfile || !g.Settings.IncludeTarballURL {
		t.Fatal("settings")
	}
	if g.PatchedDependencyHashes["a@1.0.0"] != "hash" || g.PatchedDependencies["b@1.0.0"] != "" || g.PatchedDependencies["c@1.0.0"] != "c.patch" {
		t.Fatal("patch metadata")
	}
	pin := g.Runtimes["node"]
	if pin.Specifier != "^24" || !pin.Dev || !pin.HasBin || len(pin.Variants) != 1 {
		t.Fatalf("runtime %+v", pin)
	}
	v := pin.Variants[0]
	if v.Archive != "tarball" || !v.BinIsBareString || v.Bin["node"] != "bin/node" || *v.Targets[0].Libc != "musl" {
		t.Fatal("runtime variant")
	}
}
func TestGraphRejectsUnsupportedAndIncompleteSources(t *testing.T) {
	for _, s := range []string{
		"lockfileVersion: '6.0'\n", "lockfileVersion: '9.invalid'\n", "lockfileVersion: null\n",
		"lockfileVersion: '9.0'\ndependencies: {}\n", "lockfileVersion: '9.0'\npackages: {'/a@1.0.0': {}}\n",
		"lockfileVersion: '9.0'\npackages: {'a@work:1.0.0': {}}\n",
		"lockfileVersion: '9.0'\nimporters: {'.': {dependencies: {fork: {specifier: 'npm:a@*', version: 'a@1.0.0'}}}}\n",
	} {
		if _, _, err := Parse([]byte(s), Options{}); err == nil {
			t.Errorf("accepted %s", s)
		}
	}
	body := "lockfileVersion: '9.0'\npackages: {'a@https://example.test/a.tgz': {resolution: {tarball: 'https://example.test/a.tgz'}}}\n"
	if _, _, err := Parse([]byte(body), Options{}); err == nil {
		t.Fatal("missing remote integrity accepted")
	}
	if _, _, err := Parse([]byte(body), Options{AllowMissingIntegrity: true}); err != nil {
		t.Fatal(err)
	}
	graph(t, strings.ReplaceAll(body, "example.test/a.tgz", "codeload.github.com/a/b/tar.gz/sha"))
}
func TestExistingPnpmFixtures(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "..", "vendor", "aube", "crates", "aube-lockfile", "tests", "fixtures")
	files, err := filepath.Glob(filepath.Join(dir, "pnpm-*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 10 {
		t.Fatal("reference fixtures missing", files)
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			_, _, err := Read(file, Options{})
			legacy := strings.Contains(filepath.Base(file), "pnpm-v5") || strings.Contains(filepath.Base(file), "pnpm-v6")
			if legacy && err == nil {
				t.Fatal("legacy layout accepted")
			}
			if !legacy && err != nil {
				t.Fatal(err)
			}
		})
	}
	// pnpm and Nub's native lock name share a reader; file location supplies
	// diagnostics only and never triggers project/global configuration discovery.
	data, err := os.ReadFile(filepath.Join(dir, "pnpm-native.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "nub.lock")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Read(path, Options{}); err != nil {
		t.Fatal(err)
	}
}
