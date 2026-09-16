package fetch

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/processenv"
	"github.com/nubjs/nub/pm-go/internal/registry"
	"github.com/nubjs/nub/pm-go/internal/store"
)

func TestPathImportsTrackEditsAndSkipLinks(t *testing.T) {
	f, p, _ := registryFixture(t, "archive contents")
	root := t.TempDir()
	local := filepath.Join(root, "local")
	for _, path := range []string{".git", "node_modules"} {
		if err := os.MkdirAll(filepath.Join(local, path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(local, path, "ignored"), []byte("ignored"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for i, kind := range []lockfile.SourceKind{lockfile.Directory, lockfile.Portal} {
		content := []string{"first edit", "second edit"}[i]
		if err := os.WriteFile(filepath.Join(local, "index.js"), []byte(content), 0755); err != nil {
			t.Fatal(err)
		}
		p.Source = &lockfile.Source{Kind: kind, Path: "local"}
		result, err := f.Path(t.Context(), p, root)
		if err != nil || result.Cached || len(result.Index) != 1 {
			t.Fatal(result, err)
		}
		assertContent(t, result, content)
	}
	p.Source = &lockfile.Source{Kind: lockfile.Link, Path: "does-not-exist"}
	result, err := f.Path(t.Context(), p, root)
	if err != nil || result.Index != nil {
		t.Fatal(result, err)
	}
	p.Source.Kind = lockfile.Directory
	if _, err := f.Path(t.Context(), p, root); err == nil {
		t.Fatal("missing directory accepted")
	}
}

func TestLocalAndRemoteTarballSources(t *testing.T) {
	f, p, _ := registryFixture(t, "archive contents")
	url, pin := *p.TarballURL, p.Integrity
	data, err := f.Client.Tarball(t.Context(), url, registry.Normal)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.tgz"), data, 0600); err != nil {
		t.Fatal(err)
	}
	p.Source = &lockfile.Source{Kind: lockfile.Tarball, Path: "package.tgz"}
	f.Options.Network = registry.Offline
	result, err := f.Path(t.Context(), p, root)
	if err != nil {
		t.Fatal(err)
	}
	assertContent(t, result, "archive contents")
	p.Source = &lockfile.Source{Kind: lockfile.RemoteTarball, URL: url, Integrity: pin}
	if _, err := f.Remote(t.Context(), p); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatal(err)
	}
	f.Options.Network = registry.Normal
	result, err = f.Remote(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	assertContent(t, result, "archive contents")
	p.Source.Integrity = new(store.Integrity([]byte("different archive")))
	f.Options.VerifyIntegrity = false
	if _, err := f.Remote(t.Context(), p); err == nil {
		t.Fatal("source pin bypassed by registry-store setting")
	}
}

func TestGeneratedImportUsesPATHNodeAndCleansOutput(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("PATH Node required for generators")
	}
	f, p, _ := registryFixture(t, "unused")
	root, temp := t.TempDir(), filepath.Join(t.TempDir(), "new-temp-root")
	script := `fs.writeFileSync(path.join(execEnv.buildDir, 'index.js'), process.env.MARKER);`
	if err := os.WriteFile(filepath.Join(root, "generate.cjs"), []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	p.Source = &lockfile.Source{Kind: lockfile.Exec, Path: "generate.cjs"}
	env := processenv.Environment{Dir: root, Vars: os.Environ()}.With("PATH", filepath.Dir(node)).With("MARKER", "generated contents")
	result, err := f.Generated(t.Context(), p, env, temp, false)
	if err != nil {
		t.Fatal(err)
	}
	assertContent(t, result, "generated contents")
	if len(result.Index) != 1 {
		t.Fatal("materialization imposed resolver manifest requirement", result.Index)
	}
	entries, err := os.ReadDir(temp)
	if err != nil || len(entries) != 0 {
		t.Fatal(entries, err)
	}
	_, err = f.Generated(t.Context(), p, env.With("PATH", ""), temp, true)
	if err == nil || !strings.Contains(err.Error(), "scripts are disabled") {
		t.Fatal(err)
	}
	if err := f.Store.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = f.Generated(t.Context(), p, env, temp, false)
	if !errors.Is(err, os.ErrClosed) {
		t.Fatal(err)
	}
	entries, err = os.ReadDir(temp)
	if err != nil || len(entries) != 0 {
		t.Fatal("failed import leaked generated tree", entries, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f.Generated(ctx, p, env, temp, false); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
