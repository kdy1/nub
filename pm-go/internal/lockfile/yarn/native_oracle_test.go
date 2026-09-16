package yarn

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nubjs/nub/pm-go/internal/manifest"
	"github.com/nubjs/nub/pm-go/internal/testregistry"
)

// The real Yarn executables are test oracles only; Go never invokes them.
func TestPinnedYarnRegistryCompatibility(t *testing.T) {
	packages := []testregistry.Package{
		testregistry.Package{Name: "dep", Version: "1.0.0", Files: map[string]string{"index.js": `module.exports="one"`}},
		testregistry.Package{Name: "dep", Version: "2.0.0", Files: map[string]string{"index.js": `module.exports="two"`}},
		testregistry.Package{Name: "a", Version: "1.0.0", Manifest: map[string]any{"dependencies": map[string]string{"dep": "^1"}}, Files: map[string]string{"index.js": `module.exports=require("dep")`}},
		testregistry.Package{Name: "b", Version: "1.0.0", Manifest: map[string]any{"dependencies": map[string]string{"dep": "^2"}}, Files: map[string]string{"index.js": `module.exports=require("dep")`}},
	}
	for _, scenario := range []struct{ berry, custom bool }{{false, true}, {true, false}, {true, true}} {
		berry, custom := scenario.berry, scenario.custom
		version, variable := "1.22.22", "PM_YARN_CLASSIC_CLI"
		if berry {
			version, variable = "4.18.0", "PM_YARN_BERRY_CLI"
		}
		name := version
		if custom {
			name += "-custom-archive"
		}
		t.Run(name, func(t *testing.T) {
			cli := os.Getenv(variable)
			if cli == "" {
				t.Skip("set " + variable + " to the pinned Yarn bin/yarn.js")
			}
			fixtures := append([]testregistry.Package(nil), packages...)
			if !custom {
				for i := range fixtures {
					p := &fixtures[i]
					p.TarballPath = "/" + p.Name + "/-/" + p.Name + "-" + p.Version + ".tgz"
				}
			}
			reg := testregistry.Start(t, fixtures...)
			node, err := exec.LookPath("node")
			if err != nil {
				t.Fatal(err)
			}
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			home := filepath.Join(dir, "home")
			write := func(name, body string) {
				t.Helper()
				p := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			write("home/user.npmrc", "")
			write("home/global.npmrc", "")
			var env []string
			for _, s := range os.Environ() {
				key, _, _ := strings.Cut(s, "=")
				key = strings.ToUpper(key)
				if strings.HasPrefix(key, "NPM_CONFIG_") || strings.HasPrefix(key, "NODE_") || strings.HasPrefix(key, "YARN_") || strings.HasPrefix(key, "XDG_") || key == "HOME" || key == "USERPROFILE" || key == "APPDATA" || key == "LOCALAPPDATA" || key == "CI" {
					continue
				}
				env = append(env, s)
			}
			env = append(env, "HOME="+home, "USERPROFILE="+home, "APPDATA="+home, "LOCALAPPDATA="+home, "XDG_CONFIG_HOME="+home, "XDG_CACHE_HOME="+home, "XDG_DATA_HOME="+home, "XDG_STATE_HOME="+home, "CI=1", "npm_config_userconfig="+filepath.Join(home, "user.npmrc"), "npm_config_globalconfig="+filepath.Join(home, "global.npmrc"), "YARN_IGNORE_PATH=1")
			if berry {
				env = append(env, "YARN_ENABLE_GLOBAL_CACHE=false", "YARN_ENABLE_IMMUTABLE_INSTALLS=false", "YARN_ENABLE_TELEMETRY=0", "YARN_ENABLE_HARDENED_MODE=false")
			}
			run := func(args ...string) []byte {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, node, append([]string{cli}, args...)...)
				cmd.Dir, cmd.Env = dir, env
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("Yarn %v: %v\n%s", args, err, out)
				}
				return out
			}
			if actual := strings.TrimSpace(string(run("--version"))); actual != version {
				t.Fatal(actual)
			}
			pkg := `{"name":"fixture","version":"1.0.0","private":true,"dependencies":{"a":"^1","b":"^1"}}`
			write("package.json", pkg)
			cache := filepath.Join(home, "cache")
			quote := func(s string) string { b, _ := json.Marshal(s); return string(b) }
			flags := []string{"install", "--ignore-scripts", "--non-interactive", "--registry", reg.URL, "--cache-folder", cache}
			if berry {
				write(".yarnrc.yml", "nodeLinker: node-modules\nnpmRegistryServer: "+quote(reg.URL)+"\nunsafeHttpWhitelist: [127.0.0.1]\ncacheFolder: "+quote(cache)+"\n")
				flags = []string{"install", "--mode=skip-build"}
			}
			run(flags...)
			path := filepath.Join(dir, "yarn.lock")
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			project, err := manifest.ParsePackage([]byte(pkg))
			if err != nil {
				t.Fatal(err)
			}
			g, warnings, err := Read(path, project, Options{})
			if err != nil || len(warnings) > 0 {
				t.Fatal(err, warnings)
			}
			var written []byte
			if berry {
				written, err = EncodeBerry(g, project)
			} else {
				written, err = EncodeClassic(g, project)
			}
			if err != nil {
				t.Fatal(err)
			}
			if oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE"); oracle != "" {
				compareYarnWriter(t, oracle, path, filepath.Join(dir, "package.json"), "strict", g, project, berry)
			}
			expected := original
			if berry && custom {
				expected = append([]byte(nil), original...)
				for _, p := range fixtures {
					qualifier := "::__archiveUrl=" + url.QueryEscape(reg.URL+"/tarballs/"+p.Name+"-"+p.Version+".tgz")
					if bytes.Count(expected, []byte(qualifier)) != 1 {
						t.Fatalf("missing native archive qualifier %s\n%s", qualifier, original)
					}
					expected = bytes.ReplaceAll(expected, []byte(qualifier), nil)
				}
			}
			if berry && !bytes.Equal(expected, written) {
				t.Fatalf("native Berry/Go bytes differ\nNative:\n%s\nGo:\n%s", original, written)
			}
			if err := os.WriteFile(path, written, 0600); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{filepath.Join(dir, "node_modules"), cache} {
				if err := os.RemoveAll(path); err != nil {
					t.Fatal(err)
				}
			}
			frozen := "--frozen-lockfile"
			if berry {
				frozen = "--immutable"
			}
			if berry && custom {
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, node, append([]string{cli}, append(flags, frozen)...)...)
				cmd.Dir, cmd.Env = dir, env
				out, err := cmd.CombinedOutput()
				if err == nil || !bytes.Contains(out, []byte("404")) {
					t.Fatalf("expected reference archive-URL loss to fail fetching the standard path: %v\n%s", err, out)
				}
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(written, after) {
					t.Fatal("failed install changed lockfile", err)
				}
				t.Log("reference writer loses custom archive qualifiers; native cold install rejects the rewritten source")
				return
			}
			run(append(flags, frozen)...)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			probe := exec.CommandContext(ctx, node, "-e", `if(require("a")!=="one"||require("b")!=="two")process.exit(1)`)
			probe.Dir, probe.Env = dir, env
			if out, err := probe.CombinedOutput(); err != nil {
				t.Fatal(string(out), err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(written, after) {
				t.Fatal("frozen install changed lockfile", err)
			}
		})
	}
}
