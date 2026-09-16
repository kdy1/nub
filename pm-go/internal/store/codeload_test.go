package store

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCodeloadDirectoriesAndLinks(t *testing.T) {
	data := archive(t,
		tarEntry{name: "repo/empty", kind: tar.TypeDir},
		tarEntry{name: "repo/bin/run", body: "executable", mode: 06755},
		tarEntry{name: "repo/alias", kind: tar.TypeSymlink, link: "bin/run"},
	)
	tree := filepath.Join(t.TempDir(), "tree")
	err := ExtractCodeload(t.Context(), bytes.NewReader(data), tree, DefaultArchiveLimits())
	if runtime.GOOS == "windows" {
		if err == nil {
			t.Fatal("Windows codeload symlink must trigger Git fallback")
		}
		if _, err := os.Stat(tree); !os.IsNotExist(err) {
			t.Fatal("partial tree retained", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(tree, "empty")); err != nil || !info.IsDir() {
		t.Fatal(info, err)
	}
	if info, err := os.Stat(filepath.Join(tree, "alias")); err != nil || info.Mode() != 0755 {
		t.Fatal(info, err)
	}
	if link, err := os.Readlink(filepath.Join(tree, "alias")); err != nil || link != "bin/run" {
		t.Fatal(link, err)
	}
}

func TestCodeloadRejectsEscapesAndSpecialFiles(t *testing.T) {
	for _, entry := range []tarEntry{
		{name: "repo/link", kind: tar.TypeSymlink, link: "../outside"},
		{name: "repo/link", kind: tar.TypeSymlink, link: "/outside"},
		{name: "repo/../../outside", body: "bad"},
		{name: "repo/hard", kind: tar.TypeLink, link: "repo/file"},
		{name: "repo/device", kind: tar.TypeChar},
	} {
		tree := filepath.Join(t.TempDir(), "tree")
		data := archive(t, tarEntry{name: "repo/file", body: "safe"}, entry)
		if err := ExtractCodeload(t.Context(), bytes.NewReader(data), tree, DefaultArchiveLimits()); err == nil {
			t.Fatal("accepted unsafe entry", entry)
		}
		if _, err := os.Stat(tree); !os.IsNotExist(err) {
			t.Fatal("partial tree retained", err)
		}
	}
}
