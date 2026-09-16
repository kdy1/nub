package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func fingerprintFile(t *testing.T, root, name, body string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDirectoryFingerprintsTrackImportedContent(t *testing.T) {
	root := t.TempDir()
	file := fingerprintFile(t, root, "lib/index.js", "module.exports=1")
	fingerprintFile(t, root, "package.json", `{"name":"fixture"}`)
	content, meta, err := DirectoryFingerprints(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	s := New(filepath.Join(t.TempDir(), "v1", "files"), t.TempDir())
	defer s.Close()
	index, err := s.ImportDirectory(t.Context(), root)
	if err != nil || IndexFingerprint(index) != content {
		t.Fatalf("index fingerprint %s != %s: %v", IndexFingerprint(index), content, err)
	}
	for _, name := range []string{"node_modules/ignored/x", ".git/index", "lib/node_modules/x", "lib/.git/config"} {
		fingerprintFile(t, root, name, "ignored")
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(t.TempDir(), filepath.Join(root, "link")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(file, filepath.Join(root, "file-link")); err != nil {
			t.Fatal(err)
		}
	}
	gotContent, gotMeta, err := DirectoryFingerprints(t.Context(), root)
	if err != nil || gotContent != content || gotMeta != meta {
		t.Fatalf("excluded files affected fingerprints: %s %s %v", gotContent, gotMeta, err)
	}
	stamp := time.Unix(1600000000, 123456789)
	if err := os.Chtimes(file, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	gotContent, gotMeta, err = DirectoryFingerprints(t.Context(), root)
	if err != nil || gotContent != content || gotMeta == meta {
		t.Fatal("touch must change only metadata", err)
	}
	if only, err := DirectoryMetadataFingerprint(t.Context(), root); err != nil || only != gotMeta {
		t.Fatal(only, gotMeta, err)
	}
	if err := os.WriteFile(file, []byte("module.exports=2"), 0644); err != nil {
		t.Fatal(err)
	}
	if only, err := DirectoryContentFingerprint(t.Context(), root); err != nil || only == content {
		t.Fatal("same-size content edit", only, err)
	}
	content, err = DirectoryContentFingerprint(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(file, 0755); err != nil {
			t.Fatal(err)
		}
		if only, err := DirectoryContentFingerprint(t.Context(), root); err != nil || only == content {
			t.Fatal("mode edit", only, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := DirectoryFingerprints(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, _, err := DirectoryFingerprints(t.Context(), filepath.Join(root, "missing")); err == nil {
		t.Fatal("missing source accepted")
	}
}

func TestRustDirectoryFingerprintOracle(t *testing.T) {
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		t.Skip("set PM_RUST_LOCKFILE_ORACLE for directory fingerprint parity")
	}
	var dirs []string
	var want [][2]string
	for i := range 10 {
		root := t.TempDir()
		if i != 0 {
			file := fingerprintFile(t, root, "lib/index.js", "module.exports=1")
			fingerprintFile(t, root, "package.json", `{"name":"fixture"}`)
			switch i {
			case 2:
				fingerprintFile(t, root, "node_modules/x/index.js", "excluded")
				fingerprintFile(t, root, "lib/.git/x", "excluded")
			case 3:
				if err := os.Chmod(file, 0755); err != nil {
					t.Fatal(err)
				}
			case 4:
				stamp := time.Unix(1600000000, 123456789)
				if err := os.Chtimes(file, stamp, stamp); err != nil {
					t.Fatal(err)
				}
			case 5:
				fingerprintFile(t, root, "nested/é/한글.js", "unicode")
			case 6:
				if err := os.Symlink(file, filepath.Join(root, "link")); err != nil {
					t.Fatal(err)
				}
			case 7:
				fingerprintFile(t, root, "node_modules", "excluded file")
				fingerprintFile(t, root, ".git", "excluded file")
			case 8:
				fingerprintFile(t, root, "a\\b", "backslash")
			case 9:
				fingerprintFile(t, root, "bad\xff.js", "non-utf8")
			}
		}
		c, m, err := DirectoryFingerprints(t.Context(), root)
		if err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, root)
		want = append(want, [2]string{c, m})
	}
	input, _ := json.Marshal(dirs)
	path := fingerprintFile(t, t.TempDir(), "cases.json", string(input))
	output, err := exec.CommandContext(t.Context(), oracle, "directory-fingerprints", path).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	var got [][2]string
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Rust %v; Go %v", got, want)
	}
	t.Logf("compared %d directory content/metadata fingerprints", len(want))
}
