package resolver

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
)

func TestLocalRebasingAndOverrideAnchors(t *testing.T) {
	root := t.TempDir()
	for _, kind := range []lockfile.SourceKind{lockfile.Directory, lockfile.Tarball, lockfile.Link, lockfile.Portal, lockfile.Exec} {
		local := lockfile.Source{Kind: kind, Path: "../../vendor-dir"}
		got := RebaseLocal(local, filepath.Join(root, "packages", "app"), root)
		if got.Path != "vendor-dir" {
			t.Fatal(kind, got)
		}
		other := RebaseLocal(lockfile.Source{Kind: kind, Path: "../vendor-dir"}, filepath.Join(root, "packages"), root)
		if got.DepPath("vendor-dir") != other.DepPath("vendor-dir") {
			t.Fatal(got, other)
		}
		raw := lockfile.Source{Kind: kind, Path: "./scripts/../generator.js"}
		got = RebaseLocal(raw, root, root)
		if kind == lockfile.Exec {
			if got.Path != "generator.js" {
				t.Fatal(got)
			}
		} else if got.Path != raw.Path {
			t.Fatal("root bytes changed", got)
		}
	}
	if got := normalizeLexical("/../../a/../b"); filepath.ToSlash(got) != "/../../b" {
		t.Fatal(got)
	}
	if got := normalizeLexical("."); got != "" {
		t.Fatal(got)
	}
	if got := sourceJoin(root, filepath.Join(root, "absolute")); got != filepath.Join(root, "absolute") {
		t.Fatal(got)
	}
	if runtime.GOOS == "windows" {
		for _, local := range []string{`\package`, `/package`} {
			if got := sourceJoin(`C:\project`, local); got != "C:"+local {
				t.Fatal(local, got)
			}
		}
	}
	parent := lockfile.NewPackage("parent", "1.0.0")
	parent.Source = &lockfile.Source{Kind: lockfile.Directory, Path: "vendor/parent"}
	resolved := map[string]*lockfile.Package{parent.DepPath: parent}
	task := resolveTask{Name: "child", Range: "file:../child", Importer: "packages/app", Parent: &parent.DepPath}
	local, anchor, err := prepareLocalSource(task, resolved, root, true)
	if err != nil || filepath.Clean(anchor) != filepath.Join(root, "vendor", "parent") || local.Path != "../child" {
		t.Fatal(local, anchor, err)
	}
	task.RangeFromOverride = true
	_, anchor, err = prepareLocalSource(task, resolved, root, true)
	if err != nil || anchor != root {
		t.Fatal(anchor, err)
	}
}
func TestExoticTransitivesAndMissingParentRoots(t *testing.T) {
	root := t.TempDir()
	for _, kind := range []lockfile.SourceKind{lockfile.Directory, lockfile.Link, lockfile.Portal, lockfile.Exec, lockfile.Tarball, lockfile.Git, lockfile.RemoteTarball} {
		parent := lockfile.NewPackage("parent", "1.0.0")
		parent.Source = &lockfile.Source{Kind: kind, Path: "parent"}
		resolved := map[string]*lockfile.Package{parent.DepPath: parent}
		for _, request := range []string{"file:../child", "https://example.invalid/pkg.tgz"} {
			task := resolveTask{Name: "child", Range: request, Importer: ".", Parent: &parent.DepPath}
			_, _, err := prepareLocalSource(task, resolved, root, true)
			allow := kind == lockfile.Directory || kind == lockfile.Link || kind == lockfile.Portal || kind == lockfile.Exec && strings.HasPrefix(request, "https:")
			if (err == nil) != allow {
				t.Fatal(kind, request, err)
			}
			task.RangeFromOverride = true
			if _, _, err = prepareLocalSource(task, resolved, root, true); err != nil {
				t.Fatal(kind, request, err)
			}
		}
	}
	task := resolveTask{Name: "child", Range: "link:../child", Importer: "."}
	if _, _, err := prepareLocalSource(task, nil, root, false); err == nil || !strings.Contains(err.Error(), "without the parent package source root") {
		t.Fatal(err)
	}
	task.Root = true
	if _, _, err := prepareLocalSource(task, nil, root, true); err != nil {
		t.Fatal(err)
	}
}
func TestResolveExecBoundary(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "generate.js")
	if err := os.WriteFile(inside, []byte("module.exports = 1"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveExecScriptPath(lockfile.Source{Kind: lockfile.Exec, Path: "./generate.js"}, root)
	want, e := filepath.EvalSymlinks(inside)
	if e != nil {
		t.Fatal(e)
	}
	if err != nil || got != want {
		t.Fatal(got, want, err)
	}
	for _, source := range []lockfile.Source{{Kind: lockfile.Directory, Path: "generate.js"}, {Kind: lockfile.Exec, Path: "missing"}, {Kind: lockfile.Exec, Path: "."}} {
		if _, err := ResolveExecScriptPath(source, root); err == nil {
			t.Fatal(source)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.js")
	if err := os.WriteFile(outside, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveExecScriptPath(lockfile.Source{Kind: lockfile.Exec, Path: outside}, root); err == nil || !strings.Contains(err.Error(), "outside project root") {
		t.Fatal(err)
	}
	link := filepath.Join(root, "outside-link.js")
	if err := os.Symlink(outside, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("symlink privilege unavailable")
		}
		t.Fatal(err)
	}
	if _, err := ResolveExecScriptPath(lockfile.Source{Kind: lockfile.Exec, Path: "outside-link.js"}, root); err == nil || !strings.Contains(err.Error(), "outside project root") {
		t.Fatal(err)
	}
}

type resolverTarEntry struct{ name, body string }

func resolverTarball(t *testing.T, entries ...resolverTarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: entry.name, Mode: 0644, Size: int64(len(entry.body)), Typeflag: tar.TypeReg}); err != nil {
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
	return buf.Bytes()
}
func TestLocalManifestAndTarballLimits(t *testing.T) {
	body := `{"name":"real-name","version":"2.1.0","dependencies":{"prod":"^1"},"devDependencies":{"dev":"*"},"optionalDependencies":{"opt":"*"},"peerDependencies":{"peer":"*"}}`
	root := t.TempDir()
	dir := filepath.Join(root, "pkg")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	want := LocalManifest{"real-name", "2.1.0", map[string]string{"prod": "^1"}}
	for _, kind := range []lockfile.SourceKind{lockfile.Directory, lockfile.Link, lockfile.Portal} {
		got, err := ReadLocalManifest(lockfile.Source{Kind: kind, Path: "pkg"}, root)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal(kind, got, err)
		}
	}
	data := resolverTarball(t, resolverTarEntry{"wrapper/nested/package.json", `{"name":"wrong"}`}, resolverTarEntry{"github-repo-sha/package.json", body}, resolverTarEntry{"package/package.json", `{"name":"later"}`})
	if err := os.WriteFile(filepath.Join(root, "pkg.tgz"), data, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLocalManifest(lockfile.Source{Kind: lockfile.Tarball, Path: "pkg.tgz"}, root)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, err)
	}
	got, err = parseLocalManifest([]byte(`{}`))
	if err != nil || got.Name != "" || got.Version != "0.0.0" || len(got.Dependencies) != 0 {
		t.Fatal(got, err)
	}
	for _, entries := range [][]resolverTarEntry{
		{{"package.json", body}},
		{{"pkg/package.json", strings.Repeat(" ", maxResolveManifestBytes+1)}},
		{{"pkg/dummy", strings.Repeat(" ", maxResolveTarballBytes)}, {"pkg/package.json", body}},
	} {
		if _, err := ReadTarballManifest(resolverTarball(t, entries...)); err == nil {
			t.Fatal("manifest limits not enforced", entries[0].name)
		}
	}
	if _, err := ReadTarballManifest([]byte("not gzip")); err == nil {
		t.Fatal("accepted corrupt gzip")
	}
}

func TestRustExecPathOracle(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "generator.js")
	outside := filepath.Join(t.TempDir(), "outside.js")
	for _, path := range []string{inside, outside} {
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	type input struct {
		Root, Path string
		Kind       uint8
	}
	inputs := []input{{root, "generator.js", 4}, {root, "./generator.js", 4}, {root, inside, 4}, {root, outside, 4}, {root, "missing.js", 4}, {root, ".", 4}, {root, "generator.js", 0}}
	var refs []struct{ Path, Error *string }
	runPolicyOracle(t, "exec-path", inputs, &refs)
	if len(refs) != len(inputs) {
		t.Fatal("result count")
	}
	for i, in := range inputs {
		path, err := ResolveExecScriptPath(lockfile.Source{Kind: lockfile.SourceKind(in.Kind), Path: in.Path}, in.Root)
		if refs[i].Error != nil {
			if err == nil || err.Error() != *refs[i].Error {
				t.Fatal(i, err, fmt.Sprint(refs[i].Error))
			}
		} else if err != nil || refs[i].Path == nil || path != *refs[i].Path {
			t.Fatal(i, path, err)
		}
	}
}
