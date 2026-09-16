package linker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/manifest"
)

func binPackage(t *testing.T, dir, body string) *manifest.Package {
	t.Helper()
	binFixture(t, dir, "package.json", body)
	binFixture(t, dir, "cli.js", "#!/usr/bin/env node\n")
	pkg, err := manifest.ParsePackage([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}

func materializedBin(t *testing.T, plan BinPlan, pkg *lockfile.Package, body string) string {
	t.Helper()
	dir, err := MaterializedPackageDir(plan.VirtualStoreDir, pkg.DepPath, pkg.Name, 120, plan.Placements)
	if err != nil {
		t.Fatal(err)
	}
	binPackage(t, dir, body)
	plan.Graph.Packages[pkg.DepPath] = pkg
	return dir
}

func TestIsolatedBinPassUsesManifestsWorkspacesAndTrust(t *testing.T) {
	for _, policy := range []struct {
		name                         string
		ignore, allow, floor, builds bool
	}{{"none", false, false, false, false}, {"allow", false, true, false, true}, {"floor", false, false, true, true}, {"ignored", true, true, true, false}} {
		t.Run(policy.name, func(t *testing.T) {
			root := t.TempDir()
			plan := BinPlan{ProjectDir: root, VirtualStoreDir: filepath.Join(root, "node_modules", ".store"), Graph: lockfile.NewGraph(),
				HasWorkspace: true, IgnoreScripts: policy.ignore, HasAllowRule: policy.allow, FloorMayAllow: policy.floor,
				Manifests: map[string]*manifest.Package{}, WorkspaceDirs: map[string]string{}, Options: BinOptions{ExtendNodePath: true}}
			child := lockfile.NewPackage("child", "1.0.0")
			childDir := materializedBin(t, plan, child, `{"name":"child","bin":{"child":"cli.js"}}`)
			parent := lockfile.NewPackage("@scope/parent", "1.0.0")
			parent.Dependencies = map[string]string{"child": child.DepPath, "@scope/parent": parent.DepPath, "filtered": "1.0.0"}
			parent.Bin = map[string]string{"stale-lockfile-bin": "absent.js"}
			parent.BundledDependencies = []string{"bundled", "malformed-bundle"}
			parentDir := materializedBin(t, plan, parent, `{"name":"@scope/parent","bin":{"parent":"cli.js","shared":"cli.js"}}`)
			bundleDir := filepath.Join(parentDir, "node_modules", "bundled")
			binPackage(t, bundleDir, `{"name":"bundled","bin":"cli.js"}`)
			binFixture(t, parentDir, "node_modules/malformed-bundle/package.json", "invalid")
			for _, kind := range []lockfile.SourceKind{lockfile.Link, lockfile.Portal} {
				name := "linked"
				if kind == lockfile.Portal {
					name = "portal"
				}
				pkg := lockfile.NewPackage(name, "0.0.0")
				pkg.Source = &lockfile.Source{Kind: kind, Path: "local/" + name}
				plan.Graph.Packages[pkg.DepPath] = pkg
				plan.Graph.Importers["."] = append(plan.Graph.RootDeps(), lockfile.DirectDep{Name: name, DepPath: pkg.DepPath})
				binPackage(t, filepath.Join(root, "local", name), `{"bin":"cli.js"}`)
			}
			workspace := filepath.Join(root, "packages", "workspace")
			binPackage(t, workspace, `{"name":"workspace","bin":{"workspace":"cli.js"}}`)
			plan.WorkspaceDirs["workspace"] = workspace
			plan.Graph.Importers["."] = append(plan.Graph.RootDeps(), lockfile.DirectDep{Name: parent.Name, DepPath: parent.DepPath}, lockfile.DirectDep{Name: "workspace", DepPath: "workspace@1"})
			plan.Manifests["."] = binPackage(t, root, `{"name":"root","bin":{"shared":"generated.js"}}`)
			member := filepath.Join(root, "packages", "member")
			plan.Manifests["packages/member"] = binPackage(t, member, `{"name":"member","bin":{"member":"generated.js"}}`)
			plan.Graph.Importers["packages/member"] = []lockfile.DirectDep{{Name: child.Name, DepPath: child.DepPath}, {Name: "workspace", DepPath: "workspace@1"}}
			plan.Graph.Importers["packages/member/node_modules/workspace"] = []lockfile.DirectDep{{Name: parent.Name, DepPath: parent.DepPath}}
			managed, err := LinkAllBins(t.Context(), plan, nil)
			if err != nil || managed.capture != policy.builds {
				t.Fatal(err, managed)
			}
			rootBins := filepath.Join(root, "node_modules", ".bin")
			memberBins := filepath.Join(member, "node_modules", ".bin")
			for _, expected := range []struct{ dir, name, target string }{
				{rootBins, "parent", filepath.Join(parentDir, "cli.js")}, {rootBins, "bundled", filepath.Join(bundleDir, "cli.js")},
				{rootBins, "shared", filepath.Join(root, "generated.js")}, {rootBins, "workspace", filepath.Join(workspace, "cli.js")},
				{rootBins, "linked", filepath.Join(root, "local", "linked", "cli.js")}, {rootBins, "portal", filepath.Join(root, "local", "portal", "cli.js")},
				{memberBins, "child", filepath.Join(childDir, "cli.js")}, {memberBins, "member", filepath.Join(member, "generated.js")},
				{memberBins, "workspace", filepath.Join(workspace, "cli.js")},
			} {
				if got := resolvedTestBin(t, expected.dir, expected.name); got != expected.target {
					t.Fatal(expected.name, got, expected.target)
				}
			}
			depBins := filepath.Join(DepModulesDir(parentDir, parent.Name), ".bin")
			if policy.builds {
				if got := resolvedTestBin(t, depBins, "child"); got != filepath.Join(childDir, "cli.js") {
					t.Fatal(got)
				}
			} else if _, err := os.Stat(depBins); !os.IsNotExist(err) {
				t.Fatal("bins for disabled dependency builds", err)
			}
			for _, missing := range []string{filepath.Join(rootBins, "stale-lockfile-bin"), filepath.Join(member, "node_modules", "workspace"), filepath.Join(DepModulesDir(childDir, child.Name), ".bin")} {
				if _, err := os.Stat(missing); !os.IsNotExist(err) {
					t.Fatal("unexpected entry", missing, err)
				}
			}
		})
	}
}

func TestHoistedBinPrecedenceAndEveryPlacement(t *testing.T) {
	root := t.TempDir()
	prefer := false
	plan := BinPlan{ProjectDir: root, VirtualStoreDir: filepath.Join(root, "virtual"), Graph: lockfile.NewGraph(), Hoisted: true,
		Placements: HoistedPlacements{}, Manifests: map[string]*manifest.Package{}, HasAllowRule: true, Options: BinOptions{PreferSymlinked: &prefer}}
	var direct *lockfile.Package
	for _, name := range []string{"alpha", "zeta", "direct"} {
		pkg := lockfile.NewPackage(name, "1.0.0")
		rootDir := filepath.Join(root, "node_modules", name)
		plan.Placements[pkg.DepPath] = []string{rootDir}
		materializedBin(t, plan, pkg, `{"bin":{"shared":"cli.js","direct":"cli.js","transitive":"cli.js"}}`)
		if name == "direct" {
			direct = pkg
			binPackage(t, rootDir, `{"bin":{"shared":"cli.js","direct":"cli.js"}}`)
		}
		if name == "zeta" {
			deep := filepath.Join(root, "node_modules", "parent", "node_modules", name)
			plan.Placements[pkg.DepPath] = append(plan.Placements[pkg.DepPath], deep)
			binPackage(t, deep, `{"bin":{"shared":"cli.js","direct":"cli.js","transitive":"cli.js"}}`)
		}
	}
	plan.Graph.Importers["."] = []lockfile.DirectDep{{Name: direct.Name, DepPath: direct.DepPath}}
	plan.Manifests["."] = binPackage(t, root, `{"name":"root","bin":{"shared":"self.js"}}`)
	if _, err := LinkAllBins(t.Context(), plan, nil); err != nil {
		t.Fatal(err)
	}
	rootBins := filepath.Join(root, "node_modules", ".bin")
	for name, want := range map[string]string{"shared": filepath.Join(root, "self.js"), "direct": filepath.Join(root, "node_modules", "direct", "cli.js"), "transitive": filepath.Join(root, "node_modules", "zeta", "cli.js")} {
		if got := resolvedTestBin(t, rootBins, name); got != want {
			t.Fatal(name, got, want)
		}
	}
	deepModules := filepath.Join(root, "node_modules", "parent", "node_modules")
	for _, name := range []string{"shared", "direct", "transitive"} {
		if got := resolvedTestBin(t, filepath.Join(deepModules, ".bin"), name); got != filepath.Join(deepModules, "zeta", "cli.js") {
			t.Fatal(name, got)
		}
	}
}

func TestBinPassMissingMalformedAndCancellation(t *testing.T) {
	root := t.TempDir()
	plan := BinPlan{ProjectDir: root, VirtualStoreDir: filepath.Join(root, "virtual"), Graph: lockfile.NewGraph()}
	pkg := lockfile.NewPackage("missing", "1.0.0")
	plan.Graph.Packages[pkg.DepPath] = pkg
	plan.Graph.Importers["."] = []lockfile.DirectDep{{Name: pkg.Name, DepPath: pkg.DepPath}}
	if _, err := LinkAllBins(t.Context(), plan, nil); err != nil {
		t.Fatal("missing manifest must be skipped", err)
	}
	dir, err := MaterializedPackageDir(plan.VirtualStoreDir, pkg.DepPath, pkg.Name, 120, nil)
	if err != nil {
		t.Fatal(err)
	}
	binFixture(t, dir, "package.json", "{invalid")
	if _, err := LinkAllBins(t.Context(), plan, nil); err == nil || !strings.Contains(err.Error(), "failed to parse package.json for missing") {
		t.Fatal("malformed manifest silently skipped", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := LinkAllBins(ctx, plan, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	plan.VirtualStoreOnly = true
	if managed, err := LinkAllBins(t.Context(), plan, nil); err != nil || managed.capture || len(managed.entries) != 0 {
		t.Fatal("virtual-store-only must skip the bin pass", managed, err)
	}
}
