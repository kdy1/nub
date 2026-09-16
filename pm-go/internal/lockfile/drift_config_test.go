package lockfile

import (
	"github.com/nubjs/nub/pm-go/internal/identity"
	"strings"
	"testing"
)

func TestPatchDriftFormatAndLegacyRules(t *testing.T) {
	g := NewGraph()
	g.PatchedDependencies = map[string]string{"pkg@1": "p.patch"}
	g.PatchedDependencyHashes = map[string]string{"pkg@1": "old"}
	paths, hashes := map[string]string{"pkg@1": "p.patch"}, map[string]string{"pkg@1": "new"}
	for _, kind := range []identity.Kind{identity.Npm, identity.Shrinkwrap, identity.Yarn, identity.YarnBerry} {
		if !g.CheckPatchedDependenciesDrift(kind, nil, nil).Fresh() {
			t.Fatal(kind)
		}
	}
	for _, kind := range []identity.Kind{identity.Nub, identity.Pnpm, identity.Bun} {
		if got := g.CheckPatchedDependenciesDrift(kind, paths, hashes); !strings.Contains(got.Reason, "contents changed") {
			t.Fatal(kind, got)
		}
		if got := g.CheckPatchedDependenciesDrift(kind, nil, nil); !strings.Contains(got.Reason, "no longer declared") {
			t.Fatal(kind, got)
		}
	}
	g.PatchedDependencyHashes = nil
	if got := g.CheckPatchedDependenciesDrift(identity.Pnpm, paths, hashes); !got.Fresh() {
		t.Fatal(got)
	}
	if got := g.CheckPatchedDependenciesDrift(identity.Bun, paths, hashes); !got.Fresh() {
		t.Fatal(got)
	}
	paths["pkg@1"] = "other.patch"
	if got := g.CheckPatchedDependenciesDrift(identity.Pnpm, paths, hashes); got.Reason != "patchedDependencies.pkg@1: project says other.patch, lockfile says p.patch" {
		t.Fatal(got)
	}
	g.PatchedDependencies = nil
	if got := g.CheckPatchedDependenciesDrift(identity.Pnpm, paths, hashes); !strings.Contains(got.Reason, "missing from the lockfile") {
		t.Fatal(got)
	}
	// A pnpm path with no supplied hash does not manufacture a hash comparison.
	if got := g.CheckPatchedDependenciesDrift(identity.Pnpm, paths, nil); !got.Fresh() {
		t.Fatal(got)
	}
	if got := g.CheckPatchedDependenciesDrift(identity.Bun, paths, nil); got.Fresh() {
		t.Fatal("missing Bun path accepted")
	}
}
func TestCatalogDriftOnlyComparesRecordedIntent(t *testing.T) {
	g := NewGraph()
	g.Catalogs = map[string]map[string]CatalogEntry{"default": {"a": {Specifier: "^1", Version: "1.2.3"}}}
	current := map[string]map[string]string{"default": {"a": "^1", "unused": "*"}, "unused": {"b": "2"}}
	if got := g.CheckCatalogsDrift(current); !got.Fresh() {
		t.Fatal(got)
	}
	current["default"]["a"] = "^2"
	if got := g.CheckCatalogsDrift(current); got.Reason != "catalogs.default.a: workspace says ^2, lockfile says ^1" {
		t.Fatal(got)
	}
	delete(current["default"], "a")
	if got := g.CheckCatalogsDrift(current); got.Reason != "catalogs.default: workspace removed a" {
		t.Fatal(got)
	}
}
func TestExtensionChecksumPresenceMatters(t *testing.T) {
	g := NewGraph()
	if !g.CheckPackageExtensionsDrift(nil).Fresh() {
		t.Fatal("empty")
	}
	if g.CheckPackageExtensionsDrift(new("")).Fresh() {
		t.Fatal("absent vs empty")
	}
	g.PackageExtensionsChecksum = new("hash")
	if !g.CheckPackageExtensionsDrift(new("hash")).Fresh() || g.CheckPackageExtensionsDrift(nil).Fresh() {
		t.Fatal("checksum mismatch")
	}
}
