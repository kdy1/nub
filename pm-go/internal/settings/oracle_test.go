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

type oracleCase struct {
	Name              string  `json:"name"`
	Explicit          bool    `json:"explicit"`
	Pnpm              bool    `json:"pnpm"`
	CLI               []Entry `json:"cli"`
	Env               []Entry `json:"env"`
	ProjectConfig     []Entry `json:"projectConfig"`
	ProjectNpmrc      []Entry `json:"projectNpmrc"`
	UserNpmrc         []Entry `json:"userNpmrc"`
	ProjectToolConfig []Entry `json:"projectToolConfig"`
	UserToolConfig    []Entry `json:"userToolConfig"`
	Defaults          []Entry `json:"defaults"`
	Managed           []Entry `json:"managed"`
	WorkspaceYAML     string  `json:"workspaceYAML,omitempty"`
	GlobalYAML        string  `json:"globalYAML,omitempty"`
}

func (o oracleCase) context(t *testing.T) Context {
	return Context{Pnpm: o.Pnpm, CLI: o.CLI, Env: o.Env, ProjectConfig: o.ProjectConfig, ProjectNpmrc: o.ProjectNpmrc, UserNpmrc: o.UserNpmrc, ProjectToolConfig: o.ProjectToolConfig, UserToolConfig: o.UserToolConfig, Defaults: o.Defaults, Managed: o.Managed, WorkspaceYAML: document(t, o.WorkspaceYAML), GlobalYAML: document(t, o.GlobalYAML)}
}

func settingsCorpus() []oracleCase {
	var cases []oracleCase
	for _, name := range Names() {
		d := definitions[name]
		cases = append(cases, oracleCase{Name: name})
		if d.Kind == "unsupported" {
			continue
		}
		raw := map[string]string{"bool": "FALSE", "int": "+4294967296", "string": "  value ", "list": `["a",'b',"a,b", ""]`}[d.Kind]
		if d.Kind == "enum" {
			raw = " " + strings.ToUpper(d.Variants[len(d.Variants)-1]) + " "
		}
		for i := range 8 {
			c := oracleCase{Name: name}
			entry := []Entry{{name, raw}}
			switch i {
			case 0:
				c.CLI = entry
			case 1:
				c.ProjectConfig = entry
			case 2:
				c.ProjectNpmrc = entry
			case 3:
				c.UserNpmrc = entry
			case 4:
				c.ProjectToolConfig = entry
			case 5:
				c.UserToolConfig = entry
			case 6:
				c.Defaults = entry
			case 7:
				c.Managed = entry
			}
			cases = append(cases, c)
		}
		for _, raw := range []string{"", "bad-value", "18446744073709551616", "yes"} {
			cases = append(cases, oracleCase{Name: name, CLI: []Entry{{kebab(name), raw}}, ProjectNpmrc: []Entry{{name, "true"}}})
		}
		for _, alias := range d.Env {
			for _, pnpm := range []bool{false, true} {
				cases = append(cases, oracleCase{Name: name, Pnpm: pnpm, Env: []Entry{{alias, raw}}, ProjectConfig: []Entry{{name, "true"}}})
			}
		}
		for _, key := range d.Npmrc {
			cases = append(cases, oracleCase{Name: name, ProjectNpmrc: []Entry{{key, raw}, {key, "bad-value"}}})
		}
		for _, key := range d.YAML {
			for _, raw := range []string{`"false"`, "false", "17", `"+21"`, "[one, 1, false, null, two]", `"a,b"`, "{}"} {
				parts := strings.Split(key, ".")
				text := raw
				for i := len(parts) - 1; i >= 0; i-- {
					k, _ := json.Marshal(parts[i])
					text = "{" + string(k) + ": " + text + "}"
				}
				cases = append(cases, oracleCase{Name: name, Pnpm: true, WorkspaceYAML: text, ProjectNpmrc: []Entry{{name, "true"}}}, oracleCase{Name: name, Pnpm: true, GlobalYAML: text})
			}
		}
		if d.Explicit {
			cases = append(cases, oracleCase{Name: name, Explicit: true}, oracleCase{Name: name, Explicit: true, Defaults: []Entry{{name, raw}}})
		}
	}
	for _, raw := range []string{"0x10", "0o10", "0b10", "1_000", "1.0", "1e3", "-0.0", "1e-6", "1e16", ".nan", ".inf", "-.inf", "true", "!custom value", "012", "00", "+012", "-012", "+0", "18446744073709551615", "18446744073709551616", "1e999", "+1.0", ".5", "1.", "1E+20", "2026-09-17", "null", "~", "yes", "on", "y", "1_0.5", "0X10"} {
		for _, name := range []string{"savePrefix", "networkConcurrency", "autoInstallPeers"} {
			cases = append(cases, oracleCase{Name: name, Pnpm: true, WorkspaceYAML: name + ": " + raw})
		}
		cases = append(cases, oracleCase{Name: "gitShallowHosts", Pnpm: true, WorkspaceYAML: "gitShallowHosts: [" + raw + "]"})
	}
	for _, name := range []string{"minimumReleaseAge", "minimumReleaseAgeExclude", "advisoryCheck", "dangerouslyAllowAllBuilds"} {
		for _, cli := range []string{"false", "true", "100", "off", "required", "a,b,c", "invalid"} {
			cases = append(cases, oracleCase{Name: name, CLI: []Entry{{name, cli}}, Managed: []Entry{{name, "false"}, {name, "true"}, {name, "20"}, {name, "on"}, {name, "a,b"}, {name, "b,c"}}})
		}
	}
	return cases
}

func TestRustSettingsOracle(t *testing.T) {
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		t.Skip("set PM_RUST_LOCKFILE_ORACLE for typed settings parity")
	}
	cases := settingsCorpus()
	var want []any
	for _, c := range cases {
		ctx := c.context(t)
		if c.Explicit {
			want = append(want, ctx.Explicit(c.Name))
		} else {
			want = append(want, ctx.Resolve(c.Name))
		}
	}
	data, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(t.Context(), oracle, "settings", path).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	var got, expected []json.RawMessage
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	data, err = json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &expected); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(expected) {
		t.Fatal(len(got), len(expected))
	}
	for i := range got {
		if !reflect.DeepEqual(got[i], expected[i]) {
			t.Errorf("case %+v: Rust %s; Go %s", cases[i], got[i], expected[i])
		}
	}
	t.Logf("compared %d typed settings cases across %d catalog entries", len(cases), len(definitions))
}
