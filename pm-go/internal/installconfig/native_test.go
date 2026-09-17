package installconfig

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
	"github.com/nubjs/nub/pm-go/internal/settings"
)

func nativeConfig(t *testing.T, text string) NativeInstall {
	t.Helper()
	v, err := jsonvalue.Parse([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	n, err := ParseNativeInstall(v)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestNativeLayoutAndResolutionScope(t *testing.T) {
	n := nativeConfig(t, `{"linker":{"strategy":"global-virtual-store","eject":["tool","next"]},"publicHoist":[],"minimumReleaseAge":"61s","minimumReleaseAgeExclude":[]}`)
	defaults := []settings.Entry{{"disableGlobalVirtualStoreForPackages", "next,react-native"}, {"diskMaterializePackages", "vite"}}
	for _, native := range []bool{false, true} {
		for _, local := range []bool{false, true} {
			entries, eject, err := n.Lower(defaults, native, local)
			if err != nil {
				t.Fatal(err)
			}
			c := settings.Context{ProjectConfig: entries, Defaults: defaults}
			if !reflect.DeepEqual(eject, []string{"tool", "next"}) {
				t.Fatal(eject)
			}
			if got := c.Strings("diskMaterializePackages"); !reflect.DeepEqual(got, []string{"vite", "tool", "next"}) {
				t.Fatal(got)
			}
			if len(c.Strings("publicHoistPattern")) != 0 || *c.Bool("shamefullyHoist") {
				t.Fatal(entries)
			}
			if native && c.Resolve("minimumReleaseAge") != uint64(2) {
				t.Fatal(entries)
			}
			if !native && c.Explicit("minimumReleaseAge") != nil {
				t.Fatal("foreign resolution influenced", entries)
			}
			if local && c.Explicit("enableGlobalVirtualStore") != nil {
				t.Fatal("ci must drop project GVS=true", entries)
			}
		}
	}
	defaults = append(defaults, settings.Entry{"hoist", "true"})
	entries, _, err := n.Lower(defaults, true, false)
	if err != nil {
		t.Fatal(err)
	}
	c := settings.Context{ProjectConfig: entries, Defaults: defaults}
	if c.Explicit("enableGlobalVirtualStore") != nil {
		t.Fatal("injected-dependency hoist must veto native global request")
	}
}

func TestNativeOverlayReplacesLinkerAndEmptyLists(t *testing.T) {
	global := nativeConfig(t, `{"linker":{"strategy":"global-virtual-store","eject":["old"]},"publicHoist":["*"],"minimumReleaseAge":"3d"}`)
	project := nativeConfig(t, `{"linker":{"strategy":"isolated","hoist":[]},"publicHoist":[]}`)
	merged := OverlayNativeInstall(global, project)
	entries, eject, err := merged.Lower(nil, true, false)
	if err != nil {
		t.Fatal(err)
	}
	c := settings.Context{ProjectConfig: entries}
	if len(eject) != 0 || len(c.Strings("hoistPattern")) != 0 || len(c.Strings("publicHoistPattern")) != 0 || c.Resolve("minimumReleaseAge") != uint64(4320) {
		t.Fatal(entries, eject)
	}
	if c.Resolve("enableGlobalVirtualStore") != false || c.Resolve("hoist") != true {
		t.Fatal(entries)
	}
}

func TestNativeValidationErrors(t *testing.T) {
	for _, text := range []string{`null`, `[]`, `{"linker":null}`, `{"nodeLinker":"isolated"}`, `{"linker":{}}`, `{"linker":{"strategy":"global-virtual-store","hoist":true}}`, `{"linker":{"strategy":"isolated","eject":[]}}`, `{"publicHoist":true}`, `{"publicHoist":[1]}`, `{"minimumReleaseAge":"+3d"}`, `{"minimumReleaseAge":"3"}`, `{"minimumReleaseAge":"18446744073709551615w"}`} {
		v, err := jsonvalue.Parse([]byte(text))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ParseNativeInstall(v); err == nil {
			t.Errorf("accepted %s", text)
		}
	}
	n := nativeConfig(t, `{"linker":"pnp"}`)
	if _, _, err := n.Lower(nil, false, false); err == nil {
		t.Fatal("unsupported PnP accepted")
	}
	n = nativeConfig(t, `{"minimumReleaseAge":"18446744073709551615s"}`)
	entries, _, err := n.Lower(nil, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if entries[0][1] != "307445734561825861" {
		t.Fatal(entries)
	}
}

type nativeCase struct {
	Raw           string           `json:"raw"`
	Defaults      []settings.Entry `json:"defaults"`
	Native, Local bool
}

func nativeOutcome(t *testing.T, c nativeCase) any {
	v, err := jsonvalue.Parse([]byte(c.Raw))
	if err != nil {
		t.Fatal(err)
	}
	n, err := ParseNativeInstall(v)
	if e, ok := err.(*NativeConfigError); ok {
		if e.UnknownKey {
			return map[string]any{"error": map[string]string{"path": e.Path, "key": e.Key}}
		}
		if e.Expected != "" {
			return map[string]any{"error": map[string]string{"path": e.Path, "expected": e.Expected}}
		}
		return map[string]any{"error": map[string]string{"path": e.Path, "message": e.Message}}
	}
	if err != nil {
		t.Fatal(err)
	}
	entries, eject, err := n.Lower(c.Defaults, c.Native, c.Local)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return map[string]any{"entries": entries, "eject": eject}
}

func TestRustNativeInstallOracle(t *testing.T) {
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		t.Skip("set PM_RUST_LOCKFILE_ORACLE for native install settings parity")
	}
	raws := []string{`null`, `[]`, `{}`, `{"a":0,"z":0}`, `{"z":0,"a":0}`, `{"":0}`, `{"publicHoist":true}`, `{"publicHoist":[1]}`, `{"minimumReleaseAgeExclude":null}`}
	for _, strategy := range []string{"global-virtual-store", "isolated", "hoisted", "pnp", "global", ""} {
		q, _ := json.Marshal(strategy)
		raws = append(raws, `{"linker":`+string(q)+`}`, `{"linker":{"strategy":`+string(q)+`}}`)
		for _, option := range []string{`"hoist":true`, `"hoist":false`, `"hoist":[]`, `"hoist":["*","!bad"]`, `"hoist":[0]`, `"hoist":null`, `"eject":[]`, `"eject":["vite","extra","extra"]`, `"eject":false`, `"unexpected":1`} {
			raws = append(raws, `{"linker":{"strategy":`+string(q)+`,`+option+`},"publicHoist":["@types/*"],"minimumReleaseAge":"61s","minimumReleaseAgeExclude":[]}`)
		}
	}
	for _, duration := range []string{"", "s", "3", "3m", "0s", "00d", "61s", "+3d", "-1w", "1.5h", " 3d", "3d ", "٣d", "18446744073709551615s", "18446744073709551616s", "18446744073709551615m"} {
		q, _ := json.Marshal(duration)
		raws = append(raws, `{"minimumReleaseAge":`+string(q)+`}`)
	}
	var cases []nativeCase
	var want []any
	for _, raw := range raws {
		for _, native := range []bool{false, true} {
			for _, local := range []bool{false, true} {
				for _, injected := range []bool{false, true} {
					defaults := []settings.Entry{{"disableGlobalVirtualStoreForPackages", "next,,react-native"}, {"diskMaterializePackages", "old"}, {"diskMaterializePackages", "vite"}}
					if injected {
						defaults = append(defaults, settings.Entry{"hoist", "true"})
					}
					c := nativeCase{raw, defaults, native, local}
					cases = append(cases, c)
					want = append(want, nativeOutcome(t, c))
				}
			}
		}
	}
	data, _ := json.Marshal(cases)
	path := filepath.Join(t.TempDir(), "native.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(t.Context(), oracle, "native-install", path).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	var got, expected []any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	data, _ = json.Marshal(want)
	if err := json.Unmarshal(data, &expected); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(expected) {
		t.Fatal(len(got), len(expected))
	}
	for i := range got {
		if !reflect.DeepEqual(got[i], expected[i]) {
			t.Errorf("%+v: Rust %v; Go %v", cases[i], got[i], expected[i])
		}
	}
	t.Logf("compared %d native install validation/lowering cases", len(cases))
}
