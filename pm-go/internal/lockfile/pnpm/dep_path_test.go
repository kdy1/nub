package pnpm

import (
	"maps"
	"reflect"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
)

func TestDepPathShapes(t *testing.T) {
	for _, tc := range []struct {
		raw, name, version string
		valid              bool
	}{
		{"lodash@4.17.21", "lodash", "4.17.21", true}, {"/lodash@4.17.21", "lodash", "4.17.21", true},
		{"@babel/core@7.24.0", "@babel/core", "7.24.0", true}, {"/@types/node@20.11.0", "@types/node", "20.11.0", true},
		{"foo@2.0.0(react@18)(react-dom@18(react@18))", "foo", "2.0.0", true}, {"foo@1.0.0-beta.1", "foo", "1.0.0-beta.1", true},
		{"@scope", "", "", false}, {"invalid", "", "", false}, {"name@https://user@host/file.tgz", "name", "https://user@host/file.tgz", true},
	} {
		name, v, ok := SplitDepPath(tc.raw)
		if name != tc.name || v != tc.version || ok != tc.valid {
			t.Fatalf("%s: %s %s %v", tc.raw, name, v, ok)
		}
	}
	if DepPathTail("@s/p@1(peer@2)", "@s/p") != "1(peer@2)" || PeerlessPath("@s/p", "1(peer@2)") != "@s/p@1" {
		t.Fatal("tail mismatch")
	}
	p := lockfile.NewPackage("@s/p", "1")
	if PeerlessAliasTarget(map[string]*lockfile.Package{p.DepPath: p}, "@s/p@1(peer@2)") != p {
		t.Fatal("missing peerless alias")
	}
}

func TestPeerSuffixRewriting(t *testing.T) {
	translate := func(head string) (string, bool) {
		if head == "request@url+123" {
			return "request@https://host/archive.tgz", true
		}
		return "", false
	}
	for _, tc := range []struct {
		raw, want string
		warnings  int
	}{
		{"a@1", "a@1", 0}, {"request@url+123", "request@url+123", 0},
		{"a@1(request@url+123)", "a@1(request@https://host/archive.tgz)", 0},
		{"1(request@url+123)", "1(request@https://host/archive.tgz)", 0},
		{"a@1(react@18)(b@1(request@url+123))", "a@1(react@18)(b@1(request@https://host/archive.tgz))", 0},
		{"a@1(request@url+123(react@18))", "a@1(request@url+123(react@18))", 0},
		{"a@1(request@bad", "a@1(request@bad", 1}, {"a@1(b@1(request@bad)", "a@1(b@1(request@bad)", 1},
		// The reference keeps completed outer segments and discards trailing
		// non-segment text when at least one balanced segment was recovered.
		{"a@1(react@18)trailing(broken", "a@1(react@18)", 0},
	} {
		var warnings []string
		got := RewritePeerSuffix(tc.raw, translate, func(s string) { warnings = append(warnings, s) })
		if got != tc.want || len(warnings) != tc.warnings {
			t.Fatalf("%s: %s %v", tc.raw, got, warnings)
		}
	}
}

func TestPatchMarkersAndAliases(t *testing.T) {
	for input, want := range map[string]string{
		"1(patch_hash=abc)(react@18)": "1(react@18)", "1(react@18(patch_hash=a))(patch_hash=b)": "1(react@18)", "1(patch_hash=unterminated": "1(patch_hash=unterminated",
	} {
		if got := StripPatchHash(input); got != want {
			t.Fatalf("%s: %s", input, got)
		}
	}
	deps := map[string]string{"alias": "@scope/real@1.2.3(peer@4)", "real": "real@2", "plain": "3.0.0", "z": "other@2"}
	remaps := RewriteAliases(deps)
	if !maps.Equal(deps, map[string]string{"alias": "1.2.3(peer@4)", "real": "real@2", "plain": "3.0.0", "z": "2"}) {
		t.Fatal(deps)
	}
	want := []AliasRemap{{"alias@1.2.3(peer@4)", "@scope/real@1.2.3(peer@4)", "alias", "@scope/real"}, {"z@2", "other@2", "z", "other"}}
	if !reflect.DeepEqual(remaps, want) {
		t.Fatal(remaps)
	}
}

func TestRegistryQualifiersAndHostedArchives(t *testing.T) {
	for raw, want := range map[string]string{"work:1.0.0": "work", "gh:2.1.0-beta.1": "gh", "work-reg.2:1.2.3": "work-reg.2", "file:1.0.0": "", "runtime:24.0.0": "", "work:^1.0.0": "", "9work:1.0.0": "", "1.0.0": "", "workspace:1.0.0": "", "npm:1.0.0": ""} {
		got, ok := RegistryAlias(raw)
		if got != want || ok != (want != "") {
			t.Fatalf("%s: %s %v", raw, got, ok)
		}
	}
	for raw, want := range map[string]bool{
		"https://codeload.github.com/o/r/tar.gz/main": true, "http://user@NPM.PKG.GITHUB.COM:80/a": true,
		"https://gitlab.com/o/r/-/archive/main/a.tgz": true, "https://bitbucket.org/o/r/get/a.tar.gz": true,
		"https://gitlab.com/o/r?path=/-/archive/main/a.tgz": false, "https://bitbucket.org/o/r#/get/a.tar.gz": false,
		"git+https://codeload.github.com/o/r/tar.gz/main": false, "https://evil.test/codeload.github.com": false,
	} {
		if got := HostedGitTarball(raw); got != want {
			t.Fatalf("%s: %v", raw, got)
		}
	}
}
