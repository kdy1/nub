package installconfig

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/identity"
)

func writeRC(t *testing.T, dir, text string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".yarnrc.yml"), []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestYarnLinkerScalarScan(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct{ text, want string }{
		{"nodeLinker: pnp\n", "pnp"}, {"nodeLinker: 'PnP' # hi\n", "pnp"},
		{"nodeLinker: \"node-modules\"\r\n", "node-modules"}, {"nodeLinker: hoisted # comment\n", "hoisted"},
		{"  nodeLinker: pnp\n", ""}, {"\tnodeLinker: pnp\n", ""},
		{"nodeLinker:\nnodeLinker: pnp\n", "pnp"}, {"nodeLinker: hoisted\nnodeLinker: pnp\n", "hoisted"},
		{"nodeLinker: ' pnp '\n", " pnp "}, {"NodeLinker: pnp\n", ""},
		{"nodeLinker: \"pnp#quoted\" # comment\n", "pnp#quoted"}, {"nodeLinker: \"pnp # broken\n", "\"pnp"},
		{"\xef\xbb\xbfnodeLinker: pnp\n", ""}, {"nodeLinker: pnp\n#\xff", ""},
	} {
		writeRC(t, dir, c.text)
		if got := YarnRCNodeLinker(dir); got != c.want {
			t.Errorf("%q: %q, want %q", c.text, got, c.want)
		}
	}
}

func TestYarnPnPIdentityAndLayerPrecedence(t *testing.T) {
	outer, home := t.TempDir(), t.TempDir()
	root := filepath.Join(outer, "root")
	cwd := filepath.Join(root, "packages/app")
	if err := os.MkdirAll(cwd, 0755); err != nil {
		t.Fatal(err)
	}
	in := YarnLinkerInput{Kind: identity.YarnBerry, Root: root, CWD: cwd, Home: home, Env: map[string]string{}, MutatingInstall: true}
	check := func(want bool) {
		t.Helper()
		var unsupported *PnPUnsupported
		err := CheckYarnPnP(in)
		if errors.As(err, &unsupported) != want || !want && err != nil {
			t.Fatal(in.Kind, want, err)
		}
	}
	check(true) // Berry default.
	in.Kind = identity.Yarn
	check(false)
	writeRC(t, home, "nodeLinker: pnp\n")
	check(true)
	writeRC(t, outer, "nodeLinker: node-modules\n")
	check(false) // An ancestor above the project root still overrides home.
	writeRC(t, root, "nodeLinker: pnp\n")
	check(true)
	writeRC(t, cwd, "nodeLinker: node-modules\n")
	check(false)
	in.Env["YARN_NODE_LINKER"] = " PnP "
	check(true)
	in.Env["YARN_NODE_LINKER"] = "   "
	check(false)
	in.CWD = filepath.Join(outer, "elsewhere")
	writeRC(t, in.CWD, "nodeLinker: node-modules\n")
	check(true) // An unrelated cwd cannot override the selected project.
	for _, kind := range []identity.Kind{identity.Nub, identity.Npm, identity.Shrinkwrap, identity.Pnpm, identity.Bun, ""} {
		in.Kind = kind
		check(false)
	}
	in.Kind, in.MutatingInstall = identity.YarnBerry, false
	check(false)
	if _, err := os.Stat(filepath.Join(root, "node_modules")); !os.IsNotExist(err) {
		t.Fatal("plan-time guard mutated the project", err)
	}
}

func TestNodeLinkerCLIOverridesSettingsAndRejectsUnknown(t *testing.T) {
	for _, value := range []string{" isolated ", "HOISTED", "pnp", "PnP", "unknown", ""} {
		for _, cli := range []bool{false, true} {
			var override *string
			configured := value
			if cli {
				override, configured = &value, "pnp"
			}
			got, err := ResolveNodeLinker(override, configured)
			switch value {
			case " isolated ":
				if err != nil || got != Isolated {
					t.Fatal(got, err)
				}
			case "HOISTED":
				if err != nil || got != Hoisted {
					t.Fatal(got, err)
				}
			case "pnp", "PnP":
				if err == nil {
					t.Fatal("PnP accepted")
				}
			default:
				if (err != nil) != cli || !cli && got != Isolated {
					t.Fatal(got, err)
				}
			}
		}
	}
}
