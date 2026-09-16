package linker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/store"
)

func packageFiles(t *testing.T, s *store.Store, files map[string]string) store.PackageIndex {
	t.Helper()
	index := store.PackageIndex{}
	for name, body := range files {
		file, err := s.ImportBytes(t.Context(), []byte(body), name == "cli")
		if err != nil {
			t.Fatal(err)
		}
		index[name] = file
	}
	return index
}

func materializeFixture(t *testing.T, fullEdges bool) (Materializer, *lockfile.Graph, map[string]store.PackageIndex, map[string]string) {
	t.Helper()
	root := t.TempDir()
	s := store.New(filepath.Join(root, "cas", "v1", "files"), filepath.Join(root, "cache"))
	t.Cleanup(func() { s.Close() })
	g := lockfile.NewGraph()
	parent := lockfile.NewPackage("@scope/parent", "1.0.0")
	parent.Dependencies = map[string]string{"child": "1.0.0", "@s/dep": "2.0.0", "local": "link+local", "@scope/parent": "9.0.0"}
	if fullEdges {
		parent.Dependencies["child"] = "child@1.0.0"
		parent.Dependencies["@s/dep"] = "@s/dep@2.0.0"
	}
	child, scoped := lockfile.NewPackage("child", "1.0.0"), lockfile.NewPackage("@s/dep", "2.0.0")
	for _, pkg := range []*lockfile.Package{parent, child, scoped} {
		g.Packages[pkg.DepPath] = pkg
	}
	indices := map[string]store.PackageIndex{
		parent.DepPath: packageFiles(t, s, map[string]string{"index.js": "module.exports=[require('child'),require('@s/dep'),require('local')]", "cli": "#!/usr/bin/env node\n", "deep/empty": ""}),
		child.DepPath:  packageFiles(t, s, map[string]string{"index.js": "module.exports=41"}),
		scoped.DepPath: packageFiles(t, s, map[string]string{"index.js": "module.exports=42"}),
	}
	local := filepath.Join(root, "local")
	binFixture(t, local, "index.js", "module.exports=43")
	return Materializer{Root: filepath.Join(root, "virtual"), Strategy: Copy}, g, indices, map[string]string{"local@link+local": local}
}

func TestMaterializationPublishesUsableGraphAndRepairsWarmLinks(t *testing.T) {
	for _, hashed := range []bool{false, true} {
		m, graph, indices, nested := materializeFixture(t, false)
		if hashed {
			m.Hashes = graph.ComputeHashes(lockfile.HashOptions{})
		}
		parent := graph.Packages["@scope/parent@1.0.0"]
		var parentDir string
		for _, key := range []string{parent.DepPath, "child@1.0.0", "@s/dep@2.0.0"} {
			result, err := m.EnsurePackage(t.Context(), key, graph, graph.Packages[key], indices[key], nested)
			if err != nil || result.Cached || result.FilesLinked != len(indices[key]) {
				t.Fatal(result, err)
			}
			if key == parent.DepPath {
				parentDir = result.Directory
			}
		}
		encoded, _ := json.Marshal(parentDir)
		command := exec.CommandContext(t.Context(), "node", "-e", "console.log(JSON.stringify(require("+string(encoded)+")))")
		out, err := command.CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != "[41,42,43]" {
			t.Fatal(string(out), err)
		}
		childLink := filepath.Join(DepModulesDir(parentDir, parent.Name), "child")
		if err := os.Remove(childLink); err != nil {
			t.Fatal(err)
		}
		if err := CreateDirLink(t.Context(), nested["local@link+local"], childLink); err != nil {
			t.Fatal(err)
		}
		before, err := os.Stat(filepath.Join(parentDir, "index.js"))
		if err != nil {
			t.Fatal(err)
		}
		// A warm entry repairs its dependency targets without needing CAS files.
		result, err := m.EnsurePackage(t.Context(), parent.DepPath, graph, parent, nil, nested)
		if err != nil || !result.Cached || result.FilesLinked != 0 {
			t.Fatal(result, err)
		}
		after, err := os.Stat(filepath.Join(parentDir, "index.js"))
		if err != nil || !os.SameFile(before, after) {
			t.Fatal("warm files rematerialized", err)
		}
		if data, err := os.ReadFile(filepath.Join(childLink, "index.js")); err != nil || string(data) != "module.exports=41" {
			t.Fatal(string(data), err)
		}
		assertNoStaging(t, m.Root)
	}
}

func assertNoStaging(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".tmp-") {
			t.Fatal("staging tree retained", entry.Name())
		}
	}
}

func TestMaterializationConcurrentPublishAndFailureCleanup(t *testing.T) {
	m, graph, indices, nested := materializeFixture(t, false)
	pkg := graph.Packages["@scope/parent@1.0.0"]
	type outcome struct {
		result Materialized
		err    error
	}
	results := make(chan outcome, 12)
	var workers sync.WaitGroup
	for range 12 {
		workers.Go(func() {
			result, err := m.EnsurePackage(t.Context(), pkg.DepPath, graph, pkg, indices[pkg.DepPath], nested)
			results <- outcome{result, err}
		})
	}
	workers.Wait()
	close(results)
	fresh := 0
	for out := range results {
		if out.err != nil {
			t.Fatal(out.err)
		}
		if !out.result.Cached {
			fresh++
		}
		if data, err := os.ReadFile(filepath.Join(out.result.Directory, "index.js")); err != nil || !strings.Contains(string(data), "module.exports") {
			t.Fatal(string(data), err)
		}
	}
	if fresh != 1 {
		t.Fatal("published winners", fresh)
	}
	assertNoStaging(t, m.Root)
	bad := lockfile.NewPackage("bad", "1.0.0")
	_, err := m.EnsurePackage(t.Context(), bad.DepPath, graph, bad, store.PackageIndex{"file": {Path: filepath.Join(m.Root, "absent")}}, nil)
	var missing *MissingStoreFile
	if !errors.As(err, &missing) {
		t.Fatal(err)
	}
	entry, _ := m.EntryName(bad.DepPath)
	if _, err := os.Stat(filepath.Join(m.Root, entry)); !os.IsNotExist(err) {
		t.Fatal("failed entry published", err)
	}
	assertNoStaging(t, m.Root)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := m.EnsurePackage(ctx, bad.DepPath, graph, bad, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, name := range []string{"", ".", "..", "C:drive", "@/pkg", "a/b/c", "a/b", "a\\b", "nul\x00", "/abs"} {
		bad.Name = name
		_, err := m.EnsurePackage(t.Context(), bad.DepPath, graph, bad, nil, nil)
		var unsafe *UnsafePackageName
		if !errors.As(err, &unsafe) {
			t.Fatal(name, err)
		}
	}
}

func TestMaterializationColdFullEdgeAndScopedLinks(t *testing.T) {
	m, graph, indices, nested := materializeFixture(t, true)
	pkg := graph.Packages["@scope/parent@1.0.0"]
	result, err := m.EnsurePackage(t.Context(), pkg.DepPath, graph, pkg, indices[pkg.DepPath], nested)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(DepModulesDir(result.Directory, pkg.Name), "@s", "dep")
	stored, err := os.Readlink(link)
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := m.EntryName("@s/dep@2.0.0")
	if runtime.GOOS == "windows" {
		if !strings.Contains(stored, entry) || strings.Contains(stored, ".tmp-") {
			t.Fatal("junction retained staging root", stored)
		}
	} else if stored != filepath.FromSlash("../../../"+entry+"/node_modules/@s/dep") {
		t.Fatal(stored)
	}
}
