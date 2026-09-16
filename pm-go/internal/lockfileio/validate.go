package lockfileio

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/semver"
)

type ValidationError struct {
	Code, Path, Message string
}

func (e *ValidationError) Error() string {
	if e.Code == "ERR_AUBE_RESOLUTION_SHAPE_MISMATCH" {
		return "lockfile " + e.Path + " " + e.Message
	}
	return "failed to parse lockfile " + e.Path + ": " + e.Message
}

// Format parsers preserve their native graph projection. The shared read
// boundary validates names before any installer can use them as paths.
func validateGraph(path string, g *lockfile.Graph) error {
	parseError := func(message string) error {
		return &ValidationError{"ERR_AUBE_LOCKFILE_PARSE", path, message}
	}
	for _, importer := range slices.Sorted(maps.Keys(g.Importers)) {
		for _, dep := range g.Importers[importer] {
			if !safePackageAlias(dep.Name) {
				return parseError(fmt.Sprintf("importer %s has unsafe dependency alias `%s`", importer, dep.Name))
			}
		}
	}
	for _, key := range slices.Sorted(maps.Keys(g.Packages)) {
		p := g.Packages[key]
		if !safePackageAlias(p.Name) {
			return parseError(fmt.Sprintf("package %s has unsafe package name `%s`", key, p.Name))
		}
		// Keep the reference's section order as well as its BTreeMap key order
		// so a malformed graph reports the same first offending alias.
		sections := [][]string{
			slices.Sorted(maps.Keys(p.Dependencies)),
			slices.Sorted(maps.Keys(p.OptionalDependencies)),
			slices.Sorted(maps.Keys(p.PeerDependencies)),
			slices.Sorted(maps.Keys(p.PeerDependenciesMeta)),
			slices.Sorted(maps.Keys(p.DeclaredDependencies)),
		}
		for _, aliases := range sections {
			for _, alias := range aliases {
				if !safePackageAlias(alias) {
					return parseError(fmt.Sprintf("package %s has unsafe dependency alias `%s`", key, alias))
				}
			}
		}
	}
	for _, key := range slices.Sorted(maps.Keys(g.Packages)) {
		p := g.Packages[key]
		if p.Source != nil && hasRegistryVersion(key, p.Name) {
			return &ValidationError{"ERR_AUBE_RESOLUTION_SHAPE_MISMATCH", path, fmt.Sprintf("has registry-style dependency path `%s` backed by %s resolution", key, p.Source.KindName())}
		}
	}
	return nil
}

func safePackageAlias(name string) bool {
	if name == "" || strings.ContainsAny(name, "\x00\\") || strings.HasPrefix(name, "/") || name == ".bin" || name == ".pnpm" || name == "node_modules" {
		return false
	}
	parts := strings.Split(name, "/")
	switch len(parts) {
	case 1:
		return safeAliasComponent(parts[0])
	case 2:
		return strings.HasPrefix(parts[0], "@") && len(parts[0]) > 1 && safeAliasComponent(parts[0]) && safeAliasComponent(parts[1])
	default:
		return false
	}
}

func safeAliasComponent(s string) bool {
	return s != "" && s != "." && s != ".." && !(len(s) >= 2 && s[1] == ':')
}

func hasRegistryVersion(depPath, name string) bool {
	tail, ok := strings.CutPrefix(strings.TrimPrefix(depPath, "/"), name+"@")
	if !ok {
		return false
	}
	version, _, _ := strings.Cut(tail, "(")
	_, err := semver.ParseEngineVersion(version)
	return err == nil
}
