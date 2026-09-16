package resolver

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestCatalogResolutionAndStructuredErrors(t *testing.T) {
	catalogs := Catalogs{"default": {"pkg": "^1", "alias": "npm:actual@^2"}, "next": {"pkg": "^3"}, "invalid": {"pkg": "catalog:next"}}
	for _, tc := range []struct {
		name, spec, catalog, requested string
		match                          bool
	}{
		{"pkg", "^1", "", "", false}, {"pkg", "catalog:", "default", "^1", true}, {"pkg", "catalog:next", "next", "^3", true}, {"alias", "catalog:", "default", "npm:actual@^2", true},
	} {
		catalog, requested, matched, err := catalogs.Resolve(tc.name, tc.spec)
		if err != nil || catalog != tc.catalog || requested != tc.requested || matched != tc.match {
			t.Fatal(tc, catalog, requested, matched, err)
		}
	}
	for _, tc := range []struct {
		name, spec, code string
		available        []string
		chained          bool
	}{
		{"pkg", "catalog:missing", "ERR_AUBE_UNKNOWN_CATALOG", []string{"default", "invalid", "next"}, false},
		{"missing", "catalog:", "ERR_AUBE_UNKNOWN_CATALOG_ENTRY", []string{"alias", "pkg"}, false},
		{"pkg", "catalog:invalid", "ERR_AUBE_UNKNOWN_CATALOG_ENTRY", nil, true},
	} {
		_, _, matched, err := catalogs.Resolve(tc.name, tc.spec)
		var diagnostic *CatalogError
		if !matched || !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || !reflect.DeepEqual(diagnostic.Available, tc.available) || (diagnostic.ChainedValue != nil) != tc.chained {
			t.Fatal(tc, diagnostic, err)
		}
		if tc.chained && !strings.Contains(err.Error(), "catalogs cannot chain") {
			t.Fatal(err)
		}
	}
}
func TestCatalogPicksPreferMatchingThenFirstThenRaw(t *testing.T) {
	picks := Catalogs{"default": {"matches": "^2", "overridden": "^1", "missing": "^3"}, "unused": {}}
	got := MaterializeCatalogPicks(picks, map[string][]string{"matches": {"4.0.0", "2.1.0", "2.9.0"}, "overridden": {"4.0.0", "5.0.0"}})
	if len(got) != 1 || got["default"]["matches"].Version != "2.1.0" || got["default"]["overridden"].Version != "4.0.0" || got["default"]["missing"].Version != "^3" || got["default"]["overridden"].Specifier != "^1" {
		t.Fatal(got)
	}
}
