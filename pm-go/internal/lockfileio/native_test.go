package lockfileio

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

	"github.com/nubjs/nub/pm-go/internal/identity"
	"github.com/nubjs/nub/pm-go/internal/manifest"
	"github.com/nubjs/nub/pm-go/internal/testregistry"
)

// These native files contain source information that the raw conversion
// writers don't retain. An unchanged graph must preserve those exact bytes.
func TestNativeFrozenInstallsAfterUnchangedWrite(t *testing.T) {
	for _, kind := range []identity.Kind{identity.Bun, identity.YarnBerry} {
		t.Run(string(kind), func(t *testing.T) {
			variable, version := "PM_BUN_BIN", "1.3.14"
			if kind == identity.YarnBerry {
				variable, version = "PM_YARN_BERRY_CLI", "4.18.0"
			}
			cli := os.Getenv(variable)
			if cli == "" {
				t.Skip("set " + variable + " to the pinned test oracle")
			}
			node, err := exec.LookPath("node")
			if err != nil {
				t.Fatal(err)
			}
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			home := filepath.Join(dir, "home")
			cache := filepath.Join(home, "cache")
			writeFile(t, filepath.Join(home, "user.npmrc"), nil)
			writeFile(t, filepath.Join(home, "global.npmrc"), nil)
			reg := testregistry.Start(t, testregistry.Package{Name: "actual", Version: "1.0.0", Files: map[string]string{"index.js": `module.exports="source"`}})
			var env []string
			for _, value := range os.Environ() {
				key, _, _ := strings.Cut(value, "=")
				key = strings.ToUpper(key)
				if strings.HasPrefix(key, "NPM_CONFIG_") || strings.HasPrefix(key, "NODE_") || strings.HasPrefix(key, "YARN_") || strings.HasPrefix(key, "BUN_") || strings.HasPrefix(key, "XDG_") || key == "HOME" || key == "USERPROFILE" || key == "APPDATA" || key == "LOCALAPPDATA" || key == "CI" {
					continue
				}
				env = append(env, value)
			}
			env = append(env, "HOME="+home, "USERPROFILE="+home, "APPDATA="+home, "LOCALAPPDATA="+home, "XDG_CONFIG_HOME="+home, "XDG_CACHE_HOME="+home, "XDG_DATA_HOME="+home, "CI=1", "npm_config_userconfig="+filepath.Join(home, "user.npmrc"), "npm_config_globalconfig="+filepath.Join(home, "global.npmrc"))
			command := cli
			prefix := []string{}
			if kind == identity.YarnBerry {
				command = node
				prefix = []string{cli}
				env = append(env, "YARN_IGNORE_PATH=1", "YARN_ENABLE_GLOBAL_CACHE=false", "YARN_ENABLE_IMMUTABLE_INSTALLS=false", "YARN_ENABLE_TELEMETRY=0", "YARN_ENABLE_HARDENED_MODE=false")
			}
			run := func(args ...string) []byte {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, command, append(append([]string{}, prefix...), args...)...)
				cmd.Dir, cmd.Env = dir, env
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("%s %v: %v\n%s", kind, args, err, out)
				}
				return out
			}
			if got := strings.TrimSpace(string(run("--version"))); got != version {
				t.Fatal(got)
			}
			rangeText := "1.0.0"
			if kind == identity.Bun {
				rangeText = reg.URL + "/tarballs/actual-1.0.0.tgz"
			}
			pj, err := json.Marshal(map[string]any{"name": "fixture", "version": "1.0.0", "private": true, "dependencies": map[string]string{"actual": rangeText}})
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(dir, "package.json"), pj)
			flags := []string{"install", "--ignore-scripts", "--no-progress", "--registry", reg.URL, "--cache-dir", cache}
			if kind == identity.YarnBerry {
				quote := func(s string) string { b, _ := json.Marshal(s); return string(b) }
				writeFile(t, filepath.Join(dir, "yarn.lock"), []byte("__metadata:\n  version: 10\n  cacheKey: 10c0\n"))
				writeFile(t, filepath.Join(dir, ".yarnrc.yml"), []byte("nodeLinker: node-modules\nnpmRegistryServer: "+quote(reg.URL)+"\nunsafeHttpWhitelist: [127.0.0.1]\ncacheFolder: "+quote(cache)+"\n"))
				flags = []string{"install", "--mode=skip-build"}
			}
			run(flags...)
			path := filepath.Join(dir, kind.Filename())
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			project, err := manifest.ParsePackage(pj)
			if err != nil {
				t.Fatal(err)
			}
			graph, _, err := Read(path, kind, project, ReadOptions{})
			if err != nil {
				t.Fatal(err)
			}
			result, err := Write(path, kind, graph, project, WriteOptions{})
			if err != nil || result.Written {
				t.Fatal(result, err)
			}
			assertFile(t, path, original)
			for _, p := range []string{filepath.Join(dir, "node_modules"), cache} {
				if err := os.RemoveAll(p); err != nil {
					t.Fatal(err)
				}
			}
			frozen := "--frozen-lockfile"
			if kind == identity.YarnBerry {
				frozen = "--immutable"
			}
			run(append(flags, frozen)...)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			probe := exec.CommandContext(ctx, node, "-e", `if(require("actual")!=="source")process.exit(1)`)
			probe.Dir, probe.Env = dir, env
			if out, err := probe.CombinedOutput(); err != nil {
				t.Fatal(string(out), err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(original, after) {
				t.Fatal("frozen install changed native lockfile", err)
			}
		})
	}
}
