package lockfile

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
)

func graphFixture() *Graph {
	g := NewGraph()
	for _, name := range []string{"app", "shared", "dev", "member", "leaf"} {
		pkg := NewPackage(name, "1.0.0")
		g.Packages[pkg.DepPath] = pkg
	}
	g.Importers["."] = []DirectDep{{Name: "app", DepPath: "app@1.0.0", Type: Production}, {Name: "dev", DepPath: "dev@1.0.0", Type: Dev}}
	g.Importers["packages/member"] = []DirectDep{{Name: "member", DepPath: "member@1.0.0", Type: Production}}
	g.Packages["app@1.0.0"].Dependencies["shared"] = "1.0.0"
	g.Packages["dev@1.0.0"].Dependencies["shared"] = "shared@1.0.0"
	g.Packages["member@1.0.0"].Dependencies["leaf"] = "leaf@1.0.0"
	g.Packages["shared@1.0.0"].Dependencies["app"] = "1.0.0"
	return g
}

func TestGraphClosuresAndDepths(t *testing.T) {
	g := graphFixture()
	want := Set{"app@1.0.0": {}, "shared@1.0.0": {}, "missing": {}}
	if got := g.Reachable([]string{"app@1.0.0", "missing"}, false); !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
	want = Set{"app@1.0.0": {}, "shared@1.0.0": {}, "dev@1.0.0": {}}
	if got := g.ImporterClosure([]string{"shared@1.0.0"}); !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
	depth := g.DependencyDepths()
	for key, want := range map[string]int{"app@1.0.0": 0, "shared@1.0.0": 1, "dev@1.0.0": 0, "member@1.0.0": 0, "leaf@1.0.0": 1} {
		if depth[key] != want {
			t.Fatal(depth)
		}
	}
	// Berry can carry an optional edge only in the optional map.
	g.Packages["app@1.0.0"].OptionalDependencies["leaf"] = "1.0.0"
	if !g.Reachable([]string{"app@1.0.0"}, true).Has("leaf@1.0.0") {
		t.Fatal("optional-only edge omitted")
	}
}

func TestStructuralFiltersCloneResolutionIntent(t *testing.T) {
	g := graphFixture()
	g.Settings.AutoInstallPeers = false
	g.Overrides = map[string]string{"shared": "1.0.0"}
	g.Catalogs = map[string]map[string]CatalogEntry{"default": {"shared": {"^1", "1.0.0"}}}
	g.ExtraFields = map[string]*jsonvalue.Value{"future": jsonvalue.Object()}
	g.ExtraFields["future"].Put("x", jsonvalue.String("original"))
	g.SkippedOptionalDependencies = map[string]map[string]string{".": {"root-only": "*"}, "packages/member": {"native": "^1"}}
	filtered := g.FilterDeps(func(d DirectDep) bool { return d.Type != Dev })
	if filtered.Packages["dev@1.0.0"] != nil || filtered.Packages["shared@1.0.0"] == nil || len(filtered.Importers) != 2 {
		t.Fatal(filtered)
	}
	if filtered.Settings.AutoInstallPeers || !reflect.DeepEqual(g.Catalogs, filtered.Catalogs) {
		t.Fatal("resolution metadata lost")
	}
	filtered.Packages["shared@1.0.0"].Dependencies["changed"] = "1"
	filtered.Overrides["shared"] = "2"
	filtered.ExtraFields["future"].Put("x", jsonvalue.String("edited"))
	if _, ok := g.Packages["shared@1.0.0"].Dependencies["changed"]; ok {
		t.Fatal("package clone aliases")
	}
	if g.Overrides["shared"] != "1.0.0" || g.ExtraFields["future"].Get("x").Text() != "original" {
		t.Fatal("metadata clone aliases")
	}
	subset, ok := g.SubsetToImporter("packages/member", func(DirectDep) bool { return true })
	if !ok || len(subset.Importers) != 1 || len(subset.RootDeps()) != 1 || len(subset.Packages) != 2 || len(subset.SkippedOptionalDependencies) != 1 || subset.SkippedOptionalDependencies["."]["native"] != "^1" {
		t.Fatal(subset, ok)
	}
	if _, ok := g.SubsetToImporter("missing", func(DirectDep) bool { return true }); ok {
		t.Fatal("missing importer accepted")
	}
}

func TestGraphTraversesPinnedSources(t *testing.T) {
	g := NewGraph()
	app := NewPackage("app", "1")
	remote := Source{Kind: RemoteTarball, URL: "https://host/pkg.tgz"}
	git, _ := ParseGit("git+https://host/repo.git#" + strings.Repeat("a", 40))
	git.Resolved = *git.Committish
	a, b := NewPackage("archive", "1"), NewPackage("git", "1")
	a.DepPath = remote.DepPath(a.Name)
	b.DepPath = git.DepPath(b.Name)
	app.Dependencies[a.Name] = remote.URL
	a.Dependencies[b.Name] = git.Specifier()
	for _, p := range []*Package{app, a, b} {
		g.Packages[p.DepPath] = p
	}
	g.Importers["."] = []DirectDep{{Name: "app", DepPath: app.DepPath}}
	if got := g.Reachable([]string{app.DepPath}, false); len(got) != 3 {
		t.Fatal(got)
	}
	if got := g.ImporterClosure([]string{b.DepPath}); len(got) != 3 {
		t.Fatal(got)
	}
	if got := g.DependencyDepths(); got[b.DepPath] != 2 {
		t.Fatal(got)
	}
}

func TestPackageIdentityHelpers(t *testing.T) {
	real := "actual"
	p := NewPackage("alias", "1.0.0")
	p.AliasOf = &real
	key, value, ok := LookupPatch(p, map[string]string{"actual@1.0.0": "real.patch"})
	if !ok || key != "actual@1.0.0" || value != "real.patch" {
		t.Fatal(key, value, ok)
	}
	key, _, _ = LookupPatch(p, map[string]string{"alias@1.0.0": "alias.patch", "actual@1.0.0": "real.patch"})
	if key != "alias@1.0.0" {
		t.Fatal(key)
	}
	p.RegistryGitHosted = true
	if _, ok := p.SourceApprovalKey(); ok {
		t.Fatal("registry inferred as source")
	}
	p.Source = &Source{Kind: Git, URL: "git+ssh://git@host/repo", Resolved: strings.Repeat("f", 40)}
	if key, ok := p.GitRepositoryApprovalKey(); !ok || key != "actual@git+ssh://git@host/repo" {
		t.Fatal(key, ok)
	}
	if key, ok := p.SourceApprovalKey(); !ok || key != "actual@"+p.Source.Specifier() {
		t.Fatal(key, ok)
	}
	p.PeerDependencies["typed"] = "^1"
	p.PeerDependenciesMeta["meta"] = PeerMeta{Optional: true}
	if got := p.DeclaredPeers(); got["typed"] != "^1" || got["meta"] != "*" {
		t.Fatal(got)
	}
	for _, test := range []struct{ from, to, want string }{{".", "packages/a", "packages/a"}, {"packages/a", "packages/b", "../b"}, {"packages/a", "packages/a", "."}, {"a/b", ".", "../.."}} {
		if got := LinkFromImporter(test.from, test.to); got != test.want {
			t.Fatal(test, got)
		}
	}
}
