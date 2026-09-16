package gitcache

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/processenv"
	"github.com/nubjs/nub/pm-go/internal/store"
)

func codeloadFixture(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tarball := tar.NewWriter(gz)
	data := `{"name":"git-fixture","version":"1.2.3"}`
	if err := tarball.WriteHeader(&tar.Header{Name: "repo-sha/package.json", Mode: 0644, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarball.Write([]byte(data)); err != nil {
		t.Fatal(err)
	}
	if err := tarball.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestRustCodeloadTreeAndCacheKeyOracle(t *testing.T) {
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		t.Skip("set PM_RUST_LOCKFILE_ORACLE to compare codeload trees")
	}
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	for _, entry := range []struct {
		name, body, link string
		mode             int64
		kind             byte
	}{
		{"repo/empty", "", "", 0755, tar.TypeDir},
		{"repo/package.json", `{"version":"1.0.0"}`, "", 0644, tar.TypeReg},
		{"repo/bin/run", "#!/bin/sh\necho ok\n", "", 04755, tar.TypeReg},
		{"repo/alias", "", "bin/run", 0777, tar.TypeSymlink},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: entry.name, Mode: entry.mode, Typeflag: entry.kind, Linkname: entry.link, Size: int64(len(entry.body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	archive := filepath.Join(dir, "fixture.tgz")
	if err := os.WriteFile(archive, out.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("A", 40)
	pin := store.Integrity(out.Bytes())
	c := &Cache{Root: filepath.Join(dir, "go-cache")}
	var cases, want []map[string]any
	for _, integrity := range []*string{nil, &pin} {
		url := "ssh://git@github.com/owner/repo.git"
		cases = append(cases, map[string]any{"archive": archive, "url": url, "commit": sha, "integrity": integrity})
		tree, head, err := c.ExtractCodeload(t.Context(), out.Bytes(), url, sha, integrity)
		if err != nil {
			t.Fatal(err)
		}
		files := map[string]any{}
		err = filepath.WalkDir(tree, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if path == tree {
				return nil
			}
			relative, err := filepath.Rel(tree, path)
			if err != nil {
				return err
			}
			key := filepath.ToSlash(relative)
			switch {
			case entry.Type()&os.ModeSymlink != 0:
				target, err := os.Readlink(path)
				if err != nil {
					return err
				}
				files[key] = map[string]any{"kind": "link", "target": target}
			case entry.IsDir():
				files[key] = map[string]any{"kind": "directory"}
			default:
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				info, err := entry.Info()
				if err != nil {
					return err
				}
				files[key] = map[string]any{"kind": "file", "content": string(data), "mode": float64(info.Mode().Perm())}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, map[string]any{"sha": head, "files": files, "key": strings.TrimPrefix(filepath.Base(tree), "nub-pm-go-codeload-"), "integrity": pin})
	}
	input := filepath.Join(dir, "cases.json")
	data, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), oracle, "git-codeload", input)
	env := processenv.Environment{Vars: os.Environ()}
	cmd.Env = env.With("XDG_CACHE_HOME", filepath.Join(dir, "rust-cache")).Vars
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	var got []map[string]any
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("Rust %s\nGo %#v", output, want)
	}
	t.Log("compared codeload files, symlinks, permissions, integrity and cache keys")
}

func TestCodeloadIntegrityCacheAndConcurrentPublication(t *testing.T) {
	c := &Cache{Root: filepath.Join(t.TempDir(), "cache")}
	data := codeloadFixture(t)
	sha, url := strings.Repeat("a", 40), "https://github.com/owner/repo.git"
	pin := store.Integrity(data)
	if _, _, _, ok := c.LookupCodeload(url, sha, &pin); ok {
		t.Fatal("cold cache hit")
	}
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			path, head, err := c.ExtractCodeload(t.Context(), data, url, sha, &pin)
			if err != nil || head != sha || !realDirectory(path) {
				t.Errorf("%s %s %v", path, head, err)
			}
		})
	}
	wg.Wait()
	path, head, actual, ok := c.LookupCodeload(url, strings.ToUpper(sha), &pin)
	if !ok || head != sha || actual == nil || *actual != pin {
		t.Fatal(path, head, actual, ok)
	}
	if _, _, _, ok := c.LookupCodeload(url, sha, nil); ok {
		t.Fatal("integrity is missing from cache key")
	}
	if _, _, err := c.ExtractCodeload(t.Context(), []byte("tampered"), url, sha, &pin); err == nil {
		t.Fatal("warm cache bypassed integrity verification")
	}
	if err := os.Remove(path + ".integrity"); err != nil {
		t.Fatal(err)
	}
	if reused, _, err := c.ExtractCodeload(t.Context(), data, url, sha, &pin); err != nil || reused != path {
		t.Fatal(reused, err)
	}
	if _, _, actual, ok := c.LookupCodeload(url, sha, &pin); !ok || actual == nil || *actual != pin {
		t.Fatal("missing sidecar was not repaired")
	}
	if _, _, err := c.ExtractCodeload(t.Context(), []byte("not gzip"), url, strings.Repeat("b", 40), nil); err == nil {
		t.Fatal("invalid archive accepted")
	}
	if _, _, _, ok := c.LookupCodeload(url, strings.Repeat("b", 40), nil); ok {
		t.Fatal("failed extraction published")
	}
	entries, err := os.ReadDir(c.Root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".codeload-") {
			t.Fatal("scratch directory leaked", entry.Name())
		}
	}
	if _, _, err := c.ExtractCodeload(t.Context(), data, url, sha[:9], &pin); err == nil {
		t.Fatal("abbreviated commit accepted for an archive")
	}
}
