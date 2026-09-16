package linker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/store"
)

type patchCase struct {
	name, patch, errorContains string
	original, want             map[string]string
}

func patchCases() []patchCase {
	single := func(name, original, body, want string) patchCase {
		return patchCase{name: name, patch: "--- a/x\n+++ b/x\n" + body, original: map[string]string{"x": original}, want: map[string]string{"x": want}}
	}
	cases := []patchCase{
		single("simple", "old\n", "@@ -1 +1 @@\n-old\n+new\n", "new\n"),
		single("missing-eof-marker", "a\nb\nlast", "@@ -1,3 +1,3 @@\n a\n-b\n+B\n last\n", "a\nB\nlast"),
		single("eof-context", "a\nb\nlast", "@@ -1,3 +1,3 @@\n a\n-b\n+B\n last\n\\ No newline at end of file\n", "a\nB\nlast"),
		single("eof-insertion", "first\nsecond", "@@ -1,2 +1,3 @@\n first\n-second\n\\ No newline at end of file\n+second\n+third\n\\ No newline at end of file\n", "first\nsecond\nthird"),
		single("trailing-context-space", "one  \ntwo\nthree\n", "@@ -1,3 +1,3 @@\n one\n-two\n+TWO\n three\n", "one  \nTWO\nthree\n"),
		single("trailing-deletion-space", "one\ntwo\t\nthree\n", "@@ -1,3 +1,2 @@\n one\n-two\n three\n", "one\nthree\n"),
		single("crlf", "one\r\ntwo\r\nthree\r\n", "@@ -1,3 +1,3 @@\n one\n-two\n+TWO\n three\n", "one\r\nTWO\r\nthree\r\n"),
		single("embedded-cr", "one\r\ntwo\r\n", "@@ -1,2 +1,2 @@\n-one\n+has\rcr\n two\n", "has\rcr\r\ntwo\r\n"),
		single("multiple-hunks", "1\n2\n3\n4\n5\n6\n7\n8\n", "@@ -1,3 +1,3 @@\n 1\n-2\n+TWO\n 3\n@@ -6,3 +6,3 @@\n 6\n-7\n+SEVEN\n 8\n", "1\nTWO\n3\n4\n5\n6\nSEVEN\n8\n"),
		single("blank-context", "one\n\ntwo\n", "@@ -1,3 +1,3 @@\n one\n\n-two\n+TWO\n", "one\n\nTWO\n"),
		{name: "add-delete", original: map[string]string{"old": "old\n"}, want: map[string]string{"new/added": "new\n"}, patch: "--- /dev/null\n+++ b/new/added\n@@ -0,0 +1 @@\n+new\n--- a/old\n+++ /dev/null\n@@ -1 +0,0 @@\n-old\n"},
		{name: "multiple-files", original: map[string]string{"a": "a\n", "b": "b\n"}, want: map[string]string{"a": "A\n", "b": "B\n"}, patch: "--- a/a\n+++ b/a\n@@ -1 +1 @@\n-a\n+A\n--- a/b\n+++ b/b\n@@ -1 +1 @@\n-b\n+B\n"},
		{name: "zero-count-boundary", original: map[string]string{"x": "old\n"}, want: map[string]string{"empty": "", "x": "new\n"}, patch: "--- a/empty\n+++ b/empty\n@@ -0,0 +0,0 @@\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-old\n+new\n"},
		{name: "bare-header-timestamp", original: map[string]string{"x": "old\n"}, want: map[string]string{"x": "new\n"}, patch: "--- x\t2026-09-01\n+++ x\t2026-09-02\n@@ -1 +1 @@\n-old\n+new\n"},
		{name: "space-b-path", original: map[string]string{"a b/c.js": "old\n"}, want: map[string]string{"a b/c.js": "new\n"}, patch: "diff --git a/a b/c.js b/a b/c.js\n--- a/a b/c.js\n+++ b/a b/c.js\n@@ -1 +1 @@\n-old\n+new\n"},
		{name: "quoted-path", original: map[string]string{"with spaces.js": "old\n"}, want: map[string]string{"with spaces.js": "new\n"}, patch: "diff --git \"a/with spaces.js\" \"b/with spaces.js\"\n--- a/with spaces.js\n+++ b/with spaces.js\n@@ -1 +1 @@\n-old\n+new\n"},
		{name: "octal-path", original: map[string]string{"café.js": "old\n"}, want: map[string]string{"café.js": "new\n"}, patch: "diff --git \"a/caf\\303\\251.js\" \"b/caf\\303\\251.js\"\n--- ignored\n+++ ignored\n@@ -1 +1 @@\n-old\n+new\n"},
		{name: "escape", patch: "--- a/x\n+++ b/../../outside\n@@ -0,0 +1 @@\n+bad\n", errorContains: "patch file path escapes package"},
		{name: "empty", patch: "", errorContains: "no parseable file sections"},
		{name: "pragma", patch: "--- a/x\n+++ b/x\n@@ -0,0 +1 @@\n\\ Unknown\n", errorContains: "unrecognized pragma"},
		{name: "pragma-no-part", patch: "--- a/x\n+++ b/x\n@@ -0,0 +1 @@\n\\ No newline at end of file\n", errorContains: "without a preceding line"},
		{name: "bad-header", patch: "--- a/x\n+++ b/x\n@@ -bad +1 @@\n", errorContains: "bad hunk header start"},
		{name: "bad-count", patch: "--- a/x\n+++ b/x\n@@ -1,bad +1 @@\n", errorContains: "bad hunk header length"},
	}
	for _, offset := range []int{0, 1, 20, 21, 30} {
		original := strings.Repeat("filler\n", offset) + "anchor\ntarget\ntail\n"
		c := single(fmt.Sprint("fuzz-", offset), original, "@@ -1,3 +1,3 @@\n anchor\n-target\n+TARGET\n tail\n", strings.Replace(original, "target", "TARGET", 1))
		if offset > 20 {
			c.errorContains = "could not apply hunk 1 at line 1"
		}
		cases = append(cases, c)
	}
	leading := single("leading-space", "  one\ntwo\n", "@@ -1,2 +1,2 @@\n one\n-two\n+TWO\n", "")
	leading.errorContains = "could not apply hunk 1"
	cases = append(cases, leading)
	crlfPatch := cases[0]
	crlfPatch.name, crlfPatch.patch = "crlf-patch", strings.ReplaceAll(crlfPatch.patch, "\n", "\r\n")
	return append(cases, crlfPatch)
}

func TestPatchFilesAndHunkRules(t *testing.T) {
	for _, c := range patchCases() {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			for path, body := range c.original {
				binFixture(t, root, path, body)
			}
			err := ApplyPatch(t.Context(), root, c.patch)
			if c.errorContains != "" {
				if err == nil || !strings.Contains(err.Error(), c.errorContains) {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]string{}
			for name, raw := range materializedTree(t, root) {
				if body, ok := raw.(map[string]any)["body"].(string); ok {
					got[name] = body
				}
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatal(got, c.want)
			}
		})
	}
}

func TestPatchHardlinkIsolationAndDirectoryEscape(t *testing.T) {
	root := t.TempDir()
	s := store.New(filepath.Join(root, "cas", "v1", "files"), filepath.Join(root, "cache"))
	defer s.Close()
	index := packageFiles(t, s, map[string]string{"x": "old\n"})
	pkgDir := filepath.Join(root, "pkg")
	if err := FillFiles(t.Context(), index, pkgDir, Hardlink); err != nil {
		t.Fatal(err)
	}
	if err := ApplyPatch(t.Context(), pkgDir, patchCases()[0].patch); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(index["x"].Path); err != nil || string(data) != "old\n" {
		t.Fatal("patch changed CAS", string(data), err)
	}
	outside := filepath.Join(root, "outside")
	binFixture(t, outside, "x", "outside\n")
	if err := CreateDirLink(t.Context(), outside, filepath.Join(pkgDir, "link")); err != nil {
		t.Fatal(err)
	}
	patch := "--- a/link/x\n+++ b/link/x\n@@ -1 +1 @@\n-outside\n+bad\n"
	if err := ApplyPatch(t.Context(), pkgDir, patch); err == nil || !strings.Contains(err.Error(), "patch target contains symlink") {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(outside, "x")); err != nil || string(data) != "outside\n" {
		t.Fatal(string(data), err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := ApplyPatch(ctx, pkgDir, patchCases()[0].patch); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestMaterializedPatchAliasAndFailurePublication(t *testing.T) {
	root := t.TempDir()
	s := store.New(filepath.Join(root, "cas", "v1", "files"), filepath.Join(root, "cache"))
	defer s.Close()
	index := packageFiles(t, s, map[string]string{"x": "old\n"})
	pkg := lockfile.NewPackage("alias", "1.0.0")
	real := "real"
	pkg.AliasOf = &real
	g := lockfile.NewGraph()
	g.Packages[pkg.DepPath] = pkg
	m := Materializer{Root: filepath.Join(root, "virtual"), Strategy: Hardlink, Patches: map[string]string{"real@1.0.0": patchCases()[0].patch}}
	result, err := m.EnsurePackage(t.Context(), pkg.DepPath, g, pkg, index, nil)
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(result.Directory, "x")); err != nil || string(data) != "new\n" {
		t.Fatal(string(data), err)
	}
	m.Root += "-bad"
	m.Patches = map[string]string{pkg.SpecKey(): "", "real@1.0.0": patchCases()[0].patch}
	_, err = m.EnsurePackage(t.Context(), pkg.DepPath, g, pkg, index, nil)
	var patchError *PatchError
	if !errors.As(err, &patchError) || patchError.Key != pkg.SpecKey() || patchError.Code() != "ERR_AUBE_PATCH_FAILED" {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(m.Root)
	if err != nil || len(entries) != 0 {
		t.Fatal("failed patched tree published", entries, err)
	}
}
