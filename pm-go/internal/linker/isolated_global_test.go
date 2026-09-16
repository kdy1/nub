package linker

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/fsutil"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
)

func globalIsolatedFixture(t *testing.T) IsolatedPlan {
	t.Helper()
	p := isolatedFixture(t)
	p.UseGlobalVirtualStore = true
	p.GlobalVirtualStoreDir = filepath.Join(filepath.Dir(p.ProjectDir), "global")
	p.Hoist = new(bool)
	remote := lockfile.NewPackage("remote", "1.0.0")
	remote.Source = &lockfile.Source{Kind: lockfile.RemoteTarball, URL: "https://example.test/pkg.tgz"}
	remote.DepPath = remote.Source.DepPath(remote.Name)
	p.Graph.Packages[remote.DepPath] = remote
	p.Indices[remote.DepPath] = packageFiles(t, p.Store, map[string]string{"index.js": "module.exports=7\n"})
	p.Graph.Packages["child@1.0.0"].Dependencies[remote.Name] = remote.Source.URL
	p.Graph.Importers["."] = append(p.Graph.RootDeps(), lockfile.DirectDep{Name: remote.Name, DepPath: remote.DepPath})
	p.Hashes = p.Graph.ComputeHashes(lockfile.HashOptions{})
	return p
}

func projectEntry(t *testing.T, p IsolatedPlan, key string) string {
	t.Helper()
	m := Materializer{Root: p.VirtualStoreDir, MaxFilenameLength: p.MaxFilenameLength}
	if m.Root == "" {
		m.Root = filepath.Join(p.ProjectDir, "node_modules/.store")
	}
	name, err := m.EntryName(key)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(m.Root, name)
}

func TestGlobalIsolatedEjectionMaintainsBothCopiesAndPhantomResolution(t *testing.T) {
	p := globalIsolatedFixture(t)
	p.DiskMaterialize = []string{"par*"}
	for pass := range 2 {
		if _, err := LinkIsolatedProject(t.Context(), p); err != nil {
			t.Fatal(pass, err)
		}
		if out, err := nodeRequire(t, p.ProjectDir, "parent"); err != nil || out != "[1,2,42]" {
			t.Fatal(out, err)
		}
		child := filepath.Join(projectEntry(t, p, "child@1.0.0"), "node_modules/child")
		if out, err := nodeRequire(t, child, "remote"); err != nil || out != "7" {
			t.Fatal("shared source dependency", out, err)
		}
		local := projectEntry(t, p, "parent@1.0.0")
		if !realDirectory(local) {
			t.Fatal("ejected entry remains a shared-store link")
		}
		global := Materializer{Root: p.GlobalVirtualStoreDir, Hashes: p.Hashes}
		entry, err := global.EntryName("parent@1.0.0")
		if err != nil || !realDirectory(filepath.Join(global.Root, entry)) {
			t.Fatal("store-resident dependents lost their copy", err)
		}
		if _, err := os.Lstat(filepath.Join(global.Root, "node_modules")); !os.IsNotExist(err) {
			t.Fatal("unversioned aliases leaked into the shared store", err)
		}
	}
	// Leaving the ejection set replaces the real tree with a shared-store link.
	p.DiskMaterialize = nil
	if _, err := LinkIsolatedProject(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Readlink(projectEntry(t, p, "parent@1.0.0")); err != nil {
		t.Fatal(err)
	}
	if _, err := nodeRequire(t, p.ProjectDir, "parent"); err == nil {
		t.Fatal("store-resident parent consumed project hidden aliases")
	}
}

func TestGlobalHashChangesRetargetProjectEntries(t *testing.T) {
	p := globalIsolatedFixture(t)
	if _, err := LinkIsolatedProject(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	link := projectEntry(t, p, "ghost@2.0.0")
	before, err := fsutil.Canonicalize(link)
	if err != nil {
		t.Fatal(err)
	}
	p.Patches = map[string]string{"ghost@2.0.0": "--- a/index.js\n+++ b/index.js\n@@ -1 +1 @@\n-module.exports=2\n+module.exports=9\n"}
	fingerprints := CurrentPatchHashes(p.Patches)
	p.Hashes = p.Graph.ComputeHashes(lockfile.HashOptions{Patch: func(name, version string) *string {
		if hash, ok := fingerprints[name+"@"+version]; ok {
			return &hash
		}
		return nil
	}})
	if _, err := LinkIsolatedProject(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	after, err := fsutil.Canonicalize(link)
	if err != nil || before == after {
		t.Fatal("patch identity did not change the shared target", before, after, err)
	}
	if out, err := nodeRequire(t, p.ProjectDir, "ghost"); err != nil || out != "9" {
		t.Fatal(out, err)
	}
	if data, err := os.ReadFile(filepath.Join(before, "node_modules/ghost/index.js")); err != nil || string(data) != "module.exports=2\n" {
		t.Fatal("previous shared entry was modified", string(data), err)
	}
}

func TestGlobalSourceRequiresIndexAndHoistingFallsBackToProject(t *testing.T) {
	p := globalIsolatedFixture(t)
	for key, pkg := range p.Graph.Packages {
		if pkg.Source != nil && pkg.Source.GloballyShareable() {
			delete(p.Indices, key)
		}
	}
	_, err := LinkIsolatedProject(t.Context(), p)
	var missing *MissingPackageIndex
	if !errors.As(err, &missing) {
		t.Fatal(err)
	}
	p = globalIsolatedFixture(t)
	p.Hoist = nil
	binFixture(t, p.GlobalVirtualStoreDir, "node_modules/stale/index.js", "stale")
	if _, err := LinkIsolatedProject(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if !realDirectory(projectEntry(t, p, "parent@1.0.0")) {
		t.Fatal("hidden hoist did not force a project-local layout")
	}
	if _, err := os.Lstat(filepath.Join(p.GlobalVirtualStoreDir, "node_modules")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
