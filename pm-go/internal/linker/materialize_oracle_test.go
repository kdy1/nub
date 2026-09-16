package linker

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
)

func materializedTree(t *testing.T, root string) map[string]any {
	t.Helper()
	entries := map[string]any{}
	var visit func(string, string)
	visit = func(dir, relative string) {
		children, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, child := range children {
			path := filepath.Join(dir, child.Name())
			name := filepath.ToSlash(filepath.Join(relative, child.Name()))
			info, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			entry := map[string]any{}
			if target, err := os.Readlink(path); err == nil {
				entry["link"] = target
			} else if info.IsDir() {
				entry["directory"] = true
				visit(path, name)
			} else {
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				entry["body"] = string(data)
			}
			if runtime.GOOS != "windows" {
				entry["mode"] = int(info.Mode().Perm())
			}
			entries[name] = entry
		}
	}
	visit(root, "")
	return entries
}

func TestRustAtomicMaterializationOracle(t *testing.T) {
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		t.Skip("set PM_RUST_LOCKFILE_ORACLE to compare package materialization")
	}
	var cases, expected []map[string]any
	for _, fullEdges := range []bool{false, true} {
		for _, hashed := range []bool{false, true} {
			for _, limit := range []int{60, 120} {
				m, graph, indices, nested := materializeFixture(t, fullEdges)
				m.MaxFilenameLength = limit
				if hashed {
					m.Hashes = graph.ComputeHashes(lockfile.HashOptions{})
				}
				order := []string{"@scope/parent@1.0.0", "child@1.0.0", "@s/dep@2.0.0"}
				var passes []map[string]any
				for range 2 {
					var stats []map[string]any
					for _, key := range order {
						result, err := m.EnsurePackage(t.Context(), key, graph, graph.Packages[key], indices[key], nested)
						if err != nil {
							t.Fatal(err)
						}
						stats = append(stats, map[string]any{"cached": result.Cached, "files": result.FilesLinked})
					}
					passes = append(passes, map[string]any{"stats": stats, "tree": materializedTree(t, m.Root)})
				}
				expected = append(expected, map[string]any{"passes": passes})
				if err := os.RemoveAll(m.Root); err != nil {
					t.Fatal(err)
				}
				cases = append(cases, map[string]any{"root": m.Root, "packages": graph.Packages, "indices": indices, "nested": nested, "order": order, "hashes": m.Hashes, "limit": limit})
			}
		}
	}
	input, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	path := binFixture(t, t.TempDir(), "materialize.json", string(input))
	output, err := exec.CommandContext(t.Context(), oracle, "materialize", path).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	var got, want []map[string]any
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	wantJSON, _ := json.Marshal(expected)
	if err := json.Unmarshal(wantJSON, &want); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatal(len(got), len(want))
	}
	for i := range got {
		if !reflect.DeepEqual(got[i], want[i]) {
			actual, _ := json.MarshalIndent(got[i], "", "  ")
			expected, _ := json.MarshalIndent(want[i], "", "  ")
			t.Errorf("case %d Rust %s\nGo %s", i, actual, expected)
		}
	}
	t.Log(fmt.Sprintf("compared %d cold/warm materialized graph cases", len(cases)))
}
