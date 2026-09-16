package resolver

import (
	"strings"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
)

func TestLockedReuseOrderingAndVulnerableHint(t *testing.T) {
	g := lockfile.NewGraph()
	for _, version := range []string{"2.0.0", "10.0.0", "1.0.0"} {
		p := lockfile.NewPackage("alias", version)
		p.AliasOf = new("real")
		g.Packages[p.DepPath] = p
	}
	bundle := lockfile.NewPackage("alias", "1.0.0")
	bundle.InBundle = true
	g.Packages["a-parent@1/node_modules/alias"] = bundle
	i := NewLockedIndex(g)
	vulnerable := map[string][]string{"real": {"1.x"}}
	if got := i.FindSatisfying("alias", "*", "real", vulnerable); got == nil || got.Version != "10.0.0" {
		t.Fatal(got)
	}
	if got := i.FindFirstInRange("alias", "*"); got != bundle {
		t.Fatal("hint skipped first bundled match", got)
	}
	if got := i.FindSatisfying("alias", "^1", "real", vulnerable); got != nil {
		t.Fatal(got)
	}
	if got := i.FindSatisfying("alias", "^1", "other", vulnerable); got != g.Packages["alias@1.0.0"] {
		t.Fatal(got)
	}
	if NewLockedIndex(nil).FindFirstInRange("alias", "*") != nil {
		t.Fatal("empty index")
	}
}

func TestLockedGitIntegrityMatchesCommitAndSubpath(t *testing.T) {
	g := lockfile.NewGraph()
	p := lockfile.NewPackage("pkg", "0.0.0")
	p.Integrity = new("sha512-locked")
	p.Source = &lockfile.Source{Kind: lockfile.Git, URL: "ssh://git@example.test/pkg", Resolved: "abcdef0", Subpath: new("packages/a")}
	g.Packages[p.DepPath] = p
	i := NewLockedIndex(g)
	source := &lockfile.Source{Kind: lockfile.Git, URL: "https://example.test/pkg", Resolved: "abcdef0" + strings.Repeat("1", 33), Committish: new("main"), Subpath: new("packages/a")}
	if got := i.FindLocalIntegrity("pkg", "1.2.3", source); got == nil || *got != *p.Integrity {
		t.Fatal(got)
	}
	source.Subpath = new("packages/b")
	if i.FindLocalIntegrity("pkg", "1.2.3", source) != nil {
		t.Fatal("different subpath reused")
	}
	source.Subpath = p.Source.Subpath
	source.Resolved = strings.Repeat("1", 40)
	if i.FindLocalIntegrity("pkg", "1.2.3", source) != nil {
		t.Fatal("different commit reused")
	}
}

func TestLocalIntegrityRetainsSourceEquality(t *testing.T) {
	g := lockfile.NewGraph()
	p := lockfile.NewPackage("pkg", "1.0.0")
	p.Integrity = new("sha512-old")
	p.Source = &lockfile.Source{Kind: lockfile.Directory, Path: "vendor//./pkg/"}
	g.Packages[p.DepPath] = p
	i := NewLockedIndex(g)
	if i.FindLocalIntegrity("pkg", "1.0.0", &lockfile.Source{Kind: lockfile.Directory, Path: "vendor/pkg"}) == nil {
		t.Fatal("component equality")
	}
	for _, path := range []string{"./vendor/pkg", "vendor/other/../pkg"} {
		if i.FindLocalIntegrity("pkg", "1.0.0", &lockfile.Source{Kind: lockfile.Directory, Path: path}) != nil {
			t.Fatal(path)
		}
	}
	if i.FindLocalIntegrity("pkg", "2.0.0", p.Source) != nil {
		t.Fatal("version mismatch")
	}
	if i.FindLocalIntegrity("pkg", "1.0.0", &lockfile.Source{Kind: lockfile.Link, Path: "vendor/pkg"}) != nil {
		t.Fatal("kind mismatch")
	}
	p.Source = &lockfile.Source{Kind: lockfile.RemoteTarball, URL: "https://example.test/pkg.tgz", Integrity: new("source-integrity")}
	same := *p.Source
	if i.FindLocalIntegrity("pkg", "1.0.0", &same) == nil {
		t.Fatal("same tarball")
	}
	same.Integrity = new("changed")
	if i.FindLocalIntegrity("pkg", "1.0.0", &same) != nil {
		t.Fatal("different source integrity")
	}
}
