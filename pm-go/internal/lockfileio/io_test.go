package lockfileio

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/nubjs/nub/pm-go/internal/identity"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/manifest"
)

func TestUnchangedLockfilePreservesBytesAndMtime(t *testing.T) {
	for _, tc := range []struct {
		kind identity.Kind
		body string
	}{
		{identity.Npm, "{\r\n  \"lockfileVersion\": 3, \"packages\": {\"\": {\"name\": \"fixture\"}}, \"unknown\": true\r\n}\r\n"},
		{identity.Shrinkwrap, `{"lockfileVersion":3,"packages":{}}`},
		{identity.Pnpm, "# authored comment\nlockfileVersion: '9.0'\nimporters:\n  .: {}\n"},
		{identity.Nub, "# authored comment\nlockfileVersion: '9.0'\nimporters:\n  .: {}\n"},
		{identity.Bun, "{ // authored comment\n\"lockfileVersion\":1,\"workspaces\":{\"\":{\"name\":\"fixture\"}},\"packages\":{},\n}\n"},
		{identity.Yarn, "# yarn lockfile v1\n# authored comment\n"},
		{identity.YarnBerry, "# authored comment\r\n__metadata:\r\n  version: 8\r\n  cacheKey: preserved\r\n"},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, tc.kind.Filename())
			pj := `{"name":"fixture"}`
			project, _ := manifest.ParsePackage([]byte(pj))
			writeFile(t, filepath.Join(dir, "package.json"), []byte(pj))
			writeFile(t, path, []byte(tc.body))
			fixed := time.Unix(1600000000, 0)
			if err := os.Chtimes(path, fixed, fixed); err != nil {
				t.Fatal(err)
			}
			g, _, err := Read(path, tc.kind, project, ReadOptions{})
			if err != nil {
				t.Fatal(err)
			}
			result, err := Write(path, tc.kind, g, project, WriteOptions{})
			if err != nil || result.Written {
				t.Fatal(result, err)
			}
			assertFile(t, path, []byte(tc.body))
			stat, err := os.Stat(path)
			if err != nil || !stat.ModTime().Equal(fixed) {
				t.Fatal("mtime changed", stat, err)
			}
			if oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE"); oracle != "" {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				out, err := exec.CommandContext(ctx, oracle, "noop-write", dir).CombinedOutput()
				if err != nil {
					t.Fatal(string(out), err)
				}
				var ref struct {
					OK    bool
					Error string
				}
				if err := json.Unmarshal(out, &ref); err != nil || !ref.OK {
					t.Fatal(string(out), err)
				}
				assertFile(t, path, []byte(tc.body))
				stat, err = os.Stat(path)
				if err != nil || !stat.ModTime().Equal(fixed) {
					t.Fatal("Rust reference rewrote unchanged fixture", err)
				}
			}
		})
	}
}
func TestLegacyMigrationWaitsForRealChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nub.lock")
	old := filepath.Join(dir, "lock.yaml")
	body := []byte("# legacy\nlockfileVersion: '9.0'\nimporters:\n  .: {}\n")
	project, _ := manifest.ParsePackage([]byte(`{}`))
	writeFile(t, old, body)
	g, _, err := Read(old, identity.Nub, project, ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r, err := Write(path, identity.Nub, g, project, WriteOptions{})
	if err != nil || r.Written || r.Path != path {
		t.Fatal(r, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("created canonical file during no-op", err)
	}
	assertFile(t, old, body)
	g.Importers["packages/new"] = nil
	r, err = Write(path, identity.Nub, g, project, WriteOptions{})
	if err != nil || !r.Written {
		t.Fatal(r, err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("legacy file retained after real write", err)
	}
	writeFile(t, old, body)
	g, _, err = Read(path, identity.Nub, project, ReadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r, err = Write(path, identity.Nub, g, project, WriteOptions{})
	if err != nil || r.Written {
		t.Fatal(r, err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("duplicate legacy file retained", err)
	}
}
func TestCorruptDestinationFallsThroughToWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package-lock.json")
	writeFile(t, path, []byte("{broken"))
	project, _ := manifest.ParsePackage([]byte(`{"name":"fixture"}`))
	g := lockfile.NewGraph()
	g.Importers["."] = nil
	r, err := Write(path, identity.Npm, g, project, WriteOptions{})
	if err != nil || !r.Written {
		t.Fatal(r, err)
	}
	if _, _, err := Read(path, identity.Npm, project, ReadOptions{}); err != nil {
		t.Fatal(err)
	}
}
func TestNoopIdentityIncludesResolvedPatchAndEnforcedChecksum(t *testing.T) {
	a := lockfile.NewGraph()
	p := lockfile.NewPackage("a", "1.0.0")
	a.Packages[p.DepPath] = p
	b := a.Clone()
	b.PackageExtensionsChecksum = new("extensions")
	if !Equivalent(a, b, false) || Equivalent(a, b, true) {
		t.Fatal("extension policy")
	}
	b.PatchedDependencyHashes = map[string]string{"a@^1": "patch"}
	if Equivalent(a, b, false) {
		t.Fatal("patch omitted from identity")
	}
	b.PatchedDependencyHashes = map[string]string{"a@not-semver": "patch"}
	if Equivalent(a, b, false) {
		t.Fatal("invalid patch suppressed writer")
	}
}
func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
func assertFile(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("file changed: %v\n%s", err, got)
	}
}
