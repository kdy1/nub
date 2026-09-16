package resolver

import (
	"reflect"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
)

func graphPackages(names ...string) *lockfile.Graph {
	graph := lockfile.NewGraph()
	for _, name := range names {
		pkg := lockfile.NewPackage(name, "1.0.0")
		graph.Packages[pkg.DepPath] = pkg
	}
	return graph
}
func packageAt(graph *lockfile.Graph, name string) *lockfile.Package {
	return graph.Packages[name+"@1.0.0"]
}
func direct(name string, kind lockfile.DepType) lockfile.DirectDep {
	return lockfile.DirectDep{Name: name, DepPath: name + "@1.0.0", Type: kind}
}

func TestPlatformGraphPrunesEdgesAndBundledClosures(t *testing.T) {
	g := graphPackages("app", "linux", "darwin", "bundled", "bundle-child", "shared", "required", "ignored")
	g.Importers["."] = []lockfile.DirectDep{direct("app", lockfile.Production), direct("darwin", lockfile.Optional), direct("required", lockfile.Production), direct("ignored", lockfile.Optional)}
	app := packageAt(g, "app")
	app.Dependencies = map[string]string{"linux": "1.0.0", "darwin": "darwin@1.0.0", "bundled": "1.0.0"}
	app.OptionalDependencies = map[string]string{"linux": "1.0.0", "darwin": "darwin@1.0.0"}
	app.BundledDependencies = []string{"bundled"}
	packageAt(g, "linux").OS = []string{"linux"}
	packageAt(g, "darwin").OS = []string{"darwin"}
	packageAt(g, "required").OS = []string{"darwin"}
	packageAt(g, "bundled").Dependencies = map[string]string{"bundle-child": "1.0.0", "shared": "1.0.0"}
	packageAt(g, "required").Dependencies["shared"] = "1.0.0"
	FilterGraph(g, Platform{OS: "linux", CPU: "x64", Libc: "glibc"}, Architectures{}, lockfile.Set{"ignored": {}})
	for _, name := range []string{"darwin", "ignored", "bundled", "bundle-child"} {
		if packageAt(g, name) != nil {
			t.Fatal("retained", name)
		}
	}
	for _, name := range []string{"app", "linux", "required", "shared"} {
		if packageAt(g, name) == nil {
			t.Fatal("removed", name)
		}
	}
	if len(app.Dependencies) != 1 || len(app.OptionalDependencies) != 1 {
		t.Fatal(app)
	}
}

func TestBerryOptionalAndRequiredPaths(t *testing.T) {
	g := graphPackages("app", "optional", "shared", "dev")
	g.Importers["."] = []lockfile.DirectDep{direct("app", lockfile.Production), direct("optional", lockfile.Optional)}
	g.Importers["packages/member"] = []lockfile.DirectDep{direct("dev", lockfile.Dev)}
	packageAt(g, "app").OptionalDependencies["optional"] = "optional@1.0.0"
	packageAt(g, "optional").Dependencies["shared"] = "1.0.0"
	packageAt(g, "dev").Dependencies["shared"] = "1.0.0"
	FilterGraph(g, Platform{OS: "linux", CPU: "x64"}, Architectures{}, nil)
	if len(g.Packages) != 4 {
		t.Fatal("lost optional-only edge", g.Packages)
	}
	MarkOptionalPackages(g)
	if !packageAt(g, "optional").Optional || packageAt(g, "shared").Optional || packageAt(g, "dev").Optional {
		t.Fatal("required path did not win")
	}
	// Remove the member's required path; the shared subtree becomes optional.
	delete(g.Importers, "packages/member")
	MarkOptionalPackages(g)
	if !packageAt(g, "shared").Optional {
		t.Fatal("optional descendant required")
	}
}

func TestTransitivePeerPropagationAndCycles(t *testing.T) {
	g := graphPackages("root", "middle", "leaf", "peer", "unrelated")
	root, middle, leaf, peer := packageAt(g, "root"), packageAt(g, "middle"), packageAt(g, "leaf"), packageAt(g, "peer")
	root.Dependencies["middle"] = "1.0.0"
	middle.Dependencies["leaf"] = "leaf@1.0.0"
	leaf.Dependencies["middle"] = "1.0.0"
	leaf.PeerDependencies["missing"] = "^1"
	leaf.PeerDependenciesMeta["optional-peer"] = lockfile.PeerMeta{Optional: true}
	leaf.PeerDependencies["peer"] = "^1"
	leaf.Dependencies["peer"] = "1.0.0"
	peer.PeerDependencies["not-inherited"] = "*"
	MarkTransitivePeers(g)
	for _, pkg := range []*lockfile.Package{root, middle} {
		if !reflect.DeepEqual(pkg.TransitivePeerDependencies, []string{"missing", "optional-peer"}) {
			t.Fatal(pkg.Name, pkg.TransitivePeerDependencies)
		}
	}
	if len(leaf.TransitivePeerDependencies) != 0 || len(packageAt(g, "unrelated").TransitivePeerDependencies) != 0 {
		t.Fatal("own peers escaped cycle guard")
	}
	leaf.Dependencies["missing"] = "2.0.0"
	MarkTransitivePeers(g)
	if !reflect.DeepEqual(root.TransitivePeerDependencies, []string{"optional-peer"}) {
		t.Fatal("stale peers", root.TransitivePeerDependencies)
	}
}

func TestPlatformPrunesCanonicalTarballEdge(t *testing.T) {
	g := graphPackages("app")
	source := lockfile.Source{Kind: lockfile.RemoteTarball, URL: "https://host/native.tgz"}
	native := lockfile.NewPackage("native", "1.0.0")
	native.DepPath = source.DepPath("native")
	native.OS = []string{"darwin"}
	g.Packages[native.DepPath] = native
	app := packageAt(g, "app")
	app.Dependencies["native"] = source.URL
	app.OptionalDependencies["native"] = source.URL
	g.Importers["."] = []lockfile.DirectDep{direct("app", lockfile.Production)}
	FilterGraph(g, Platform{OS: "linux", CPU: "x64"}, Architectures{}, nil)
	if len(g.Packages) != 1 || len(app.Dependencies) != 0 {
		t.Fatal(g.Packages, app.Dependencies)
	}
}
