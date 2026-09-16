package npm

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

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
	"github.com/nubjs/nub/pm-go/internal/manifest"
	"github.com/nubjs/nub/pm-go/internal/testregistry"
)

// The production writer needs neither Node nor npm. This opt-in oracle uses a
// pinned npm installation and an in-process registry, without Rust or addons.
func TestNPMOracleAcceptsGoLockfile(t *testing.T) {
	cli := os.Getenv("PM_NPM_CLI")
	if cli == "" {
		t.Skip("set PM_NPM_CLI to npm 11.19.0's npm-cli.js")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	reg := testregistry.Start(t,
		testregistry.Package{Name: "dep", Version: "1.0.0", Files: map[string]string{"index.js": `module.exports="1.0.0"`}},
		testregistry.Package{Name: "dep", Version: "2.0.0", Files: map[string]string{"index.js": `module.exports="2.0.0"`}},
		testregistry.Package{Name: "a", Version: "1.0.0", Manifest: map[string]any{"dependencies": map[string]string{"dep": "^1"}}, Files: map[string]string{"index.js": `module.exports=require("dep")`}},
		testregistry.Package{Name: "b", Version: "1.0.0", Manifest: map[string]any{"dependencies": map[string]string{"dep": "^2"}}, Files: map[string]string{"index.js": `module.exports=require("dep")`}},
		testregistry.Package{Name: "actual", Version: "1.0.0", Files: map[string]string{"index.js": `module.exports="alias-ok"`}},
		testregistry.Package{Name: "plugin", Version: "1.0.0", Manifest: map[string]any{"peerDependencies": map[string]string{"peer": "^1"}}, Files: map[string]string{"index.js": `module.exports=require("peer")`}},
		testregistry.Package{Name: "peer", Version: "1.0.0", Files: map[string]string{"index.js": `module.exports="peer-ok"`}},
		testregistry.Package{Name: "dev-only", Version: "1.0.0"}, testregistry.Package{Name: "opt-only", Version: "1.0.0"},
	)
	for _, name := range []string{"aliases-peers-optional", "workspace", "remote-tarball"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			home := filepath.Join(dir, "home")
			if err := os.Mkdir(home, 0755); err != nil {
				t.Fatal(err)
			}
			user, global := filepath.Join(home, "user.npmrc"), filepath.Join(home, "global.npmrc")
			for _, path := range []string{user, global} {
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			env := []string{}
			for _, item := range os.Environ() {
				key, _, _ := strings.Cut(item, "=")
				upper := strings.ToUpper(key)
				if strings.HasPrefix(upper, "NPM_CONFIG_") || strings.HasPrefix(upper, "NODE_") || upper == "HOME" || upper == "USERPROFILE" || upper == "CI" {
					continue
				}
				env = append(env, item)
			}
			env = append(env, "HOME="+home, "USERPROFILE="+home, "CI=1")
			run := func(args ...string) []byte {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				flags := []string{cli, "--ignore-scripts", "--no-audit", "--no-fund", "--update-notifier=false", "--registry=" + reg.URL, "--cache=" + filepath.Join(home, "cache"), "--userconfig=" + user, "--globalconfig=" + global}
				cmd := exec.CommandContext(ctx, node, append(flags, args...)...)
				cmd.Dir, cmd.Env = dir, env
				output, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("npm %v: %v\n%s", args, err, output)
				}
				return output
			}
			if version := strings.TrimSpace(string(run("--version"))); version != "11.19.0" {
				t.Fatalf("oracle version %s, want 11.19.0", version)
			}
			pkg := map[string]any{"name": "fixture", "version": "1.0.0", "private": true}
			probe := `if(require("alias")!=="alias-ok"||require("a")!=="1.0.0"||require("b")!=="2.0.0"||require("plugin")!=="peer-ok")process.exit(1)`
			switch name {
			case "aliases-peers-optional":
				pkg["dependencies"] = map[string]string{"a": "1.0.0", "b": "1.0.0", "alias": "npm:actual@1.0.0", "plugin": "1.0.0"}
				pkg["devDependencies"] = map[string]string{"dev-only": "1.0.0"}
				pkg["optionalDependencies"] = map[string]string{"opt-only": "1.0.0"}
			case "workspace":
				pkg["dependencies"] = map[string]string{"a": "1.0.0"}
				pkg["workspaces"] = []string{"packages/*"}
				member := filepath.Join(dir, "packages", "member")
				if err := os.MkdirAll(member, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(member, "package.json"), []byte(`{"name":"member","version":"1.0.0","dependencies":{"b":"1.0.0"}}`), 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(member, "index.js"), []byte(`module.exports=require("b")`), 0644); err != nil {
					t.Fatal(err)
				}
				probe = `if(require("a")!=="1.0.0"||require("member")!=="2.0.0")process.exit(1)`
			case "remote-tarball":
				pkg["dependencies"] = map[string]string{"actual": reg.URL + "/tarballs/actual-1.0.0.tgz"}
				probe = `if(require("actual")!=="alias-ok")process.exit(1)`
			}
			manifestBytes, err := json.Marshal(pkg)
			if err != nil {
				t.Fatal(err)
			}
			manifestPath := filepath.Join(dir, "package.json")
			if err := os.WriteFile(manifestPath, manifestBytes, 0644); err != nil {
				t.Fatal(err)
			}
			run("install", "--package-lock-only")
			path := filepath.Join(dir, "package-lock.json")
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			m, err := manifest.ParsePackage(manifestBytes)
			if err != nil {
				t.Fatal(err)
			}
			g, warnings, err := Parse(original, m)
			if err != nil || len(warnings) != 0 {
				t.Fatalf("parse: %v %v", warnings, err)
			}
			var rustWritten []byte
			if oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE"); oracle != "" {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				cmd := exec.CommandContext(ctx, oracle, path, manifestPath)
				cmd.Dir, cmd.Env = dir, env
				output, err := cmd.CombinedOutput()
				cancel()
				if err != nil {
					t.Fatalf("Rust lockfile oracle: %v\n%s", err, output)
				}
				rustWritten, err = os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, original, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if err := Write(path, g, m); err != nil {
				t.Fatal(err)
			}
			written, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if rustWritten != nil && !bytes.Equal(rustWritten, written) {
				t.Fatalf("Rust/Go byte mismatch\nRust:\n%s\nGo:\n%s", rustWritten, written)
			}
			expected := original
			if name == "workspace" {
				// The reference writer's member-local tree retains this extra
				// hoist after its parent is reused at root. Record the precise
				// difference; do not normalize it out of Rust/Go comparisons.
				v, err := jsonvalue.Parse(original)
				if err != nil {
					t.Fatal(err)
				}
				packages := v.Get("packages")
				packages.Put("packages/member/node_modules/dep", packages.Get("node_modules/b/node_modules/dep").Clone())
				expected, err = v.Pretty()
				if err != nil {
					t.Fatal(err)
				}
			}
			if !bytes.Equal(expected, written) {
				t.Fatalf("unexpected npm to Go difference\nexpected:\n%s\nGo:\n%s", expected, written)
			}
			run("ci")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, node, "-e", probe)
			cmd.Dir, cmd.Env = dir, env
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("installed graph: %v\n%s", err, output)
			}
			run("install", "--package-lock-only")
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(original, after) {
				t.Fatalf("npm output changed from its original lockfile\noriginal:\n%s\nafter Go and npm:\n%s", original, after)
			}
		})
	}
}
