package linker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/store"
)

func isolatedFixture(t *testing.T) IsolatedPlan {
	t.Helper()
	root := t.TempDir()
	s := store.New(filepath.Join(root, "cas"), filepath.Join(root, "cache"))
	t.Cleanup(func() { s.Close() })
	p := IsolatedPlan{ProjectDir: filepath.Join(root, "project"), Strategy: Copy, Store: s, Graph: lockfile.NewGraph(), Indices: map[string]store.PackageIndex{}, WorkspaceDirs: map[string]string{}}
	add := func(name, version, script string) *lockfile.Package {
		pkg := lockfile.NewPackage(name, version)
		p.Graph.Packages[pkg.DepPath] = pkg
		p.Indices[pkg.DepPath] = packageFiles(t, s, map[string]string{"index.js": script + "\n"})
		return pkg
	}
	parent := add("parent", "1.0.0", "module.exports=[require('child'),require('ghost'),require('local')]")
	child := add("child", "1.0.0", "module.exports=require('ghost')")
	add("ghost", "1.0.0", "module.exports=1")
	add("ghost", "2.0.0", "module.exports=2")
	add("@scope/alias", "1.0.0", "module.exports=5")
	local := lockfile.NewPackage("local", "1.0.0")
	local.DepPath = "local@link+local"
	local.Source = &lockfile.Source{Kind: lockfile.Link, Path: "local"}
	p.Graph.Packages[local.DepPath] = local
	dir := add("mutable", "1.0.0", "module.exports=3")
	dir.Source = &lockfile.Source{Kind: lockfile.Directory, Path: "mutable"}
	parent.Dependencies = map[string]string{"child": "1.0.0", "local": "link+local"}
	child.Dependencies["ghost"] = "1.0.0"
	dep := func(name, key string) lockfile.DirectDep { return lockfile.DirectDep{Name: name, DepPath: key} }
	p.Graph.Importers["."] = []lockfile.DirectDep{dep("parent", parent.DepPath), dep("ghost", "ghost@2.0.0"), dep("mutable", dir.DepPath), dep("@scope/alias", "@scope/alias@1.0.0")}
	p.Graph.Importers["packages/a"] = []lockfile.DirectDep{dep("parent", parent.DepPath), dep("ghost", "ghost@2.0.0"), dep("member-b", "member-b@1.0.0")}
	p.Graph.Importers["packages/b"] = []lockfile.DirectDep{dep("ghost", "ghost@1.0.0")}
	p.Graph.Importers["packages/a/node_modules/member-b"] = []lockfile.DirectDep{dep("invalid-virtual-only", "absent@1")}
	for name, relative := range map[string]string{"member-a": "packages/a", "member-b": "packages/b", "unreferenced": "packages/c"} {
		p.WorkspaceDirs[name] = filepath.Join(p.ProjectDir, filepath.FromSlash(relative))
	}
	seedIsolatedFixture(t, p)
	return p
}

func seedIsolatedFixture(t *testing.T, p IsolatedPlan) {
	t.Helper()
	nm := p.ModulesDirName
	if nm == "" {
		nm = "node_modules"
	}
	for path, body := range map[string]string{
		"local/index.js": "module.exports=42", "mutable/index.js": "module.exports=3",
		nm + "/old/stale": "old", nm + "/@scope/old/stale": "old", nm + "/.keep": "keep",
		nm + "/parent/old.js": "old", "package.json": `{"name":"root","version":"1.0.0"}`,
	} {
		binFixture(t, p.ProjectDir, path, body)
	}
	for name, dir := range p.WorkspaceDirs {
		binFixture(t, dir, "package.json", `{"name":"`+name+`","version":"1.0.0"}`)
	}
}

func nodeRequire(t *testing.T, project, name string) (string, error) {
	t.Helper()
	value, _ := json.Marshal(name)
	cmd := exec.CommandContext(t.Context(), "node", "-e", "console.log(JSON.stringify(require("+string(value)+")))")
	cmd.Dir = project
	data, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(data)), err
}

func TestIsolatedProjectLayoutResolvesDeclaredAndHiddenDependencies(t *testing.T) {
	p := isolatedFixture(t)
	stats, err := LinkIsolatedProject(t.Context(), p)
	if err != nil || stats.PackagesLinked != 6 || stats.PackagesCached != 0 || stats.TopLevelLinked != 4 {
		t.Fatal(stats, err)
	}
	if out, err := nodeRequire(t, p.ProjectDir, "parent"); err != nil || out != "[1,2,42]" {
		t.Fatal(out, err)
	}
	for _, path := range []string{"old", "@scope/old", "parent/old.js"} {
		if _, err := os.Stat(filepath.Join(p.ProjectDir, "node_modules", path)); !os.IsNotExist(err) {
			t.Fatal(path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(p.ProjectDir, "node_modules/.keep")); err != nil {
		t.Fatal(err)
	}
	// Cached registry entries avoid all CAS reads; mutable sources are refreshed.
	p.Indices = map[string]store.PackageIndex{"mutable@1.0.0": p.Indices["mutable@1.0.0"]}
	stats, err = LinkIsolatedProject(t.Context(), p)
	if err != nil || stats.PackagesLinked != 1 || stats.PackagesCached != 5 || stats.TopLevelLinked != 0 {
		t.Fatal(stats, err)
	}
	p.Hoist = new(bool)
	var remaining []lockfile.DirectDep
	for _, dep := range p.Graph.RootDeps() {
		if dep.Name != "ghost" {
			remaining = append(remaining, dep)
		}
	}
	p.Graph.Importers["."] = remaining
	if _, err := LinkIsolatedProject(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if _, err := nodeRequire(t, p.ProjectDir, "parent"); err == nil {
		t.Fatal("disabled hidden hoist still supplied the phantom")
	}
}

func TestIsolatedWorkspaceResolutionAndDedupe(t *testing.T) {
	p := isolatedFixture(t)
	p.HasWorkspace, p.DedupeDirectDeps = true, true
	p.PublicHoistPatterns = []string{"MEMBER-*", "unreferenced", "!member-a"}
	if _, err := LinkIsolatedProject(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	a := p.WorkspaceDirs["member-a"]
	if _, err := os.Lstat(filepath.Join(a, "node_modules/parent")); !os.IsNotExist(err) {
		t.Fatal("direct dependency was not deduped", err)
	}
	if out, err := nodeRequire(t, a, "parent"); err != nil || out != "[1,2,42]" {
		t.Fatal(out, err)
	}
	for _, name := range []string{"member-b", "unreferenced"} {
		if _, err := os.Readlink(filepath.Join(p.ProjectDir, "node_modules", name)); err != nil {
			t.Fatal(name, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(p.ProjectDir, "node_modules/member-a")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(p.WorkspaceDirs["member-b"], "node_modules/invalid-virtual-only")); !os.IsNotExist(err) {
		t.Fatal("virtual importer was linked", err)
	}
}

func TestIsolatedFailureDoesNotPublishTrackingState(t *testing.T) {
	p := isolatedFixture(t)
	p.Store = nil
	delete(p.Indices, "child@1.0.0")
	_, err := LinkIsolatedProject(t.Context(), p)
	var missing *MissingPackageIndex
	if !errors.As(err, &missing) || missing.DepPath != "child@1.0.0" {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.ProjectDir, "node_modules", AppliedPatchesFilename)); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := LinkIsolatedProject(ctx, p); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestModulesContainmentAndSweepDoNotFollowOutsideLinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	sentinel := binFixture(t, outside, "keep", "outside")
	if err := CreateDirLink(t.Context(), outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".", "..", "../neighbor", "escape", "escape/nested", outside} {
		_, err := CheckedModulesDir(root, name)
		var unsafe *UnsafeModulesDir
		if !errors.As(err, &unsafe) {
			t.Fatal(name, err)
		}
	}
	if _, err := CheckedModulesDir(root, "deep/modules"); err != nil {
		t.Fatal(err)
	}
	if err := CreateDirLink(t.Context(), outside, filepath.Join(root, "@scope")); err != nil {
		t.Fatal(err)
	}
	sweepTopLevel(t.Context(), root, map[string]bool{"@scope/current": true}, "")
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatal("sweep followed an outside scope link", err)
	}
}

func TestStagingSweepRetainsCurrentProcessAndUnrelatedFiles(t *testing.T) {
	root := t.TempDir()
	current := ".tmp-" + strconv.Itoa(os.Getpid()) + "-current"
	for _, name := range []string{current, ".tmp-0-aborted", ".tmp-user-data", ".tmp-123", "pkg@1"} {
		binFixture(t, root, name+"/file", "content")
	}
	SweepStaleTemps(t.Context(), root)
	for _, name := range []string{current, ".tmp-user-data", ".tmp-123", "pkg@1"} {
		if _, err := os.Stat(filepath.Join(root, name, "file")); err != nil {
			t.Fatal(name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".tmp-0-aborted")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
