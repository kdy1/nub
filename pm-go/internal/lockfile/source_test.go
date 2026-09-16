package lockfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func text(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func TestGitSpecifierGrammar(t *testing.T) {
	sha := strings.Repeat("a", 40)
	for _, test := range []struct{ input, url, ref, sub string }{
		{"git+https://example.com/team/pkg#main", "https://example.com/team/pkg", "main", ""},
		{"git+ssh://git@example.com/team/pkg.git#commit=" + sha + "&path:/packages/a", "ssh://git@example.com/team/pkg.git", sha, "packages/a"},
		{"git://example.com/repo", "git://example.com/repo", "", ""},
		{"https://example.com/repo.git#tag=v1", "https://example.com/repo.git", "v1", ""},
		{"https://example.com/repo#" + sha, "https://example.com/repo", sha, ""},
		{"git@github.com:org/repo.git#main", "ssh://git@github.com/org/repo.git", "main", ""},
		{"github:org/repo#branch:next", "https://github.com/org/repo.git", "next", ""},
		{"gitlab:org/subgroup/repo", "https://gitlab.com/org/subgroup/repo.git", "", ""},
		{"bitbucket:org/repo", "https://bitbucket.org/org/repo.git", "", ""},
		{"owner/repo#semver:^1.0.0", "https://github.com/owner/repo.git", "semver:^1.0.0", ""},
		{"file:///tmp/repo.git#abc", "file:///tmp/repo.git", "abc", ""},
	} {
		source, ok := ParseGit(test.input)
		if !ok || source.URL != test.url || text(source.Committish) != test.ref || text(source.Subpath) != test.sub {
			t.Errorf("%s: %#v", test.input, source)
		}
	}
	for _, input := range []string{"@scope/pkg", "./repo", "../repo", "a/b/c", "file:./a", "git@example.com:a/b.git", "github.com:a/b", "git@github.com:/a/b", "https://example.com/pkg.tgz", "https://example.com/pkg#branch", "owner/", "/repo"} {
		if _, ok := ParseGit(input); ok {
			t.Fatal("not git", input)
		}
	}
}

func TestGitFragments(t *testing.T) {
	for _, test := range []struct{ raw, ref, path string }{
		{"", "", ""}, {"tag=v1&commit=abc&commit=def&path:/sub/dir&path:/other", "abc", "sub/dir"},
		{"branch=main&tag=v2", "main", ""}, {"release:2026-01", "release:2026-01", ""}, {"semver:^1", "semver:^1", ""},
		{"unknown=x", "", ""}, {"path:/../../etc&path:/valid/path", "", "valid/path"},
		{"path:/a//b&path:/.&path:/a/../b", "", ""}, {"path:/&head:main", "main", ""},
	} {
		ref, path := ParseGitFragment(test.raw)
		if text(ref) != test.ref || text(path) != test.path {
			t.Fatal(test, text(ref), text(path))
		}
	}
}

func TestLocalClassificationAndStableKeys(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "PKG.TGZ"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		raw  string
		kind SourceKind
	}{{"file:PKG.TGZ", Tarball}, {"file:missing.tgz", Directory}, {"file:vendor", Directory}, {"link:../shared", Link}, {"portal:../shared", Portal}, {"exec:./generator.js", Exec}, {"https://pkg.pr.new/pkg", RemoteTarball}} {
		source := ParseSource(test.raw, root)
		if source == nil || source.Kind != test.kind {
			t.Fatal(test, source)
		}
	}
	if ParseSource("^1.0.0", root) != nil {
		t.Fatal("range classified as source")
	}
	a := Source{Kind: Directory, Path: "./vendor/../vendor/pkg"}
	b := Source{Kind: Directory, Path: "vendor/pkg"}
	if a.DepPath("pkg") != b.DepPath("pkg") {
		t.Fatal("equivalent paths split")
	}
	for _, other := range []Source{{Kind: Directory, Path: "../vendor/pkg"}, {Kind: Directory, Path: "__/vendor/pkg"}, {Kind: Link, Path: "vendor/pkg"}, {Kind: Directory, Path: "vendor.pkg"}} {
		if other.DepPath("pkg") == a.DepPath("pkg") {
			t.Fatal("distinct sources collide", other)
		}
	}
	if a.Specifier() != "file:./vendor/../vendor/pkg" || a.GloballyShareable() {
		t.Fatal(a)
	}
	git, ok := ParseGit("github:org/pkg#main&path:/packages/a")
	if !ok {
		t.Fatal("git parse")
	}
	git.Resolved = strings.Repeat("a", 40)
	if !git.GloballyShareable() || git.Specifier() != "https://github.com/org/pkg.git#"+git.Resolved+"&path:/packages/a" {
		t.Fatal(git)
	}
	without := git
	without.Subpath = nil
	if without.DepPath("pkg") == git.DepPath("pkg") {
		t.Fatal("subpath ignored")
	}
}

func TestSourceEdges(t *testing.T) {
	sha := strings.Repeat("b", 40)
	git, _ := ParseGit("git+https://example.com/pkg.git#" + sha)
	git.Resolved = sha
	remote := Source{Kind: RemoteTarball, URL: "https://example.com/package.tgz"}
	keys := map[string]bool{"foo@1.0.0(peer@2.0.0)": true, git.DepPath("git-pkg"): true, remote.DepPath("url-pkg"): true}
	for _, test := range []struct{ name, tail, key string }{
		{"foo", "1.0.0(peer@2.0.0)", "foo@1.0.0(peer@2.0.0)"}, {"foo", "foo@1.0.0(peer@2.0.0)", "foo@1.0.0(peer@2.0.0)"},
		{"git-pkg", "https://example.com/pkg.git#" + sha + "(peer@2.0.0)", git.DepPath("git-pkg")},
		{"url-pkg", remote.URL, remote.DepPath("url-pkg")},
	} {
		key, ok := ResolveEdge(test.name, test.tail, func(k string) bool { return keys[k] })
		if !ok || key != test.key {
			t.Fatal(test, key, ok)
		}
	}
	if _, ok := ResolveEdge("missing", "1.0.0", func(k string) bool { return keys[k] }); ok {
		t.Fatal("missing edge resolved")
	}
	if _, ok := SharedLocalDepPath("git-pkg", "https://example.com/pkg.git"); ok {
		t.Fatal("unpinned Git edge")
	}
	if key, _ := ResolveEdge("x", "1", func(string) bool { return true }); key != "1" {
		t.Fatal("full key must win", key)
	}
}

func TestHostedGitRoundTrips(t *testing.T) {
	sha := strings.Repeat("ABCDEF12", 5)
	for _, host := range []string{"github.com", "gitlab.com", "bitbucket.org"} {
		hosted := HostedGit{host, "owner", "repo"}
		for _, raw := range []string{hosted.HTTPSURL(), hosted.SSHURL(), "git+" + hosted.SSHURL(), "git@" + host + ":owner/repo.git"} {
			got, ok := ParseHostedGit(raw)
			if !ok || got != hosted {
				t.Fatal(raw, got, ok)
			}
		}
		archive, ok := hosted.TarballURL(sha)
		if !ok {
			t.Fatal(hosted)
		}
		got, commit, ok := ParseHostedTarball(archive + "?download=1")
		if !ok || got != hosted || commit != strings.ToLower(sha) {
			t.Fatal(archive, got, commit, ok)
		}
		source, ok := (Source{Kind: RemoteTarball, URL: archive}).HostedGitSource()
		if !ok || source.URL != hosted.HTTPSURL() || source.Resolved != strings.ToLower(sha) {
			t.Fatal(source, ok)
		}
		if _, ok := hosted.TarballURL("main"); ok {
			t.Fatal("unverified branch archive")
		}
	}
	for _, raw := range []string{"https://self-hosted.example/owner/repo.git", "https://gitlab.com/group/sub/repo.git", "https://github.com/owner/repo/", "https://github.com/owner/.git"} {
		if _, ok := ParseHostedGit(raw); ok {
			t.Fatal(raw)
		}
	}
	if _, _, ok := ParseHostedTarball("https://codeload.github.com/owner/repo/tar.gz/main"); ok {
		t.Fatal("unpinned archive")
	}
}

func TestGitCommitComparison(t *testing.T) {
	full := strings.Repeat("abcdef12", 5)
	for _, pair := range [][2]string{{full, strings.ToUpper(full)}, {full, "abcdef1"}, {" abcdef12 ", full}, {"main", "MAIN"}} {
		if !GitCommitsMatch(pair[0], pair[1]) {
			t.Fatal(pair)
		}
	}
	for _, pair := range [][2]string{{full, "abcdef"}, {"abcdef1", "abcdef12"}, {full, "1234567"}, {full, full + "a"}} {
		if GitCommitsMatch(pair[0], pair[1]) {
			t.Fatal(pair)
		}
	}
}
