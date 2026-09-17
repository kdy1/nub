package installconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/fsutil"
	"github.com/nubjs/nub/pm-go/internal/linker"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/manifest"
	"github.com/nubjs/nub/pm-go/internal/settings"
	"github.com/nubjs/nub/pm-go/internal/store"
)

func configManifest(t *testing.T, text string) *manifest.Package {
	t.Helper()
	p, err := manifest.ParsePackage([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNubDefaultsUseWorkspaceDeclarations(t *testing.T) {
	root := configManifest(t, `{"dependencies":{"expo":"^56.0.0","remix":"^2.0.0"},"devDependencies":{"vite":null}}`)
	for _, local := range []bool{false, true} {
		in := DefaultsInput{TrulyFresh: true, ProjectLocal: local, Root: root}
		c := settings.Context{Defaults: NubDefaults(in)}
		if *c.String("defaultLockfileFormat") != "nub" || c.Resolve("minimumReleaseAge") != uint64(1440) {
			t.Fatal(c.Defaults)
		}
		if !reflect.DeepEqual(c.Strings("disableGlobalVirtualStoreForPackages"), []string{"next", "react-native"}) {
			t.Fatal(c.Defaults)
		}
		if got := c.Strings("diskMaterializePackages"); (len(got) == 0) != local {
			t.Fatal(got)
		}
		in.Members = []*manifest.Package{configManifest(t, `{"dependencies":{"expo":"catalog:","remix":">=2"},"dependenciesMeta":{"not-a-dependency":{"injected":true}}}`)}
		c.Defaults = NubDefaults(in)
		if !reflect.DeepEqual(c.Strings("disableGlobalVirtualStoreForPackages"), []string{"next", "react-native", "expo", "remix"}) || c.Explicit("hoist") != true {
			t.Fatal(c.Defaults)
		}
	}
	for _, disabled := range []string{"true", " ON ", "1", "yes"} {
		c := settings.Context{Defaults: NubDefaults(DefaultsInput{Root: root, Env: map[string]string{"__NUB_VITE_COMPAT_DISABLE": disabled}})}
		if len(c.Strings("diskMaterializePackages")) != 0 {
			t.Fatal(disabled, c.Defaults)
		}
	}
}

func TestNativeSettingsDriveActualLayouts(t *testing.T) {
	for _, strategy := range []string{"global-virtual-store", "isolated", "hoisted"} {
		t.Run(strategy, func(t *testing.T) {
			root := t.TempDir()
			project := filepath.Join(root, "project")
			if err := os.MkdirAll(project, 0755); err != nil {
				t.Fatal(err)
			}
			cfg := settings.Context{Defaults: NubDefaults(DefaultsInput{TrulyFresh: true})}
			native := nativeConfig(t, `{"linker":"`+strategy+`","publicHoist":["child"]}`)
			var err error
			cfg.ProjectConfig, _, err = native.Lower(cfg.Defaults, true, false)
			if err != nil {
				t.Fatal(err)
			}
			layout, err := ResolveLayout(LayoutInput{Settings: cfg, Project: project, GlobalStore: filepath.Join(root, "global")})
			if err != nil {
				t.Fatal(err)
			}
			s := store.New(filepath.Join(root, "cas"), filepath.Join(root, "cache"))
			defer s.Close()
			g := lockfile.NewGraph()
			parent := lockfile.NewPackage("parent", "1.0.0")
			child := lockfile.NewPackage("child", "1.0.0")
			g.Packages[parent.DepPath], g.Packages[child.DepPath] = parent, child
			parent.Dependencies["child"] = "1.0.0"
			g.Importers["."] = []lockfile.DirectDep{{Name: parent.Name, DepPath: parent.DepPath}}
			indices := map[string]store.PackageIndex{}
			for _, p := range []*lockfile.Package{parent, child} {
				file, err := s.ImportBytes(t.Context(), []byte(`{"name":"`+p.Name+`","version":"1.0.0"}`), false)
				if err != nil {
					t.Fatal(err)
				}
				indices[p.DepPath] = store.PackageIndex{"package.json": file}
			}
			plan := layout.Apply(linker.IsolatedPlan{ProjectDir: project, Store: s, Graph: g, Indices: indices, Strategy: linker.Copy, Hashes: g.ComputeHashes(lockfile.HashOptions{})})
			if layout.NodeLinker == Hoisted {
				_, _, err = linker.LinkHoistedProject(t.Context(), linker.HoistedPlan{IsolatedPlan: plan, Limits: layout.HoistingLimits})
			} else {
				_, err = linker.LinkIsolatedProject(t.Context(), plan)
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"parent", "child"} {
				path := filepath.Join(project, "node_modules", name, "package.json")
				data, err := os.ReadFile(path)
				if err != nil || !strings.Contains(string(data), name) {
					t.Fatal(path, err, string(data))
				}
				resolved, err := fsutil.Canonicalize(path)
				if err != nil {
					t.Fatal(err)
				}
				inGlobal := strings.HasPrefix(resolved, layout.GlobalStore+string(filepath.Separator))
				if inGlobal != (strategy == "global-virtual-store") {
					t.Fatal(strategy, resolved)
				}
			}
		})
	}
}

func TestLayoutDefaultsCIAndStrictCLI(t *testing.T) {
	root := t.TempDir()
	c := settings.Context{Defaults: NubDefaults(DefaultsInput{}), ProjectNpmrc: []settings.Entry{{"modules-dir", "custom"}}}
	l, err := ResolveLayout(LayoutInput{Settings: c, Project: root, Env: map[string]string{"CI": "false"}})
	if err != nil || l.Materialization.Mode != DiskWithHiddenTree || l.VirtualStoreDir != filepath.Join(root, "node_modules/.store") {
		t.Fatal(l, err)
	}
	c.ConfigOverrides = []settings.Entry{{"node-linker", "typo"}}
	if _, err := ResolveLayout(LayoutInput{Settings: c, Project: root}); err == nil {
		t.Fatal("generic CLI typo accepted")
	}
	c.ConfigOverrides = nil
	c.ProjectNpmrc = append(c.ProjectNpmrc, settings.Entry{"node-linker", "typo"})
	if l, err := ResolveLayout(LayoutInput{Settings: c, Project: root}); err != nil || l.NodeLinker != Isolated {
		t.Fatal(l, err)
	}
	for method, want := range map[string]linker.Strategy{"auto": linker.Copy, "hardlink": linker.Hardlink, "clone-or-copy": linker.Reflink, "copy": linker.Copy} {
		c.ConfigOverrides = []settings.Entry{{"packageImportMethod", method}}
		got, err := ResolveImportStrategy(c, func() linker.Strategy { return linker.Copy })
		if err != nil || got != want {
			t.Fatal(method, got, err)
		}
	}
	c.ConfigOverrides = []settings.Entry{{"packageImportMethod", "bad"}}
	if _, err := ResolveImportStrategy(c, nil); err == nil {
		t.Fatal("bad import method accepted")
	}
}
