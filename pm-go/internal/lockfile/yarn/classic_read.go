package yarn

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/manifest"
	"github.com/nubjs/nub/pm-go/internal/semver"
)

type Options struct {
	AllowUnsupportedSources bool
	// A non-nil map supplies the adapter's identity-scoped override selection.
	// Nil preserves the standalone engine's manifest-source precedence.
	Overrides map[string]string
}
type Warning struct{ Code, Message string }
type UnsupportedSource struct{ Name, Spec, Protocol string }

func (e *UnsupportedSource) Error() string {
	return fmt.Sprintf("lockfile entry `%s` uses unsupported source `%s`", e.Spec, e.Protocol)
}
func sortedKeys[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }
func ReadClassic(path string, project *manifest.Package, options Options) (*lockfile.Graph, []Warning, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	return ParseClassic(path, data, project, options)
}
func ParseClassic(path string, data []byte, project *manifest.Package, options Options) (*lockfile.Graph, []Warning, error) {
	if !utf8.Valid(data) {
		return nil, nil, fmt.Errorf("invalid UTF-8 in yarn.lock")
	}
	blocks, err := tokenizeClassic(string(data))
	if err != nil {
		return nil, nil, err
	}
	g := lockfile.NewGraph()
	bySpec := map[string]string{}
	unsupported := map[string]*UnsupportedSource{}
	for _, b := range blocks {
		version, ok := b.fields["version"]
		if !ok {
			return nil, nil, fmt.Errorf("yarn.lock block %q has no version", b.specs)
		}
		name, ok := specName(b.specs[0])
		if !ok {
			return nil, nil, fmt.Errorf("could not parse package name from yarn.lock spec '%s'", b.specs[0])
		}
		var alias *string
		for _, s := range b.specs {
			if real, ok := npmAliasName(s); ok {
				if real != name {
					alias = &real
				}
				break
			}
		}
		var resolved *string
		if s, ok := b.fields["resolved"]; ok {
			resolved = &s
		}
		source := classicSource{}
		for _, s := range b.specs {
			if classified := classifyClassic(s, name, resolved); classified.local != nil || classified.isUnsupported {
				source = classified
				break
			}
		}
		if source.isUnsupported && !options.AllowUnsupportedSources {
			for _, s := range b.specs {
				unsupported[s] = &UnsupportedSource{name, s, source.unsupported}
			}
			continue
		}
		key := name + "@" + version
		if source.local != nil {
			key = source.local.DepPath(name)
		}
		for _, s := range b.specs {
			bySpec[s] = key
		}
		if g.Packages[key] != nil {
			continue
		}
		p := lockfile.NewPackage(name, version)
		p.DepPath = key
		p.Source = source.local
		p.AliasOf = alias
		p.DeclaredDependencies = maps.Clone(b.dependencies)
		for dep, rangeText := range b.dependencies {
			p.Dependencies[dep] = dep + "@" + rangeText
		}
		if integrity, ok := b.fields["integrity"]; ok {
			p.Integrity = &integrity
		}
		if resolved != nil {
			p.TarballURL = resolvedTarball(*resolved)
		}
		if p.TarballURL != nil {
			p.ExtraMeta = map[string]*jsonvalue.Value{"__aube_preserve_tarball_url": {Kind: 'b', Scalar: true}}
		}
		g.Packages[key] = p
	}
	var warnings []Warning
	resolve := func(spec string, optional bool) (string, error) {
		if key, ok := bySpec[spec]; ok {
			return key, nil
		}
		if issue := unsupported[spec]; issue != nil {
			if !optional {
				return "", issue
			}
			warnings = append(warnings, Warning{"WARN_AUBE_LOCKFILE_UNSUPPORTED_SOURCE", fmt.Sprintf("optional dependency `%s` uses the unsupported `%s` source — skipping it (the install continues)", issue.Name, issue.Protocol)})
		}
		return "", nil
	}
	for _, key := range sortedKeys(g.Packages) {
		p := g.Packages[key]
		deps := map[string]string{}
		for _, name := range sortedKeys(p.Dependencies) {
			target, err := resolve(p.Dependencies[name], false)
			if err != nil {
				return nil, warnings, err
			}
			if target != "" {
				deps[name] = strings.TrimPrefix(target, name+"@")
			}
		}
		p.Dependencies = deps
	}
	memberVersions := map[string]string{}
	members := map[string]*manifest.Package{}
	if project.Workspaces != nil {
		for _, dir := range discoverMembers(filepath.Dir(path), project.Workspaces.Patterns) {
			pj, err := manifest.ReadPackage(filepath.Join(filepath.Dir(path), dir, "package.json"))
			if err != nil {
				continue
			}
			if pj.Name != nil {
				version := "0.0.0"
				if pj.Version != nil {
					version = *pj.Version
				}
				memberVersions[*pj.Name] = version
			}
			members[dir] = pj
		}
	}
	workspaceLink := func(name, rangeText string) string {
		version, ok := memberVersions[name]
		if !ok {
			return ""
		}
		matches := false
		if rest, ok := strings.CutPrefix(rangeText, "workspace:"); ok {
			matches = rest == "" || rest == "*" || rest == "^" || rest == "~" || versionSatisfies(version, rest)
		} else if strings.HasPrefix(rangeText, "link:") || strings.HasPrefix(rangeText, "portal:") {
			matches = true
		} else {
			matches = rangeText == "" || rangeText == "*" || versionSatisfies(version, rangeText)
		}
		if matches {
			return name + "@" + version
		}
		return ""
	}
	g.SkippedOptionalDependencies = map[string]map[string]string{}
	importer := func(path string, pj *manifest.Package) error {
		var direct []lockfile.DirectDep
		for i, deps := range []map[string]string{pj.Dependencies, pj.DevDependencies, pj.OptionalDependencies} {
			for _, name := range sortedKeys(deps) {
				rangeText := deps[name]
				spec := name + "@" + rangeText
				kind := lockfile.DepType(i)
				target, err := resolve(spec, kind == lockfile.Optional)
				if err != nil {
					return err
				}
				if target == "" && kind == lockfile.Optional && unsupported[spec] != nil {
					if g.SkippedOptionalDependencies[path] == nil {
						g.SkippedOptionalDependencies[path] = map[string]string{}
					}
					g.SkippedOptionalDependencies[path][name] = rangeText
					continue
				}
				if target == "" {
					target = workspaceLink(name, rangeText)
				}
				if target != "" {
					direct = append(direct, lockfile.DirectDep{Name: name, DepPath: target, Type: kind, Specifier: &rangeText})
				}
			}
		}
		g.Importers[path] = direct
		return nil
	}
	if err := importer(".", project); err != nil {
		return nil, warnings, err
	}
	for _, path := range sortedKeys(members) {
		if err := importer(path, members[path]); err != nil {
			return nil, warnings, err
		}
	}
	return g, warnings, nil
}
func versionSatisfies(version, rangeText string) bool {
	if strings.TrimSpace(rangeText) == "" {
		rangeText = "*"
	}
	r, err := semver.ParseRange(rangeText)
	if err != nil {
		return false
	}
	v, err := semver.ParseVersion(version)
	return err == nil && r.Contains(v)
}
