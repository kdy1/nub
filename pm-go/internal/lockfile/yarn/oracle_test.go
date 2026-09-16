package yarn

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/manifest"
	"github.com/nubjs/nub/pm-go/internal/testutil"
)

func TestRustClassicGraphOracle(t *testing.T) {
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		t.Skip("Rust library oracle only runs in the reference CI job")
	}
	root := filepath.Join("..", "..", "..", "..", "vendor/aube/crates/aube-lockfile")
	cases := map[string][]byte{}
	for _, file := range []string{"src/yarn/tests.rs", "tests/unsupported_source.rs"} {
		source, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range regexp.MustCompile(`(?s)r#"(.*?)"#`).FindAllSubmatchIndex(source, -1) {
			data := source[match[2]:match[3]]
			if IsBerry(string(data)) || !regexp.MustCompile(`(?m)^  version `).Match(data) {
				continue
			}
			line := 1 + bytes.Count(source[:match[0]], []byte{'\n'})
			cases[fmt.Sprintf("%s-line-%d", filepath.Base(file), line)] = data
		}
	}
	for _, data := range []string{
		"a@1:\n  version 1\n  version 2\n", "a@1:\n  version 1\n  dependencies:\n    x exotic:x\nx@exotic:x:\n  version 2\n",
		"a@1:\n  version 1\n  optionalDependencies:\n    x exotic:x\nx@exotic:x:\n  version 2\n",
		"a@1, a@2:\n  version 1\na@1:\n  version 2\n", "a@1:\n  version 1\na@1:\n  version 2\n  dependencies:\n    missing 1\n",
		"a@1:\n  dependencies:\n    x 1\n", "@broken:\n  version 1\n", "a@1:\n  version 1\n  resolved https://private/a#hash\n",
	} {
		cases[fmt.Sprintf("shape-%d", len(cases))] = []byte(data)
	}
	if len(cases) < 15 {
		t.Fatal("fixture extraction unexpectedly small", len(cases))
	}
	accepted := 0
	for _, name := range sortedKeys(cases) {
		t.Run(name, func(t *testing.T) {
			data := cases[name]
			dir := t.TempDir()
			path := filepath.Join(dir, "yarn.lock")
			pjPath := filepath.Join(dir, "package.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			// Supply root and member manifests to exercise direct-dependency
			// lookup, optional refusal, and sibling discovery for every lock.
			deps := map[string]string{}
			if blocks, err := tokenizeClassic(string(data)); err == nil {
				for _, b := range blocks {
					for _, spec := range b.specs {
						if n, ok := specName(spec); ok {
							if _, exists := deps[n]; !exists {
								deps[n] = spec[len(n)+1:]
							}
						}
					}
				}
			}
			for _, optional := range []bool{false, true} {
				field := "dependencies"
				if optional {
					field = "optionalDependencies"
				}
				pjData, err := json.Marshal(map[string]any{field: deps, "workspaces": []string{"packages/*"}})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(pjPath, pjData, 0600); err != nil {
					t.Fatal(err)
				}
				for member, body := range map[string]string{"a": `{"name":"@ws/a","version":"1.0.0","dependencies":{"@ws/b":"^2"}}`, "b": `{"name":"@ws/b","version":"2.0.0"}`} {
					p := filepath.Join(dir, "packages", member)
					if err := os.MkdirAll(p, 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(p, "package.json"), []byte(body), 0600); err != nil {
						t.Fatal(err)
					}
				}
				pj, err := manifest.ParsePackage(pjData)
				if err != nil {
					t.Fatal(err)
				}
				for _, relaxed := range []bool{false, true} {
					mode := "strict"
					if relaxed {
						mode = "relaxed"
					}
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
					defer cancel()
					out, err := exec.CommandContext(ctx, oracle, "yarn-graph", path, pjPath, mode).CombinedOutput()
					if err != nil {
						t.Fatal(string(out), err)
					}
					var ref struct {
						OK    bool
						Graph json.RawMessage
						Error string
					}
					if err := json.Unmarshal(out, &ref); err != nil {
						t.Fatal(string(out), err)
					}
					g, _, err := ParseClassic(path, data, pj, Options{AllowUnsupportedSources: relaxed})
					if (err == nil) != ref.OK {
						t.Fatalf("%s optional=%v acceptance: Go %v; Rust %v (%s)\n%s", mode, optional, err, ref.OK, ref.Error, data)
					}
					if err != nil {
						continue
					}
					accepted++
					got, err := testutil.GraphJSON(g)
					if err != nil {
						t.Fatal(err)
					}
					var a, b any
					if err := json.Unmarshal(got, &a); err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(ref.Graph, &b); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(a, b) {
						a, _ := json.MarshalIndent(a, "", "  ")
						b, _ := json.MarshalIndent(b, "", "  ")
						t.Fatalf("%s optional=%v graph differs\nGo: %s\nRust: %s\nInput: %s", mode, optional, a, b, strings.TrimSpace(string(data)))
					}
					compareClassicWriter(t, oracle, path, pjPath, mode, g, pj)
				}
			}
		})
	}
	t.Logf("compared %d classic documents in strict/lenient and required/optional modes (%d accepted)", len(cases), accepted)
}

func compareClassicWriter(t *testing.T, oracle, input, manifestPath, mode string, g *lockfile.Graph, pj *manifest.Package) {
	t.Helper()
	output := filepath.Join(filepath.Dir(input), "output.lock")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := exec.CommandContext(ctx, oracle, "yarn-classic-write", input, manifestPath, output, mode).CombinedOutput()
	if err != nil {
		t.Fatal(string(result), err)
	}
	var ref struct {
		OK    bool
		Error string
	}
	if err := json.Unmarshal(result, &ref); err != nil {
		t.Fatal(string(result), err)
	}
	got, err := EncodeClassic(g, pj)
	if (err == nil) != ref.OK {
		t.Fatalf("writer acceptance Go %v; Rust %v (%s)", err, ref.OK, ref.Error)
	}
	if err != nil {
		return
	}
	want, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("classic writer differs\nGo: %s\nRust: %s", got, want)
	}
}
