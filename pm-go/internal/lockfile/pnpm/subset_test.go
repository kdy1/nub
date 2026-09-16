package pnpm

import "testing"

func TestSubsetDuplicateFieldBehavior(t *testing.T) {
	body := `lockfileVersion: '9.0'
lockfileVersion: '10.0'
packageExtensionsChecksum: null
importers:
  .:
    dependencies:
      a:
        specifier: ^1
        version: 1.0.0
        version: 1.1.0
packages:
  a@1.0.0: {}
packages:
  a@1.1.0: {}
`
	r := raw(t, body)
	if r.version.Value != "10.0" || r.extensionChecksum == nil || *r.extensionChecksum != "null" || r.importers["."].deps["a"].version != "1.1.0" || len(r.packages) != 1 || r.packages["a@1.1.0"] == nil {
		t.Fatal("subset overwrite/scalar behavior changed")
	}
	if _, err := parseRaw([]byte(body + "unknown: true\n")); err == nil {
		t.Fatal("general YAML path accepted duplicate struct fields")
	}
	if _, err := parseRaw([]byte("lockfileVersion: 9\npackages:\n  a@1.0.0:\n    hasBin: true\n    hasBin: false\n")); err == nil {
		t.Fatal("delegated package struct accepted duplicates")
	}
}
func TestSubsetFallbackDoesNotDropFlowData(t *testing.T) {
	cases := []string{
		"lockfileVersion: 9\nsettings: {autoInstallPeers: false}\n",
		"lockfileVersion: 9\nimporters: {'.': {dependencies: {a: {specifier: '^1', version: 1.0.0}}}}\n",
		"lockfileVersion: 9\npackages: {a@1.0.0: {hasBin: true}}\n",
		"lockfileVersion: 9\nsnapshots: {a@1.0.0: {dependencies: {b: 1.0.0}}}\n",
		"lockfileVersion: 9\n---\nlockfileVersion: 10\nsettings: {}\n",
	}
	for _, body := range cases {
		if trySubset([]byte(body)) != nil {
			t.Fatal("subset should decline", body)
		}
		raw(t, body)
	}
	r := raw(t, cases[0])
	if r.settings.autoPeers == nil || *r.settings.autoPeers {
		t.Fatal("flow settings dropped")
	}
}
func TestSubsetCommentAndEmptyBlockRules(t *testing.T) {
	body := `lockfileVersion: '9.0'
importers:
  .:
    dependencies:
      a:
        specifier: ^1 # comment
        version: 1.0.0
packages:
  a@1.0.0:
snapshots:
  a@1.0.0:
`
	if trySubset([]byte(body)) == nil {
		t.Fatal("native block shape declined")
	}
	g := graph(t, body)
	if *g.RootDeps()[0].Specifier != "^1" || g.Packages["a@1.0.0"] == nil {
		t.Fatal("subset graph")
	}
}
