package pnpm

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/manifest"
)

func pointer(s string) *string { return &s }
func TestNativePnpmWriterBytes(t *testing.T) {
	for _, name := range []string{"pnpm-native.yaml", "pnpm-native-workspace.yaml"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "..", "..", "..", "vendor/aube/crates/aube-lockfile/tests/fixtures", name)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			g, _, err := Parse(data, Options{})
			if err != nil {
				t.Fatal(err)
			}
			before := g.Clone()
			output := filepath.Join(t.TempDir(), "pnpm-lock.yaml")
			got, _, err := Encode(output, g, nil)
			if err != nil {
				t.Fatal(err)
			}
			// The reference fixture test also undoes autocrlf checkout conversion.
			data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
			if !bytes.Equal(got, data) {
				t.Fatalf("writer differs\n%s\nexpected\n%s", got, data)
			}
			if !reflect.DeepEqual(g, before) {
				t.Fatal("writer changed graph")
			}
			if _, err := Write(output, g, nil); err != nil {
				t.Fatal(err)
			}
			onDisk, err := os.ReadFile(output)
			if err != nil || !bytes.Equal(onDisk, got) {
				t.Fatal("published bytes differ", err)
			}
		})
	}
}
func TestWriterAliasPatchAndPeerProjection(t *testing.T) {
	g := lockfile.NewGraph()
	p := lockfile.NewPackage("alias", "1.2.3")
	p.AliasOf = pointer("real")
	p.Integrity = pointer("sha512-test")
	p.PeerDependenciesMeta["optional-peer"] = lockfile.PeerMeta{Optional: true}
	p.PeerDependenciesMeta["required-peer"] = lockfile.PeerMeta{}
	p.Engines = map[string]string{"node": "*", "other": ">=1"}
	g.Packages[p.DepPath] = p
	g.Importers["."] = []lockfile.DirectDep{{Name: "alias", DepPath: p.DepPath, Specifier: pointer("npm:real@^1")}}
	parent := lockfile.NewPackage("parent", "1.0.0")
	parent.Dependencies["alias"] = "1.2.3"
	parent.OptionalDependencies["alias"] = "1.2.3"
	g.Packages[parent.DepPath] = parent
	g.PatchedDependencies = map[string]string{"real@^1": "patches/real.patch"}
	g.PatchedDependencyHashes = map[string]string{"real@^1": "hash"}
	g.Times = map[string]string{"real@1.2.3": "today", "parent@1.0.0": "yesterday"}
	for _, native := range []bool{true, false} {
		name := "pnpm-lock.yaml"
		canonical := "real@1.2.3"
		version := "real@1.2.3(patch_hash=hash)"
		if !native {
			name = "nub.lock"
			canonical = "alias@1.2.3"
			version = "1.2.3(patch_hash=hash)"
		}
		built, err := Build(filepath.Join(t.TempDir(), name), g, nil)
		if err != nil {
			t.Fatal(err)
		}
		doc := built.Lockfile
		if doc.Get("importers").Get(".").Get("dependencies").Get("alias").Get("version").Text() != version {
			t.Fatal("importer alias/patch spelling")
		}
		info := doc.Get("packages").Get(canonical)
		if info == nil || info.Get("engines").Get("node") != nil || info.Get("peerDependencies").Get("optional-peer").Text() != "*" || info.Get("peerDependenciesMeta").Get("required-peer") != nil {
			t.Fatal("package projection")
		}
		if (info.Get("aliasOf") == nil) != native {
			t.Fatal("native aliasOf leak")
		}
		snap := doc.Get("snapshots").Get("parent@1.0.0")
		if snap.Get("dependencies") != nil || snap.Get("optionalDependencies").Get("alias").Text() != version {
			t.Fatal("optional edge duplicated")
		}
		if len(doc.Get("time").Object) != 1 || doc.Get("time").Get(canonical) == nil {
			t.Fatal("time pruning")
		}
		if built.SnapshotKeys[canonical+"(patch_hash=hash)"] != p.DepPath {
			t.Fatal("hook snapshot correspondence")
		}
	}
}
func TestWriterWorkspaceMemberRecovery(t *testing.T) {
	dir := t.TempDir()
	for path, body := range map[string]string{"packages/core": "{\"name\":\"@ws/core\"}", "packages/app": "{\"name\":\"app\",\"version\":\"1.0.0\"}"} {
		p := filepath.Join(dir, path)
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "package.json"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	g := lockfile.NewGraph()
	g.Importers["."] = []lockfile.DirectDep{{Name: "@ws/core", DepPath: "@ws/core@0.0.0"}}
	g.Importers["packages/core"] = nil
	g.Importers["packages/app"] = []lockfile.DirectDep{{Name: "@ws/core", DepPath: "@ws/core@0.0.0", Specifier: pointer("workspace:^")}}
	p, err := Build(filepath.Join(dir, "pnpm-lock.yaml"), g, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Lockfile.Get("importers").Get(".").Object) != 0 {
		t.Fatal("implicit npm workspace root edge emitted")
	}
	d := p.Lockfile.Get("importers").Get("packages/app").Get("dependencies").Get("@ws/core")
	if d.Get("version").Text() != "link:../core" {
		t.Fatal("versionless member")
	}
	g.Settings.ExcludeLinksFromLockfile = true
	p, err = Build(filepath.Join(dir, "pnpm-lock.yaml"), g, &manifest.Package{Dependencies: map[string]string{"@ws/core": "*"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Lockfile.Get("importers").Get("packages/app").Object) != 0 {
		t.Fatal("excludeLinks ignored")
	}
}
func TestWriterRuntimeAndURLMetadata(t *testing.T) {
	g := lockfile.NewGraph()
	g.Importers["."] = nil
	g.Runtimes = map[string]lockfile.RuntimePin{"node": {Specifier: "^24", Version: "24.4.1", Dev: true, HasBin: true, Variants: []lockfile.RuntimeVariant{{Targets: []lockfile.RuntimeTarget{{OS: "linux", CPU: "x64"}}, Archive: "tarball", URL: "https://node.test/node.tgz", Bin: map[string]string{"node": "bin/node"}, BinIsBareString: true}}}}
	for _, url := range []string{"https://registry.npmjs.org/a/-/a-1.0.0.tgz", "https://registry.example.test/opaque/archive"} {
		p := lockfile.NewPackage("a", "1.0.0")
		p.TarballURL = pointer(url)
		g.Packages[p.DepPath] = p
		built, err := Build("pnpm-lock.yaml", g, nil)
		if err != nil {
			t.Fatal(err)
		}
		res := built.Lockfile.Get("packages").Get(p.DepPath).Get("resolution")
		if strings.Contains(url, "opaque") {
			if res == nil || res.Get("tarball").Text() != url {
				t.Fatal("opaque URL lost")
			}
		} else if res != nil {
			t.Fatal("derivable URL emitted without opt-in")
		}
	}
	encoded, _, err := Encode("pnpm-lock.yaml", g, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "resolution: {type: variations") {
		t.Fatal("runtime layout")
	}
	parsed, _, err := Parse(encoded, Options{AllowMissingIntegrity: true})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parsed.Runtimes, g.Runtimes) {
		t.Fatalf("runtime metadata changed: %+v", parsed.Runtimes)
	}
}
