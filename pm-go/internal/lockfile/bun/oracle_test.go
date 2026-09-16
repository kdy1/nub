package bun

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

	"github.com/nubjs/nub/pm-go/internal/testutil"
)

func TestRustBunGraphOracle(t *testing.T) {
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		t.Skip("Rust library oracle only runs in the reference CI job")
	}
	root := filepath.Join("..", "..", "..", "..", "vendor/aube/crates/aube-lockfile")
	cases := map[string][]byte{}
	native, err := os.ReadFile(filepath.Join(root, "tests/fixtures/bun-native.lock"))
	if err != nil {
		t.Fatal(err)
	}
	cases["native"] = native
	for _, file := range []string{"src/bun/tests.rs", "tests/unsupported_source.rs"} {
		source, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range regexp.MustCompile(`(?s)r#"(.*?)"#`).FindAllSubmatchIndex(source, -1) {
			data := string(source[match[2]:match[3]])
			if !strings.Contains(data, `"lockfileVersion"`) {
				continue
			}
			// Raw format! templates escape braces. Supply deterministic SRI
			// values; these strings are test inputs, not observed output.
			if strings.HasPrefix(strings.TrimSpace(data), "{{") {
				data = strings.ReplaceAll(strings.ReplaceAll(data, "{{", "{"), "}}", "}")
			}
			data = regexp.MustCompile(`SRI_[A-Z_]+|\{(?:0|sri|sri_[a-z]+)\}`).ReplaceAllString(data, "sha512-"+strings.Repeat("a", 88))
			line := 1 + bytes.Count(source[:match[0]], []byte{'\n'})
			cases[fmt.Sprintf("%s-line-%d", filepath.Base(file), line)] = []byte(data)
		}
	}
	for _, extra := range []string{
		`"packages":null`, `"lockfileVersion":2`, `"configVersion":1.0`, `"workspaces":{"":{"dependencies":{},"dependencies":{}}}`,
		`"unknown":{"a":1,"a":2}`, `"packages":{"a":["a@1",{"dependencies":null,"bin":"x"}]}`,
		`"packages":{"a":["a@1",{"dependencies":{"x":1,"x":"2"}}]}`,
		`"packages":{"a":["a@1",{"optionalPeers":null}]}`, `"packages":{"a":["a@1",{"bin":{"x":"a","n":2},"os":["linux",null]}]}`,
		`"packages":{"a":["a@1","before",{},"after"]}`, `"packages":{"a":["a@1",{}, {"peerDependencies":{"a":"*"}}]}`,
		`"packages":{"a":["a@1"],"x":["x@npm:foo@1"]}`, `"future":"\ud800"`,
		`"packages":{"a":["a@1"]},"workspaces":{"":{"peerDependencies":{"a":7}}}`,
	} {
		cases[fmt.Sprintf("shape-%d", len(cases))] = []byte(`{"lockfileVersion":1,` + extra + `}`)
	}
	if len(cases) < 30 {
		t.Fatal("fixture extraction unexpectedly small", len(cases))
	}
	accepted := 0
	for _, name := range sortedKeys(cases) {
		t.Run(name, func(t *testing.T) {
			data := cases[name]
			path := filepath.Join(t.TempDir(), "bun.lock")
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
				output, err := exec.CommandContext(ctx, oracle, "bun-graph", path, mode).CombinedOutput()
				if err != nil {
					t.Fatalf("reference probe: %v\n%s", err, output)
				}
				var ref struct {
					OK    bool
					Graph json.RawMessage
					Error string
				}
				if err := json.Unmarshal(output, &ref); err != nil {
					t.Fatalf("decode reference: %v\n%s", err, output)
				}
				g, _, err := Parse(data, Options{AllowUnsupportedSources: relaxed})
				if (err == nil) != ref.OK {
					t.Fatalf("%s acceptance differs: Go %v; Rust %v (%s)\n%s", mode, err, ref.OK, ref.Error, data)
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
					t.Fatalf("%s graph differs\nGo: %s\nRust: %s\nInput: %s", mode, a, b, data)
				}
			}
		})
	}
	t.Logf("compared %d documents in strict and relaxed modes (%d accepted)", len(cases), accepted)
}
