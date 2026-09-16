package pnpm

import (
	"strings"
	"testing"
)

func raw(t *testing.T, s string) *rawLock {
	t.Helper()
	v, err := parseRaw([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func TestRawMultiDocumentSelection(t *testing.T) {
	bootstrap := "lockfileVersion: '9.0'\nimporters: {'.': {packageManagerDependencies: {pnpm: {version: '11.0.0'}}}}\n"
	project := "lockfileVersion: '9.0'\nimporters: {'.': {dependencies: {a: {specifier: '^1', version: '1.0.0'}}}}\n"
	r := raw(t, bootstrap+"---\n"+project)
	if len(r.importers["."].deps) != 1 {
		t.Fatal("bootstrap selected")
	}
	r = raw(t, project+"---\n[broken")
	if len(r.importers["."].deps) != 1 {
		t.Fatal("valid preceding document lost")
	}
	r = raw(t, "lockfileVersion: '9.0'\n---\nlockfileVersion: '10.0'\n")
	if r.version.Value != "9.0" {
		t.Fatal("score tie should select first")
	}
	r = raw(t, strings.Repeat(bootstrap+"---\n", 16)+project)
	if len(r.importers["."].deps) != 0 {
		t.Fatal("inspected beyond document cap")
	}
	if _, err := parseRaw([]byte("[broken\n---\n" + project)); err == nil {
		t.Fatal("invalid first doc skipped")
	}
}
func TestRawScalarAndCollectionRules(t *testing.T) {
	r := raw(t, `lockfileVersion: 9.0
settings: {autoInstallPeers: false, excludeLinksFromLockfile: null}
importers:
  .:
    dependencies: {a: {specifier: 1, version: 1.0}}
packages:
  a@1.0.0:
    engines: {node: '>=18', npm: 10, cordovaDependencies: {x: true}}
    os: [linux, 3, false, null, darwin]
    cpu: x64
    libc: {bad: true}
    hasBin: true
    peerDependenciesMeta: {b: {optional: false}}
    resolution: {repo: example, commit: abc, path: /a/b}
`)
	if r.importers["."].deps["a"].version != "1.0" {
		t.Fatal("string scalar spelling changed")
	}
	p := r.packages["a@1.0.0"]
	if len(p.engines) != 1 || len(p.os) != 2 || len(p.cpu) != 1 || len(p.libc) != 0 || !p.hasBin {
		t.Fatalf("tolerant fields: %+v", p)
	}
	if *p.resolution.subpath != "a/b" {
		t.Fatal("subpath")
	}
	for _, extra := range []string{
		"packages: null", "snapshots: []", "settings: {autoInstallPeers: yes}",
		"packages: {a: {hasBin: null}}", "packages: {a: {peerDependenciesMeta: {b: {optional: null}}}}",
		"importers: {'.': {dependencies: {a: {version: '1.0.0'}}}}",
		"catalogs: {default: {a: {specifier: '*'}}}", "patchedDependencies: {a: 123}",
		"packages: {a: {resolution: {variants: [{resolution: {url: x}}]}}}",
		"packages: {a: {resolution: {variants: [{targets: [], resolution: {url: x, bin: 3}}]}}}",
		"lockfileVersion: 10\nunknown: true", // general-parser duplicate struct field
	} {
		if _, err := parseRaw([]byte("lockfileVersion: '9.0'\n" + extra + "\n")); err == nil {
			t.Errorf("accepted invalid shape: %s", extra)
		}
	}
	for _, s := range []string{"", "null", "[]", string([]byte{0xff})} {
		if _, err := parseRaw([]byte(s)); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
}
func TestRawAliasesAndPatchCollisions(t *testing.T) {
	r := raw(t, `lockfileVersion: '9.0'
packages:
  a@1.0.0: &a {hasBin: true}
  a@1.0.0(patch_hash=hash): {hasBin: false}
  b@1.0.0: *a
snapshots:
  a@1.0.0(patch_hash=hash): {dependencies: {c: '1.0.0(patch_hash=hash)(x@2.0.0)'}}
patchedDependencies:
  a@1.0.0: hash
  b@1.0.0: {path: b.patch, hash: old}
  c@1.0.0: {path: c.patch}
`)
	r.stripPatchMarkers()
	if len(r.packages) != 2 || !r.packages["a@1.0.0"].hasBin || !r.packages["b@1.0.0"].hasBin {
		t.Fatal("key collision or YAML alias")
	}
	if r.snapshots["a@1.0.0"].deps["c"] != "1.0.0(x@2.0.0)" {
		t.Fatal("patch value")
	}
	for _, path := range []string{"'/'", "'a/../b'", "'a//b'", "'a/./b'"} {
		r := raw(t, "lockfileVersion: 9\npackages: {a: {resolution: {path: "+path+"}}}\n")
		if r.packages["a"].resolution.subpath != nil {
			t.Fatal("unsafe subpath", path)
		}
	}
}
func TestLockfileVersion(t *testing.T) {
	for _, s := range []string{"'9.0'", "9.5", "10", "' +9.1.0 '", "'9'"} {
		r := raw(t, "lockfileVersion: "+s)
		if v, ok := versionMajor(r.version); !ok || v < 9 {
			t.Errorf("valid version %s", s)
		}
	}
	for _, s := range []string{"'9.invalid'", "'9.'", "'9..0'", "'-9'", "-9", ".inf", ".nan", "null", "true", "{}"} {
		r := raw(t, "lockfileVersion: "+s)
		if _, ok := versionMajor(r.version); ok {
			t.Errorf("invalid version %s", s)
		}
	}
}
