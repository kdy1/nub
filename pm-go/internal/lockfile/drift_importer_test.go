package lockfile

import (
	"context"
	"encoding/json"
	"github.com/nubjs/nub/pm-go/internal/identity"
	"github.com/nubjs/nub/pm-go/internal/manifest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestImporterDriftRules(t *testing.T) {
	row := func(name, spec string, kind DepType) DirectDep {
		return DirectDep{Name: name, DepPath: name + "@1.0.0", Specifier: &spec, Type: kind}
	}
	for _, tc := range []struct {
		name, manifest, reason string
		deps                   []DirectDep
		configure              func(*Graph)
		overrides              map[string]string
	}{
		{name: "added", manifest: `{"dependencies":{"a":"^1"}}`, reason: "manifest adds a@^1"},
		{name: "empty", manifest: `{}`},
		{name: "no-specifier-format", manifest: `{"dependencies":{"other":"*"}}`, deps: []DirectDep{{Name: "a", DepPath: "a@1.0.0"}}},
		{name: "two-sections", manifest: `{"dependencies":{"a":"^1"},"devDependencies":{"a":"~1"}}`, deps: []DirectDep{row("a", "^1", Production), row("a", "~1", Dev)}},
		{name: "moved-section", manifest: `{"devDependencies":{"a":"^1"}}`, deps: []DirectDep{row("a", "^1", Production)}, reason: "a: manifest section is devDependencies, lockfile section is dependencies"},
		{name: "changed", manifest: `{"dependencies":{"a":"^2"}}`, deps: []DirectDep{row("a", "^1", Production)}, reason: "a: manifest says ^2, lockfile says ^1"},
		{name: "removed", manifest: `{}`, deps: []DirectDep{row("a", "^1", Production)}, reason: "manifest removed a"},
		{name: "override", manifest: `{"dependencies":{"a":"^1.0.0"}}`, deps: []DirectDep{row("a", "2.0.0", Production)}, overrides: map[string]string{"a@<2": "2.0.0"}},
		{name: "skipped-optional", manifest: `{"optionalDependencies":{"a":"^1"}}`, configure: func(g *Graph) { g.SkippedOptionalDependencies = map[string]map[string]string{".": {"a": "^1"}} }},
		{name: "skipped-changed", manifest: `{"optionalDependencies":{"a":"^2"}}`, configure: func(g *Graph) { g.SkippedOptionalDependencies = map[string]map[string]string{".": {"a": "^1"}} }, reason: "a: manifest says ^2, lockfile (skipped) says ^1"},
		{name: "skipped-now-required", manifest: `{"dependencies":{"a":"^1"}}`, configure: func(g *Graph) { g.SkippedOptionalDependencies = map[string]map[string]string{".": {"a": "^1"}} }, reason: "manifest adds a@^1"},
		{name: "ignored-optional", manifest: `{"optionalDependencies":{"a":"^1"}}`, configure: func(g *Graph) { g.IgnoredOptionalDependencies = Set{"a": {}} }},
		{name: "required-peer-exact", manifest: `{"peerDependencies":{"a":"^1"}}`, deps: []DirectDep{row("a", "1.2.0", Production)}},
		{name: "optional-peer-retained", manifest: `{"peerDependencies":{"a":"^1"},"peerDependenciesMeta":{"a":{"optional":true}}}`, deps: []DirectDep{row("a", "1.2.0", Production)}},
		{name: "optional-peer-stale", manifest: `{"peerDependencies":{"a":"^2"},"peerDependenciesMeta":{"a":{"optional":true}}}`, deps: []DirectDep{row("a", "1.2.0", Production)}, reason: "manifest removed a"},
		{name: "peers-disabled", manifest: `{"peerDependencies":{"a":"^1"}}`, deps: []DirectDep{row("a", "1.2.0", Production)}, configure: func(g *Graph) { g.Settings.AutoInstallPeers = false }, reason: "manifest removed a"},
		{name: "hook-link", manifest: `{"dependencies":{"a":"*"}}`, deps: []DirectDep{row("a", "link:../a/dist", Production)}, configure: func(g *Graph) { g.PnpmfileChecksum = new("hash") }},
		{name: "hook-authored-local", manifest: `{"dependencies":{"a":"file:../a"}}`, deps: []DirectDep{row("a", "link:../a/dist", Production)}, configure: func(g *Graph) { g.PnpmfileChecksum = new("hash") }, reason: "a: manifest says file:../a, lockfile says link:../a/dist"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := manifest.ParsePackage([]byte(tc.manifest))
			if err != nil {
				t.Fatal(err)
			}
			g := NewGraph()
			g.Importers["."] = tc.deps
			if tc.configure != nil {
				tc.configure(g)
			}
			got := g.CheckImporterDrift(".", p, tc.overrides, nil)
			if got.Reason != tc.reason {
				t.Fatalf("%q, want %q", got.Reason, tc.reason)
			}
			compareImporterDrift(t, g, tc.manifest, tc.overrides, nil, got)
		})
	}
}
func TestImporterWorkspaceLinkAndLabel(t *testing.T) {
	g := NewGraph()
	p, _ := manifest.ParsePackage([]byte(`{}`))
	g.Importers["."] = []DirectDep{{Name: "member", DepPath: "member@1.0.0", Specifier: new("link:packages/member")}}
	pkg := NewPackage("member", "1.0.0")
	pkg.Source = &Source{Kind: Link, Path: "packages/member"}
	g.Packages[pkg.DepPath] = pkg
	links := Set{"member": {}}
	got := g.CheckImporterDrift(".", p, nil, links)
	if !got.Fresh() {
		t.Fatal(got)
	}
	compareImporterDrift(t, g, `{}`, nil, links, got)
	pkg.Source.Kind = Directory
	if g.CheckImporterDrift(".", p, nil, links).Fresh() {
		t.Fatal("non-link retained")
	}
	p, _ = manifest.ParsePackage([]byte(`{"dependencies":{"new":"*"}}`))
	if got := g.CheckImporterDrift("packages/member", p, nil, nil); got.Reason != "packages/member: manifest adds new@*" {
		t.Fatal(got)
	}
}

func compareImporterDrift(t *testing.T, g *Graph, pj string, overrides map[string]string, links Set, want DriftStatus) {
	compareDrift(t, g, pj, DriftOptions{Kind: identity.Npm, WorkspaceOverrides: overrides}, links, want)
}
func compareDrift(t *testing.T, g *Graph, pj string, options DriftOptions, links Set, want DriftStatus) {
	t.Helper()
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		return
	}
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "package.json")
	requestPath := filepath.Join(dir, "request.json")
	if err := os.WriteFile(manifestPath, []byte(pj), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"deps": g.RootDeps(), "autoPeers": g.Settings.AutoInstallPeers, "skipped": g.SkippedOptionalDependencies["."], "ignored": g.IgnoredOptionalDependencies.Sorted(), "hook": g.PnpmfileChecksum, "overrides": options.WorkspaceOverrides, "kind": options.Kind, "lockedOverrides": g.Overrides, "catalogs": options.WorkspaceCatalogs, "workspaceIgnored": options.WorkspaceIgnoredOptional, "runtimes": g.Runtimes, "workspaceNames": links.Sorted()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(requestPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, oracle, "importer-drift", requestPath, manifestPath).CombinedOutput()
	if err != nil {
		t.Fatal(string(out), err)
	}
	var ref struct{ Reason string }
	if err := json.Unmarshal(out, &ref); err != nil {
		t.Fatal(string(out), err)
	}
	if ref.Reason != want.Reason {
		t.Fatalf("Rust %q, Go %q", ref.Reason, want.Reason)
	}
}
