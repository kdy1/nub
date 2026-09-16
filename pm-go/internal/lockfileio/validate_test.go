package lockfileio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nubjs/nub/pm-go/internal/identity"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/manifest"
)

func TestGraphAliasBoundary(t *testing.T) {
	invalid := []string{"", ".", "..", ".bin", ".pnpm", "node_modules", "../escape", "@scope/../escape", "@scope/pkg/extra", "@/pkg", "@scope/", "@scope/..", "C:pkg", "@scope/D:pkg", "\\evil", "foo\x00bar", "/etc/passwd"}
	valid := []string{"ok", "@scope/pkg", "@scope/.bin", "@scope/node_modules", "foo:bar", "@unscoped", "space name", "é:pkg"}
	for _, name := range append(invalid, valid...) {
		want := true
		for _, s := range invalid {
			if s == name {
				want = false
			}
		}
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			for _, field := range []string{"importer", "name", "dependencies", "optionalDependencies", "peerDependencies", "peerDependenciesMeta", "declaredDependencies"} {
				g := lockfile.NewGraph()
				p := lockfile.NewPackage("parent", "1.0.0")
				g.Packages[p.DepPath] = p
				switch field {
				case "importer":
					g.Importers["."] = []lockfile.DirectDep{{Name: name, DepPath: p.DepPath}}
				case "name":
					p.Name = name
				case "dependencies":
					p.Dependencies[name] = "1.0.0"
				case "optionalDependencies":
					p.OptionalDependencies[name] = "1.0.0"
				case "peerDependencies":
					p.PeerDependencies[name] = "*"
				case "peerDependenciesMeta":
					p.PeerDependenciesMeta[name] = lockfile.PeerMeta{Optional: true}
				case "declaredDependencies":
					p.DeclaredDependencies = map[string]string{name: "*"}
				}
				err := validateGraph("input.lock", g)
				if (err == nil) != want {
					t.Fatalf("%s: %v", field, err)
				}
				if err != nil {
					var validation *ValidationError
					if !errors.As(err, &validation) || validation.Code != "ERR_AUBE_LOCKFILE_PARSE" {
						t.Fatal(err)
					}
				}
			}
		})
	}
}

func TestGraphSourceShapeBoundary(t *testing.T) {
	for _, kind := range []lockfile.SourceKind{lockfile.Directory, lockfile.Tarball, lockfile.Link, lockfile.Portal, lockfile.Exec, lockfile.Git, lockfile.RemoteTarball} {
		for _, name := range []string{"pkg", "@scope/pkg"} {
			for _, prefix := range []string{"", "/"} {
				for _, suffix := range []string{"", "(peer@2.0.0)"} {
					g := lockfile.NewGraph()
					p := lockfile.NewPackage(name, "1.2.3-beta.1+build")
					p.Source = &lockfile.Source{Kind: kind, Path: "vendor/pkg", URL: "https://example.test/pkg.tgz", Resolved: "abc"}
					key := prefix + p.DepPath + suffix
					g.Packages[key] = p
					var validation *ValidationError
					if err := validateGraph("input.lock", g); !errors.As(err, &validation) || validation.Code != "ERR_AUBE_RESOLUTION_SHAPE_MISMATCH" {
						t.Fatal(key, kind, err)
					}
					delete(g.Packages, key)
					g.Packages[p.Source.DepPath(name)] = p
					if err := validateGraph("input.lock", g); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	}
	for _, key := range []string{"pkg@1", "pkg@1.2", "pkg@^1.2.3", "other@1.2.3", "//pkg@1.2.3", "pkg@1.2.3junk"} {
		if hasRegistryVersion(key, "pkg") {
			t.Fatal(key)
		}
	}
}

func TestReadRejectsUnsafeAliasesWithReferenceError(t *testing.T) {
	for _, alias := range []string{"../escape", ".bin", "node_modules", "@scope/pkg/extra", "@scope/C:pkg", "foo\x00bar", "@scope/pkg"} {
		t.Run(fmt.Sprintf("%q", alias), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "pnpm-lock.yaml")
			name, _ := json.Marshal(alias)
			body := fmt.Sprintf("lockfileVersion: '9.0'\nimporters:\n  .:\n    dependencies:\n      %s: {specifier: '*', version: 'ok@1.0.0'}\npackages:\n  ok@1.0.0: {}\nsnapshots:\n  ok@1.0.0: {}\n", name)
			writeFile(t, path, []byte(body))
			writeFile(t, filepath.Join(dir, "package.json"), []byte(`{}`))
			project, _ := manifest.ParsePackage([]byte(`{}`))
			_, _, err := Read(path, identity.Pnpm, project, ReadOptions{})
			want := alias == "@scope/pkg"
			if (err == nil) != want {
				t.Fatal(err)
			}
			if err != nil && !strings.Contains(err.Error(), "unsafe dependency alias") {
				t.Fatal(err)
			}
			if oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE"); oracle != "" {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				out, execErr := exec.CommandContext(ctx, oracle, "read-project", dir).CombinedOutput()
				if execErr != nil {
					t.Fatal(string(out), execErr)
				}
				var ref struct {
					OK    bool
					Error string
				}
				if jsonErr := json.Unmarshal(out, &ref); jsonErr != nil {
					t.Fatal(string(out), jsonErr)
				}
				if ref.OK != want || err != nil && ref.Error != err.Error() {
					t.Fatalf("Go: %v; Rust: %s", err, out)
				}
			}
		})
	}
}
