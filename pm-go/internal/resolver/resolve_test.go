package resolver

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/manifest"
	"github.com/nubjs/nub/pm-go/internal/npmconfig"
	"github.com/nubjs/nub/pm-go/internal/processenv"
	"github.com/nubjs/nub/pm-go/internal/registry"
	"github.com/nubjs/nub/pm-go/internal/testregistry"
	"github.com/nubjs/nub/pm-go/internal/testutil"
)

func resolutionRegistry(t *testing.T) *testregistry.Registry {
	return testregistry.Start(t,
		testregistry.Package{Name: "child", Version: "1.0.0", Published: "2020-01-01T00:00:00.000Z"},
		testregistry.Package{Name: "child", Version: "1.5.0", Published: "2020-03-01T00:00:00.000Z"},
		testregistry.Package{Name: "child", Version: "2.0.0", Published: "2020-05-01T00:00:00.000Z"},
		testregistry.Package{Name: "parent", Version: "1.0.0", Published: "2020-02-01T00:00:00.000Z", Manifest: map[string]any{"dependencies": map[string]string{"child": "^1"}, "peerDependencies": map[string]string{"peer": "^1"}, "bin": "cli.js", "license": "MIT", "deprecated": "old", "hasInstallScript": true}},
		testregistry.Package{Name: "parent", Version: "2.0.0", Published: "2020-04-01T00:00:00.000Z", Manifest: map[string]any{"dependencies": map[string]string{"child": "*"}, "peerDependencies": map[string]string{"peer": "^1"}}},
		testregistry.Package{Name: "peer", Version: "1.0.0"},
		testregistry.Package{Name: "peer", Version: "1.8.0"},
		testregistry.Package{Name: "cycle", Version: "1.0.0", Manifest: map[string]any{"dependencies": map[string]string{"cycle": "1.0.0"}}},
		testregistry.Package{Name: "optional", Version: "1.0.0", Manifest: map[string]any{"os": []string{"unavailable-os"}}},
		testregistry.Package{Name: "bundle", Version: "1.0.0", Manifest: map[string]any{"dependencies": map[string]string{"embedded": "*", "child": "1.0.0"}, "bundledDependencies": []string{"embedded"}, "peerDependenciesMeta": map[string]any{"meta-peer": map[string]bool{"optional": true}}}},
	)
}
func resolutionClient(t *testing.T, server *testregistry.Registry, root string) *registry.Client {
	config := npmconfig.Resolve([]npmconfig.Entry{{Source: npmconfig.User, Key: "registry", Value: server.URL}}, nil)
	policy := registry.DefaultFetchPolicy()
	policy.Retries = 0
	client := registry.NewClient(config, registry.ClientOptions{Dir: root, Env: []string{}, Policy: policy})
	t.Cleanup(client.Close)
	return client
}
func importer(t *testing.T, path, raw string) lockfile.ImporterManifest {
	t.Helper()
	p, err := manifest.ParsePackage([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return lockfile.ImporterManifest{Path: path, Package: p}
}

func TestResolveRegistryGraphReuseAndOptionalPolicy(t *testing.T) {
	server := resolutionRegistry(t)
	root := t.TempDir()
	r := New(resolutionClient(t, server, root), processenv.Environment{Dir: root, Vars: os.Environ()}, t.TempDir())
	projects := []lockfile.ImporterManifest{importer(t, ".", `{"dependencies":{"parent":"1.0.0","alias":"npm:child@2.0.0","bundle":"1.0.0","cycle":"1.0.0"},"optionalDependencies":{"optional":"*","child":"9.x","missing":"*"}}`)}
	graph, err := r.Resolve(t.Context(), projects, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.RootDeps()) != 4 || graph.SkippedOptionalDependencies["."]["optional"] != "*" || graph.SkippedOptionalDependencies["."]["child"] != "9.x" {
		t.Fatal(graph.RootDeps(), graph.SkippedOptionalDependencies)
	}
	if _, ok := graph.SkippedOptionalDependencies["."]["missing"]; ok {
		t.Fatal("failed fetch recorded as no-match")
	}
	if graph.Packages["alias@2.0.0"].RegistryName() != "child" {
		t.Fatal(graph.Packages)
	}
	if _, ok := graph.Packages["peer@1.8.0"]; !ok {
		t.Fatal("peer was not auto installed", graph.Packages)
	}
	if graph.Packages["bundle@1.0.0"].Dependencies["embedded"] != "" {
		t.Fatal("bundled dependency resolved separately")
	}
	r.Options.Network = registry.Offline
	if _, err := r.Resolve(t.Context(), projects, graph, nil); err != nil {
		t.Fatal("locked and cached graph cannot resolve offline", err)
	}
}

func TestResolveHooksAndExtensionsDoNotMutateInputs(t *testing.T) {
	server := resolutionRegistry(t)
	root := t.TempDir()
	r := New(resolutionClient(t, server, root), processenv.Environment{Dir: root}, t.TempDir())
	r.Options.PackageExtensions = []PackageExtension{{Selector: "parent@1", Dependencies: map[string]string{"bundle": "1"}}}
	calls := map[string]int{}
	r.ReadPackage = func(_ context.Context, p *registry.Version) (*registry.Version, error) {
		calls[p.Name]++
		if p.Name == "" {
			p.Dependencies["parent"] = "1.0.0"
		}
		if p.Name == "parent" {
			p.Dependencies["child"] = "2.0.0"
			p.Name = "forged"
			p.Version = "99.0.0"
			p.Dist = nil
			p.OS = []string{"unavailable-os"}
		}
		return p, nil
	}
	projects := []lockfile.ImporterManifest{importer(t, ".", `{}`)}
	graph, err := r.Resolve(t.Context(), projects, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects[0].Package.Dependencies) != 0 {
		t.Fatal("importer input mutated")
	}
	var parent *lockfile.Package
	for _, p := range graph.Packages {
		if p.Name == "parent" {
			parent = p
		}
	}
	if parent == nil || parent.Version != "1.0.0" || parent.Integrity == nil || len(parent.OS) != 0 || parent.Dependencies["child"] != "2.0.0" || parent.Dependencies["bundle"] != "1.0.0" || calls["parent"] != 1 {
		t.Fatal(parent, calls)
	}
}

func TestRustResolutionGraphOracle(t *testing.T) {
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		t.Skip("set PM_RUST_LOCKFILE_ORACLE to compare full resolver graphs")
	}
	server := resolutionRegistry(t)
	root := t.TempDir()
	local := filepath.Join(root, "local")
	if err := os.Mkdir(local, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "package.json"), []byte(`{"name":"local","version":"3.2.1","dependencies":{"child":"1.0.0"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	fixtures := []string{
		`{"dependencies":{"parent":"*","alias":"npm:child@2.0.0"}}`,
		`{"dependencies":{"parent":"1.0.0","peer":"1.0.0","cycle":"1.0.0","bundle":"1.0.0"}}`,
		`{"optionalDependencies":{"optional":"*","child":"9.x","missing":"*"}}`,
		`{"dependencies":{"child":"catalog:"}}`,
		`{"dependencies":{"parent":"1.0.0","child":"*"}}`,
		`{"dependencies":{"local":"file:./local","linked":"link:./local","portaled":"portal:./local"}}`,
		`{"dependencies":{"child":"no-such-tag"}}`,
		`{"dependencies":{"absent":"workspace:*"}}`,
	}
	var cases []map[string]any
	for mode := Highest; mode <= LowestDirect; mode++ {
		for _, autoPeers := range []bool{false, true} {
			for _, fixture := range fixtures {
				p := importer(t, ".", fixture)
				for _, reuse := range []bool{false, true} {
					r := New(resolutionClient(t, server, root), processenv.Environment{Dir: root}, t.TempDir())
					r.Options.Mode = mode
					r.Options.AutoInstallPeers = autoPeers
					r.Options.Catalogs = Catalogs{"default": {"child": "^1"}}
					r.Options.Overrides = map[string]string{"parent>child": "1.0.0"}
					g, err := r.Resolve(t.Context(), []lockfile.ImporterManifest{p}, nil, nil)
					if err == nil && reuse {
						g, err = r.Resolve(t.Context(), []lockfile.ImporterManifest{p}, g, nil)
					}
					result := map[string]any{"ok": err == nil}
					if err != nil {
						result["error"] = err.Error()
					} else {
						raw, e := testutil.GraphJSON(g)
						if e != nil {
							t.Fatal(e)
						}
						var value any
						if e := json.Unmarshal(raw, &value); e != nil {
							t.Fatal(e)
						}
						result["graph"] = value
					}
					var authored any
					if err := json.Unmarshal([]byte(fixture), &authored); err != nil {
						t.Fatal(err)
					}
					cases = append(cases, map[string]any{"root": root, "registry": server.URL, "manifest": authored, "mode": int(mode), "autoPeers": autoPeers, "reuse": reuse, "expected": result})
				}
			}
		}
	}
	input := filepath.Join(t.TempDir(), "cases.json")
	data, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), oracle, "resolve", input)
	cmd.Env = processenv.Environment{Vars: os.Environ()}.With("XDG_CACHE_HOME", t.TempDir()).Vars
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	var results []map[string]any
	if err := json.Unmarshal(output, &results); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	if len(results) != len(cases) {
		t.Fatal(len(results), len(cases))
	}
	for i, result := range results {
		if !reflect.DeepEqual(result, cases[i]["expected"]) {
			got, _ := json.Marshal(result)
			want, _ := json.Marshal(cases[i]["expected"])
			t.Fatalf("case %d %v mode=%v peers=%v reuse=%v\nRust %s\nGo %s", i, cases[i]["manifest"], cases[i]["mode"], cases[i]["autoPeers"], cases[i]["reuse"], got, want)
		}
	}
	t.Logf("compared %d fresh/reused resolution graphs and errors", len(cases))
}

func TestResolutionCancellationAndIgnoredGenerator(t *testing.T) {
	r := New(nil, processenv.Environment{Dir: t.TempDir()}, t.TempDir())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.Resolve(ctx, nil, nil, nil); err != context.Canceled {
		t.Fatal(err)
	}
	r.Options.IgnoreScripts = true
	p := importer(t, ".", `{"dependencies":{"generated":"exec:generate.js"}}`)
	if _, err := r.Resolve(t.Context(), []lockfile.ImporterManifest{p}, nil, nil); err == nil || !strings.Contains(err.Error(), "scripts are disabled") {
		t.Fatal(err)
	}
}
