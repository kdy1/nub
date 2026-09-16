package linker

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
)

func TestRustHoistedProjectOracle(t *testing.T) {
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		t.Skip("set PM_RUST_LOCKFILE_ORACLE to compare hoisted project linking")
	}
	var inputs []map[string]any
	var expected [][]map[string]any
	for _, workspace := range []bool{false, true} {
		for _, limits := range []HoistingLimits{HoistNone, HoistWorkspaces, HoistDependencies} {
			for mode := range 4 {
				p := isolatedFixture(t)
				p.HasWorkspace = workspace
				p.ModulesDirName = "node_modules"
				p.VirtualStoreDir = filepath.Join(p.ProjectDir, "node_modules/.store")
				p.HoistWorkspacePackages = new(bool)
				*p.HoistWorkspacePackages = mode != 1
				p.VirtualStoreOnly = mode == 2 // Hoisted deliberately ignores this setting.
				if mode == 3 {
					p.ModulesDirName = "deps"
					p.VirtualStoreDir = filepath.Join(p.ProjectDir, ".custom-store")
				}
				hp := HoistedPlan{IsolatedPlan: p, Limits: limits, Reusable: lockfile.Set{}}
				for key, pkg := range p.Graph.Packages {
					if pkg.Source != nil && pkg.Source.Kind == lockfile.Link {
						continue
					}
					file, err := p.Store.ImportBytes(t.Context(), []byte(`{"name":"`+pkg.Name+`","version":"`+pkg.Version+`"}`), false)
					if err != nil {
						t.Fatal(err)
					}
					p.Indices[key]["package.json"] = file
					if mode == 2 {
						hp.Reusable.Add(key)
					}
				}
				if err := os.RemoveAll(p.ProjectDir); err != nil {
					t.Fatal(err)
				}
				seedIsolatedFixture(t, p)
				var passes []map[string]any
				for range 2 {
					stats, placements, err := LinkHoistedProject(t.Context(), hp)
					var message any
					if err != nil {
						message = err.Error()
					}
					passes = append(passes, map[string]any{"tree": materializedTree(t, p.ProjectDir), "stats": stats, "error": message, "globalTree": map[string]any{}, "placements": placements})
				}
				expected = append(expected, passes)
				if err := os.RemoveAll(p.ProjectDir); err != nil {
					t.Fatal(err)
				}
				seedIsolatedFixture(t, p)
				inputs = append(inputs, map[string]any{
					"root": p.ProjectDir, "graph": p.Graph, "indices": p.Indices, "workspace": p.WorkspaceDirs,
					"modules": p.ModulesDirName, "virtual": p.VirtualStoreDir, "hasWorkspace": p.HasWorkspace,
					"hoist": true, "hoistWorkspace": *p.HoistWorkspacePackages, "patterns": nil,
					"shamefully": false, "dedupe": false, "only": p.VirtualStoreOnly, "public": []string{},
					"global": false, "globalRoot": "", "hoisted": true, "limits": limits, "reusable": hp.Reusable.Sorted(),
				})
			}
		}
	}
	data, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	path := binFixture(t, t.TempDir(), "hoisted.json", string(data))
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
		for pass := range want[i] {
			for key, value := range want[i][pass] {
				if !reflect.DeepEqual(got[i][pass][key], value) {
					a, _ := json.MarshalIndent(got[i][pass][key], "", "  ")
					b, _ := json.MarshalIndent(value, "", "  ")
					t.Errorf("case%d pass%d %s Rust%s Go%s", i, pass, key, a, b)
				}
			}
		}
	}
	t.Logf("compared %d cold/warm hoisted project cases", len(inputs))
}
