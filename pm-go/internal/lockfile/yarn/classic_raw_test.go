package yarn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
)

func TestClassicTokenizer(t *testing.T) {
	data := "# yarn lockfile v1\r\n\"@s/a@^1\", '@s/a@~1'::\n  version \"1.2.3\"\n  integrity sha512-abc\n  dependencies:\n    \"@s/b\" \"^2\"\n      z latest\n  optionalDependencies:\n    ignored nope\n  peerDependencies:\n    ignored nope\n  unknown:\n    ignored nope\n  version '1.2.4'\n\nfoo@1:\n  version 1\n"
	blocks, err := tokenizeClassic(data)
	if err != nil || len(blocks) != 2 {
		t.Fatal(blocks, err)
	}
	b := blocks[0]
	if len(b.specs) != 2 || b.specs[1] != "@s/a@~1" || b.fields["version"] != "1.2.4" || b.dependencies["@s/b"] != "^2" || b.dependencies["z"] != "latest" || len(b.dependencies) != 2 {
		t.Fatal(b)
	}
	for _, input := range []string{"bad", "  version 1", "foo@1, :\n  version 1", "foo@1:\n  bad", "foo@1:\n  dependencies:\n    bad"} {
		if _, err := tokenizeClassic(input); err == nil {
			t.Fatal(input)
		}
	}
	b1, err := tokenizeClassic("x@1:\n\tignored\n  path \"a\\nb\"\n")
	if err != nil || b1[0].fields["path"] != `a\nb` {
		t.Fatal(b1, err)
	}
}
func TestBerryDetectionBoundaries(t *testing.T) {
	for _, s := range []string{"__metadata:\n", "# comment\n  __metadata: value", "\t__metadata:"} {
		if !IsBerry(s) {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"# __metadata:", "x__metadata:\n", "a: '__metadata:'"} {
		if IsBerry(s) {
			t.Fatal(s)
		}
	}
	path := filepath.Join(t.TempDir(), "yarn.lock")
	for _, tc := range []struct {
		input string
		want  bool
	}{{"# header\n__metadata:\n", true}, {"  __metadata:", false}, {strings.Repeat("x", 4090) + "\n__metadata:", false}, {strings.Repeat("x", 4080) + "\n__metadata:", true}} {
		if err := os.WriteFile(path, []byte(tc.input), 0600); err != nil {
			t.Fatal(err)
		}
		if IsBerryPath(path) != tc.want {
			t.Fatal("prefix detection", tc.want)
		}
	}
}
func TestClassicAliasesAndSources(t *testing.T) {
	for spec, want := range map[string]string{"a@npm:b@1": "b", "@s/a@npm:@r/b@^1": "@r/b"} {
		if got, ok := npmAliasName(spec); !ok || got != want {
			t.Fatal(spec, got)
		}
	}
	for _, spec := range []string{"a@npm:1", "a@1", "@s/a"} {
		if _, ok := npmAliasName(spec); ok {
			t.Fatal(spec)
		}
	}
	for spec, kind := range map[string]lockfile.SourceKind{"a@link:../x#frag": lockfile.Link, "a@portal:../x": lockfile.Portal, "a@file:a.gz": lockfile.Tarball, "a@file:a.TGZ#hash": lockfile.Tarball, "a@file:dir": lockfile.Directory} {
		s := classifyClassic(spec, "a", nil)
		if s.local == nil || s.local.Kind != kind || s.isUnsupported {
			t.Fatal(spec, s)
		}
	}
	for _, spec := range []string{"a@1", "a@next", "a@npm:@s/b@1", "x@file:a.tgz"} {
		s := classifyClassic(spec, "a", nil)
		if s.local != nil || s.isUnsupported {
			t.Fatal(spec, s)
		}
	}
	for spec, protocol := range map[string]string{"a@user/repo#ref": "git", "a@user/repo#semver:^1": "git", "a@github:user/repo": "git", "a@https://example/a.tgz": "https", "a@jsr:@s/a": "jsr", "a@:empty": ""} {
		s := classifyClassic(spec, "a", nil)
		if !s.isUnsupported || s.unsupported != protocol {
			t.Fatal(spec, s)
		}
	}
	resolved := "git+https://example/repo.git#tag=one&path=src"
	s := classifyClassic("a@user/repo#ref", "a", &resolved)
	if s.local == nil || s.local.Resolved != "one" || *s.local.Subpath != "src" {
		t.Fatal(s)
	}
	resolved = "https://codeload.github.com/o/r/tar.gz/" + strings.Repeat("a", 40)
	s = classifyClassic("a@github:o/r#branch", "a", &resolved)
	if s.local == nil || s.local.Kind != lockfile.RemoteTarball || !s.local.GitHosted {
		t.Fatal(s)
	}
	for _, url := range []string{"https://registry.yarnpkg.com/a/-/a.tgz#sha", "https://user@REGISTRY.NPMJS.ORG:443/a.tgz", "https://example/repo.git#sha"} {
		if resolvedTarball(url) != nil {
			t.Fatal(url)
		}
	}
	if got := resolvedTarball("https://private/a.tgz#sha"); got == nil || *got != "https://private/a.tgz" {
		t.Fatal(got)
	}
}
