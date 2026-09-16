package lockfile

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDepPathFilenameBoundsAndPeerEncoding(t *testing.T) {
	for path, want := range map[string]string{"foo@1.0.0": "foo@1.0.0", "@s/p@1(a@2)(b@3)": "@s+p@1_a@2_b@3", "file:../Upper": "file+..+Upper", "/foo@1": "foo@1", "foo@1(a@2(b@3))": "foo@1_a@2_b@3_"} {
		got, err := DepPathFilename(path, 120)
		if err != nil || got != want {
			t.Fatal(path, got, want, err)
		}
	}
	for _, path := range []string{"Upper", strings.Repeat("résumé", 50), "@s/p@1(" + strings.Repeat("nested@1", 50) + ")"} {
		for _, limit := range []int{34, 60, 120, 256} {
			got, err := DepPathFilename(path, limit)
			if err != nil || len(got) > limit || !utf8.ValidString(got) {
				t.Fatal(path, limit, got, err)
			}
		}
	}
}

func TestRustDepPathFilenameOracle(t *testing.T) {
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		t.Skip("set PM_RUST_LOCKFILE_ORACLE to compare store filenames")
	}
	paths := []string{"", "foo@1.0.0", "@scope/pkg@1(peer@2)", "@scope/pkg@1(a@2)(b@3(c@4))", "file:../Upper", "/absolute@1", "Upper", `pkg\\/:*?"<>|#`, "odd)parenthesis", "odd(unclosed", strings.Repeat("é", 150), strings.Repeat("@Scope/PKG@1(peer@2)", 12)}
	var cases []map[string]any
	var want []string
	for _, path := range paths {
		for _, limit := range []int{34, 60, 120, 256} {
			cases = append(cases, map[string]any{"path": path, "limit": limit})
			got, err := DepPathFilename(path, limit)
			if err != nil {
				t.Fatal(err)
			}
			want = append(want, got)
		}
	}
	data, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(t.TempDir(), "filenames.json")
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	output, err := exec.CommandContext(t.Context(), oracle, "dep-filenames", input).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	var got []string
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Rust %v\nGo %v", got, want)
	}
	t.Logf("compared %d virtual-store filenames", len(cases))
}
