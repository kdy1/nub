package linker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/fsutil"
)

func TestDirectoryLinksRelocateAndReplaceOnlyUnpopulatedSlots(t *testing.T) {
	root := t.TempDir()
	target, parent := filepath.Join(root, "target"), filepath.Join(root, "links")
	for _, path := range []string{target, parent} {
		if err := os.Mkdir(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(target, "sentinel"), []byte("preserved"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "package")
	for _, kind := range []string{"missing", "existing-link", "dangling-link", "empty-directory", "plain-file"} {
		_ = os.Remove(link)
		switch kind {
		case "existing-link", "dangling-link":
			path := target
			if kind == "dangling-link" {
				path += "-missing"
			}
			if err := CreateDirLink(t.Context(), path, link); err != nil {
				t.Fatal(err)
			}
		case "empty-directory":
			if err := os.Mkdir(link, 0755); err != nil {
				t.Fatal(err)
			}
		case "plain-file":
			if err := os.WriteFile(link, []byte("old"), 0644); err != nil {
				t.Fatal(err)
			}
		}
		if err := CreateDirLink(t.Context(), "../target", link); err != nil {
			t.Fatal(kind, err)
		}
		data, err := os.ReadFile(filepath.Join(link, "sentinel"))
		if err != nil || string(data) != "preserved" {
			t.Fatal(kind, string(data), err)
		}
		stored, err := os.Readlink(link)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && stored != "../target" {
			t.Fatal(stored)
		}
		if runtime.GOOS == "windows" && !filepath.IsAbs(stored) {
			t.Fatal("junction is not absolute", stored)
		}
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(link, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(link, "owned"), []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := CreateDirLink(t.Context(), target, link); err == nil {
		t.Fatal("populated directory overwritten")
	}
	if data, err := os.ReadFile(filepath.Join(link, "owned")); err != nil || string(data) != "keep" {
		t.Fatal(string(data), err)
	}
	if _, err := os.Stat(filepath.Join(target, "sentinel")); err != nil {
		t.Fatal("link replacement affected target", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := CreateDirLink(ctx, target, filepath.Join(parent, "cancelled")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestCanonicalizeDirectoryLinksAndNestedPaths(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "physical", strings.Repeat("deep", 40), strings.Repeat("path", 40))
	file := binFixture(t, target, "nested/file", "data")
	link := filepath.Join(root, "link")
	if err := CreateDirLink(t.Context(), target, link); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"", "nested", "nested/file"} {
		got, err := fsutil.Canonicalize(filepath.Join(link, relative))
		if err != nil {
			t.Fatal(relative, err)
		}
		want, err := fsutil.Canonicalize(filepath.Join(target, relative))
		if err != nil || got != want {
			t.Fatal(relative, got, want, err)
		}
	}
	if _, err := fsutil.Canonicalize(filepath.Join(link, "absent")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := fsutil.Canonicalize("relative"); err == nil {
		t.Fatal("relative path used process cwd")
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("removing a link removed its target", err)
	}
}
