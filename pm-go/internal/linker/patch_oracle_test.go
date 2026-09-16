package linker

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/store"
)

func TestRustPatchApplicationOracle(t *testing.T) {
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		t.Skip("set PM_RUST_LOCKFILE_ORACLE to compare patch application")
	}
	var cases, expected []map[string]any
	corpus := patchCases()
	for _, c := range corpus {
		root := t.TempDir()
		s := store.New(filepath.Join(root, "cas", "v1", "files"), filepath.Join(root, "cache"))
		t.Cleanup(func() { s.Close() })
		index := packageFiles(t, s, c.original)
		pkg := lockfile.NewPackage("pkg", "1.0.0")
		g := lockfile.NewGraph()
		g.Packages[pkg.DepPath] = pkg
		m := Materializer{Root: filepath.Join(root, "virtual"), Strategy: Copy, Patches: map[string]string{pkg.SpecKey(): c.patch}}
		_, err := m.EnsurePackage(t.Context(), pkg.DepPath, g, pkg, index, nil)
		var message any
		if err != nil {
			message = err.Error()
		}
		expected = append(expected, map[string]any{"error": message, "tree": materializedTree(t, m.Root)})
		if err := os.RemoveAll(m.Root); err != nil {
			t.Fatal(err)
		}
		cases = append(cases, map[string]any{"root": m.Root, "index": index, "patch": c.patch})
	}
	data, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	path := binFixture(t, t.TempDir(), "patches.json", string(data))
	output, err := exec.CommandContext(t.Context(), oracle, "patches", path).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	var got, want []map[string]any
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	expectedJSON, _ := json.Marshal(expected)
	if err := json.Unmarshal(expectedJSON, &want); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatal(len(got), len(want))
	}
	for i := range got {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("%s Rust %v; Go %v", corpus[i].name, got[i], want[i])
		}
	}
	t.Logf("compared %d patch application cases", len(cases))
}
