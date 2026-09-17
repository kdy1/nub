package installconfig

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/linker"
)

func TestRustMaterializationOracle(t *testing.T) {
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		t.Skip("set PM_RUST_LOCKFILE_ORACLE for GVS policy parity")
	}
	var cases []any
	var want []any
	for _, mode := range []NodeLinker{Isolated, Hoisted} {
		for _, explicit := range []*bool{nil, boolp(false), boolp(true)} {
			for _, hoist := range []*bool{nil, boolp(false), boolp(true)} {
				for _, resolved := range []bool{false, true} {
					for _, env := range []map[string]string{nil, {"CI": ""}, {"CI": "false"}, {"CI": "1"}} {
						input := MaterializationInput{Linker: mode, EnableGlobalVirtualStore: explicit, HoistExplicit: hoist, ResolvedHoist: resolved, Env: env}
						cases = append(cases, input)
						got, err := SelectMaterialization(input)
						if err != nil {
							want = append(want, map[string]any{"error": err.Error()})
						} else {
							want = append(want, map[string]any{"shared": got.Mode.UsesSharedStore(), "hidden": got.Mode.BuildsHiddenTree(), "prewarm": got.PrewarmOverride(mode)})
						}
					}
				}
			}
		}
	}
	for i := range 4 {
		dir := t.TempDir()
		if i > 0 {
			if err := os.Mkdir(filepath.Join(dir, "@scope+real@1"), 0755); err != nil {
				t.Fatal(err)
			}
		}
		if i > 1 {
			if err := linker.CreateDirLink(t.Context(), filepath.Join(dir, "missing"), filepath.Join(dir, "z-link@1")); err != nil {
				t.Fatal(err)
			}
		}
		if i == 3 {
			if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
		}
		cases = append(cases, map[string]any{"directory": dir})
		want = append(want, DetectStoreMode(dir))
	}
	data, _ := json.Marshal(cases)
	path := filepath.Join(t.TempDir(), "cases.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	output, err := exec.CommandContext(t.Context(), oracle, "gvs", path).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	var got, expected []any
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	data, _ = json.Marshal(want)
	if err := json.Unmarshal(data, &expected); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(expected) {
		t.Fatal(len(got), len(expected))
	}
	for i := range got {
		if !reflect.DeepEqual(got[i], expected[i]) {
			t.Errorf("case %v: Rust %v; Go %v", cases[i], got[i], expected[i])
		}
	}
	t.Logf("compared %d materialization and store-mode cases", len(cases))
}
