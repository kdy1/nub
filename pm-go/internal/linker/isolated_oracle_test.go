package linker

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRustIsolatedProjectOracle(t *testing.T) {
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		t.Skip("set PM_RUST_LOCKFILE_ORACLE to compare isolated project linking")
	}
	var inputs []map[string]any
	var expected [][]map[string]any
	for _, workspace := range []bool{false, true} {
		for mode := range 8 {
			for _, custom := range []bool{false, true} {
				p := isolatedFixture(t)
				p.HasWorkspace = workspace
				p.ModulesDirName = "node_modules"
				p.VirtualStoreDir = filepath.Join(p.ProjectDir, "node_modules/.store")
				p.PublicHoistPatterns = []string{}
				p.Hoist = new(bool)
				*p.Hoist = mode != 0
				p.HoistWorkspacePackages = new(bool)
				*p.HoistWorkspacePackages = mode != 1
				p.ShamefullyHoist = mode == 2 || mode == 3
				if mode == 3 || mode == 4 {
					p.PublicHoistPatterns = []string{"GHOST", "member-*", "unreferenced", "!member-a"}
				}
				if mode == 5 {
					p.HoistPatterns = []string{"*", "!ghost"}
				}
				p.VirtualStoreOnly = mode == 6
				p.DedupeDirectDeps = mode == 7
				if custom {
					p.ModulesDirName = "deps"
					p.VirtualStoreDir = filepath.Join(p.ProjectDir, "deps/vstore")
				}
				if err := os.RemoveAll(p.ProjectDir); err != nil {
					t.Fatal(err)
				}
				seedIsolatedFixture(t, p)
				var passes []map[string]any
				for range 2 {
					stats, err := LinkIsolatedProject(t.Context(), p)
					var message any
					if err != nil {
						message = err.Error()
					}
					passes = append(passes, map[string]any{"tree": materializedTree(t, p.ProjectDir), "stats": stats, "error": message})
				}
				expected = append(expected, passes)
				if err := os.RemoveAll(p.ProjectDir); err != nil {
					t.Fatal(err)
				}
				seedIsolatedFixture(t, p)
				inputs = append(inputs, map[string]any{
					"root": p.ProjectDir, "graph": p.Graph, "indices": p.Indices, "workspace": p.WorkspaceDirs,
					"modules": p.ModulesDirName, "virtual": p.VirtualStoreDir, "hasWorkspace": p.HasWorkspace,
					"hoist": *p.Hoist, "hoistWorkspace": *p.HoistWorkspacePackages, "patterns": p.HoistPatterns,
					"shamefully": p.ShamefullyHoist, "dedupe": p.DedupeDirectDeps, "only": p.VirtualStoreOnly, "public": p.PublicHoistPatterns,
				})
			}
		}
	}
	data, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	path := binFixture(t, t.TempDir(), "isolated.json", string(data))
	output, err := exec.CommandContext(t.Context(), oracle, "isolated", path).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	var got, want [][]map[string]any
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
			// Keep CI output useful: report the changed tree entries individually.
			for pass := range want[i] {
				for key, value := range want[i][pass] {
					if !reflect.DeepEqual(got[i][pass][key], value) {
						if key == "tree" {
							a, b := got[i][pass][key].(map[string]any), value.(map[string]any)
							for path, entry := range b {
								if !reflect.DeepEqual(a[path], entry) {
									t.Errorf("case%d pass%d %s Rust%v Go%v", i, pass, path, a[path], entry)
								}
							}
							for path := range a {
								if _, ok := b[path]; !ok {
									t.Errorf("case%d pass%d Rust-only %s: %v", i, pass, path, a[path])
								}
							}
						} else {
							t.Errorf("case%d pass%d %s Rust%v Go%v", i, pass, key, got[i][pass][key], value)
						}
					}
				}
			}
		}
	}
	t.Log(fmt.Sprintf("compared %d cold/warm isolated project cases", len(inputs)))
}
