package settings

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestManagedFilesKeepSystemRestrictionsAndInvocationScope(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	system := write("system.toml", "minimumReleaseAge = 40\nengineStrict = true\n")
	write("custom.toml", "minimum-release-age = 12\nengine-strict = false\n")
	write("foreign.toml", "minimumReleaseAge = 1000\n")
	var warnings []string
	in := ManagedFiles{Dir: dir, SystemPath: &system, Env: map[string]string{"NUB_MANAGED_CONFIG_PATH": "custom.toml", "AUBE_MANAGED_CONFIG_PATH": "foreign.toml"}, Warn: func(s string) { warnings = append(warnings, s) }}
	entries, err := LoadManagedFiles(in)
	if err != nil || len(entries) != 4 || len(warnings) != 0 {
		t.Fatal(entries, warnings, err)
	}
	c := Context{Managed: entries, CLI: []Entry{{"minimumReleaseAge", "1"}, {"engineStrict", "false"}}}
	if *c.Uint64("minimumReleaseAge") != 40 || !*c.Bool("engineStrict") {
		t.Fatal("extra managed file weakened system policy")
	}
	in.Env = map[string]string{}
	entries, err = LoadManagedFiles(in)
	if err != nil || len(entries) != 2 {
		t.Fatal("another invocation inherited explicit file", entries, err)
	}
	write("system.toml", "minimumReleaseAge = 80\n")
	entries, err = LoadManagedFiles(in)
	if err != nil || !reflect.DeepEqual(entries, []Entry{{"minimumReleaseAge", "80"}}) {
		t.Fatal("managed file changes were cached across invocations", entries, err)
	}
	write("custom.toml", "broken=[\n")
	in.Env["NUB_MANAGED_CONFIG_PATH"] = "custom.toml"
	entries, err = LoadManagedFiles(in)
	if err != nil || len(entries) != 1 || len(warnings) != 1 || !strings.Contains(warnings[0], "failed to load nub config") {
		t.Fatal(entries, warnings, err)
	}
	in.Env["NUB_MANAGED_CONFIG_PATH"] = "absent.toml"
	_, err = LoadManagedFiles(in)
	if err != nil || len(warnings) != 2 || !strings.Contains(warnings[1], "does not exist") {
		t.Fatal(warnings, err)
	}
	in.SystemPath = new("")
	in.Env = map[string]string{}
	entries, err = LoadManagedFiles(in)
	if err != nil || len(entries) != 0 || len(warnings) != 2 {
		t.Fatal(entries, warnings, err)
	}
}

func TestManagedTOMLSourceOrderAndTypes(t *testing.T) {
	raw := "z = true\na = ['a,b', {ignored=1}, [3, false], []]\n\"dot.key\" = 0x10\nnegative = -0.0\ndotted.value = 4\n[section]\nignored = 8\n"
	got, err := ParseTOMLEntries([]byte(raw))
	want := []Entry{{"z", "true"}, {"a", "a,b,3,false,"}, {"dot.key", "16"}, {"negative", "-0"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, err)
	}
	withBOM, err := ParseTOMLEntries(append([]byte{0xef, 0xbb, 0xbf}, []byte(raw)...))
	if err != nil || !reflect.DeepEqual(withBOM, want) {
		t.Fatal(withBOM, err)
	}
	for _, raw := range [][]byte{[]byte("k=1\nk=2"), []byte("k=9223372036854775808"), []byte("k=\"" + string([]byte{255}) + "\"")} {
		if got, err := ParseTOMLEntries(raw); err == nil || got != nil {
			t.Fatal("invalid source partially accepted", got, err)
		}
	}
}

func TestRustManagedTOMLOracle(t *testing.T) {
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		t.Skip("set PM_RUST_LOCKFILE_ORACLE for managed TOML parity")
	}
	cases := []string{"", "# comment", "z=1\na=2\n", "key='value'\nkey='repeat'", "x={a=1}\ny=2\n", "x.a=1\ny=2\n", "[[table]]\na=1\n[[table]]\na=2\n", "\"\"='empty'\n\"a.b\"=true\n", "\uFEFFkey=42"}
	for _, raw := range []string{`"string"`, `'raw\literal'`, `"\u0041\U0001F600"`, `"\x41\e"`, `"""multi
line"""`, `true`, `false`, `0`, `+1`, `-0`, `-1`, `0x10`, `0o17`, `0b11`, `1_000`, `-9223372036854775808`, `9223372036854775807`, `9223372036854775808`, `1.0`, `-0.0`, `1e-10`, `1e30`, `1e300`, `1e999`, `+inf`, `-inf`, `nan`, `-nan`, `[]`, `[1, "x", false]`, `[[], ["a", ["b"]], {x=1}]`, `{}`, `{x=1,}`, `[
]`, `1979-05-27`, `07:32:00`, `07:32`, `07:32:00.000`, `07:32:00.0100`, `07:32:00.123456789999`, `1979-05-27T07:32:00Z`, `1979-05-27t07:32:00z`, `1979-05-27 07:32:00+00:00`, `1979-05-27T07:32:00-07:00`, `1979-05-27T07:32`, `1979-05-27T07:32Z`, `1979-05-27T07:32:00.000Z`, `1979-02-30`, `25:00:00`, `null`} {
		cases = append(cases, "setting = "+raw)
	}
	data, _ := json.Marshal(cases)
	path := filepath.Join(t.TempDir(), "managed.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(t.Context(), oracle, "managed-toml", path).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	type outcome struct {
		Accepted bool
		Entries  []Entry
	}
	var got []outcome
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if len(got) != len(cases) {
		t.Fatal(len(got), len(cases))
	}
	for i, raw := range cases {
		entries, err := ParseTOMLEntries([]byte(raw))
		want := outcome{Accepted: err == nil, Entries: entries}
		if !reflect.DeepEqual(got[i], want) {
			t.Errorf("%q: Rust %+v; Go %+v (%v)", raw, got[i], want, err)
		}
	}
	t.Logf("compared %d managed TOML source cases", len(cases))
}
