package settings

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/npmconfig"
)

func TestFileSourcesGateForeignPathsAndReload(t *testing.T) {
	base := t.TempDir()
	home, project, config := filepath.Join(base, "home"), filepath.Join(base, "project"), filepath.Join(base, "config")
	write := func(path, data string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, ".npmrc"), "network-concurrency=3\n")
	write(filepath.Join(project, ".npmrc"), "network-concurrency=5\nmodules-dir=custom\n")
	write(filepath.Join(home, ".config/aube/config.toml"), "networkConcurrency=100\n")
	write(filepath.Join(project, "aube.toml"), "networkConcurrency=200\n")
	global := filepath.Join(config, "pnpm/config.yaml")
	write(global, "networkConcurrency: 11\npreferFrozenLockfile: false\n")
	workspacePath := filepath.Join(project, "pnpm-workspace.yaml")
	write(workspacePath, "networkConcurrency: 8\nnodeLinker: hoisted\n")
	for _, pnpm := range []bool{false, true} {
		for _, v11 := range []bool{false, true} {
			files := npmconfig.Files{Dir: project, Home: home, OS: "linux", Pnpm: pnpm, Pnpm11: v11, Env: map[string]string{"XDG_CONFIG_HOME": config}}
			sources, err := LoadSources(FileInput{Files: files, SystemManagedPath: new("")})
			if err != nil {
				t.Fatal(err)
			}
			workspace, err := LoadWorkspaceYAML(project, pnpm)
			if err != nil {
				t.Fatal(err)
			}
			c := sources.Context(workspace, nil, nil)
			want := uint64(5)
			if pnpm {
				want = 8
			}
			if *c.Uint64("networkConcurrency") != want || *c.String("modulesDir") != "custom" || *c.String("nodeLinker") != "isolated" {
				t.Fatal(pnpm, v11, c.Resolve("networkConcurrency"), c.Resolve("modulesDir"), c.Resolve("nodeLinker"))
			}
			if (c.Bool("preferFrozenLockfile") != nil) != (pnpm && v11) {
				t.Fatal("global YAML crossed identity/major boundary", pnpm, v11)
			}
			withoutWorkspace := sources.Context(nil, nil, nil)
			if pnpm && v11 && *withoutWorkspace.Uint64("networkConcurrency") != 11 {
				t.Fatal(withoutWorkspace)
			}
			// A derived context cannot mutate the stored CLI/file source bags.
			c.ProjectNpmrc[0][1] = "999"
			if reflect.DeepEqual(c.ProjectNpmrc, sources.Npmrc.Project) {
				t.Fatal("context aliases source entries")
			}
		}
	}
	write(workspacePath, "bad: [unterminated")
	if _, err := LoadWorkspaceYAML(project, false); err != nil {
		t.Fatal("read foreign pnpm file", err)
	}
	if _, err := LoadWorkspaceYAML(project, true); err == nil {
		t.Fatal("malformed project YAML accepted")
	}
	files := npmconfig.Files{Dir: project, Home: home, OS: "linux", Pnpm: true, Pnpm11: true, Env: map[string]string{"XDG_CONFIG_HOME": config}}
	for _, raw := range []string{"", "null", "[invalid", "minimumReleaseAge: 18446744073709551616", "networkConcurrency: 17\nunknown: {a: 1, a: 2}"} {
		write(global, raw)
		sources, err := LoadSources(FileInput{Files: files, SystemManagedPath: new("")})
		if err != nil || sources.Context(nil, nil, nil).Explicit("networkConcurrency") != nil {
			t.Fatal(raw, err)
		}
	}
	write(global, "networkConcurrency: 19")
	sources, err := LoadSources(FileInput{Files: files, SystemManagedPath: new("")})
	if err != nil || *sources.Context(nil, nil, nil).Uint64("networkConcurrency") != 19 {
		t.Fatal(err)
	}
}

func yamlSourceCorpus() []string {
	cases := []string{"", "# comment", "{}", "[]", "null", "~", `""`, "---", "---\n...", "{}\n---\n{}", "savePrefix: one\nsavePrefix: two", "unknown: {a: 1, a: 2}", "unknown: {1: a, 1.0: b}", "unknown: {0x10: a, 16: b}", "unknown: {true: a, TRUE: b}", "unknown: {[a,b]: 1, [a,b]: 2}", "unknown: &a [*a]", "unknown: &a {b: 1}\nother: *a", "true: value\nnull: value\n12: value", "? [a,b]\n: value", "!!map {savePrefix: test}", "!custom {savePrefix: test}", "updateConfig: {ignoreDependencies: [a, !custom b, 1]}", "updateConfig: !custom {ignoreDependencies: [a]}", "default: &a {savePrefix: ignored}\n<<: *a", "\uFEFFsavePrefix: bom"}
	for _, scalar := range []string{"true", "False", "TrUE", "null", "NULL", "012", "+012", "-012", "+-1", "0x10", "-0x10", "+0x10", "0X10", "0o12", "0b10", "1_000", "1e999", "1e-999", "1e16", "1E+20", "1.0", "1.", ".5", "-.0", ".inf", "+.inf", "-.inf", ".nan", "+.nan", "-.nan", "18446744073709551615", "18446744073709551616", "-9223372036854775809", "340282366920938463463374607431768211455", "340282366920938463463374607431768211456", "-170141183460469231731687303715884105728", "-170141183460469231731687303715884105729", "2026-09-17", "!!str 12", "!!str true", "!!int '12'", "!!int 012", "!!bool yes", "!!float 1e999", "!!timestamp 2026-09-17", "!!binary YQ==", "!custom 12", "!custom '12'", "!<tag:example.test> true", "!!null ''"} {
		cases = append(cases, "savePrefix: "+scalar, "networkConcurrency: "+scalar, "minimumReleaseAgeExclude: ["+scalar+"]")
	}
	for _, depth := range []int{126, 127, 128, 129} {
		cases = append(cases, "unknown: "+strings.Repeat("[", depth)+"0"+strings.Repeat("]", depth))
	}
	return cases
}

func yamlSourceResult(raw string) any {
	node, err := ParseYAMLSource([]byte(raw))
	if err != nil {
		return map[string]any{"accepted": false}
	}
	keys := []string{}
	for i := 0; i < len(node.Content); i += 2 {
		keys = append(keys, node.Content[i].Value)
	}
	slices.Sort(keys)
	context := Context{Pnpm: true, WorkspaceYAML: node}
	values := []any{}
	for _, name := range []string{"savePrefix", "networkConcurrency", "autoInstallPeers", "minimumReleaseAgeExclude", "updateConfig.ignoreDependencies"} {
		values = append(values, context.Resolve(name))
	}
	return map[string]any{"accepted": true, "keys": keys, "values": values}
}

func TestYAMLSourceValidationAndRootKeys(t *testing.T) {
	for _, depth := range []int{126, 127, 128, 129} {
		_, err := ParseYAMLSource([]byte("unknown: " + strings.Repeat("[", depth) + "0" + strings.Repeat("]", depth)))
		if (err == nil) != (depth <= 127) {
			t.Fatal("collection nesting boundary", depth, err)
		}
	}
	for _, raw := range []string{"unknown: {a: 1, a: 2}", "unknown: 18446744073709551616", "unknown: &a [*a]", "{}\n---\n{}", "null"} {
		if _, err := ParseYAMLSource([]byte(raw)); err == nil {
			t.Fatal("accepted invalid source", raw)
		}
	}
	node, err := ParseYAMLSource([]byte("savePrefix: old\nsavePrefix: new\ntrue: kept\nminimumReleaseAgeExclude: [012, !custom string, !custom true]\n"))
	if err != nil {
		t.Fatal(err)
	}
	c := Context{Pnpm: true, WorkspaceYAML: node}
	if *c.String("savePrefix") != "new" || !reflect.DeepEqual(c.Strings("minimumReleaseAgeExclude"), []string{"012", "string"}) {
		t.Fatal(node)
	}
}

func TestRustYAMLSourceOracle(t *testing.T) {
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		t.Skip("set PM_RUST_LOCKFILE_ORACLE for YAML source parity")
	}
	cases := yamlSourceCorpus()
	data, _ := json.Marshal(cases)
	path := filepath.Join(t.TempDir(), "sources.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(t.Context(), oracle, "settings-yaml", path).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	var got, want []any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	for _, raw := range cases {
		want = append(want, yamlSourceResult(raw))
	}
	data, _ = json.Marshal(want)
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(cases) {
		t.Fatal(len(got), len(cases))
	}
	for i := range got {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("%q: Rust %v; Go %v", cases[i], got[i], want[i])
		}
	}
	t.Logf("compared %d YAML source parsing/settings cases", len(cases))
}
