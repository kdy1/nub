package settings

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/npmconfig"
)

func TestSettingsNpmrcVersionPolicyKeepsRegistryTrack(t *testing.T) {
	root := t.TempDir()
	home, project := filepath.Join(root, "home"), filepath.Join(root, "project")
	for path, content := range map[string]string{
		filepath.Join(home, ".npmrc"):           "network-concurrency=3\nhoist=false\nregistry=https://user.test/\n_authToken=user-token\n",
		filepath.Join(project, ".npmrc"):        "network-concurrency=5\nhoist=true\nmodules-dir=custom\nno-proxy=host\nnoproxy=other\n",
		filepath.Join(project, "member/.npmrc"): "hoist=false\nnetwork-concurrency=7\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, pnpm := range []bool{false, true} {
		for _, v11 := range []bool{false, true} {
			files := npmconfig.Files{Dir: project, Home: home, Pnpm: pnpm, Pnpm11: v11}
			sources := LoadNpmrcSources(files)
			ctx := Context{ProjectNpmrc: sources.Project, UserNpmrc: sources.User}
			if ctx.Resolve("hoist") != true || ctx.Resolve("modulesDir") != "custom" {
				t.Fatal(sources)
			}
			if pnpm && v11 {
				if ctx.Explicit("networkConcurrency") != nil || slices.Contains(sources.Project, Entry{"noproxy", "other"}) {
					t.Fatal(sources)
				}
				if !slices.Contains(sources.Project, Entry{"no-proxy", "host"}) {
					t.Fatal(sources)
				}
			} else if ctx.Resolve("networkConcurrency") != uint64(5) {
				t.Fatal(sources)
			}
			registry := npmconfig.Resolve(npmconfig.LoadFiles(files), nil)
			auth := registry.AuthFor("https://user.test/package", "package")
			if auth == nil || !auth.HasCredentials() {
				t.Fatal("settings filter altered registry credentials")
			}
			files.Dir = filepath.Join(project, "member")
			sources.ExtendProject(files)
			ctx.ProjectNpmrc = sources.Project
			if ctx.Resolve("hoist") != false {
				t.Fatal("member did not override root", sources)
			}
			if !pnpm || !v11 {
				if ctx.Resolve("networkConcurrency") != uint64(7) {
					t.Fatal(sources)
				}
			}
		}
	}
}
