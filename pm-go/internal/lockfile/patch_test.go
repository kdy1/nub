package lockfile

import (
	"errors"
	"reflect"
	"testing"
)

func TestPatchSelectorGrammar(t *testing.T) {
	for _, tc := range []struct {
		key, name string
		form      PatchForm
	}{
		{"foo@1.2.3", "foo", PatchExact}, {"@babel/core@7.0.0", "@babel/core", PatchExact},
		{"foo@>=3", "foo", PatchRange}, {"foo@*", "foo", PatchAll}, {"foo@ * ", "foo", PatchAll},
		{"foo", "foo", PatchAll}, {"@babel/core", "@babel/core", PatchAll}, {"foo@", "foo@", PatchAll},
	} {
		got, err := ClassifyPatchKey(tc.key, true)
		if err != nil || got.Name != tc.name || got.Form != tc.form {
			t.Fatalf("%s: %+v %v", tc.key, got, err)
		}
	}
	for _, key := range []string{"foo@github:org/pkg#abc", "foo@file:./vendor", "foo@git+ssh://example.test/repo.git"} {
		if _, err := ClassifyPatchKey(key, false); err != nil {
			t.Fatal(err)
		}
		if _, err := ClassifyPatchKey(key, true); err == nil {
			t.Fatal("source identity leaked into pnpm grammar", key)
		}
	}
	for _, key := range []string{"foo@^^1:2", "foo@not-a-range", "foo@x_:a"} {
		if _, err := ClassifyPatchKey(key, false); err == nil {
			t.Fatal("invalid range accepted", key)
		}
	}
}
func TestPatchPriorityAndConflict(t *testing.T) {
	g, err := NewPatchGroups([]string{"foo@1.2.3", "foo@>=1", "foo@^1", "foo", "foo@*"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ version, want string }{{"1.2.3", "foo@1.2.3"}, {"2.0.0", "foo@>=1"}, {"0.5.0", "foo@*"}, {"git-sha", "foo@*"}} {
		got, ok, err := g.Resolve("foo", tc.version)
		if err != nil || !ok || got != tc.want {
			t.Fatalf("%s: %s %v %v", tc.version, got, ok, err)
		}
	}
	_, _, err = g.Resolve("foo", "1.3.0")
	var conflict *PatchKeyConflict
	if !errors.As(err, &conflict) {
		t.Fatal("expected conflict", err)
	}
	if conflict.Error() != "Unable to choose between 2 version ranges to patch foo@1.3.0: >=1, ^1" || conflict.Hint() != "Explicitly set the exact version (foo@1.3.0) to resolve conflict" {
		t.Fatal(conflict)
	}
	if _, ok, err := g.Resolve("other", "1.2.3"); ok || err != nil {
		t.Fatal("unrelated patch matched")
	}
	// Exact selectors retain their spelling. semver equality is only for ranges.
	g, err = NewPatchGroups([]string{"foo@v1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := g.Resolve("foo", "1.2.3"); ok {
		t.Fatal("normalized exact selector")
	}
}
func TestPatchAliasesAndUnusedSelectors(t *testing.T) {
	p := NewPackage("alias", "1.2.3")
	real := "real"
	p.AliasOf = &real
	peer := p.Clone()
	peer.DepPath += "(peer@1.0.0)"
	packages := map[string]*Package{p.DepPath: p, peer.DepPath: peer}
	resolved, err := ResolvePatchValues(map[string]string{"real@^1": "real.patch", "alias@^1": "alias.patch"}, packages)
	if err != nil || !reflect.DeepEqual(resolved, map[string]string{"alias@1.2.3": "real.patch"}) {
		t.Fatal(resolved, err)
	}
	unused, err := UnusedPatchKeys([]string{"alias@^1", "real@^1", "ghost"}, packages)
	if err != nil || len(unused) != 2 || unused[0].SourceKey != "alias@^1" || unused[0].RegistryNameSpelling == nil || *unused[0].RegistryNameSpelling != "real@^1" || unused[1].SourceKey != "ghost" {
		t.Fatal(unused, err)
	}
}
