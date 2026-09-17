package jsonc

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadBoundedRegularUTF8File(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "nub.jsonc")
	for _, data := range []string{"", "\uFEFF{}", "\uFEFF\uFEFF{}", "{a:'\uFEFF'}", strings.Repeat(" ", MaxFileBytes)} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := Read(path)
		if err != nil || got != strings.TrimPrefix(data, "\uFEFF") {
			t.Fatal(len(data), len(got), err)
		}
	}
	if err := os.WriteFile(path, []byte(strings.Repeat(" ", MaxFileBytes)+"\xc3"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil || err.Error() != "larger than the 1 MiB limit" {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte{0xff}, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil {
		t.Fatal("accepted invalid UTF-8")
	}
	for _, path := range []string{root, os.DevNull, filepath.Join(root, "missing")} {
		if _, err := Read(path); err == nil {
			t.Fatal("accepted non-regular/missing path", path)
		}
	}
}

func TestNestingIgnoresTextAndRejectsDeepInput(t *testing.T) {
	for _, raw := range []string{`{"a":"[[[\\\"[[["}`, `{'a':'[[['}`, "// [[[\n{}", "/* [[[ */{}", "[[],[],[]]", "/* truncated[[[", "'truncated[[[", "}}[]"} {
		if err := CheckNesting(raw, 2); err != nil {
			t.Fatal(raw, err)
		}
	}
	if err := CheckNesting(strings.Repeat("[", MaxNestingDepth), MaxNestingDepth); err != nil {
		t.Fatal(err)
	}
	if err := CheckNesting(strings.Repeat("[", MaxNestingDepth+1), MaxNestingDepth); err == nil {
		t.Fatal("nesting bound not enforced")
	}
}

func TestRustJSONCReadAndGuardOracle(t *testing.T) {
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		t.Skip("set PM_RUST_LOCKFILE_ORACLE for guarded configuration reads")
	}
	type input struct {
		Text  string `json:"text"`
		Depth int    `json:"depth"`
		Path  string `json:"path,omitempty"`
	}
	type output struct {
		Value string `json:"value"`
		Error string `json:"error"`
	}
	var cases []input
	for _, text := range []string{"", "{}", "[][]", "[[[]]]", "}}[[]]", "'[[[", "\"\\\"[[[", "//[[[\r[[[", "//[[[\n[[[", "/*[[[*/[]", "/*[[[*", "{a:'unicode한글[[[]'}", strings.Repeat("[", 2000)} {
		for _, depth := range []int{0, 1, 2, 64} {
			cases = append(cases, input{Text: text, Depth: depth})
		}
	}
	dir := t.TempDir()
	for i, contents := range []string{"", "\uFEFF{one:1}", "\uFEFF\uFEFF{}", strings.Repeat(" ", MaxFileBytes), strings.Repeat(" ", MaxFileBytes+1), "a\xff", "a\xc3", "a\xc3(", "\xed\xa0\x80", "\xe2\x82x", "\xe2\x82", "\xf0\x90\x80x", "\xf4\x90\x80\x80", "\ufeff\xe2\x82"} {
		path := filepath.Join(dir, string(rune('a'+i)))
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		cases = append(cases, input{Path: path})
	}
	cases = append(cases, input{Path: dir})
	data, _ := json.Marshal(cases)
	path := filepath.Join(dir, "cases.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	data, err := exec.CommandContext(t.Context(), oracle, "jsonc-read", path).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, data)
	}
	var got []output
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(cases) {
		t.Fatal(len(got), len(cases))
	}
	for i, c := range cases {
		want := output{}
		var err error
		if c.Path != "" {
			want.Value, err = Read(c.Path)
		} else {
			err = CheckNesting(c.Text, c.Depth)
		}
		if err != nil {
			want.Error = err.Error()
		}
		if got[i] != want {
			t.Errorf("case %d: Rust value=%dB error=%q; Go value=%dB error=%q", i, len(got[i].Value), got[i].Error, len(want.Value), want.Error)
		}
	}
	t.Logf("compared %d bounded configuration read/nesting cases", len(cases))
}
