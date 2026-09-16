package gitcache

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/processenv"
)

func gitFixture(t *testing.T) (*Cache, string, string, string) {
	t.Helper()
	dir := t.TempDir()
	env := processenv.Environment{Dir: dir, Vars: os.Environ()}
	for key, value := range map[string]string{
		"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": filepath.Join(dir, "no-config"),
		"GIT_AUTHOR_NAME": "fixture", "GIT_AUTHOR_EMAIL": "fixture@example.invalid",
		"GIT_COMMITTER_NAME": "fixture", "GIT_COMMITTER_EMAIL": "fixture@example.invalid",
	} {
		env = env.With(key, value)
	}
	c := &Cache{Root: filepath.Join(dir, "cache"), Env: env}
	run := func(args ...string) string {
		t.Helper()
		out, err := c.output(t.Context(), dir, args...)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out)
	}
	run("init", "-q", "-b", "main")
	file := filepath.Join(dir, "package.json")
	if err := os.WriteFile(file, []byte(`{"name":"fixture","version":"1.0.0"}`), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "package.json")
	run("commit", "-q", "-m", "initial")
	first := run("rev-parse", "HEAD")
	run("tag", "-a", "v1", "-m", "annotated")
	if err := os.WriteFile(file, []byte(`{"name":"fixture","version":"2.0.0"}`), 0644); err != nil {
		t.Fatal(err)
	}
	run("commit", "-q", "-a", "-m", "second")
	second := run("rev-parse", "HEAD")
	run("branch", "v1")
	return c, dir, first, second
}

func TestRefsAndCloneReuse(t *testing.T) {
	c, repo, first, second := gitFixture(t)
	for _, test := range []struct{ ref, want string }{{"v1", first}, {"main", second}, {first[:9], first[:9]}, {strings.ToUpper(first), first}} {
		got, err := c.ResolveRef(t.Context(), repo, &test.ref)
		if err != nil || got != test.want {
			t.Fatal(test, got, err)
		}
	}
	if got, err := c.ResolveRef(t.Context(), repo, nil); err != nil || got != second {
		t.Fatal(got, err)
	}
	// The abbreviated fetch falls back to a full fetch, then canonicalizes HEAD.
	path, head, err := c.Clone(t.Context(), repo, first[:9], true, false)
	if err != nil || head != first || path != c.clonePath(repo, first) {
		t.Fatal(path, head, err)
	}
	data, err := os.ReadFile(filepath.Join(path, "package.json"))
	if err != nil || !strings.Contains(string(data), "1.0.0") {
		t.Fatal(string(data), err)
	}
	// Remove the source repository to prove this reuses the checkout offline.
	if err := os.RemoveAll(filepath.Join(repo, ".git")); err != nil {
		t.Fatal(err)
	}
	reused, gotHead, err := c.Clone(t.Context(), repo, first, false, true)
	if err != nil || reused != path || gotHead != first {
		t.Fatal(reused, gotHead, err)
	}
	if _, _, err := c.Clone(t.Context(), repo, second, false, true); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatal(err)
	}
}

func TestCloneConcurrentRepairAndFailureCleanup(t *testing.T) {
	c, repo, _, head := gitFixture(t)
	target := c.clonePath(repo, head)
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "partial"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			path, sha, err := c.Clone(t.Context(), repo, head, true, false)
			if err != nil || sha != head || path != target {
				t.Errorf("%s %s %v", path, sha, err)
			}
		})
	}
	wg.Wait()
	if _, err := os.Stat(filepath.Join(target, "partial")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, _, err := c.Clone(t.Context(), filepath.Join(repo, "absent"), head, true, false); err == nil {
		t.Fatal("missing remote accepted")
	}
	entries, err := os.ReadDir(c.Root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".git-clone-") {
			t.Fatal("failed clone leaked scratch directory", entry.Name())
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := c.Clone(ctx, repo, head, true, false); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestGitArgumentGuardsAndHostSelection(t *testing.T) {
	c := &Cache{Env: processenv.Environment{Dir: t.TempDir()}}
	sha := strings.Repeat("A", 40)
	if got, err := c.ResolveRef(t.Context(), "https://example.invalid/r.git", &sha); err != nil || got != strings.ToLower(sha) {
		t.Fatal("pinned ref needs no Git executable", got, err)
	}
	for _, url := range []string{"--upload-pack=bad", "a\x00b"} {
		if _, err := c.ResolveRef(t.Context(), url, &sha); err == nil {
			t.Fatal(url)
		}
	}
	for _, ref := range []string{"-q", "a\x00b", "../../outside", "main", "aabbcc"} {
		if _, _, err := c.Clone(t.Context(), "repo", ref, true, false); err == nil {
			t.Fatal(ref)
		}
	}
	for url, want := range map[string]string{
		"git+ssh://git@github.com:22/owner/repo.git": "github.com",
		"git@github.com:owner/repo.git":              "github.com", "host:path": "host",
		"ssh://git@[::1]:22/repo": "::1", "ssh://[::1": "::1",
		"/tmp/repo": "", "file:///tmp/repo": "", "a/b:c": "", "": "",
	} {
		if got := Host(url); got != want {
			t.Fatal(url, got, want)
		}
	}
	if !HostInList("git@github.com:r", []string{"github.com"}) || HostInList("https://api.github.com/r", []string{"github.com"}) || HostInList("https://GitHub.com/r", []string{"github.com"}) {
		t.Fatal("host list is not exact")
	}
	refs := "first\trefs/heads/z\nmaster\trefs/heads/master\nmain\trefs/heads/main\n"
	if got, err := resolveAdvertisedRefs("repo", nil, refs); err != nil || got != "main" {
		t.Fatal(got, err)
	}
}

func TestRustGitRefOracle(t *testing.T) {
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		t.Skip("set PM_RUST_LOCKFILE_ORACLE to compare unchanged Rust Git helpers")
	}
	c, repo, first, second := gitFixture(t)
	var cases []map[string]any
	var want []map[string]any
	for _, ref := range []*string{nil, new("v1"), new("main"), new("missing"), new(first[:9]), new(strings.ToUpper(second)), new("aabbcc")} {
		cases = append(cases, map[string]any{"url": repo, "ref": ref})
		got, err := c.ResolveRef(t.Context(), repo, ref)
		if err == nil {
			want = append(want, map[string]any{"ok": true, "sha": got})
		} else {
			want = append(want, map[string]any{"ok": false, "error": err.Error()})
		}
	}
	for _, url := range []string{"https://github.com/a/b.git", "git+ssh://git@host:22/a", "git@host:a", "ssh://[::1]:23/r", "file:///tmp/r", "a/b:c", "https://Host/r"} {
		cases = append(cases, map[string]any{"url": url, "host": true})
		var host any
		if h := Host(url); h != "" {
			host = h
		}
		want = append(want, map[string]any{"host": host})
	}
	file := filepath.Join(t.TempDir(), "cases.json")
	data, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), oracle, "git-refs", file)
	cmd.Env, cmd.Dir = c.Env.Vars, c.Env.Dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	var got []map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Rust %s\nGo %#v", out, want)
	}
	t.Logf("compared %d Git ref and host cases", len(cases))
}
