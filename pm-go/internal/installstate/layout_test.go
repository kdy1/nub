package installstate

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/linker"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/store"
)

func layoutFixture(t *testing.T) (Paths, linker.IsolatedPlan) {
	t.Helper()
	p := paths(t)
	s := store.New(filepath.Join(t.TempDir(), "v1", "files"), t.TempDir())
	t.Cleanup(func() { s.Close() })
	g := lockfile.NewGraph()
	indices := map[string]store.PackageIndex{}
	for _, name := range []string{"parent", "child"} {
		pkg := lockfile.NewPackage(name, "1.0.0")
		g.Packages[pkg.DepPath] = pkg
		data := `{"name":"` + name + `","version":"1.0.0"}`
		file, err := s.ImportBytes(t.Context(), []byte(data), false)
		if err != nil {
			t.Fatal(err)
		}
		indices[pkg.DepPath] = store.PackageIndex{"package.json": file}
	}
	g.Packages["parent@1.0.0"].Dependencies["child"] = "1.0.0"
	g.Importers["."] = []lockfile.DirectDep{{Name: "parent", DepPath: "parent@1.0.0"}}
	g.Importers["packages/member"] = []lockfile.DirectDep{{Name: "parent", DepPath: "parent@1.0.0"}}
	put(t, filepath.Join(p.Project, "package.json"), `{"name":"root"}`)
	put(t, filepath.Join(p.Project, "packages/member/package.json"), `{"name":"member"}`)
	return p, linker.IsolatedPlan{ProjectDir: p.Project, Graph: g, Store: s, Indices: indices, Strategy: linker.Copy, HasWorkspace: true}
}

func TestCaptureAndVerifyMaterializedLayouts(t *testing.T) {
	for _, mode := range []string{"isolated", "hoisted"} {
		t.Run(mode, func(t *testing.T) {
			p, plan := layoutFixture(t)
			var placements linker.HoistedPlacements
			var err error
			if mode == "hoisted" {
				_, placements, err = linker.LinkHoistedProject(t.Context(), linker.HoistedPlan{IsolatedPlan: plan})
			} else {
				_, err = linker.LinkIsolatedProject(t.Context(), plan)
			}
			if err != nil {
				t.Fatal(err)
			}
			input := LayoutInput{Graph: plan.Graph, Linker: mode, VirtualStore: filepath.Join(p.Project, "node_modules", ".store"), Placements: placements}
			l, err := CaptureLayout(t.Context(), p.Project, input)
			if err != nil || VerifyLayout(p.Project, l) != "" {
				t.Fatal(l, err, VerifyLayout(p.Project, l))
			}
			if len(l.Packages) != 1 || l.Packages["parent@1.0.0"].Name != "parent" {
				t.Fatal(l.Packages)
			}
			expected := "packages/member/node_modules/parent"
			if mode == "hoisted" {
				expected = "node_modules/parent"
			}
			if !reflect.DeepEqual(l.DirectEntries["packages/member"], []string{expected}) {
				t.Fatal(l.DirectEntries)
			}
			meta := l.Packages["parent@1.0.0"].PackageJSONPath
			put(t, join(p.Project, meta), `{"name":"parent","version":"1.0.0","description":"changed by lifecycle"}`)
			if reason := VerifyLayout(p.Project, l); reason != "" {
				t.Fatal("identity-preserving change rejected", reason)
			}
			put(t, join(p.Project, meta), `{"name":"parent","version":"2.0.0"}`)
			if reason := VerifyLayout(p.Project, l); reason != "installed package metadata changed: "+meta {
				t.Fatal(reason)
			}
			put(t, join(p.Project, meta), `{"name":null}`)
			if reason := VerifyLayout(p.Project, l); reason != "installed package metadata unreadable: "+meta {
				t.Fatal(reason)
			}
			if err := os.Remove(join(p.Project, meta)); err != nil {
				t.Fatal(err)
			}
			if reason := VerifyLayout(p.Project, l); reason != "installed package metadata missing: "+meta {
				t.Fatal(reason)
			}
			if err := os.RemoveAll(filepath.Join(p.Project, "packages/member/node_modules")); err != nil {
				t.Fatal(err)
			}
			if mode == "isolated" && VerifyLayout(p.Project, l) != "installed entry missing: "+expected {
				t.Fatal(VerifyLayout(p.Project, l))
			}
		})
	}
}

func TestLayoutDanglingLinksAndSharedTopology(t *testing.T) {
	p, plan := layoutFixture(t)
	plan.Hoist = new(bool)
	plan.UseGlobalVirtualStore = true
	plan.GlobalVirtualStoreDir = filepath.Join(t.TempDir(), "virtual")
	plan.Hashes = plan.Graph.ComputeHashes(lockfile.HashOptions{})
	if _, err := linker.LinkIsolatedProject(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	input := LayoutInput{Graph: plan.Graph, Linker: "isolated", VirtualStore: filepath.Join(p.Project, "node_modules", ".store"), UseGlobalVirtualStore: true}
	l, err := CaptureLayout(t.Context(), p.Project, input)
	if err != nil || !GVSNestedLinksCurrent(p.Project, l) || VerifyLayout(p.Project, l) != "" {
		t.Fatal(l, err, VerifyLayout(p.Project, l))
	}
	if len(*l.GVSNestedLinks) != 1 {
		t.Fatal(l.GVSNestedLinks)
	}
	var rel string
	for path := range *l.GVSNestedLinks {
		rel = path
	}
	if err := os.Remove(join(p.Project, rel)); err != nil {
		t.Fatal(err)
	}
	if GVSNestedLinksCurrent(p.Project, l) || VerifyLayout(p.Project, l) != "global virtual store link missing: "+rel {
		t.Fatal(VerifyLayout(p.Project, l))
	}
	broken, err := CaptureLayout(t.Context(), p.Project, input)
	if err != nil || broken.GVSNestedLinks != nil {
		t.Fatal("incomplete topology claimed recordable", broken, err)
	}
	if err := linker.CreateDirLink(t.Context(), p.Project, join(p.Project, rel)); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(VerifyLayout(p.Project, l), "global virtual store link changed:") {
		t.Fatal(VerifyLayout(p.Project, l))
	}
	// A dangling link is a valid direct entry. Its arbitrary target does not
	// participate in the installed-manifest identity check.
	dangling := filepath.Join(p.Modules, "dangling")
	if err := linker.CreateDirLink(t.Context(), filepath.Join(p.Project, "not-built"), dangling); err != nil {
		t.Fatal(err)
	}
	ll := &Layout{DirectEntries: map[string][]string{".": {"node_modules/dangling"}}, Packages: map[string]InstalledPackage{"linked": {Link: true, PackageJSONPath: "not-built/package.json"}}}
	if reason := VerifyLayout(p.Project, ll); reason != "" {
		t.Fatal(reason)
	}
}

func TestReferencePathEqualityRetainsParentComponents(t *testing.T) {
	for _, pair := range [][2]string{{"a//b/./c/", "a/b/c"}, {"/a/./b", "/a/b"}, {"./a", "./a"}} {
		if pathComponents(pair[0]) != pathComponents(pair[1]) {
			t.Fatal(pair)
		}
	}
	for _, pair := range [][2]string{{"a/../b", "b"}, {"./a", "a"}, {"/a", "a"}} {
		if pathComponents(pair[0]) == pathComponents(pair[1]) {
			t.Fatal(pair)
		}
	}
}
