package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nubjs/nub/pm-go/internal/identity"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
)

func peerGraph(root string, packages ...*lockfile.Package) *lockfile.Graph {
	g := lockfile.NewGraph()
	for _, p := range packages {
		g.Packages[p.DepPath] = p
	}
	g.Importers["."] = []lockfile.DirectDep{direct(root, lockfile.Production)}
	return g
}
func peerPackage(name, version string, deps, peers map[string]string) *lockfile.Package {
	p := lockfile.NewPackage(name, version)
	for n, v := range deps {
		p.Dependencies[n] = v
	}
	for n, v := range peers {
		p.PeerDependencies[n] = v
	}
	return p
}
func nestedPeerGraph() *lockfile.Graph {
	return peerGraph("consumer",
		peerPackage("consumer", "1.0.0", map[string]string{"adapter": "1.0.0", "core": "1.0.0"}, map[string]string{"adapter": "^1", "core": "^1"}),
		peerPackage("adapter", "1.0.0", map[string]string{"core": "1.0.0"}, map[string]string{"core": "^1"}),
		peerPackage("core", "1.0.0", nil, nil))
}
func cousinPeerGraph(optional bool) *lockfile.Graph {
	g := peerGraph("app",
		peerPackage("app", "1.0.0", map[string]string{"plugin": "1.0.0", "sibling": "1.0.0"}, nil),
		peerPackage("plugin", "1.0.0", nil, map[string]string{"theme": "*"}),
		peerPackage("sibling", "1.0.0", map[string]string{"theme": "1.0.0"}, nil),
		peerPackage("theme", "1.0.0", nil, nil))
	packageAt(g, "plugin").PeerDependenciesMeta["theme"] = lockfile.PeerMeta{Optional: optional}
	return g
}
func mutualPeerGraph() *lockfile.Graph {
	return peerGraph("a", peerPackage("a", "1.0.0", map[string]string{"b": "1.0.0"}, map[string]string{"b": "^1"}), peerPackage("b", "1.0.0", map[string]string{"a": "1.0.0"}, map[string]string{"a": "^1"}))
}
func chainPeerGraph(count int) *lockfile.Graph {
	g := peerGraph("peer-00")
	for i := 0; i < count; i++ {
		p := peerPackage(fmt.Sprintf("peer-%02d", i), "1.0.0", nil, nil)
		if i+1 < count {
			name := fmt.Sprintf("peer-%02d", i+1)
			p.Dependencies[name] = "1.0.0"
			p.PeerDependencies[name] = "^1"
		}
		g.Packages[p.DepPath] = p
	}
	return g
}
func assertPeerEdges(t *testing.T, g *lockfile.Graph) {
	t.Helper()
	for _, p := range g.Packages {
		for name, tail := range p.Dependencies {
			if g.Packages[name+"@"+tail] == nil {
				t.Fatalf("%s -> missing %s@%s", p.DepPath, name, tail)
			}
		}
	}
	for _, deps := range g.Importers {
		for _, d := range deps {
			if g.Packages[d.DepPath] == nil {
				t.Fatal("missing importer target", d)
			}
		}
	}
}
func TestPeerContextsNestedCycleAndDeepChain(t *testing.T) {
	nested := "consumer@1.0.0(adapter@1.0.0(core@1.0.0))(core@1.0.0)"
	deep := "peer-17@1.0.0"
	for i := 16; i >= 0; i-- {
		deep = fmt.Sprintf("peer-%02d@1.0.0(%s)", i, deep)
	}
	for _, tc := range []struct {
		g      *lockfile.Graph
		target string
	}{{nestedPeerGraph(), nested}, {mutualPeerGraph(), "a@1.0.0(b@1.0.0)"}, {chainPeerGraph(18), deep}} {
		before := tc.g.Clone()
		g, err := ApplyPeerContexts(tc.g, DefaultPeerContextOptions())
		if err != nil {
			t.Fatal(err)
		}
		if g.Importers["."][0].DepPath != tc.target || g.Packages[tc.target] == nil {
			t.Fatal(g.Importers, peerKeys(g.Packages), tc.target)
		}
		assertPeerEdges(t, g)
		if !reflect.DeepEqual(tc.g, before) {
			t.Fatal("modified input")
		}
	}
}
func TestOptionalPeerDoesNotBindCousin(t *testing.T) {
	for _, optional := range []bool{false, true} {
		g, err := ApplyPeerContexts(cousinPeerGraph(optional), DefaultPeerContextOptions())
		if err != nil {
			t.Fatal(err)
		}
		key := "plugin@1.0.0(theme@1.0.0)"
		if optional {
			key = "plugin@1.0.0"
		}
		if g.Packages[key] == nil {
			t.Fatal(peerKeys(g.Packages), key)
		}
		assertPeerEdges(t, g)
	}
}
func TestPeerContextsLocalProviderAndImportScaffolding(t *testing.T) {
	for _, kind := range []lockfile.SourceKind{lockfile.Directory, lockfile.Link, lockfile.Portal} {
		p := peerPackage("theme", "4.10.0", nil, nil)
		p.Source = &lockfile.Source{Kind: kind, Path: "theme"}
		p.DepPath = p.Source.DepPath("theme")
		plugin := peerPackage("plugin", "1.0.0", nil, map[string]string{"theme": "^4"})
		plugin.PeerDependenciesMeta["theme"] = lockfile.PeerMeta{Optional: true}
		g := peerGraph("theme", p, plugin)
		g.Importers["."][0].DepPath = p.DepPath
		g.Importers["member"] = []lockfile.DirectDep{direct("plugin", lockfile.Dev)}
		out, err := ApplyPeerContexts(g, DefaultPeerContextOptions())
		if err != nil {
			t.Fatal(err)
		}
		got := out.Packages["plugin@1.0.0(theme@4.10.0)"]
		if got == nil || got.Dependencies["theme"] != strings.TrimPrefix(p.DepPath, "theme@") {
			t.Fatal(out.Packages)
		}
		assertPeerEdges(t, out)
	}
	g := peerGraph("plugin", peerPackage("plugin", "1.0.0", nil, map[string]string{"theme": "*"}), peerPackage("theme", "1.0.0", nil, nil))
	for _, kind := range []identity.Kind{identity.Npm, identity.Shrinkwrap, identity.Bun} {
		out, err := PeerPassForImport(g, kind)
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Importers["."]) != 1 || out.Importers["."][0].Name != "plugin" || out.Packages["plugin@1.0.0(theme@1.0.0)"] == nil {
			t.Fatal(kind, out)
		}
		assertPeerEdges(t, out)
	}
	for _, kind := range []identity.Kind{identity.Pnpm, identity.Nub, identity.Yarn, identity.YarnBerry} {
		out, err := PeerPassForImport(g, kind)
		if err != nil || !reflect.DeepEqual(out, g) {
			t.Fatal(kind, err, out)
		}
	}
}

func TestPeerSuffixNamesBoundariesAndCollisions(t *testing.T) {
	for _, tc := range []struct{ input, want string }{{"a@1(b@2(c@3))", "a@1(2(3))"}, {"@s/a@1(@s/b@2)(c@3)", "@s/a@1(2)(3)"}, {"a@1(1234567890abcdef1234567890abcdef)", "a@1(1234567890abcdef1234567890abcdef)"}} {
		if got := dedupePeerKey(tc.input); got != tc.want {
			t.Fatal(tc, got)
		}
	}
	for _, tc := range []struct {
		value string
		want  bool
	}{{"a@1.0.0", false}, {"1(a@1.0.0)", true}, {"1(a@1.0.0(b@2))", true}, {"1(a@1.0.0-extra)", false}, {"1(aa@1.0.0)", false}, {"1(a@1.0.0", true}} {
		if got := containsCanonicalBackRef(tc.value, "a@1.0.0"); got != tc.want {
			t.Fatal(tc, got)
		}
	}
	input := "(@scope/react@1.0.0)(core@2.0.0)"
	if effectivePeerSuffix(input, len(input)-2) != input || !hashedPeerSuffix(effectivePeerSuffix(input, len(input)-3)) {
		t.Fatal("suffix byte cap")
	}
	g := graphPackages("foo", "bar")
	for _, name := range []string{"foo", "bar"} {
		p := peerPackage("consumer", "1.0.0", map[string]string{name: "1.0.0"}, map[string]string{name: "^1"})
		p.DepPath += "(" + name + "@1.0.0)"
		g.Packages[p.DepPath] = p
		g.Importers["."] = append(g.Importers["."], lockfile.DirectDep{Name: "consumer", DepPath: p.DepPath})
	}
	dedupePeerSuffixes(g)
	if len(g.Packages) != 4 || g.Packages["consumer@1.0.0(foo@1.0.0)"] == nil || g.Packages["consumer@1.0.0(bar@1.0.0)"] == nil {
		t.Fatal(peerKeys(g.Packages))
	}
	assertPeerEdges(t, g)
}

func contextOracleGraphs() []*lockfile.Graph {
	graphs := []*lockfile.Graph{nestedPeerGraph(), mutualPeerGraph(), chainPeerGraph(18), cousinPeerGraph(true), cousinPeerGraph(false), peerHoistFixture(lockfile.Dev, lockfile.Optional, false, "")}
	// Different ancestor versions and optional/required ranges, with a root
	// provider and a closer incompatible provider on a workspace member path.
	for _, optional := range []bool{false, true} {
		for _, own := range []string{"", "1.0.0", "2.0.0"} {
			g := peerGraph("theme", peerPackage("theme", "2.0.0", nil, nil), peerPackage("theme", "1.0.0", nil, nil), peerPackage("plugin", "1.0.0", nil, map[string]string{"theme": "^2"}))
			g.Importers["."][0].DepPath = "theme@2.0.0"
			g.Importers["member"] = []lockfile.DirectDep{direct("theme", lockfile.Production), direct("plugin", lockfile.Dev)}
			p := packageAt(g, "plugin")
			p.PeerDependenciesMeta["theme"] = lockfile.PeerMeta{Optional: optional}
			if own != "" {
				p.Dependencies["theme"] = own
				p.OptionalDependencies["theme"] = own
			}
			graphs = append(graphs, g)
		}
	}
	// Canonical consumer reached under two different peer scopes.
	g := peerGraph("app", peerPackage("app", "1.0.0", map[string]string{"left": "1.0.0", "right": "1.0.0"}, nil), peerPackage("left", "1.0.0", map[string]string{"theme": "1.0.0", "plugin": "1.0.0"}, nil), peerPackage("right", "1.0.0", map[string]string{"theme": "2.0.0", "plugin": "1.0.0"}, nil), peerPackage("plugin", "1.0.0", nil, map[string]string{"theme": "*"}), peerPackage("theme", "1.0.0", nil, nil), peerPackage("theme", "2.0.0", nil, nil))
	graphs = append(graphs, g)
	// Local linked providers use manifest versions in suffixes and local targets
	// in edges; all other source kinds use their graph tails.
	for _, kind := range []lockfile.SourceKind{lockfile.Directory, lockfile.Tarball, lockfile.Link, lockfile.Portal, lockfile.Exec} {
		p := peerPackage("theme", "4.10.0", nil, nil)
		p.Source = &lockfile.Source{Kind: kind, Path: "theme"}
		p.DepPath = p.Source.DepPath("theme")
		g := peerGraph("theme", p, peerPackage("plugin", "1.0.0", nil, map[string]string{"theme": "^4"}))
		g.Importers["."][0].DepPath = p.DepPath
		g.Importers["member"] = []lockfile.DirectDep{direct("plugin", lockfile.Dev)}
		graphs = append(graphs, g)
	}
	// Deterministic small graph corpus includes unreachable nodes, optional
	for _, kind := range []lockfile.SourceKind{lockfile.Git, lockfile.RemoteTarball} {
		g := cousinPeerGraph(false)
		p := packageAt(g, "app")
		delete(g.Packages, p.DepPath)
		p.Source = &lockfile.Source{Kind: kind, URL: "https://example.invalid/app", Resolved: "0123456789abcdef0123456789abcdef01234567"}
		p.DepPath = p.Source.DepPath(p.Name)
		g.Packages[p.DepPath] = p
		g.Importers["."][0].DepPath = p.DepPath
		graphs = append(graphs, g)
	}
	// Deterministic small graph corpus includes unreachable nodes, optional
	// edges, cycles, meta-only peers, missing providers and incompatible ranges.
	for seed := 0; seed < 24; seed++ {
		g := graphPackages("a", "b", "c", "d", "e")
		g.Importers["."] = []lockfile.DirectDep{direct("a", lockfile.Production), direct("c", lockfile.Optional)}
		for i, name := range []string{"a", "b", "c", "d", "e"} {
			p := packageAt(g, name)
			child := []string{"b", "c", "d", "e", "a"}[(i+seed)%5]
			if seed%3 != 0 {
				p.Dependencies[child] = "1.0.0"
			}
			if seed%4 == 0 {
				p.OptionalDependencies[child] = "1.0.0"
			}
			peer := []string{"c", "d", "e", "a", "b"}[(i+seed/3)%5]
			if (seed+i)%3 != 0 {
				p.PeerDependencies[peer] = []string{"*", "^1", "^2"}[(seed+i)%3]
			}
			if (seed+i)%2 == 0 {
				p.PeerDependenciesMeta[peer] = lockfile.PeerMeta{Optional: true}
			}
		}
		graphs = append(graphs, g)
	}
	return graphs
}
func TestRustPeerContextOracle(t *testing.T) {
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		t.Skip("reference library CI")
	}
	type input struct {
		Graph   *lockfile.Graph
		Options PeerContextOptions
		Hoist   bool
	}
	var inputs []input
	for _, g := range contextOracleGraphs() {
		for _, dedupe := range []bool{false, true} {
			for _, root := range []bool{false, true} {
				for _, cap := range []int{0, 1000} {
					opts := DefaultPeerContextOptions()
					opts.DedupePeers = dedupe
					opts.ResolveFromWorkspaceRoot = root
					opts.PeersSuffixMaxLength = cap
					inputs = append(inputs, input{g, opts, false})
				}
			}
		}
		opts := DefaultPeerContextOptions()
		opts.DedupePeerDependents = false
		inputs = append(inputs, input{g, opts, true})
	}
	data, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "input.json")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, oracle, "peer-contexts", path).CombinedOutput()
	if err != nil {
		t.Fatal(string(out), err)
	}
	var refs []struct {
		Graph json.RawMessage
		Error *string
	}
	if err = json.Unmarshal(out, &refs); err != nil {
		t.Fatal(string(out), err)
	}
	if len(refs) != len(inputs) {
		t.Fatal("result count", len(refs))
	}
	for i, in := range inputs {
		g := in.Graph.Clone()
		var hoisted AutoInstalledPeers
		if in.Hoist {
			hoisted = HoistAutoInstalledPeers(g)
		}
		got, err := ApplyPeerContexts(g, in.Options)
		if refs[i].Error != nil {
			if err == nil || err.Error() != *refs[i].Error {
				t.Fatalf("case %d: Go=%v Rust=%s", i, err, *refs[i].Error)
			}
			continue
		}
		if err != nil {
			t.Fatalf("case %d: Go=%v Rust succeeded", i, err)
		}
		RemoveAutoInstalledPeers(got, hoisted)
		assertPeerGraph(t, i, got, refs[i].Graph)
	}
	t.Logf("compared %d peer-context graphs across scope, dedupe and suffix settings", len(inputs))
}
