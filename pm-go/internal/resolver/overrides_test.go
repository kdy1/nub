package resolver

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestOverrideAncestorAnchorWildcardsAndSpecificity(t *testing.T) {
	ancestors := []AncestorFrame{{"root", "1.0.0"}, {"mid", "2.0.0"}}
	for _, tc := range []struct {
		rules map[string]string
		want  string
	}{
		{map[string]string{"pkg": "bare", "root>pkg": "wrong", "mid>pkg": "parent"}, "parent"},
		{map[string]string{"root>**>pkg": "wild", "root>mid>pkg": "chain"}, "chain"},
		{map[string]string{"**/pkg": "wild", "pkg@<2": "version"}, "version"},
		{map[string]string{"mid/pkg": "slash", "mid>pkg": "arrow"}, "arrow"},
		{map[string]string{"mid@^1>pkg": "wrong", "mid@^2>pkg": "right"}, "right"},
		{map[string]string{"pkg@>=1": "first", "pkg@^1": "last"}, "last"},
		{map[string]string{"root>pkg": "wrong"}, ""},
	} {
		got := CompileOverrides(tc.rules).Pick("pkg", "^1", ancestors)
		if got == nil && tc.want != "" || got != nil && *got != tc.want {
			t.Fatal(tc, got)
		}
	}
	if got := CompileOverrides(map[string]string{"**/pkg": "root"}).Pick("pkg", "*", nil); got == nil || *got != "root" {
		t.Fatal(got)
	}
}
func TestOverrideRangeAndAliasChecks(t *testing.T) {
	for _, tc := range []struct {
		key, requested string
		want           bool
	}{
		{"semver@>=7.5.0", "^7.0.0", true},
		{"semver@>=8", "^7.0.0", false},
		{"semver@>=7 <9.0.6", "npm:@scope/semver@6.0.9-patched.1", false},
		{"semver@>=7 <9.0.6", "jsr:@scope/semver@8.0.0", true},
		{"semver@>=7", "1.2.3", false},
		{"semver@invalid", "1.2.3", true},
		{"semver@>=7", "workspace:*", true},
	} {
		if got := CompileOverrides(map[string]string{tc.key: "replacement"}).Pick("semver", tc.requested, nil); (got != nil) != tc.want {
			t.Fatal(tc, got)
		}
	}
	for raw, want := range map[string]string{"npm:pkg": "pkg", "jsr:^1": "^1", "npm:@scope/pkg": "@scope/pkg", "npm:pkg@": "", "^1": "^1"} {
		if got := stripTaskAlias(raw); got != want {
			t.Fatal(raw, got)
		}
	}
}
func TestRustOverrideRulesOracle(t *testing.T) {
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		t.Skip("reference library CI")
	}
	raw := map[string]string{}
	for _, key := range []string{"pkg", "pkg@<2", "pkg@>=7.5.0", "pkg@>1.0.0", "pkg@<=1.2", "pkg@invalid", "root>pkg", "root@^1>pkg", "mid>pkg", "root>mid>pkg", "root>**>pkg", "**/pkg", "**/mid/pkg", "@scope/parent/pkg", "@scope/parent/@other/pkg@>=1", "parent@>=1 <3>pkg@>1", "parent>>pkg", "parent/", "@scope/", "pkg@", "**", "", ">pkg", "a>b>**", "**>**>pkg", "root>pkg@1.2.3 garbage"} {
		raw[key] = key + "-replacement"
	}
	type task struct {
		Name, Range string
		Ancestors   []AncestorFrame
	}
	var tasks []task
	for _, name := range []string{"pkg", "@other/pkg"} {
		for _, requested := range []string{"*", "^1", "^7.0.0", "1.0.0", "1.2.3", "2.0.0", "latest", "workspace:*", "^1 || ^9"} {
			for _, anc := range [][]AncestorFrame{{}, {{"root", "1.0.0"}}, {{"root", "1.0.0"}, {"mid", "2.0.0"}}, {{"root", "1.0.0"}, {"other", "1.0.0"}, {"mid", "2.0.0"}}, {{"@scope/parent", "2.0.0"}}, {{"parent", "1.2.3"}}} {
				tasks = append(tasks, task{name, requested, anc})
			}
		}
	}
	input, err := json.Marshal(struct {
		Rules map[string]string
		Tasks []task
	}{raw, tasks})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(path, input, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, oracle, "overrides", path).CombinedOutput()
	if err != nil {
		t.Fatal(string(out), err)
	}
	var ref struct {
		Rules   OverrideRules
		Matches [][]bool
	}
	if err := json.Unmarshal(out, &ref); err != nil {
		t.Fatal(string(out), err)
	}
	rules := CompileOverrides(raw)
	if !reflect.DeepEqual(rules, ref.Rules) {
		t.Fatalf("Go: %+v\nRust: %s", rules, out)
	}
	if len(ref.Matches) != len(tasks) {
		t.Fatal("result count")
	}
	for i, task := range tasks {
		if len(ref.Matches[i]) != len(rules) {
			t.Fatal("rule count")
		}
		for j, rule := range rules {
			if got := rule.Matches(task.Name, task.Range, task.Ancestors); got != ref.Matches[i][j] {
				t.Errorf("%s task %+v: Go=%v Rust=%v", rule.RawKey, task, got, ref.Matches[i][j])
			}
		}
	}
	t.Logf("compared %d compiled rules across %d tasks", len(rules), len(tasks))
}
