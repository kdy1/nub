package lockfile

import (
	"github.com/nubjs/nub/pm-go/internal/identity"
	"github.com/nubjs/nub/pm-go/internal/manifest"
	"testing"
)

func TestResolutionMetadataDrift(t *testing.T) {
	for _, tc := range []struct {
		name, pj, reason string
		configure        func(*Graph, *DriftOptions)
	}{
		{name: "added-override", pj: `{"overrides":{"a":"1"}}`, reason: "overrides: manifest adds a@1"},
		{name: "changed-override", pj: `{"overrides":{"a":"2"}}`, configure: func(g *Graph, o *DriftOptions) { g.Overrides = map[string]string{"a": "1"} }, reason: "overrides: a changed (1 → 2)"},
		{name: "removed-override", pj: `{}`, configure: func(g *Graph, o *DriftOptions) { g.Overrides = map[string]string{"a": "1"} }, reason: "overrides: manifest removes a"},
		{name: "catalog-chain", pj: `{"overrides":{"parent/@scope/a":"catalog:"}}`, configure: func(g *Graph, o *DriftOptions) {
			g.Overrides = map[string]string{"parent/@scope/a": "^2"}
			o.WorkspaceCatalogs = map[string]map[string]string{"default": {"@scope/a": "^2"}}
		}},
		{name: "workspace-priority", pj: `{"overrides":{"a":"1"}}`, configure: func(g *Graph, o *DriftOptions) {
			g.Overrides = map[string]string{"a": "2"}
			o.WorkspaceOverrides = map[string]string{"a": "2"}
		}},
		{name: "unresolved-ref", pj: `{"overrides":{"a":"$missing"}}`},
		{name: "ignored-added", pj: `{"pnpm":{"ignoredOptionalDependencies":[false,"optional"]}}`, reason: "ignoredOptionalDependencies: manifest adds optional"},
		{name: "ignored-removed", pj: `{}`, configure: func(g *Graph, o *DriftOptions) { g.IgnoredOptionalDependencies = Set{"optional": {}} }, reason: "ignoredOptionalDependencies: manifest removes optional"},
		{name: "ignored-workspace", pj: `{}`, configure: func(g *Graph, o *DriftOptions) {
			g.IgnoredOptionalDependencies = Set{"optional": {}}
			o.WorkspaceIgnoredOptional = []string{"optional"}
		}},
		{name: "runtime-removed", pj: `{}`, configure: func(g *Graph, o *DriftOptions) {
			g.Runtimes = map[string]RuntimePin{"node": {Specifier: "^22", Version: "22.1.0"}}
		}, reason: "devEngines.runtime: manifest no longer pins node (lockfile records 22.1.0)"},
		{name: "runtime-changed", pj: `{"devEngines":{"runtime":{"name":"node","version":"^24"}}}`, configure: func(g *Graph, o *DriftOptions) {
			g.Runtimes = map[string]RuntimePin{"node": {Specifier: "^22", Version: "22.1.0"}}
		}, reason: "devEngines.runtime: node changed (^22 → ^24)"},
		{name: "runtime-no-range", pj: `{"devEngines":{"runtime":{"name":"node"}}}`, configure: func(g *Graph, o *DriftOptions) {
			g.Runtimes = map[string]RuntimePin{"node": {Specifier: "^22", Version: "22.1.0"}}
		}},
		{name: "runtime-first-valid", pj: `{"devEngines":{"runtime":[{"name":"node","version":5},{"name":"node","version":"^22"},{"name":"node","version":"^24"}]}}`, configure: func(g *Graph, o *DriftOptions) {
			g.Runtimes = map[string]RuntimePin{"node": {Specifier: "^22", Version: "22.1.0"}}
		}},
		{name: "new-runtime-not-drift", pj: `{"devEngines":{"runtime":{"name":"node","version":"^24"}}}`},
		{name: "npm-no-metadata", pj: `{"overrides":{"a":"1"}}`, configure: func(g *Graph, o *DriftOptions) { o.Kind = identity.Npm }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := manifest.ParsePackage([]byte(tc.pj))
			if err != nil {
				t.Fatal(err)
			}
			g := NewGraph()
			o := DriftOptions{Kind: identity.Pnpm}
			if tc.configure != nil {
				tc.configure(g, &o)
			}
			got := g.CheckDrift(p, o)
			if got.Reason != tc.reason {
				t.Fatalf("%q, want %q", got.Reason, tc.reason)
			}
			compareDrift(t, g, tc.pj, o, nil, got)
		})
	}
}
func TestWorkspaceDriftAndSourceScope(t *testing.T) {
	p, _ := manifest.ParsePackage([]byte(`{"pnpm":{"overrides":{"a":"1"},"ignoredOptionalDependencies":["a"]}}`))
	g := NewGraph()
	o := DriftOptions{ManifestOverrides: map[string]string{}, ManifestIgnoredOptional: Set{}, WorkspaceInstall: true}
	if got := g.CheckWorkspaceDrift([]ImporterManifest{{".", p}}, o); !got.Fresh() {
		t.Fatal(got)
	}
	g.Importers["packages/removed"] = nil
	if got := g.CheckWorkspaceDrift([]ImporterManifest{{".", p}}, o); got.Reason != "workspace importer packages/removed is in the lockfile but not in the workspace" {
		t.Fatal(got)
	}
	root, _ := manifest.ParsePackage([]byte(`{"overrides":{"a":"2"}}`))
	member, _ := manifest.ParsePackage([]byte(`{"name":"member","dependencies":{"a":"^1"},"overrides":{"a":"3"}}`))
	g = NewGraph()
	g.Overrides = map[string]string{"a": "2"}
	g.Importers["packages/member"] = []DirectDep{{Name: "a", DepPath: "a@2.0.0", Specifier: new("2")}}
	if got := g.CheckWorkspaceDrift([]ImporterManifest{{".", root}, {"packages/member", member}}, DriftOptions{WorkspaceInstall: true}); !got.Fresh() {
		t.Fatal(got)
	}
}
