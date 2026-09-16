package store

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type tarEntry struct {
	name, body string
	kind       byte
	mode       int64
	link       string
}

func archive(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		kind := e.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		mode := e.mode
		if mode == 0 {
			mode = 0644
		}
		h := &tar.Header{Name: e.name, Typeflag: kind, Mode: mode, Linkname: e.link, Size: int64(len(e.body))}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestExtractPackageFilesAndModes(t *testing.T) {
	data := archive(t, tarEntry{name: "package/", kind: tar.TypeDir}, tarEntry{name: "./package/package.json", body: `{"name":"pkg","version":"1.0.0"}`}, tarEntry{name: "package/bin/cli", body: "#!/bin/sh\necho ok\n", mode: 04755}, tarEntry{name: "package/a//./b", body: "first"}, tarEntry{name: "package/a/b", body: "last"}, tarEntry{name: "package/" + strings.Repeat("long/", 30) + "entry.js", body: "long"})
	dest := filepath.Join(t.TempDir(), "package")
	index, err := ExtractTarball(context.Background(), bytes.NewReader(data), dest, DefaultArchiveLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(index) != 4 || !index["bin/cli"].Executable || index["a/b"].Size != 4 {
		t.Fatal(index)
	}
	actual, err := os.ReadFile(filepath.Join(dest, "a", "b"))
	if err != nil || string(actual) != "last" {
		t.Fatal(string(actual), err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dest, "bin", "cli"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode() != 0755 {
			t.Fatal("unexpected executable permissions", info.Mode())
		}
	}
}

func TestExtractRejectsUnsafeEntriesAndCleansPartialTree(t *testing.T) {
	for _, bad := range []tarEntry{
		{name: "package/../../escape", body: "bad"}, {name: "/absolute/file", body: "bad"},
		{name: "package/link", kind: tar.TypeSymlink, link: "../../escape"}, {name: "package/hard", kind: tar.TypeLink, link: "package/file"},
		{name: "package/fifo", kind: tar.TypeFifo}, {name: "package/device", kind: tar.TypeChar},
	} {
		t.Run(bad.name+string(bad.kind), func(t *testing.T) {
			root := t.TempDir()
			dest := filepath.Join(root, "package")
			data := archive(t, tarEntry{name: "package/valid", body: "before-error"}, bad)
			if _, err := ExtractTarball(context.Background(), bytes.NewReader(data), dest, DefaultArchiveLimits()); err == nil {
				t.Fatal("unsafe entry accepted", bad)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatal("partial extraction retained", entries, err)
			}
		})
	}
}

func TestExtractSizeCountAndChecksumLimits(t *testing.T) {
	data := archive(t, tarEntry{name: "package/first", body: strings.Repeat("a", 2000)}, tarEntry{name: "package/second", body: "b"})
	for _, limits := range []ArchiveLimits{{1 << 20, 1000, 10}, {1024, 3000, 10}, {1 << 20, 3000, 1}} {
		dest := filepath.Join(t.TempDir(), "package")
		if _, err := ExtractTarball(context.Background(), bytes.NewReader(data), dest, limits); err == nil {
			t.Fatal("archive limit ignored", limits)
		}
		if _, err := os.Stat(dest); !os.IsNotExist(err) {
			t.Fatal("partial tree retained", err)
		}
	}
	corrupt := bytes.Clone(data)
	corrupt[len(corrupt)-8] ^= 1
	if _, err := ExtractTarball(context.Background(), bytes.NewReader(corrupt), filepath.Join(t.TempDir(), "package"), DefaultArchiveLimits()); err == nil {
		t.Fatal("gzip checksum ignored")
	}
	if _, err := ExtractTarball(context.Background(), bytes.NewReader(data[:len(data)/2]), filepath.Join(t.TempDir(), "package"), DefaultArchiveLimits()); err == nil {
		t.Fatal("truncated gzip accepted")
	}
}

func TestExtractDoesNotOverwriteExistingTree(t *testing.T) {
	dest := t.TempDir()
	existing := filepath.Join(dest, "keep")
	if err := os.WriteFile(existing, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	data := archive(t, tarEntry{name: "package/keep", body: "changed"})
	if _, err := ExtractTarball(context.Background(), bytes.NewReader(data), dest, DefaultArchiveLimits()); err == nil {
		t.Fatal("existing tree accepted")
	}
	got, err := os.ReadFile(existing)
	if err != nil || string(got) != "original" {
		t.Fatal(string(got), err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ExtractTarball(ctx, bytes.NewReader(data), filepath.Join(t.TempDir(), "package"), DefaultArchiveLimits()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestNormalizeTarPaths(t *testing.T) {
	for _, platform := range []string{"linux", "darwin", "windows"} {
		for _, tc := range []struct{ raw, want string }{{"./package/a//./b", "a/b"}, {"wrapper/file", "file"}, {"././package/", ""}, {"package", ""}} {
			got, err := NormalizeTarPath(tc.raw, platform)
			if err != nil || got != tc.want {
				t.Fatal(platform, tc, got, err)
			}
		}
		for _, bad := range []string{"../x", "./../x", "/package/x", "package/../x", "package/a/../../x", "package/a\x00b", "package/\xff"} {
			if _, err := NormalizeTarPath(bad, platform); err == nil {
				t.Fatal(platform, bad)
			}
		}
	}
	for _, bad := range []string{"C:/package/x", "package/CON.js", "package/Lpt1", "package/name.", "package/name ", "package/file:stream", "package/a\x01b"} {
		if _, err := NormalizeTarPath(bad, "windows"); err == nil {
			t.Fatal(bad)
		}
	}
	for _, valid := range []string{"package/CON.js", "package/name.", "package/file:stream"} {
		if _, err := NormalizeTarPath(valid, "linux"); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := NormalizeTarPath(`package\folder\file`, "windows"); err != nil || got != "folder/file" {
		t.Fatal(got, err)
	}
	if _, err := NormalizeTarPath(`package/folder\file`, "linux"); err == nil {
		t.Fatal("embedded backslash accepted")
	}
}
