package linker

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/store"
	"golang.org/x/sys/unix"
)

func quarantineFile(t *testing.T, path string) {
	t.Helper()
	if err := unix.Lsetxattr(path, "com.apple.quarantine", []byte("0083;68000000;TestApp;"), 0); err != nil {
		t.Fatal(err)
	}
}

func assertQuarantine(t *testing.T, path string, want bool) {
	t.Helper()
	_, err := unix.Lgetxattr(path, "com.apple.quarantine", nil)
	if want && err != nil || !want && !errors.Is(err, unix.ENOATTR) {
		t.Fatalf("%s quarantine=%v: %v", path, want, err)
	}
}

func TestQuarantineIndexedRemovalPreservesOtherAttributes(t *testing.T) {
	dir := t.TempDir()
	index := store.PackageIndex{}
	for _, name := range []string{"lib/binding.node", "lib/native.so", "lib/native.dylib", "bin/tool", "index.js"} {
		path := binFixture(t, dir, name, "content")
		quarantineFile(t, path)
		if err := unix.Lsetxattr(path, "user.keepme", []byte("retained"), 0); err != nil {
			t.Fatal(err)
		}
		index[name] = store.StoredFile{Executable: name == "bin/tool"}
	}
	index["absent.node"] = store.StoredFile{}
	q := &Quarantine{Warn: func(code, message string) { t.Error(code, message) }}
	q.StripIndexed(dir, index)
	q.StripIndexed(dir, index)
	for name := range index {
		if name == "absent.node" {
			continue
		}
		path := filepath.Join(dir, name)
		assertQuarantine(t, path, name == "index.js")
		buf := make([]byte, 20)
		n, err := unix.Lgetxattr(path, "user.keepme", buf)
		if err != nil || string(buf[:n]) != "retained" {
			t.Fatal(n, err)
		}
	}
}

func TestQuarantineTreeWalkAndIndexedLinksStayInsideTree(t *testing.T) {
	dir := t.TempDir()
	outside := binFixture(t, dir, "outside/native.node", "outside")
	quarantineFile(t, outside)
	tree := filepath.Join(dir, "tree")
	for _, name := range []string{"build/Release/binding.node", "build/Release/helper", "README.md"} {
		path := binFixture(t, tree, name, "content")
		quarantineFile(t, path)
		if name == "build/Release/helper" {
			if err := os.Chmod(path, 0755); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.Symlink(outside, filepath.Join(tree, "link.node")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(tree, "outside")); err != nil {
		t.Fatal(err)
	}
	q := &Quarantine{}
	q.StripTree(tree)
	assertQuarantine(t, filepath.Join(tree, "build/Release/binding.node"), false)
	assertQuarantine(t, filepath.Join(tree, "build/Release/helper"), false)
	assertQuarantine(t, filepath.Join(tree, "README.md"), true)
	assertQuarantine(t, outside, true)
	q.StripIndexed(tree, store.PackageIndex{"link.node": {}})
	assertQuarantine(t, outside, true)
	q.StripTree(filepath.Join(dir, "missing"))
}

func TestQuarantineMaterializerColdAndWarmSeams(t *testing.T) {
	for _, name := range []string{"pkg", "@scope/pkg"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			s := store.New(filepath.Join(root, "cas"), filepath.Join(root, "cache"))
			t.Cleanup(func() { s.Close() })
			index := packageFiles(t, s, map[string]string{"build/Release/native.node": "native", "index.js": "source"})
			for _, file := range index {
				quarantineFile(t, file.Path)
			}
			pkg := lockfile.NewPackage(name, "1.2.3")
			graph := lockfile.NewGraph()
			graph.Packages[pkg.DepPath] = pkg
			q := &Quarantine{Warn: func(code, message string) { t.Error(code, message) }}
			m := Materializer{Root: filepath.Join(root, "virtual"), Strategy: Hardlink, Quarantine: q}
			cold, err := m.EnsurePackage(t.Context(), pkg.DepPath, graph, pkg, index, nil)
			if err != nil || cold.Cached {
				t.Fatal(cold, err)
			}
			addon := filepath.Join(cold.Directory, "build/Release/native.node")
			assertQuarantine(t, addon, false)
			assertQuarantine(t, filepath.Join(cold.Directory, "index.js"), true)
			before, err := os.Stat(addon)
			if err != nil {
				t.Fatal(err)
			}
			quarantineFile(t, addon)
			warm, err := m.EnsurePackage(t.Context(), pkg.DepPath, graph, pkg, index, nil)
			if err != nil || !warm.Cached || warm.FilesLinked != 0 {
				t.Fatal(warm, err)
			}
			assertQuarantine(t, addon, false)
			after, err := os.Stat(addon)
			if err != nil || !os.SameFile(before, after) {
				t.Fatal("warm entry was rebuilt", err)
			}
		})
	}
}
