package settings

import (
	"reflect"
	"testing"

	"go.yaml.in/yaml/v4"
)

func document(t *testing.T, text string) *yaml.Node {
	t.Helper()
	if text == "" {
		return nil
	}
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(text), &node); err != nil {
		t.Fatal(err)
	}
	return &node
}

func TestSourcePrecedenceAndInvalidValues(t *testing.T) {
	c := Context{ProjectConfig: []Entry{{"networkConcurrency", "7"}}, ProjectNpmrc: []Entry{{"network-concurrency", "8"}}, Env: []Entry{{"npm_config_network_concurrency", "5"}}}
	if got := c.Resolve("networkConcurrency"); got != uint64(5) {
		t.Fatal(got)
	}
	c.ConfigOverrides = []Entry{{"NETWORK_CONCURRENCY", "4"}}
	if got := c.Resolve("networkConcurrency"); got != uint64(4) {
		t.Fatal(got)
	}
	c.CLI = []Entry{{"networkConcurrency", "3"}, {"networkConcurrency", "-1"}}
	if got := c.Resolve("networkConcurrency"); got != uint64(3) {
		t.Fatal(got)
	}
	c.CLI, c.ConfigOverrides = nil, nil
	c.Env = []Entry{{"npm_config_network_concurrency", "5"}, {"PNPM_CONFIG_NETWORK_CONCURRENCY", "invalid"}}
	c.Pnpm = true
	if got := c.Resolve("networkConcurrency"); got != uint64(7) {
		t.Fatal("invalid winning env alias must fall to file source", got)
	}
	c = Context{ProjectNpmrc: []Entry{{"node-linker", "unknown"}}, UserNpmrc: []Entry{{"node-linker", "hoisted"}}}
	if got := c.Resolve("nodeLinker"); got != "isolated" {
		t.Fatal("bad enum masks lower source and uses builtin", got)
	}
}

func TestIdentityAndLayoutGates(t *testing.T) {
	c := Context{WorkspaceYAML: document(t, "autoInstallPeers: false\nnodeLinker: hoisted\n"), Env: []Entry{{"AUBE_NODE_LINKER", "hoisted"}, {"PNPM_CONFIG_NODE_LINKER", "hoisted"}}}
	if got := c.Resolve("nodeLinker"); got != "isolated" {
		t.Fatal(got)
	}
	if got := c.Resolve("autoInstallPeers"); got != true {
		t.Fatal(got)
	}
	c.Pnpm = true
	if got := c.Resolve("autoInstallPeers"); got != false {
		t.Fatal(got)
	}
	if got := c.Resolve("nodeLinker"); got != "hoisted" {
		t.Fatal(got)
	}
	c.Env = nil
	if got := c.Resolve("nodeLinker"); got != "isolated" {
		t.Fatal("YAML must not choose Nub layout", got)
	}
	if c.Explicit("hoist") != nil || c.Resolve("hoist") != true {
		t.Fatal("implicit hoist default")
	}
	c.Defaults = []Entry{{"hoist", "true"}}
	if c.Explicit("hoist") != true {
		t.Fatal("embedder value counts as explicit")
	}
}

func TestManagedHardening(t *testing.T) {
	var warnings []string
	c := Context{CLI: []Entry{{"minimumReleaseAge", "1"}, {"dangerouslyAllowAllBuilds", "true"}, {"advisoryCheck", "OFF"}, {"minimumReleaseAgeExclude", "one,two"}}, Managed: []Entry{{"minimumReleaseAge", "20"}, {"minimum-release-age", "10"}, {"dangerouslyAllowAllBuilds", "false"}, {"advisoryCheck", "on"}, {"advisoryCheck", "required"}, {"minimumReleaseAgeExclude", "one,two,one"}, {"minimumReleaseAgeExclude", "one"}}, Warn: func(_, text string) { warnings = append(warnings, text) }}
	for name, want := range map[string]any{"minimumReleaseAge": uint64(20), "dangerouslyAllowAllBuilds": false, "advisoryCheck": "required", "minimumReleaseAgeExclude": []string{"one", "one"}} {
		if got := c.Resolve(name); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v != %v", name, got, want)
		}
	}
	if len(warnings) != 4 {
		t.Fatal(warnings)
	}
	c.CLI = []Entry{{"minimumReleaseAge", "100"}}
	if c.Resolve("minimumReleaseAge") != uint64(100) {
		t.Fatal("managed policy weakened CLI")
	}
}

func TestIndependentInvocationValues(t *testing.T) {
	a := Context{ProjectNpmrc: []Entry{{"hoist-pattern", "[\"a\", 'b']"}}}
	list := a.Strings("hoistPattern")
	list[0] = "mutated"
	if got := a.Strings("hoistPattern"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatal(got)
	}
	b := Context{}
	defaults := b.Strings("hoistPattern")
	if len(defaults) != 0 {
		defaults[0] = "mutated"
	}
	if got := b.Strings("hoistPattern"); !reflect.DeepEqual(got, []string{"*"}) {
		t.Fatal(got)
	}
	if b.Resolve("doesNotExist") != nil || b.Resolve("overrides") != nil {
		t.Fatal("unknown/complex setting resolved")
	}
}
