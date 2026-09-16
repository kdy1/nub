package pnpm

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
)

// Empty Go containers and Rust's empty BTreeMaps/Vecs have one test projection.
// Optional scalars stay null; package keys, metadata, ranges and sources are
// compared without path or version normalization.
func graphJSON(g *lockfile.Graph) ([]byte, error) {
	g = g.Clone()
	for _, p := range g.Packages {
		if p.Source != nil && p.Source.Kind == lockfile.RemoteTarball && p.Source.Integrity == nil {
			s := ""
			p.Source.Integrity = &s
		}
	}
	return json.Marshal(containers(reflect.ValueOf(g)))
}
func containers(v reflect.Value) any {
	if !v.IsValid() {
		return nil
	}
	if (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) && v.IsNil() {
		return nil
	}
	if v.CanInterface() {
		if _, ok := v.Interface().(json.Marshaler); ok {
			return v.Interface()
		}
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		return containers(v.Elem())
	case reflect.Map:
		out := map[string]any{}
		it := v.MapRange()
		for it.Next() {
			out[it.Key().String()] = containers(it.Value())
		}
		return out
	case reflect.Slice, reflect.Array:
		out := make([]any, v.Len())
		for i := range out {
			out[i] = containers(v.Index(i))
		}
		return out
	case reflect.Struct:
		out := map[string]any{}
		for i := 0; i < v.NumField(); i++ {
			out[v.Type().Field(i).Name] = containers(v.Field(i))
		}
		return out
	default:
		return v.Interface()
	}
}
func TestRustPnpmGraphOracle(t *testing.T) {
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		t.Skip("Rust library oracle only runs in the reference CI job")
	}
	root := filepath.Join("..", "..", "..", "..")
	cases := map[string][]byte{}
	files, err := filepath.Glob(filepath.Join(root, "vendor/aube/crates/aube-lockfile/tests/fixtures/pnpm-*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range files {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		cases[filepath.Base(p)] = data
	}
	// Reuse the literal documents in the unchanged Rust reader/writer tests.
	// Some are intentionally malformed or pre-v9; acceptance is compared too.
	source, err := os.ReadFile(filepath.Join(root, "vendor/aube/crates/aube-lockfile/src/pnpm/tests.rs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, match := range regexp.MustCompile(`(?s)r#"(.*?)"#`).FindAllSubmatchIndex(source, -1) {
		data := source[match[2]:match[3]]
		if !bytes.Contains(data, []byte("lockfileVersion:")) {
			continue
		}
		line := 1 + bytes.Count(source[:match[0]], []byte{'\n'})
		cases[fmt.Sprintf("reference-line-%d", line)] = data
	}
	// The generic YAML fallback's shape rules need coverage beyond native YAML.
	for _, extra := range []string{
		"packages: null", "packages: {a: {hasBin: null}}", "settings: {autoInstallPeers: yes}",
		"settings: {autoInstallPeers: !!bool true}", "importers: {'.': {dependencies: {a: {specifier: null, version: 1.0}}}}",
		"importers: {'.': {dependencies: {a: {specifier: 1, version: 1}}}}",
		"packages: {a@1.0.0: {engines: {node: 12, other: false}, os: [linux, 3, false]}}",
		"patchedDependencies: {a: 123}", "patchedDependencies: {a: '123'}",
		"packages: {a@1.0.0: &a {hasBin: true}, b@1.0.0: *a}",
		"snapshots: {a@1.0.0: {dependencies: {x: 1.0.0, x: 2.0.0}}}",
		"lockfileVersion: 10", "---\nlockfileVersion: '9.0'\nsettings: {}",
	} {
		cases[fmt.Sprintf("shape-%d", len(cases))] = []byte("lockfileVersion: '9.0'\n" + extra + "\n")
	}
	if len(cases) < 50 {
		t.Fatal("reference fixture extraction unexpectedly small", len(cases))
	}
	accepted := 0
	for _, name := range sortedKeys(cases) {
		t.Run(name, func(t *testing.T) {
			data := cases[name]
			path := filepath.Join(t.TempDir(), "pnpm-lock.yaml")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			for _, relaxed := range []bool{false, true} {
				mode := "strict"
				if relaxed {
					mode = "relaxed"
				}
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, oracle, "pnpm-graph", path, mode)
				output, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("reference probe: %v\n%s", err, output)
				}
				var ref struct {
					OK    bool            `json:"ok"`
					Graph json.RawMessage `json:"graph"`
					Error string          `json:"error"`
				}
				if err := json.Unmarshal(output, &ref); err != nil {
					t.Fatalf("decode reference: %v\n%s", err, output)
				}
				g, _, err := Parse(data, Options{AllowMissingIntegrity: relaxed})
				if (err == nil) != ref.OK {
					t.Fatalf("%s acceptance differs: Go %v; Rust %v (%s)\n%s", mode, err, ref.OK, ref.Error, data)
				}
				if err != nil {
					continue
				}
				accepted++
				got, err := graphJSON(g)
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
					goPretty, _ := json.MarshalIndent(a, "", "  ")
					rustPretty, _ := json.MarshalIndent(b, "", "  ")
					t.Fatalf("%s graph differs\nGo: %s\nRust: %s\nInput: %s", mode, goPretty, rustPretty, strings.TrimSpace(string(data)))
				}
			}
		})
	}
	t.Logf("compared %d literal/reference documents in strict and relaxed modes (%d accepted)", len(cases), accepted)
}
