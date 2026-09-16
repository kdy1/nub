package resolver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/gitcache"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/processenv"
	"github.com/nubjs/nub/pm-go/internal/registry"
	"github.com/nubjs/nub/pm-go/internal/store"
)

type gitArchiveFixture struct {
	data []byte
	err  error
	urls []string
}

func (f *gitArchiveFixture) Tarball(_ context.Context, url string, mode registry.NetworkMode) ([]byte, error) {
	f.urls = append(f.urls, url)
	if mode == registry.Offline {
		return nil, fmt.Errorf("offline fixture")
	}
	return f.data, f.err
}

func TestHostedGitMetadataAndWarmPin(t *testing.T) {
	sha := strings.Repeat("a", 40)
	data := resolverTarball(t,
		resolverTarEntry{"repo-sha/package.json", `{"version":"1.2.3","dependencies":{"root-child":"^1"}}`},
		resolverTarEntry{"repo-sha/packages/child/package.json", `{"version":"4.5.6","dependencies":{"sub-child":"^4"}}`},
	)
	for _, subpath := range []*string{nil, new("packages/child")} {
		cache := &gitcache.Cache{Root: filepath.Join(t.TempDir(), "git"), Env: processenv.Environment{Dir: t.TempDir()}}
		client := &gitArchiveFixture{data: data}
		source := lockfile.Source{Kind: lockfile.Git, URL: "ssh://git@github.com/owner/repo.git", Committish: &sha, Subpath: subpath}
		got, meta, actual, err := ResolveGitSource(t.Context(), "alias", source, cache, client, registry.Normal, true)
		if err != nil || actual == nil || *actual != store.Integrity(data) || meta.Name != "alias" {
			t.Fatal(got, meta, actual, err)
		}
		if subpath == nil {
			if got.Kind != lockfile.RemoteTarball || !got.GitHosted || meta.Version != "1.2.3" {
				t.Fatal(got, meta)
			}
		} else if got.Kind != lockfile.Git || got.URL != source.URL || got.Resolved != sha || meta.Version != "4.5.6" {
			t.Fatal(got, meta)
		}
		if len(client.urls) != 1 || client.urls[0] != "https://codeload.github.com/owner/repo/tar.gz/"+sha {
			t.Fatal(client.urls)
		}
		source.Integrity = actual
		warm, warmMeta, warmActual, err := ResolveGitSource(t.Context(), "alias", source, cache, client, registry.Offline, false)
		if err != nil || warm.Kind != got.Kind || warmMeta.Version != meta.Version || warmActual == nil || *warmActual != *actual || len(client.urls) != 1 {
			t.Fatal(warm, warmMeta, warmActual, err, client.urls)
		}
		bad := store.Integrity([]byte("other bytes"))
		source.Integrity = &bad
		if _, _, _, err := ResolveGitSource(t.Context(), "alias", source, cache, client, registry.Normal, true); err == nil || !strings.Contains(err.Error(), "integrity mismatch") {
			t.Fatal("pinned integrity must fail before clone", err)
		}
	}
}

func TestGitCloneFallbackAndManifestlessRepository(t *testing.T) {
	root := t.TempDir()
	env := processenv.Environment{Dir: root, Vars: os.Environ()}
	for key, value := range map[string]string{
		"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": filepath.Join(root, "no-config"),
		"GIT_AUTHOR_NAME": "fixture", "GIT_AUTHOR_EMAIL": "fixture@example.invalid", "GIT_COMMITTER_NAME": "fixture", "GIT_COMMITTER_EMAIL": "fixture@example.invalid",
	} {
		env = env.With(key, value)
	}
	run := func(args ...string) string {
		t.Helper()
		cmd, err := env.Command(t.Context(), "git", args...)
		if err != nil {
			t.Fatal(err)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(root, "README"), []byte("no package.json"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "README")
	run("commit", "-q", "-m", "fixture")
	sha := run("rev-parse", "HEAD")
	// Keep hosted transport rewriting real, but route Git to a local fixture.
	env = env.With("GIT_CONFIG_COUNT", "1").With("GIT_CONFIG_KEY_0", "url."+filepath.ToSlash(root)+".insteadOf").With("GIT_CONFIG_VALUE_0", "https://github.com/owner/repo.git")
	source := lockfile.Source{Kind: lockfile.Git, URL: "ssh://git@github.com/owner/repo.git", Committish: &sha}
	for _, client := range []*gitArchiveFixture{
		{err: fmt.Errorf("not found")}, {data: []byte("broken gzip")},
		{data: resolverTarball(t, resolverTarEntry{"repo/package.json", `{invalid`})},
	} {
		cache := &gitcache.Cache{Root: filepath.Join(t.TempDir(), "git"), Env: env}
		got, meta, actual, err := ResolveGitSource(t.Context(), "alias", source, cache, client, registry.Normal, true)
		if err != nil || got.Kind != lockfile.Git || got.URL != source.URL || got.Resolved != sha || got.Integrity != nil || actual != nil || meta.Version != "0.0.0" || len(meta.Dependencies) != 0 {
			t.Fatal(got, meta, actual, err)
		}
		if _, _, _, err := ResolveGitSource(t.Context(), "alias", source, cache, nil, registry.Offline, false); err != nil {
			t.Fatal(err)
		}
	}
	source.Committish = new("main")
	cache := &gitcache.Cache{Root: filepath.Join(t.TempDir(), "git"), Env: env}
	if _, _, _, err := ResolveGitSource(t.Context(), "alias", source, cache, nil, registry.Offline, false); err == nil || !strings.Contains(err.Error(), "pinned commit") {
		t.Fatal(err)
	}
}

func TestGitManifestRootValidation(t *testing.T) {
	tree := t.TempDir()
	if _, err := readGitManifest("p", tree, "clone", new("missing")); err == nil {
		t.Fatal("missing subpath accepted")
	}
	if err := os.WriteFile(filepath.Join(tree, "file"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readGitManifest("p", tree, "clone", new("file")); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatal(err)
	}
	if _, err := readGitManifest("p", tree, "clone", new("../outside")); err == nil {
		t.Fatal("subpath escaped checkout")
	}
}
