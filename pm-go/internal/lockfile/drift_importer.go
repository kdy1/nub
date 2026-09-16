package lockfile

import (
	"github.com/nubjs/nub/pm-go/internal/manifest"
	"github.com/nubjs/nub/pm-go/internal/semver"
	"strings"
)

// CheckImporterDrift compares recorded intent, including duplicate declarations
// in separate dependency sections and intentionally skipped optional packages.
func (g *Graph) CheckImporterDrift(importer string, project *manifest.Package, overrides map[string]string, workspaceLinks Set) DriftStatus {
	label := ""
	if importer != "." {
		label = importer + ": "
	}
	deps := g.Importers[importer]
	noSpec := len(deps) > 0
	for _, dep := range deps {
		if dep.Specifier != nil {
			noSpec = false
			break
		}
	}
	if noSpec {
		return DriftStatus{}
	}
	type sectionKey struct {
		name string
		kind DepType
	}
	locked := map[string]string{}
	bySection := map[sectionKey]string{}
	for _, dep := range deps {
		if dep.Specifier != nil {
			locked[dep.Name] = *dep.Specifier
			bySection[sectionKey{dep.Name, dep.Type}] = *dep.Specifier
		}
	}
	rules := CompileDirectOverrides(overrides)
	owns := func(name string) bool {
		for _, section := range []map[string]string{project.Dependencies, project.DevDependencies, project.OptionalDependencies} {
			if _, ok := section[name]; ok {
				return true
			}
		}
		return false
	}
	type declaration struct {
		name, spec string
		kind       DepType
		optional   bool
	}
	var declared []declaration
	for i, section := range []map[string]string{project.Dependencies, project.DevDependencies, project.OptionalDependencies, project.PeerDependencies} {
		for _, name := range driftKeys(section) {
			if i == 2 && g.IgnoredOptionalDependencies.Has(name) {
				continue
			}
			if i == 3 && (!g.Settings.AutoInstallPeers || project.OptionalPeer(name) || owns(name)) {
				continue
			}
			kind := DepType(i)
			if i == 3 {
				kind = Production
			}
			declared = append(declared, declaration{name, section[name], kind, i == 2})
		}
	}
	for _, dep := range declared {
		name, spec := dep.name, dep.spec
		if s, ok := bySection[sectionKey{name, dep.kind}]; ok && s == spec {
			continue
		}
		lockedSpec, ok := locked[name]
		if !ok {
			if old, ok := g.SkippedOptionalDependencies[importer][name]; dep.optional && ok {
				if old == spec {
					continue
				}
				return drift("%s%s: manifest says %s, lockfile (skipped) says %s", label, name, spec, old)
			}
			return drift("%smanifest adds %s@%s", label, name, spec)
		}
		if lockedSpec != spec {
			if !owns(name) && retainedPeerCompatible(spec, lockedSpec) {
				continue
			}
			if replacement := rules.Apply(name, spec); replacement != nil && *replacement == lockedSpec {
				continue
			}
			if g.PnpmfileChecksum != nil && driftLocalSpec(lockedSpec) && !driftLocalSpec(spec) {
				continue
			}
			return drift("%s%s: manifest says %s, lockfile says %s", label, name, spec, lockedSpec)
		}
	}
	types := map[string][]DepType{}
	for i, section := range []map[string]string{project.Dependencies, project.DevDependencies, project.OptionalDependencies, project.PeerDependencies} {
		for _, name := range driftKeys(section) {
			if i == 2 && g.IgnoredOptionalDependencies.Has(name) {
				continue
			}
			if i == 3 && (!g.Settings.AutoInstallPeers || owns(name)) {
				continue
			}
			kind := DepType(i)
			if i == 3 {
				kind = Production
			}
			types[name] = append(types[name], kind)
		}
	}
	for _, dep := range deps {
		expected, ok := types[dep.Name]
		if !ok {
			continue
		}
		matches := false
		var names []string
		for _, kind := range expected {
			if kind == dep.Type {
				matches = true
			}
			names = append(names, kind.Label())
		}
		if !matches {
			return drift("%s%s: manifest section is %s, lockfile section is %s", label, dep.Name, strings.Join(names, " and "), dep.Type.Label())
		}
	}
	names := Set{}
	for _, dep := range declared {
		names.Add(dep.name)
	}
	for _, name := range driftKeys(locked) {
		if names.Has(name) {
			continue
		}
		if peer, ok := project.PeerDependencies[name]; g.Settings.AutoInstallPeers && ok && retainedPeerCompatible(peer, locked[name]) {
			continue
		}
		linked := false
		if importer == "." && workspaceLinks.Has(name) {
			for _, dep := range deps {
				if dep.Name != name {
					continue
				}
				p := g.Packages[dep.DepPath]
				linked = p != nil && p.Source != nil && p.Source.Kind == Link
				break
			}
		}
		if linked {
			continue
		}
		return drift("%smanifest removed %s", label, name)
	}
	return DriftStatus{}
}
func driftLocalSpec(spec string) bool {
	return strings.HasPrefix(spec, "link:") || strings.HasPrefix(spec, "file:") || strings.HasPrefix(spec, "portal:") || strings.HasPrefix(spec, "exec:")
}
func retainedPeerCompatible(peer, locked string) bool {
	if peer == locked {
		return true
	}
	if strings.TrimSpace(peer) == "" {
		peer = "*"
	}
	rangeValue, err := semver.ParseRange(peer)
	if err != nil {
		return false
	}
	v, err := semver.ParseVersion(locked)
	return err == nil && rangeValue.Contains(v)
}
