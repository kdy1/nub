package resolver

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/testutil"
)

func peerHoistFixture(left, right lockfile.DepType, optional bool, edge string) *lockfile.Graph {
	g := graphPackages("first", "second", "transitive", "unused", "peer", "missing-canonical")
	g.Importers["."] = []lockfile.DirectDep{direct("first", left), direct("second", right), direct("absent", lockfile.Production)}
	g.Importers["packages/member"] = []lockfile.DirectDep{direct("first", lockfile.Dev), direct("peer", lockfile.Dev)}
	first := packageAt(g, "first")
	first.PeerDependencies = map[string]string{"peer": "^1", "unavailable": "*", "missing-canonical": "*"}
	first.Dependencies["transitive"] = "1.0.0"
	first.Dependencies["missing-canonical"] = "9.0.0"
	if edge != "" {
		first.Dependencies["peer"] = edge
	}
	second := packageAt(g, "second")
	second.PeerDependencies["peer"] = "^9"
	second.PeerDependenciesMeta["peer"] = lockfile.PeerMeta{Optional: optional}
	first.PeerDependenciesMeta["meta-only"] = lockfile.PeerMeta{}
	packageAt(g, "transitive").PeerDependencies["unused"] = "*"
	for _, version := range []string{"2.0.0", "10.0.0", "5-not-semver"} {
		p := lockfile.NewPackage("peer", version)
		g.Packages[p.DepPath] = p
	}
	return g
}

func TestPeerHoistScopeSectionsAndRestoration(t *testing.T) {
	for _, left := range []lockfile.DepType{lockfile.Production, lockfile.Dev, lockfile.Optional} {
		for _, right := range []lockfile.DepType{lockfile.Production, lockfile.Dev, lockfile.Optional} {
			for _, optional := range []bool{false, true} {
				g := peerHoistFixture(left, right, optional, "")
				packageCount := len(g.Packages)
				hoisted := HoistAutoInstalledPeers(g)
				if len(hoisted) != 1 || !hoisted["."].Has("peer") || len(hoisted["."]) != 1 {
					t.Fatal(hoisted)
				}
				var peer lockfile.DirectDep
				for _, d := range g.Importers["."] {
					if d.Name == "peer" {
						peer = d
					}
				}
				wantType := left
				if left != right && !optional {
					wantType = lockfile.Production
				}
				if peer.DepPath != "peer@10.0.0" || peer.Type != wantType || peer.Specifier == nil || *peer.Specifier != "^1" {
					t.Fatal(peer, wantType)
				}
				if got := HoistAutoInstalledPeers(g); len(got) != 0 {
					t.Fatal("not idempotent", got)
				}
				RemoveAutoInstalledPeers(g, hoisted)
				if len(g.Packages) != packageCount || len(g.Importers["."]) != 3 || len(g.Importers["packages/member"]) != 2 {
					t.Fatal(g)
				}
			}
		}
	}
}
func TestPeerHoistWiredProviderPrecedence(t *testing.T) {
	for _, tc := range []struct{ edge, want string }{{"1.0.0(other@2.0.0)", "peer@1.0.0"}, {"9.0.0", ""}, {"peer@1.0.0", ""}} {
		g := peerHoistFixture(lockfile.Dev, lockfile.Production, true, tc.edge)
		hoisted := HoistAutoInstalledPeers(g)
		var got string
		for _, d := range g.Importers["."] {
			if d.Name == "peer" {
				got = d.DepPath
			}
		}
		if got != tc.want || (len(hoisted) != 0) != (tc.want != "") {
			t.Fatal(tc, got, hoisted)
		}
	}
}
func TestUnmetPeerDiagnostics(t *testing.T) {
	g := peerHoistFixture(lockfile.Dev, lockfile.Production, true, "1.0.0(context@2)")
	want := []UnmetPeer{
		{FromDepPath: "first@1.0.0", FromName: "first", PeerName: "unavailable", Declared: "*"},
		{FromDepPath: "transitive@1.0.0", FromName: "transitive", PeerName: "unused", Declared: "*"},
	}
	// The diagnostic checks edge versions, not whether a graph node exists.
	if got := DetectUnmetPeers(g); !reflect.DeepEqual(got, want) {
		t.Fatal(got, want)
	}
	packageAt(g, "first").Dependencies["peer"] = "peer@1.0.0"
	if got := DetectUnmetPeers(g); len(got) != 3 || got[0].PeerName != "peer" || *got[0].Found != "peer@1.0.0" {
		t.Fatal(got)
	}
}

func TestRustPeerHoistOracle(t *testing.T) {
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		t.Skip("reference library CI")
	}
	var inputs []*lockfile.Graph
	for _, left := range []lockfile.DepType{lockfile.Production, lockfile.Dev, lockfile.Optional} {
		for _, right := range []lockfile.DepType{lockfile.Production, lockfile.Dev, lockfile.Optional} {
			for _, optional := range []bool{false, true} {
				for _, edge := range []string{"", "1.0.0(other@2)", "9.0.0", "peer@1.0.0", "not-semver"} {
					inputs = append(inputs, peerHoistFixture(left, right, optional, edge))
				}
			}
		}
	}
	data, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "input.json")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, oracle, "peer-hoist", path).CombinedOutput()
	if err != nil {
		t.Fatal(string(out), err)
	}
	var refs []struct {
		Hoisted        map[string][]string
		Graph, Removed json.RawMessage
		Unmet          []UnmetPeer
	}
	if err = json.Unmarshal(out, &refs); err != nil {
		t.Fatal(string(out), err)
	}
	if len(refs) != len(inputs) {
		t.Fatal("result count", len(refs))
	}
	for i, g := range inputs {
		ref := refs[i]
		if got := DetectUnmetPeers(g); !reflect.DeepEqual(got, ref.Unmet) {
			t.Fatalf("case %d unmet: Go=%+v Rust=%+v", i, got, ref.Unmet)
		}
		hoisted := HoistAutoInstalledPeers(g)
		names := map[string][]string{}
		for path, set := range hoisted {
			names[path] = set.Sorted()
		}
		if !reflect.DeepEqual(names, ref.Hoisted) {
			t.Fatalf("case %d hoists: Go=%v Rust=%v", i, names, ref.Hoisted)
		}
		assertPeerGraph(t, i, g, ref.Graph)
		RemoveAutoInstalledPeers(g, hoisted)
		assertPeerGraph(t, i, g, ref.Removed)
	}
	t.Logf("compared %d peer hoist/removal/diagnostic cases", len(inputs))
}
func assertPeerGraph(t *testing.T, index int, g *lockfile.Graph, ref json.RawMessage) {
	t.Helper()
	data, err := testutil.GraphJSON(g)
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if err = json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(ref, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("case %d graph: Go=%s\nRust=%s", index, data, ref)
	}
}
