package bun

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nubjs/nub/pm-go/internal/manifest"
	"github.com/nubjs/nub/pm-go/internal/testregistry"
)

// Bun is only an isolated test oracle; the Go adapter has no Node dependency.
func TestPinnedBunAcceptsGoLockfile(t *testing.T) {
	cli := os.Getenv("PM_BUN_BIN")
	if cli == "" {
		t.Skip("set PM_BUN_BIN to Bun 1.3.14 executable")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	reg := testregistry.Start(t,
		testregistry.Package{Name: "dep", Version: "1.0.0", Files: map[string]string{"index.js": `module.exports="1"`}},
		testregistry.Package{Name: "dep", Version: "2.0.0", Files: map[string]string{"index.js": `module.exports="2"`}},
		testregistry.Package{Name: "a", Version: "1.0.0", Manifest: map[string]any{"dependencies": map[string]string{"dep": "^1"}}, Files: map[string]string{"index.js": `module.exports=require("dep")`}},
		testregistry.Package{Name: "b", Version: "1.0.0", Manifest: map[string]any{"dependencies": map[string]string{"dep": "^2"}}, Files: map[string]string{"index.js": `module.exports=require("dep")`}},
		testregistry.Package{Name: "actual", Version: "1.0.0", Files: map[string]string{"index.js": `module.exports="alias"`}},
		testregistry.Package{Name: "plugin", Version: "1.0.0", Manifest: map[string]any{"peerDependencies": map[string]string{"peer": "^1"}}, Files: map[string]string{"index.js": `module.exports=require("peer")`}},
		testregistry.Package{Name: "peer", Version: "1.0.0", Files: map[string]string{"index.js": `module.exports="peer"`}},
		testregistry.Package{Name: "dev-only", Version: "1.0.0"}, testregistry.Package{Name: "opt-only", Version: "1.0.0"},
	)
	for _, name := range []string{"aliases-peers-optional", "workspace", "catalog", "remote-tarball"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			home := filepath.Join(dir, "home")
			write := func(path, body string) {
				t.Helper()
				p := filepath.Join(dir, path)
				if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			write("home/user.npmrc", "")
			write("home/global.npmrc", "")
			env := []string{}
			for _, s := range os.Environ() {
				key, _, _ := strings.Cut(s, "=")
				key = strings.ToUpper(key)
				if strings.HasPrefix(key, "NPM_CONFIG_") || strings.HasPrefix(key, "NODE_") || strings.HasPrefix(key, "BUN_") || strings.HasPrefix(key, "XDG_") || key == "HOME" || key == "USERPROFILE" || key == "CI" || key == "APPDATA" || key == "LOCALAPPDATA" {
					continue
				}
				env = append(env, s)
			}
			env = append(env, "HOME="+home, "USERPROFILE="+home, "APPDATA="+home, "LOCALAPPDATA="+home, "XDG_CACHE_HOME="+home, "XDG_CONFIG_HOME="+home, "XDG_DATA_HOME="+home, "XDG_STATE_HOME="+home, "CI=1", "npm_config_userconfig="+filepath.Join(home, "user.npmrc"), "npm_config_globalconfig="+filepath.Join(home, "global.npmrc"), "npm_config_update_notifier=false")
			run := func(args ...string) []byte {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, cli, args...)
				cmd.Dir, cmd.Env = dir, env
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("bun %v: %v\n%s", args, err, out)
				}
				return out
			}
			if got := strings.TrimSpace(string(run("--version"))); got != "1.3.14" {
				t.Fatalf("bun version %s", got)
			}
			pkg := map[string]any{"name": "fixture", "version": "1.0.0", "private": true}
			probe := `if(require("alias")!=="alias"||require("a")!=="1"||require("b")!=="2"||require("plugin")!=="peer")process.exit(1)`
			switch name {
			case "aliases-peers-optional":
				pkg["dependencies"] = map[string]string{"a": "1.0.0", "b": "1.0.0", "alias": "npm:actual@1.0.0", "plugin": "1.0.0", "peer": "1.0.0"}
				pkg["devDependencies"] = map[string]string{"dev-only": "1.0.0"}
				pkg["optionalDependencies"] = map[string]string{"opt-only": "1.0.0"}
			case "workspace":
				pkg["dependencies"] = map[string]string{"a": "1.0.0"}
				pkg["workspaces"] = []string{"packages/*"}
				write("packages/app/package.json", `{"name":"app","version":"1.0.0","dependencies":{"@fixture/core":"workspace:^","b":"1.0.0"}}`)
				write("packages/app/index.js", `module.exports=[require("@fixture/core"),require("b")]`)
				write("packages/core/package.json", `{"name":"@fixture/core","version":"1.0.0"}`)
				write("packages/core/index.js", `module.exports="core"`)
				probe = `if(require("a")!=="1"||JSON.stringify(require("./packages/app"))!=='["core","2"]')process.exit(1)`
			case "catalog":
				pkg["dependencies"] = map[string]string{"a": "catalog:"}
				pkg["workspaces"] = map[string]any{"packages": []string{}, "catalog": map[string]string{"a": "1.0.0"}}
				probe = `if(require("a")!=="1")process.exit(1)`
			case "remote-tarball":
				pkg["dependencies"] = map[string]string{"actual": reg.URL + "/tarballs/actual-1.0.0.tgz"}
				probe = `if(require("actual")!=="alias")process.exit(1)`
			}
			manifestBytes, err := json.Marshal(pkg)
			if err != nil {
				t.Fatal(err)
			}
			write("package.json", string(manifestBytes))
			flags := []string{"install", "--ignore-scripts", "--save-text-lockfile", "--registry=" + reg.URL, "--cache-dir=" + filepath.Join(home, "cache")}
			run(append(flags, "--lockfile-only")...)
			path := filepath.Join(dir, "bun.lock")
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			project, err := manifest.ParsePackage(manifestBytes)
			if err != nil {
				t.Fatal(err)
			}
			g, warnings, err := Parse(original, Options{})
			if err != nil || len(warnings) > 0 {
				t.Fatal(warnings, err)
			}
			if err := Write(path, g, project); err != nil {
				t.Fatal(err)
			}
			written, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			assertNativeRewrite(t, name, original, written)
			compareNativeRustWriter(t, dir, original, written)
			if err := os.RemoveAll(filepath.Join(home, "cache")); err != nil {
				t.Fatal(err)
			}
			run(append(flags, "--frozen-lockfile")...)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, node, "-e", probe)
			cmd.Dir, cmd.Env = dir, env
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("installed graph: %v\n%s", err, out)
			}
			run(append(flags, "--lockfile-only")...)
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			// Bun restores its own nested ordering and tarball tuple shape.
			if !bytes.Equal(original, after) {
				t.Fatalf("bun rewrote more than the recorded reference differences\nnative:\n%s\nafter:\n%s", original, after)
			}
		})
	}
}

// These are explicit baseline writer differences from native Bun, not graph
// normalization. Every other byte must match, and Rust/Go compare unmodified
// output bytes in the reference job.
func assertNativeRewrite(t *testing.T, name string, original, written []byte) {
	t.Helper()
	expected := string(original)
	switch name {
	case "aliases-peers-optional", "workspace":
		lines := strings.Split(expected, "\n")
		nested := ""
		for i, line := range lines {
			if strings.HasPrefix(line, `    "b/dep":`) {
				nested = line
				lines = append(lines[:i-1], lines[i+1:]...)
				break
			}
		}
		if nested == "" {
			t.Fatal("native fixture lost nested dependency")
		}
		for i, line := range lines {
			if strings.HasPrefix(line, `    "dep":`) {
				lines = append(lines[:i], append([]string{nested, ""}, lines[i:]...)...)
				break
			}
		}
		expected = strings.Join(lines, "\n")
	case "remote-tarball":
		if strings.Count(expected, `", {}, "sha512-`) != 1 {
			t.Fatal("native tarball tuple shape changed")
		}
		expected = strings.Replace(expected, `", {}, "sha512-`, `", "", {}, "sha512-`, 1)
	}
	if !bytes.Equal([]byte(expected), written) {
		t.Fatalf("Go differs from the reference's expected native rewrite\nexpected:\n%s\nGo:\n%s", expected, written)
	}
}
func compareNativeRustWriter(t *testing.T, dir string, original, written []byte) {
	t.Helper()
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		return
	}
	input, output := filepath.Join(dir, "reference.input"), filepath.Join(dir, "reference.output")
	if err := os.WriteFile(input, original, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, oracle, "bun-write", input, filepath.Join(dir, "package.json"), output, "strict").CombinedOutput()
	if err != nil {
		t.Fatal(string(out), err)
	}
	var ref struct {
		OK    bool
		Error string
	}
	if err := json.Unmarshal(out, &ref); err != nil || !ref.OK {
		t.Fatal(string(out), err)
	}
	want, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, written) {
		t.Fatalf("native fixture Rust/Go bytes differ\nRust: %s\nGo: %s", want, written)
	}
}
