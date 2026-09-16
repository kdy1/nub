package resolver

import (
	"strings"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
)

func TestTaskPreprocessingPreservesImporterIdentity(t *testing.T) {
	for _, tc := range []struct{ name, spec, registry, requested string }{
		{"alias", "npm:actual@^2", "actual", "^2"},
		{"alias", "npm:@scope/actual", "@scope/actual", "latest"},
		{"alias", "npm:@scope/actual@", "@scope/actual", ""},
		{"@std/collections", "jsr:^1", "@jsr/std__collections", "^1"},
		{"alias", "jsr:@std/collections@^2", "@jsr/std__collections", "^2"},
		{"alias", "jsr:@std/collections", "@jsr/std__collections", "latest"},
	} {
		task := rootTask(tc.name, tc.spec, lockfile.Dev, "packages/app")
		p := taskPreprocessor{}
		keep, route, err := p.apply(&task)
		if !keep || route != nil || err != nil || task.Name != tc.name || task.registryName() != tc.registry || task.Range != tc.requested || *task.lockfileSpecifier() != tc.spec || task.Importer != "packages/app" || task.Type != lockfile.Dev {
			t.Fatal(tc, task, keep, route, err)
		}
	}
}
func TestCatalogOverrideAndAliasOrdering(t *testing.T) {
	catalogs := Catalogs{"default": {"alias": "npm:actual@^1"}, "next": {"alias": "npm:next@^2"}}
	for _, tc := range []struct {
		spec, override, registry, requested, locked string
		wantPick                                    bool
	}{
		{"catalog:", "", "actual", "^1", "catalog:", true},
		{"catalog:", "^3", "alias", "^3", "^3", true},
		{"^1", "catalog:next", "next", "^2", "npm:next@^2", true},
		{"npm:actual@^1", "npm:next@^2", "next", "^2", "npm:next@^2", false},
	} {
		task := rootTask("alias", tc.spec, lockfile.Production, ".")
		p := taskPreprocessor{Catalogs: catalogs}
		if tc.override != "" {
			p.Overrides = CompileOverrides(map[string]string{"alias": tc.override})
		}
		keep, _, err := p.apply(&task)
		if !keep || err != nil || task.registryName() != tc.registry || task.Range != tc.requested || *task.lockfileSpecifier() != tc.locked || *task.OriginalSpecifier != tc.spec || (len(p.CatalogPicks) > 0) != tc.wantPick {
			t.Fatal(tc, task, p.CatalogPicks, err)
		}
	}
	task := rootTask("alias", "^1", lockfile.Production, ".")
	task.RealName = new("old")
	p := taskPreprocessor{Overrides: CompileOverrides(map[string]string{"alias": "^3"})}
	if _, _, err := p.apply(&task); err != nil || task.RealName != nil {
		t.Fatal(task, err)
	}
}
func TestOverrideRemovalAndRootRelativeSource(t *testing.T) {
	for _, spec := range []string{"link:./libs/pkg", "file:./libs/pkg", "https://example.test/pkg.tgz", "github:org/pkg"} {
		task := rootTask("pkg", "*", lockfile.Optional, "packages/app")
		p := taskPreprocessor{Overrides: CompileOverrides(map[string]string{"pkg": spec})}
		if keep, _, err := p.apply(&task); !keep || err != nil || !task.RangeFromOverride || *task.lockfileSpecifier() != spec {
			t.Fatal(task, err)
		}
	}
	task := rootTask("pkg", "*", lockfile.Optional, ".")
	p := taskPreprocessor{Overrides: CompileOverrides(map[string]string{"pkg": "-"})}
	if keep, route, err := p.apply(&task); keep || route != nil || err != nil {
		t.Fatal(keep, route, err)
	}
	task = resolveTask{Name: "pkg", Range: "^1", Parent: new("parent@1.0.0")}
	p.Overrides = CompileOverrides(map[string]string{"pkg": "^2"})
	if _, _, err := p.apply(&task); err != nil || task.lockfileSpecifier() != nil {
		t.Fatal(task, err)
	}
}
func TestPreprocessRejectsInvalidCatalogAndJSR(t *testing.T) {
	for _, spec := range []string{"catalog:missing", "jsr:plain", "jsr:@Std/pkg", "jsr:@std/../pkg", "jsr:@std/pkg/extra", "jsr:@/pkg"} {
		task := rootTask("alias", spec, lockfile.Production, ".")
		p := taskPreprocessor{}
		if keep, _, err := p.apply(&task); keep || err == nil {
			t.Fatal(spec, keep, err)
		}
	}
	if name, ok := jsrNpmName("@foo-bar/baz_qux.v1"); !ok || name != "@jsr/foo-bar__baz_qux.v1" {
		t.Fatal(name, ok)
	}
}
func TestNamedRegistryTaskRouting(t *testing.T) {
	for _, tc := range []struct {
		alias, spec, name, requested string
		match                        bool
	}{
		{"pkg", "corp:^1", "pkg", "^1", true},
		{"pkg", "corp:", "pkg", "latest", true},
		{"pkg", "corp:@owner/actual@^2", "@owner/actual", "^2", true},
		{"pkg", "corp:@owner/actual", "@owner/actual", "latest", true},
		{"pkg", "corp:actual@next", "actual", "next", true},
		{"@owner/pkg", "corp:next", "@owner/pkg", "next", true},
		{"pkg", "corp:@owner/", "", "", false},
		{"pkg", "corp:@owner", "", "", false},
		{"pkg", "unknown:^1", "", "", false},
		{"pkg", "^1", "", "", false},
	} {
		task := rootTask(tc.alias, tc.spec, lockfile.Production, ".")
		p := taskPreprocessor{NamedRegistries: map[string]string{"corp": "https://corp.example/"}}
		keep, route, err := p.apply(&task)
		if !keep || err != nil || (route != nil) != tc.match {
			t.Fatal(tc, task, route, err)
		}
		if tc.match && (task.Range != tc.requested || task.registryName() != tc.name || route.Name != tc.name || route.Registry != "https://corp.example/" || *task.lockfileSpecifier() != tc.spec) {
			t.Fatal(tc, task, route)
		}
	}
	for _, scheme := range strings.Fields("npm jsr catalog workspace file link portal exec git github http https node") {
		if _, _, _, ok := parseNamedRegistry(scheme+":^1", "pkg", map[string]string{scheme: "https://wrong.example/"}); ok {
			t.Fatal("builtin overridden", scheme)
		}
	}
}
