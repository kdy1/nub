package installstate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nubjs/nub/pm-go/internal/linker"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
)

func recordedFixture(t *testing.T) (Paths, RecordInput) {
	t.Helper()
	p, plan := layoutFixture(t)
	if _, err := linker.LinkIsolatedProject(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	local := lockfile.NewPackage("local", "1.0.0")
	local.Source = &lockfile.Source{Kind: lockfile.Directory, Path: "local"}
	plan.Graph.Packages[local.DepPath] = local
	put(t, filepath.Join(p.Project, "local", "index.js"), "module.exports=1")
	put(t, filepath.Join(p.Project, "nub.lock"), "fixture lock")
	input := RecordInput{
		State:     State{EngineVersion: "reference", SettingsHash: "settings", DepBuildPolicyHash: "builds", ReleasePolicyHash: "age", PackageJSONHashes: CollectManifestHashes(p.Project, []string{".", "packages/member"})},
		Layout:    LayoutInput{Graph: plan.Graph, Linker: "isolated", VirtualStore: filepath.Join(p.Project, "node_modules", ".store")},
		Workspace: FreshnessInput{Members: []string{filepath.Join(p.Project, "packages/member")}}, SharedWorkspaceLockfile: true,
	}
	if err := p.Record(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	return p, input
}

func TestFreshnessDecisionPriorityAndCompletion(t *testing.T) {
	cases := []struct {
		name, want string
		change     func(*testing.T, Paths, *RecordInput, *State)
	}{
		{"warm", "", func(t *testing.T, p Paths, i *RecordInput, s *State) {}},
		{"lockfile", "nub.lock has changed", func(t *testing.T, p Paths, i *RecordInput, s *State) {
			put(t, join(p.Project, "nub.lock"), "changed lock longer")
		}},
		{"script-only", "", func(t *testing.T, p Paths, i *RecordInput, s *State) {
			put(t, join(p.Project, "package.json"), `{"scripts":{"test":"echo changed"},"name":"root"}`)
		}},
		{"dependencies", "package.json has changed", func(t *testing.T, p Paths, i *RecordInput, s *State) {
			put(t, join(p.Project, "package.json"), `{"name":"root","dependencies":{"x":"1"}}`)
		}},
		{"missing-root", ". is missing", func(t *testing.T, p Paths, i *RecordInput, s *State) {
			if err := os.Remove(join(p.Project, "package.json")); err != nil {
				t.Fatal(err)
			}
		}},
		{"new-member", "packages/new is a new workspace member", func(t *testing.T, p Paths, i *RecordInput, s *State) {
			i.Workspace.Members = append(i.Workspace.Members, join(p.Project, "packages/new"))
		}},
		{"filtered", "previous install omitted dependency sections; auto-installing full graph", func(t *testing.T, p Paths, i *RecordInput, s *State) {
			s.SectionFiltered = true
			s.DeferredDepBuilds = nil
		}},
		{"legacy-builds", "install state predates dependency-build completion tracking; re-checking builds", func(t *testing.T, p Paths, i *RecordInput, s *State) { s.DeferredDepBuilds = nil }},
		{"owed-build", "a dependency build did not run on the last install (a, b, c, and 2 more); retrying", func(t *testing.T, p Paths, i *RecordInput, s *State) {
			b := []string{"a", "b", "c", "d", "e"}
			s.DeferredDepBuilds = &b
		}},
		{"denied-build", "", func(t *testing.T, p Paths, i *RecordInput, s *State) { s.UnreviewedBuilds = []string{"denied@1"} }},
		{"missing-policy", "dependency build policy state is missing", func(t *testing.T, p Paths, i *RecordInput, s *State) { s.DepBuildPolicyHash = "" }},
		{"settings", "install settings or the active Node version have changed", func(t *testing.T, p Paths, i *RecordInput, s *State) { h := "other"; i.Workspace.SettingsHash = &h }},
		{"missing-lock", "no lockfile found", func(t *testing.T, p Paths, i *RecordInput, s *State) {
			if err := os.Remove(join(p.Project, "nub.lock")); err != nil {
				t.Fatal(err)
			}
		}},
		{"legacy-local", "local dependency fingerprints not recorded", func(t *testing.T, p Paths, i *RecordInput, s *State) { s.LocalDirectoryHashes = nil }},
		{"local-change", "local dependency local has changed", func(t *testing.T, p Paths, i *RecordInput, s *State) {
			put(t, join(p.Project, "local/index.js"), "module.exports=longer")
		}},
		{"local-removed", "local dependency local is unreadable", func(t *testing.T, p Paths, i *RecordInput, s *State) {
			if err := os.RemoveAll(join(p.Project, "local")); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, input := recordedFixture(t)
			s := p.Read()
			c.change(t, p, &input, s)
			if err := p.Write(s); err != nil {
				t.Fatal(err)
			}
			reason, err := p.Check(t.Context(), input.Workspace)
			if err != nil || reason != c.want {
				t.Fatalf("got %q (%v), want %q", reason, err, c.want)
			}
		})
	}
}

func TestFreshnessMetadataRefreshAndExplicitStateLocation(t *testing.T) {
	p, input := recordedFixture(t)
	before := p.Read()
	stamp := time.Unix(1500000000, 123456700)
	for _, rel := range []string{"nub.lock", "local/index.js"} {
		path := join(p.Project, rel)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if reason, err := p.Check(t.Context(), input.Workspace); reason != "" || err != nil {
		t.Fatal(reason, err)
	}
	f := p.ReadFreshness()
	if reflect.DeepEqual(before.LockfileMeta, f.LockfileMeta) || (*before.LocalDirectoryHashes)["local"].MetadataHash == (*f.LocalDirectoryHashes)["local"].MetadataHash {
		t.Fatal("metadata was not refreshed")
	}
	if reason, err := p.Check(t.Context(), input.Workspace); reason != "" || err != nil {
		t.Fatal(reason, err)
	}
	// State can live outside modulesDir; removing modules must still invalidate.
	p.State = filepath.Join(t.TempDir(), ".nub-state")
	if err := p.Record(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(p.Modules); err != nil {
		t.Fatal(err)
	}
	if reason, err := p.Check(t.Context(), input.Workspace); reason != "node_modules is missing" || err != nil {
		t.Fatal(reason, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.Check(ctx, input.Workspace); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestMemberOwnedLockfileFreshness(t *testing.T) {
	p, input := recordedFixture(t)
	input.SharedWorkspaceLockfile = false
	member := input.Workspace.Members[0]
	put(t, join(member, "package-lock.json"), "member lock")
	if err := os.Remove(join(p.Project, "nub.lock")); err != nil {
		t.Fatal(err)
	}
	if err := p.Record(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	check := func(want string) {
		t.Helper()
		got, err := p.Check(t.Context(), input.Workspace)
		if err != nil || got != want {
			t.Fatal(got, want, err)
		}
	}
	check("")
	put(t, join(member, "package-lock.json"), "member lock changed")
	check("packages/member lockfile has changed")
	if err := os.Remove(join(member, "package-lock.json")); err != nil {
		t.Fatal(err)
	}
	check("packages/member lockfile is missing")
	input.Workspace.Members = nil
	check("packages/member was removed from the workspace")
	input.Workspace.Members = []string{member, join(p.Project, "new")}
	check("packages/member lockfile is missing")
	put(t, join(member, "package-lock.json"), "member lock")
	check("new is a new workspace member")
}

func TestRecordLicensesAndReuseGates(t *testing.T) {
	p, input := recordedFixture(t)
	input.Layout.Linker = "hoisted" // This test vouches by hashes only; the linker
	// independently checks package.json before honoring a reusable placement.
	input.State.PackageContentHashes = map[string]string{"same": "a", "changed": "b", "removed": "c"}
	if err := p.Record(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	current := map[string]string{"same": "a", "changed": "new", "added": "d"}
	if got := p.ReusableHoisted(current).Sorted(); !reflect.DeepEqual(got, []string{"same"}) {
		t.Fatal(got)
	}
	if err := p.MarkLinkInProgress(); err != nil {
		t.Fatal(err)
	}
	if len(p.ReusableHoisted(current)) != 0 {
		t.Fatal("interrupted link reused")
	}
	if err := p.ClearLinkInProgress(); err != nil {
		t.Fatal(err)
	}
	if !p.SettingsChanged("changed") || p.SettingsChanged("settings") || !p.ReleasePolicyChanged("changed") || p.ReleasePolicyChanged("age") {
		t.Fatal("policy change gates")
	}
	s := p.Read()
	s.ReleasePolicyHash = ""
	if err := p.Write(s); err != nil {
		t.Fatal(err)
	}
	if p.ReleasePolicyChanged("new") {
		t.Fatal("unknown release policy forces repick")
	}
	for _, c := range []struct {
		raw, want string
		ok        bool
	}{
		{`{"license":"MIT"}`, "MIT", true}, {`{"license":{"type":"ISC"}}`, "ISC", true},
		{`{"licenses":[{"type":"MIT"},false,"ISC"]}`, "MIT OR ISC", true},
		{`{"license":"","licenses":["MIT"]}`, "", true},
		{`{"license":"MIT","license":"ISC"}`, "", false},
		{`{"license":{"type":"MIT","type":"ISC"}}`, "ISC", true},
	} {
		dir := t.TempDir()
		put(t, join(dir, "package.json"), c.raw)
		got, ok := readLicense(dir)
		if got != c.want || ok != c.ok {
			t.Fatal(c, got, ok)
		}
	}
	// A missing mutable source must not publish a new successful-state record.
	before, err := os.ReadFile(join(p.State, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(join(p.Project, "local")); err != nil {
		t.Fatal(err)
	}
	if err := p.Record(t.Context(), input); err == nil {
		t.Fatal("missing source sealed")
	}
	after, err := os.ReadFile(join(p.State, "state.json"))
	if err != nil || string(after) != string(before) {
		t.Fatal("failed record replaced successful state", err)
	}
	if !strings.HasPrefix(HashFile(join(p.State, "state.json")), "blake3:") {
		t.Fatal("state hash")
	}
}
