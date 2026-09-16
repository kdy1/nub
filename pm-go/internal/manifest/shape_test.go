package manifest

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
)

func shape(t *testing.T, raw string) string {
	t.Helper()
	v, err := jsonvalue.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return InstallShapeDigest(v)
}

func TestInstallShapeIgnoresScriptsAndDocumentation(t *testing.T) {
	before := `{"name":"p","dependencies":{"z":"2","a":"1"},"scripts":{"test":"exit 1"}}`
	after := `{"scripts":{"test":"exit 0"},"description":"edited","dependencies":{"a":"1","z":"2"},"name":"p","nub":{"x":1},"aube":{"x":2}}`
	if shape(t, before) != shape(t, after) {
		t.Fatal("unrelated edit invalidated install shape")
	}
	for _, field := range installShapeFields {
		if shape(t, `{}`) == shape(t, `{"`+field+`":{}}`) {
			t.Fatal("ignored install field", field)
		}
	}
	if shape(t, `{"dependencies":{"a":"1"}}`) == shape(t, `{"dependencies":{"a":"2"}}`) {
		t.Fatal("dependency change ignored")
	}
	if shape(t, `{"trustedDependencies":["a","b"]}`) == shape(t, `{"trustedDependencies":["b","a"]}`) {
		t.Fatal("array order erased")
	}
	if shape(t, `null`) != shape(t, `[]`) || shape(t, `null`) == shape(t, `{}`) {
		t.Fatal("non-object sentinel")
	}
}

func TestRustInstallShapeOracle(t *testing.T) {
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		t.Skip("set PM_RUST_LOCKFILE_ORACLE for manifest shape parity")
	}
	corpus := []string{`null`, `[]`, `"text"`, `1`, `{}`, `{"dependencies":{"z":"1","a":"2"}}`, `{"scripts":{"x":"hello"}}`, `{"aube":{"x":1},"nub":{"x":2}}`}
	values := []string{`null`, `false`, `true`, `1`, `1.0`, `-0`, `1e30`, `1e-30`, `"quote\"\n\\한글"`, `["b","a",null,1.2]`, `{"z":"last","a":"first","\"":"newline\n"}`}
	for _, field := range installShapeFields {
		for _, v := range values {
			corpus = append(corpus, `{"`+field+`":`+v+`}`)
		}
	}
	var want []string
	for _, item := range corpus {
		want = append(want, shape(t, item))
	}
	data, _ := json.Marshal(corpus)
	path := filepath.Join(t.TempDir(), "shapes.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	output, err := exec.CommandContext(t.Context(), oracle, "manifest-shapes", path).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	var got []string
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	if len(got) != len(want) {
		t.Fatalf("Rust returned %d digests; expected %d", len(got), len(want))
	}
	if !reflect.DeepEqual(got, want) {
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("case %s: Rust %v Go %s", corpus[i], got[i], want[i])
			}
		}
	}
	t.Logf("compared %d manifest shape digests", len(corpus))
}
