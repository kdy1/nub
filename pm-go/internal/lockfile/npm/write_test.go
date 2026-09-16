package npm

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/manifest"
)

func encode(t *testing.T, g *lockfile.Graph, pkg, dir string, existing []byte) (*jsonvalue.Value, []byte) {
	t.Helper()
	m, err := manifest.ParsePackage([]byte(pkg))
	if err != nil {
		t.Fatal(err)
	}
	before := g.Clone()
	body, err := Encode(g, m, dir, existing)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, before) {
		t.Fatal("writer mutated graph")
	}
	v, err := jsonvalue.Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	return v, body
}
func stringPtr(s string) *string { return &s }
func fieldKeys(v *jsonvalue.Value) []string {
	var keys []string
	for _, item := range v.Object {
		keys = append(keys, item.Key)
	}
	return keys
}

func TestNativeNPMByteRoundTrips(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "vendor", "aube", "crates", "aube-lockfile", "tests", "fixtures")
	if _, err := os.Stat(root); os.IsNotExist(err) {
		t.Skip("standalone module checkout: repository corpus unavailable")
	}
	for _, name := range []string{"npm-native.json", "npm-native-peer.json", "npm-native-root-peer.json"} {
		t.Run(name, func(t *testing.T) {
			original, err := os.ReadFile(filepath.Join(root, name))
			if err != nil {
				t.Fatal(err)
			}
			original = bytes.ReplaceAll(original, []byte("\r\n"), []byte("\n"))
			v, err := jsonvalue.Parse(original)
			if err != nil {
				t.Fatal(err)
			}
			m, err := manifest.PackageFromValue(v.Get("packages").Get(""))
			if err != nil {
				t.Fatal(err)
			}
			g, _, err := Parse(original, m)
			if err != nil {
				t.Fatal(err)
			}
			written, err := Encode(g, m, t.TempDir(), original)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(written, original) {
				t.Fatalf("native npm byte drift\nwant:\n%s\ngot:\n%s", original, written)
			}
			path := filepath.Join(t.TempDir(), "nested", "package-lock.json")
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, original, 0644); err != nil {
				t.Fatal(err)
			}
			if err := Write(path, g, m); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, original) {
				t.Fatal("atomic write changed output")
			}
		})
	}
}

func TestNPMRootSerialization(t *testing.T) {
	for _, tc := range []struct {
		workspaces string
		keys       []string
	}{
		{`["packages/*"]`, []string{"name", "version", "license", "workspaces", "dependencies", "bin", "engines"}},
		{`{"packages":["packages/*"],"nohoist":[],"catalog":{},"catalogs":{}}`, []string{"name", "version", "license", "dependencies", "bin", "engines", "workspaces"}},
	} {
		v, body := encode(t, lockfile.NewGraph(), `{"name":"@s/project","version":"1.2.3","license":{"type":"MIT"},"bin":"././bin.js","engines":{"node":">=22"},"dependencies":{"a":"^1"},"workspaces":`+tc.workspaces+`}`, t.TempDir(), nil)
		entry := v.Get("packages").Get("")
		if !slices.Equal(fieldKeys(entry), tc.keys) || entry.Get("bin").Get("project").Text() != "bin.js" {
			t.Fatalf("%s", body)
		}
		w, _ := jsonvalue.Parse([]byte(tc.workspaces))
		got, _ := entry.Get("workspaces").MarshalJSON()
		want, _ := w.MarshalJSON()
		if !bytes.Equal(got, want) {
			t.Fatalf("workspace field: %s != %s", got, want)
		}
	}
	v, _ := encode(t, lockfile.NewGraph(), `{"bin":{"a":"./a.js","b":"b.js","c":"./nested/c.js","d":"././d.js","invalid":false}}`, t.TempDir(), nil)
	bin := v.Get("packages").Get("").Get("bin")
	if bin.Get("a").Text() != "a.js" || bin.Get("b").Text() != "b.js" || bin.Get("c").Text() != "nested/c.js" || bin.Get("d").Text() != "d.js" || bin.Get("invalid") != nil {
		t.Fatal(bin)
	}
}

func TestNPMFlagsAndDeclaredRanges(t *testing.T) {
	graph, _ := read(t, `{"lockfileVersion":3,"packages":{
		"":{"dependencies":{"a":"^1"},"devDependencies":{"dev":"*"},"optionalDependencies":{"opt":"*"}},
		"node_modules/a":{"version":"1","peerDependencies":{"peer":"*"},"dependencies":{"b":"^2","absent":"*"},"optionalDependencies":{"opt":"*"}},
		"node_modules/b":{"version":"2","bin":{"":"skip","b":"b.js"},"hasInstallScript":true,"peerDependenciesMeta":{"extra":{}}},
		"node_modules/dev":{"version":"1","dependencies":{"shared":"*"},"optionalDependencies":{"both":"*"}},
		"node_modules/opt":{"version":"1","dependencies":{"shared":"*"}},
		"node_modules/shared":{"version":"1"},"node_modules/both":{"version":"1"},"node_modules/peer":{"version":"1","dependencies":{"peerchild":"*"}},"node_modules/peerchild":{"version":"1"}
	}}`, "")
	v, body := encode(t, graph, `{"dependencies":{"a":"^1"},"devDependencies":{"dev":"*"},"optionalDependencies":{"opt":"*"}}`, t.TempDir(), nil)
	packages := v.Get("packages")
	for _, tc := range []struct{ name, flag string }{{"a", ""}, {"b", ""}, {"dev", "dev"}, {"opt", "optional"}, {"shared", "devOptional"}, {"both", "devOptional"}, {"peer", "peer"}, {"peerchild", "peer"}} {
		p := packages.Get("node_modules/" + tc.name)
		if p == nil {
			t.Fatalf("missing %s: %s", tc.name, body)
		}
		for _, flag := range []string{"dev", "optional", "devOptional", "peer"} {
			if (p.Get(flag) != nil) != (flag == tc.flag) {
				t.Fatalf("%s %s: %s", tc.name, flag, body)
			}
		}
	}
	a := packages.Get("node_modules/a")
	if a.Get("dependencies").Get("b").Text() != "^2" || a.Get("dependencies").Get("peer") != nil || a.Get("dependencies").Get("absent") != nil {
		t.Fatal(a)
	}
	b := packages.Get("node_modules/b")
	if b.Get("bin").Get("") != nil || b.Get("peerDependenciesMeta").Get("extra") == nil || len(b.Get("peerDependenciesMeta").Get("extra").Object) != 0 {
		t.Fatal(b)
	}
}

func TestNPMFreshWorkspaceAndLocalConflicts(t *testing.T) {
	dir := t.TempDir()
	writeMember := func(path, body string) {
		t.Helper()
		path = filepath.Join(dir, filepath.FromSlash(path), "package.json")
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	writeMember("packages/a", `{"name":"a","version":"1","peerDependencies":{"peer":"^1"}}`)
	writeMember("packages/b", `{"name":"@s/b","version":"2"}`)
	g := lockfile.NewGraph()
	for _, p := range []*lockfile.Package{lockfile.NewPackage("outer", "1"), lockfile.NewPackage("shared", "2"), lockfile.NewPackage("peer", "1")} {
		g.Packages[p.DepPath] = p
	}
	g.Packages["outer@1"].Dependencies["shared"] = "2"
	g.Importers["."] = []lockfile.DirectDep{{Name: "outer", DepPath: "outer@1"}}
	for _, tc := range []struct{ member, path string }{{"packages/a", "vendor/one"}, {"packages/b", "vendor/two"}} {
		p := lockfile.NewPackage("shared", "1")
		p.Source = &lockfile.Source{Kind: lockfile.Directory, Path: tc.path}
		p.DepPath = p.Source.DepPath(p.Name)
		g.Packages[p.DepPath] = p
		g.Importers[tc.member] = []lockfile.DirectDep{{Name: "shared", DepPath: p.DepPath, Specifier: stringPtr("file:../../" + tc.path)}}
	}
	g.Importers["packages/a"] = append(g.Importers["packages/a"], lockfile.DirectDep{Name: "peer", DepPath: "peer@1", Specifier: stringPtr("^1")})
	v, body := encode(t, g, `{"name":"root","workspaces":["packages/*"],"dependencies":{"outer":"1"}}`, dir, nil)
	packages := v.Get("packages")
	for _, path := range []string{"packages/a/node_modules/shared", "packages/b/node_modules/shared", "node_modules/shared", "node_modules/a", "node_modules/@s/b", "vendor/one", "vendor/two"} {
		if packages.Get(path) == nil {
			t.Fatalf("missing %s: %s", path, body)
		}
	}
	if packages.Get("node_modules/shared").Get("version").Text() != "2" || packages.Get("packages/a/node_modules/shared").Get("resolved").Text() != "vendor/one" {
		t.Fatal(string(body))
	}
	if packages.Get("packages/a").Get("name") != nil || packages.Get("packages/b").Get("name").Text() != "@s/b" {
		t.Fatal(string(body))
	}
	if packages.Get("packages/a").Get("dependencies").Get("peer") != nil || packages.Get("packages/a").Get("peerDependencies").Get("peer").Text() != "^1" {
		t.Fatal(string(body))
	}
	if packages.Get("packages/a/node_modules/peer").Get("peer") == nil {
		t.Fatal("synthesized workspace peer lost flag")
	}
}

func TestNPMLocalLinkHoistAndRootReservation(t *testing.T) {
	for _, kind := range []lockfile.SourceKind{lockfile.Directory, lockfile.Tarball, lockfile.Link} {
		g := lockfile.NewGraph()
		p := lockfile.NewPackage("local", "1")
		p.Source = &lockfile.Source{Kind: kind, Path: "./vendor/local"}
		p.DepPath = p.Source.DepPath(p.Name)
		g.Packages[p.DepPath] = p
		g.Importers["."] = []lockfile.DirectDep{{Name: p.Name, DepPath: p.DepPath, Specifier: stringPtr("file:./vendor/local")}}
		v, body := encode(t, g, `{"dependencies":{"local":"file:./vendor/local"}}`, t.TempDir(), nil)
		if v.Get("packages").Get("node_modules/local").Get("resolved").Text() != "vendor/local" || v.Get("packages").Get("vendor/local").Get("version").Text() != "1" {
			t.Fatal(string(body))
		}
	}
	// With no root registry reservation, the first member owns the root
	// alias and the conflicting member gets its own nested link.
	g, _ := read(t, `{"lockfileVersion":3,"packages":{
		"":{"workspaces":["packages/*"]},"node_modules/a":{"link":true,"resolved":"packages/a"},"node_modules/b":{"link":true,"resolved":"packages/b"},
		"packages/a":{"name":"a","version":"1","dependencies":{"shared":"file:../../vendor/one"}},"packages/b":{"name":"b","version":"1","dependencies":{"shared":"file:../../vendor/two"}},
		"node_modules/shared":{"link":true,"resolved":"vendor/one"},"packages/b/node_modules/shared":{"link":true,"resolved":"vendor/two"},"vendor/one":{"name":"shared","version":"1"},"vendor/two":{"name":"shared","version":"2"}
	}}`, "")
	v, body := encode(t, g, `{"workspaces":["packages/*"]}`, t.TempDir(), nil)
	if v.Get("packages").Get("node_modules/shared").Get("resolved").Text() != "vendor/one" || v.Get("packages").Get("packages/b/node_modules/shared").Get("resolved").Text() != "vendor/two" {
		t.Fatal(string(body))
	}
}

func TestNPMPreferredWorkspacePlacement(t *testing.T) {
	lock := `{"lockfileVersion":3,"packages":{
		"":{"workspaces":["packages/*"]},"node_modules/app":{"link":true,"resolved":"packages/app"},
		"packages/app":{"name":"app","version":"1","dependencies":{"dep":"^1"}},"node_modules/dep":{"version":"1","dependencies":{"child":"^1"}},"node_modules/child":{"version":"1"}
	}}`
	g, _ := read(t, lock, "")
	v, body := encode(t, g, `{"workspaces":["packages/*"]}`, t.TempDir(), []byte(lock))
	packages := v.Get("packages")
	if packages.Get("node_modules/dep") == nil || packages.Get("node_modules/child") == nil || packages.Get("packages/app/node_modules/dep") != nil {
		t.Fatal(string(body))
	}
	fresh, _ := encode(t, g, `{"workspaces":["packages/*"]}`, t.TempDir(), nil)
	if fresh.Get("packages").Get("packages/app/node_modules/dep") == nil {
		t.Fatal("fresh member dependency not nested")
	}
}

func TestNPMResolvedSources(t *testing.T) {
	sha := strings.Repeat("A", 40)
	for _, host := range []string{"github.com", "gitlab.com", "bitbucket.org"} {
		h := lockfile.HostedGit{Host: host, Owner: "owner", Repo: "repo"}
		tarball, _ := h.TarballURL(sha)
		p := lockfile.NewPackage("a", "1")
		p.Source = &lockfile.Source{Kind: lockfile.RemoteTarball, URL: tarball, GitHosted: true}
		p.TarballURL = stringPtr(tarball)
		if got := value(resolvedField(p), ""); got != "git+"+h.SSHURL()+"#"+strings.ToLower(sha) {
			t.Fatal(got)
		}
		p.Source.GitHosted = false
		if got := value(resolvedField(p), ""); got != tarball {
			t.Fatal(got)
		}
		p.TarballURL = nil
		p.Source = &lockfile.Source{Kind: lockfile.Git, URL: h.HTTPSURL(), Resolved: "abcdef0", Subpath: stringPtr("packages/a")}
		if got := value(resolvedField(p), ""); got != "git+"+h.SSHURL()+"#abcdef0&path:/packages/a" {
			t.Fatal(got)
		}
	}
	for _, raw := range []string{"https://codeload.github.com/o/r/tar.gz/main", "https://codeload.github.com/o/r/tar.gz/" + sha + "?token=x", "http://codeload.github.com/o/r/tar.gz/" + sha} {
		if _, _, ok := hostedArchive(raw); ok {
			t.Fatal("accepted noncanonical archive", raw)
		}
	}
}
