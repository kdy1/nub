package linker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/store"
)

func TestAppliedPatchStateEncodingAndRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, AppliedPatchesFilename)
	for _, raw := range []string{"", "null", "[]", `{"pkg":"ok","bad":null}`, `{"bad":12}`, `{"bad":"\ud800"}`, "{\"bad\":\"\xff\"}", "{}{}"} {
		if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
			t.Fatal(err)
		}
		if got := ReadAppliedPatches(dir); len(got) != 0 {
			t.Fatalf("%q: %v", raw, got)
		}
	}
	hashes := map[string]string{"z": "last", "a<&\u2028": "value\n", "alias@1": ""}
	if err := WriteAppliedPatches(dir, hashes); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "{\"a<&\u2028\":\"value\\n\",\"alias@1\":\"\",\"z\":\"last\"}" {
		t.Fatalf("%s: %v", data, err)
	}
	if got := ReadAppliedPatches(dir); !reflect.DeepEqual(got, hashes) {
		t.Fatal(got)
	}
	if err := WriteAppliedPatches(dir, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := WriteAppliedPatches(dir, nil); err != nil || len(ReadAppliedPatches(dir)) != 0 {
		t.Fatal(err)
	}
	if err := WriteAppliedPatches(filepath.Join(dir, "missing"), hashes); err == nil {
		t.Fatal("lost state must be reported")
	}
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	if err := WriteAppliedPatches(dir, nil); err == nil {
		t.Fatal("tracking removal must not delete a directory")
	}
}

func TestPatchFingerprintsNormalizeCRLFOnly(t *testing.T) {
	got := CurrentPatchHashes(map[string]string{"empty": "", "lf": "a\nb\n", "crlf": "a\r\nb\r\n", "embedded": "a\rb\n"})
	if got["empty"] != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" || got["lf"] != got["crlf"] || got["embedded"] == got["lf"] {
		t.Fatal(got)
	}
}

func TestPatchReconciliationRebuildsAliasAndPeerEntries(t *testing.T) {
	root := t.TempDir()
	s := store.New(filepath.Join(root, "cas"), filepath.Join(root, "cache"))
	t.Cleanup(func() { s.Close() })
	index := packageFiles(t, s, map[string]string{"index.js": "original\n"})
	graph := lockfile.NewGraph()
	for _, key := range []string{"alias@1.0.0", "alias@1.0.0(peer@2.0.0)", "other@1.0.0"} {
		name := "alias"
		if strings.HasPrefix(key, "other") {
			name = "other"
		}
		pkg := lockfile.NewPackage(name, "1.0.0")
		pkg.DepPath = key
		graph.Packages[key] = pkg
	}
	// Registry selectors invalidate every alias and peer-context placement.
	for _, pkg := range graph.Packages {
		if pkg.Name == "alias" {
			realName := "real"
			pkg.AliasOf = &realName
		}
	}
	nm := filepath.Join(root, "node_modules")
	m := Materializer{Root: filepath.Join(nm, ".store"), Strategy: Copy}
	previous := map[string]string{}
	patch := func(word string) map[string]string {
		return map[string]string{"real@1.0.0": "--- a/index.js\n+++ b/index.js\n@@ -1 +1 @@\n-original\n+" + word + "\n"}
	}
	for i, patches := range []map[string]string{nil, patch("first"), patch("second"), nil} {
		current := CurrentPatchHashes(patches)
		if err := WipeChangedPatchedEntries(t.Context(), m.Root, graph, previous, current, 0); err != nil {
			t.Fatal(err)
		}
		m.Patches = patches
		for key, pkg := range graph.Packages {
			got, err := m.EnsurePackage(t.Context(), key, graph, pkg, index, nil)
			if err != nil {
				t.Fatal(err)
			}
			wantCached := i > 0 && pkg.Name == "other"
			if got.Cached != wantCached {
				t.Fatalf("pass %d %s cached %v", i, key, got.Cached)
			}
			want := "original\n"
			if pkg.Name == "alias" && (i == 1 || i == 2) {
				want = []string{"", "first\n", "second\n"}[i]
			}
			body, err := os.ReadFile(filepath.Join(got.Directory, "index.js"))
			if err != nil || string(body) != want {
				t.Fatalf("pass %d %s: %q %v", i, key, body, err)
			}
		}
		if err := WriteAppliedPatches(nm, current); err != nil {
			t.Fatal(err)
		}
		previous = ReadAppliedPatches(nm)
	}
}

func TestPatchReconciliationPreservesUnchangedAndHonorsCancellation(t *testing.T) {
	graph := lockfile.NewGraph()
	pkg := lockfile.NewPackage("pkg", "1.0.0")
	graph.Packages[pkg.DepPath] = pkg
	m := Materializer{Root: t.TempDir(), MaxFilenameLength: 36}
	entry, err := m.EntryName(pkg.DepPath)
	if err != nil {
		t.Fatal(err)
	}
	file := binFixture(t, filepath.Join(m.Root, entry), "sentinel", "keep")
	previous := map[string]string{pkg.SpecKey(): ""}
	if err := WipeChangedPatchedEntries(t.Context(), m.Root, graph, previous, previous, 36); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := WipeChangedPatchedEntries(ctx, m.Root, graph, previous, nil, 36); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal(err)
	}
	// An absent key differs from a recorded empty fingerprint.
	if err := WipeChangedPatchedEntries(t.Context(), m.Root, graph, previous, nil, 36); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
