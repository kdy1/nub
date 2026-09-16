package installdelta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
)

func ptr(s string) *string { return &s }
func graphFixture() *lockfile.Graph {
	g := lockfile.NewGraph()
	for _, name := range []string{"parent", "bridge", "child", "independent", "unrelated", "deep"} {
		p := lockfile.NewPackage(name, "1.0.0")
		g.Packages[p.DepPath] = p
	}
	g.Packages["parent@1.0.0"].Dependencies = map[string]string{"bridge": "1.0.0", "unrelated": "1.0.0"}
	g.Packages["bridge@1.0.0"].Dependencies["child"] = "1.0.0"
	g.Packages["unrelated@1.0.0"].Dependencies["deep"] = "1.0.0"
	return g
}

func TestDeltaContentAndAncestorInvalidation(t *testing.T) {
	g := graphFixture()
	root := t.TempDir()
	before, subtree, err := Compute(t.Context(), g, nil, root)
	if err != nil {
		t.Fatal(err)
	}
	g.Packages["child@1.0.0"].Integrity = ptr("sha512-changed")
	after, changed, err := Compute(t.Context(), g, nil, root)
	if err != nil {
		t.Fatal(err)
	}
	d := Diff(before, after)
	if d.Touched() != 1 || !d.ShouldTouch("child@1.0.0") || d.ShouldTouch("parent@1.0.0") {
		t.Fatal(d)
	}
	if got := ChangedSubtreeRoots(subtree, changed); !reflect.DeepEqual(got, []string{"bridge@1.0.0", "child@1.0.0", "parent@1.0.0"}) {
		t.Fatal(got)
	}
	if Diff(after, after).Touched() != 0 || !Diff(after, after).Empty() {
		t.Fatal("stable graph")
	}
	d = Diff(map[string]string{"removed": "", "changed": "a"}, map[string]string{"new": "", "changed": "b"})
	if d.Touched() != 3 || d.ShouldTouch("removed") || !d.ShouldTouch("new") || len(d.TouchedSet()) != 2 {
		t.Fatal(d)
	}
	p := g.Packages["child@1.0.0"]
	p.PeerDependencies["peer"] = "*"
	p.Engines = map[string]string{"node": ">=22"}
	p.Bin = map[string]string{"cli": "cli.js"}
	p.OptionalDependencies["optional"] = "1"
	metadata, err := PackageHashes(t.Context(), g, nil, root)
	if err != nil || !reflect.DeepEqual(metadata, after) {
		t.Fatal("metadata changed content identity", err)
	}
	p.TarballURL = ptr("https://registry.test/child/-/child-1.0.0.tgz?token=x#fragment")
	derivable, _ := PackageHashes(t.Context(), g, nil, root)
	if !reflect.DeepEqual(derivable, after) {
		t.Fatal("derivable integrity-pinned URL changes identity")
	}
	p.TarballURL = ptr("https://registry.test/custom.tar.gz")
	custom, _ := PackageHashes(t.Context(), g, nil, root)
	if custom[p.DepPath] == after[p.DepPath] {
		t.Fatal("custom URL ignored")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := PackageHashes(ctx, g, nil, root); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestLtHashMultiplicityAndInverse(t *testing.T) {
	var empty, one, two LtHash
	one.Add("x")
	two.Add("x")
	two.Add("x")
	if empty == two || one == two || empty.Digest() == two.Digest() {
		t.Fatal("duplicate vanished")
	}
	two.Remove("x")
	if one != two {
		t.Fatal("remove is not inverse")
	}
	two.Remove("x")
	if empty != two {
		t.Fatal("not empty")
	}
	a := GraphHash(map[string]string{"a": "a", "b": "b"})
	var b LtHash
	b.Add("b")
	b.Add("a")
	if a != b {
		t.Fatal("order dependent")
	}
	var c LtHash
	c.Add("c")
	a.Combine(c)
	b.Add("c")
	if a != b {
		t.Fatal("combine")
	}
}

func TestBuildPhasesBridgesCyclesAndDeepGraphs(t *testing.T) {
	g := graphFixture()
	selected := lockfile.Set{"parent@1.0.0": {}, "child@1.0.0": {}, "independent@1.0.0": {}, "missing": {}}
	want := [][]string{{"child@1.0.0", "independent@1.0.0"}, {"parent@1.0.0"}}
	if got := BuildPhases(g, selected); !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
	g.Packages["child@1.0.0"].Dependencies["parent"] = "1.0.0"
	if got := BuildPhases(g, selected); !reflect.DeepEqual(got, [][]string{{"child@1.0.0", "independent@1.0.0", "parent@1.0.0"}}) {
		t.Fatal(got)
	}
	leaf, sub, err := Compute(t.Context(), g, nil, t.TempDir())
	if err != nil || sub["parent@1.0.0"] != sub["child@1.0.0"] || sub["parent@1.0.0"] == leaf["parent@1.0.0"] {
		t.Fatal(sub, err)
	}
	deep := lockfile.NewGraph()
	selected = lockfile.Set{}
	for i := range 10000 {
		p := lockfile.NewPackage(fmt.Sprintf("p%05d", i), "1")
		if i < 9999 {
			p.Dependencies[fmt.Sprintf("p%05d", i+1)] = "1"
		}
		deep.Packages[p.DepPath] = p
		if i == 0 || i == 9999 {
			selected.Add(p.DepPath)
		}
	}
	if got := BuildPhases(deep, selected); !reflect.DeepEqual(got, [][]string{{"p09999@1"}, {"p00000@1"}}) {
		t.Fatal(got)
	}
}

func TestRustDeltaAndBuildGraphOracle(t *testing.T) {
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		t.Skip("set PM_RUST_LOCKFILE_ORACLE for install delta parity")
	}
	root := t.TempDir()
	script := filepath.Join(root, "generate.cjs")
	if err := os.WriteFile(script, []byte("console.log('generated')"), 0644); err != nil {
		t.Fatal(err)
	}
	var cases []map[string]any
	var want []map[string]any
	for i := range 30 {
		g := graphFixture()
		p := g.Packages["child@1.0.0"]
		patches := map[string]string{}
		stored := map[string]string{"removed": "hash"}
		switch i {
		case 1:
			p.Integrity = ptr("sha512-integrity")
		case 2:
			p.AliasOf = ptr("real")
			patches["real@1.0.0"] = "alias patch"
		case 3:
			p.AliasOf = ptr("real")
			patches[p.SpecKey()] = "alias first"
			patches["real@1.0.0"] = "registry second"
		case 4:
			p.Integrity = ptr("")
		case 5:
			p.OS = []string{"darwin", "linux"}
			p.CPU = []string{"x64", "arm64"}
			p.Libc = []string{"glibc"}
		case 6:
			p.OS = []string{"linux", "darwin"}
		case 7:
			p.TarballURL = ptr("https://example.test/child/-/child-1.0.0.tgz")
		case 8:
			p.TarballURL = ptr("https://example.test/child/-/child-1.0.0.tgz?x#y")
			p.Integrity = ptr("known")
		case 9:
			p.TarballURL = ptr("https://example.test/custom.tgz")
			p.Integrity = ptr("known")
		case 10:
			p.TarballURL = ptr("https://example.test/child/-/child-1.0.0.tgz")
			p.Integrity = ptr("known")
			p.RegistryGitHosted = true
		case 11, 12, 13, 14:
			p.Source = &lockfile.Source{Kind: lockfile.SourceKind(i - 11), Path: "local/path"}
		case 15:
			p.Source = &lockfile.Source{Kind: lockfile.Exec, Path: "generate.cjs"}
		case 16:
			p.Source = &lockfile.Source{Kind: lockfile.Exec, Path: script}
		case 17:
			p.Source = &lockfile.Source{Kind: lockfile.Exec, Path: "../outside"}
		case 18:
			p.Source = &lockfile.Source{Kind: lockfile.Git, URL: "https://example.test/repo.git", Resolved: "commit", Committish: ptr("main"), Subpath: ptr("packages/a"), Integrity: ptr("ignored")}
		case 19:
			p.Source = &lockfile.Source{Kind: lockfile.RemoteTarball, URL: "https://example.test/pkg.tgz", Integrity: ptr("sha512-pinned")}
		case 20:
			p.Dependencies["parent"] = "1.0.0"
		case 21:
			p.Dependencies["child"] = "1.0.0"
		case 22:
			p.Dependencies["missing"] = "99"
		case 23:
			p.Dependencies["parent"] = "parent@1.0.0"
		case 24:
			g = lockfile.NewGraph()
		case 25:
			p.Source = &lockfile.Source{Kind: lockfile.Git, URL: "git@host:path", Resolved: "sha"}
		case 26:
			p.Source = &lockfile.Source{Kind: lockfile.Portal, Path: "local"}
		case 27:
			p.AliasOf = ptr("@scope/real")
			p.Integrity = ptr("known")
			p.TarballURL = ptr("https://host/@scope/real/-/real-1.0.0.tgz")
		case 28:
			p.DepPath = "child@1.0.0(peer@2)"
		case 29:
			p.Dependencies = map[string]string{"a": "bc", "ab": "c"}
		}
		leaf, subtree, err := Compute(t.Context(), g, patches, root)
		if err != nil {
			t.Fatal(err)
		}
		if i%2 == 0 {
			stored["child@1.0.0"] = leaf["child@1.0.0"]
		}
		stored["parent@1.0.0"] = "different"
		for j := range 3 {
			selected := lockfile.Set{}
			for _, key := range keys(g.Packages) {
				if j == 0 || j == 1 && (key == "child@1.0.0" || key == "parent@1.0.0") {
					selected.Add(key)
				}
			}
			plan := Diff(stored, leaf)
			h := GraphHash(leaf)
			digest := h.Digest()
			h.Add("duplicate")
			h.Add("duplicate")
			h.Remove("duplicate")
			cases = append(cases, map[string]any{"graph": g, "patches": patches, "stored": stored, "selected": selected.Sorted(), "project": root})
			want = append(want, map[string]any{"leaf": leaf, "subtree": subtree, "digest": digest, "incremented": h.Digest(), "phases": BuildPhases(g, selected), "added": plan.Added, "removed": plan.Removed, "changed": plan.Changed, "touch": plan.TouchedSet().Sorted(), "roots": ChangedSubtreeRoots(stored, subtree)})
		}
	}
	data, _ := json.Marshal(cases)
	path := filepath.Join(t.TempDir(), "cases.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	output, err := exec.CommandContext(t.Context(), oracle, "delta", path).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	var got, expected []map[string]any
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	data, _ = json.Marshal(want)
	if err := json.Unmarshal(data, &expected); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(expected) {
		t.Fatal(len(got), len(expected))
	}
	for i := range got {
		if !reflect.DeepEqual(got[i], expected[i]) {
			t.Errorf("case %d: Rust %v; Go %v", i, got[i], expected[i])
		}
	}
	t.Logf("compared %d content/subtree/multiset/delta/build-phase cases", len(cases))
}
