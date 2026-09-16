package resolver

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/semver"
)

type Catalogs map[string]map[string]string

type CatalogError struct {
	Code, Name, Spec, Catalog string
	Available                 []string
	ChainedValue              *string
}

func (e *CatalogError) Error() string {
	if e.Code == "ERR_AUBE_UNKNOWN_CATALOG" {
		return fmt.Sprintf("%s: catalog reference `%s` does not resolve — catalog `%s` is not defined (add it to `catalog:` / `catalogs.%s:` in pnpm-workspace.yaml, or under `workspaces.catalog` / `pnpm.catalog` in package.json)", e.Name, e.Spec, e.Catalog, e.Catalog)
	}
	return fmt.Sprintf("%s: catalog reference `%s` does not resolve — catalog `%s` has no entry for `%s`", e.Name, e.Spec, e.Catalog, e.Name)
}

// Resolve performs exactly one catalog expansion. Chained references are invalid.
func (catalogs Catalogs) Resolve(name, spec string) (catalog, requested string, matched bool, err error) {
	catalog, matched = strings.CutPrefix(spec, "catalog:")
	if !matched {
		return "", "", false, nil
	}
	if catalog == "" {
		catalog = "default"
	}
	entries, ok := catalogs[catalog]
	if !ok {
		return "", "", true, &CatalogError{Code: "ERR_AUBE_UNKNOWN_CATALOG", Name: name, Spec: spec, Catalog: catalog, Available: slices.Sorted(maps.Keys(catalogs))}
	}
	requested, ok = entries[name]
	if !ok {
		return "", "", true, &CatalogError{Code: "ERR_AUBE_UNKNOWN_CATALOG_ENTRY", Name: name, Spec: spec, Catalog: catalog, Available: slices.Sorted(maps.Keys(entries))}
	}
	if strings.HasPrefix(requested, "catalog:") {
		return "", "", true, &CatalogError{Code: "ERR_AUBE_UNKNOWN_CATALOG_ENTRY", Name: name, Spec: spec, Catalog: fmt.Sprintf("%s (value %s is itself a catalog: reference, catalogs cannot chain)", catalog, requested), ChainedValue: new(requested)}
	}
	return catalog, requested, true, nil
}

func MaterializeCatalogPicks(picks Catalogs, versions map[string][]string) map[string]map[string]lockfile.CatalogEntry {
	out := map[string]map[string]lockfile.CatalogEntry{}
	for catalog, entries := range picks {
		if len(entries) == 0 {
			continue
		}
		resolved := map[string]lockfile.CatalogEntry{}
		for name, requested := range entries {
			version := requested
			if candidates := versions[name]; len(candidates) > 0 {
				version = candidates[0]
				for _, v := range candidates {
					if semver.Satisfies(v, requested) {
						version = v
						break
					}
				}
			}
			resolved[name] = lockfile.CatalogEntry{Specifier: requested, Version: version}
		}
		out[catalog] = resolved
	}
	return out
}
