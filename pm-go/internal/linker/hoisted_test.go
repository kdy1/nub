package linker

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/store"
)

func TestHoistedLayoutResolvesConflictsAndReplacesBundledFiles(t *testing.T) {
	p := isolatedFixture(t)
	// child needs ghost@1, which conflicts with root ghost@2. A bundled stale
	// copy at the nested placement must be replaced after child is filled.
	extra := packageFiles(t, p.Store, map[string]string{"node_modules/ghost/old.js": "stale", "node_modules/ghost/index.js": "module.exports=999"})
	for name, file := range extra {
		p.Indices["child@1.0.0"][name] = file
	}
	binFixture(t, p.ProjectDir, "node_modules/.store/node_modules/stale/old.js", "stale")
	stats, placements, err := LinkHoistedProject(t.Context(), HoistedPlan{IsolatedPlan: p})
	if err != nil || stats.PackagesLinked != 6 || stats.PackagesCached != 0 || stats.TopLevelLinked != 6 {
		t.Fatal(stats, err)
	}
	if out, err := nodeRequire(t, p.ProjectDir, "parent"); err != nil || out != "[1,2,42]" {
		t.Fatal(out, err)
	}
	child := filepath.Join(p.ProjectDir, "node_modules/child")
	if got := placements["ghost@1.0.0"]; !reflect.DeepEqual(got, []string{filepath.Join(child, "node_modules/ghost")}) {
		t.Fatal(got)
	}
	for _, path := range []string{"node_modules/child/node_modules/ghost/old.js", "node_modules/.store/node_modules"} {
		if _, err := os.Stat(filepath.Join(p.ProjectDir, path)); !os.IsNotExist(err) {
			t.Fatal(path, err)
		}
	}
}

func TestHoistedReuseKeepsBuildOutputAndRefillsIncompletePlacements(t *testing.T) {
	p := isolatedFixture(t)
	for key, pkg := range p.Graph.Packages {
		if pkg.Source != nil && pkg.Source.Kind == lockfile.Link {
			continue
		}
		file, err := p.Store.ImportBytes(t.Context(), []byte(`{"name":"`+pkg.Name+`","version":"`+pkg.Version+`"}`), false)
		if err != nil {
			t.Fatal(err)
		}
		p.Indices[key]["package.json"] = file
	}
	hp := HoistedPlan{IsolatedPlan: p, Reusable: lockfile.Set{}}
	_, places, err := LinkHoistedProject(t.Context(), hp)
	if err != nil {
		t.Fatal(err)
	}
	for key := range places {
		hp.Reusable.Add(key)
	}
	built := binFixture(t, places["parent@1.0.0"][0], "build/Release/native.node", "built")
	oldIndices := hp.Indices
	hp.Indices, hp.Store = nil, nil
	stats, _, err := LinkHoistedProject(t.Context(), hp)
	if err != nil || stats.PackagesCached != 6 || stats.PackagesLinked != 0 {
		t.Fatal(stats, err)
	}
	if body, err := os.ReadFile(built); err != nil || string(body) != "built" {
		t.Fatal(string(body), err)
	}
	// Missing package.json prevents a stale state vouch from preserving a
	// partial entry. The supplied index refills it and drops its stale files.
	child := places["child@1.0.0"][0]
	if err := os.Remove(filepath.Join(child, "package.json")); err != nil {
		t.Fatal(err)
	}
	stale := binFixture(t, child, "stale", "old")
	hp.Indices = map[string]store.PackageIndex{"child@1.0.0": oldIndices["child@1.0.0"], "ghost@1.0.0": oldIndices["ghost@1.0.0"]}
	stats, _, err = LinkHoistedProject(t.Context(), hp)
	if err != nil || stats.PackagesLinked != 2 {
		t.Fatal(stats, err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestHoistedWorkspaceRootPriorityAndOutsideMembers(t *testing.T) {
	p := isolatedFixture(t)
	p.HasWorkspace = true
	p.Graph.Importers["../outside"] = []lockfile.DirectDep{{Name: "ghost", DepPath: "ghost@1.0.0"}}
	p.Graph.Importers["packages/c"] = []lockfile.DirectDep{{Name: "ghost", DepPath: "ghost@1.0.0"}}
	for _, limits := range []HoistingLimits{HoistNone, HoistWorkspaces, HoistDependencies} {
		stats, places, err := LinkHoistedProject(t.Context(), HoistedPlan{IsolatedPlan: p, Limits: limits})
		if err != nil || stats.PackagesLinked == 0 {
			t.Fatal(stats, err)
		}
		for _, c := range []struct{ dir, version string }{
			{p.ProjectDir, "2"}, {p.WorkspaceDirs["member-a"], "2"},
			{p.WorkspaceDirs["member-b"], "1"}, {filepath.Join(p.ProjectDir, "../outside"), "1"},
		} {
			if out, err := nodeRequire(t, c.dir, "ghost"); err != nil || out != c.version {
				t.Fatal(limits, c.dir, out, err)
			}
		}
		if len(places["ghost@1.0.0"]) < 2 {
			t.Fatal("outside importer incorrectly shared the root tree", places)
		}
		recovered, err := HoistedPlacementsFromGraph(p.ProjectDir, p.Graph, "node_modules", limits)
		if err != nil {
			t.Fatal(err)
		}
		for key, paths := range places {
			if len(recovered[key]) != len(paths) {
				t.Fatal(key, paths, recovered[key])
			}
		}
	}
}

func TestHoistedPlannerPrefersMostReferencedVersion(t *testing.T) {
	g := lockfile.NewGraph()
	for _, name := range []string{"a", "b", "c"} {
		pkg := lockfile.NewPackage(name, "1.0.0")
		version := "2.0.0"
		if name == "a" {
			version = "1.0.0"
		}
		pkg.Dependencies["dep"] = version
		g.Packages[pkg.DepPath] = pkg
		g.Importers["."] = append(g.RootDeps(), lockfile.DirectDep{Name: name, DepPath: pkg.DepPath})
	}
	for _, version := range []string{"1.0.0", "2.0.0"} {
		pkg := lockfile.NewPackage("dep", version)
		g.Packages[pkg.DepPath] = pkg
	}
	plans, err := hoistedPlans(t.TempDir(), "node_modules", g, false, HoistNone)
	if err != nil {
		t.Fatal(err)
	}
	tree := plans[0]
	if got := tree.nodes[tree.nodes[0].children["dep"]].key; got != "dep@2.0.0" {
		t.Fatal(got)
	}
	// Root direct versions outrank an arbitrarily more popular transitive.
	g.Importers["."] = append(g.RootDeps(), lockfile.DirectDep{Name: "dep", DepPath: "dep@1.0.0"})
	plans, err = hoistedPlans(t.TempDir(), "node_modules", g, false, HoistNone)
	if err != nil || plans[0].nodes[plans[0].nodes[0].children["dep"]].key != "dep@1.0.0" {
		t.Fatal(err)
	}
}
