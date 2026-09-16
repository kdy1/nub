package store

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestTarballToCAS(t *testing.T) {
	s := testStore(t)
	data := archive(t, tarEntry{name: "package/package.json", body: `{"name":"@org/pkg","version":"v1.0.0+build"}`}, tarEntry{name: "package/bin/tool.js", body: "#!/usr/bin/env node\n", mode: 0755})
	index, err := s.ImportTarball(t.Context(), bytes.NewReader(data), DefaultArchiveLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(index) != 2 || !index["bin/tool.js"].Executable {
		t.Fatal(index)
	}
	if err := ValidateIndexContent(index, "@org/pkg", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateIndexContent(index, "alias", "1.0.0"); err == nil {
		t.Fatal("alias accepted as registry name")
	}
	integrity := Integrity(data)
	if err := s.SaveIndex(t.Context(), "@org/pkg", "1.0.0", &integrity, index); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.LoadIndex("@org/pkg", "1.0.0", &integrity, true); !ok {
		t.Fatal("warm package miss")
	}
	paths, _ := filepath.Glob(filepath.Join(s.VersionDir(), ".extract-*"))
	if len(paths) != 0 {
		t.Fatal(paths)
	}
}

func TestMalformedArchiveDoesNotImportPrefix(t *testing.T) {
	s := testStore(t)
	data := archive(t, tarEntry{name: "package/good", body: "a good prefix"}, tarEntry{name: "package/escape", kind: tar.TypeSymlink, link: "../../outside"})
	if _, err := s.ImportTarball(t.Context(), bytes.NewReader(data), DefaultArchiveLimits()); err == nil {
		t.Fatal("bad archive imported")
	}
	if _, err := os.Stat(s.Root); !os.IsNotExist(err) {
		t.Fatal("prefix CAS published", err)
	}
	paths, _ := filepath.Glob(filepath.Join(s.VersionDir(), ".extract-*"))
	if len(paths) != 0 {
		t.Fatal(paths)
	}
	if err := ValidateIndexContent(PackageIndex{}, "x", "1"); err == nil {
		t.Fatal("missing manifest accepted")
	}
}

func TestLocalDirectoryToCAS(t *testing.T) {
	s := testStore(t)
	dir := t.TempDir()
	for name, data := range map[string]string{"package.json": `{"name":"local","version":"2.0.0"}`, "bin/cli": "#!/bin/sh\n", ".git/config": "private", "node_modules/dep/index.js": "nested", "sub/node_modules/dep/index.js": "nested"} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Join(dir, "package.json"), filepath.Join(dir, "linked")); err != nil {
			t.Fatal(err)
		}
	}
	index, err := s.ImportDirectory(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(index) != 2 {
		t.Fatal(index)
	}
	if runtime.GOOS != "windows" && !index["bin/cli"].Executable {
		t.Fatal("mode lost")
	}
	if err := ValidateIndexContent(index, "local", "file:../local"); err != nil {
		t.Fatal(err)
	}
}
