package resolver

import (
	"cmp"
	"maps"
	"slices"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/semver"
)

type UnmetPeer struct {
	FromDepPath, FromName, PeerName, Declared string
	Found                                     *string
}

func canonicalTail(tail string) string {
	base, _, _ := strings.Cut(tail, "(")
	return base
}

// DetectUnmetPeers reports required peers using the edges written by peer
// contextualization. Optional peers never produce this diagnostic.
func DetectUnmetPeers(g *lockfile.Graph) []UnmetPeer {
	out := []UnmetPeer{}
	for _, key := range slices.Sorted(maps.Keys(g.Packages)) {
		p := g.Packages[key]
		for _, name := range slices.Sorted(maps.Keys(p.PeerDependencies)) {
			if p.PeerDependenciesMeta[name].Optional {
				continue
			}
			rangeSpec := p.PeerDependencies[name]
			var found *string
			if tail, ok := p.Dependencies[name]; ok {
				version := canonicalTail(tail)
				found = &version
				if semver.EngineSatisfies(version, rangeSpec) {
					continue
				}
			}
			out = append(out, UnmetPeer{p.DepPath, p.Name, name, rangeSpec, found})
		}
	}
	slices.SortStableFunc(out, func(a, b UnmetPeer) int {
		if c := cmp.Compare(a.FromDepPath, b.FromDepPath); c != 0 {
			return c
		}
		return cmp.Compare(a.PeerName, b.PeerName)
	})
	return out
}

type AutoInstalledPeers map[string]lockfile.Set

// HoistAutoInstalledPeers temporarily exposes direct dependencies' required
// peers in importer scopes. RemoveAutoInstalledPeers must run after graph
// filtering and contextualization, before serializing or linking importers.
// Existing importer entries and all package nodes are left intact.
func HoistAutoInstalledPeers(g *lockfile.Graph) AutoInstalledPeers {
	hoisted := AutoInstalledPeers{}
	packageKeys := slices.Sorted(maps.Keys(g.Packages))
	for _, importer := range slices.Sorted(maps.Keys(g.Importers)) {
		deps := g.Importers[importer]
		satisfied := lockfile.Set{}
		for _, d := range deps {
			satisfied.Add(d.Name)
		}
		var additions []lockfile.DirectDep
		byName := map[string]int{}
		for _, d := range deps {
			p := g.Packages[d.DepPath]
			if p == nil {
				continue
			}
			for _, name := range slices.Sorted(maps.Keys(p.PeerDependencies)) {
				if satisfied.Has(name) || p.PeerDependenciesMeta[name].Optional {
					continue
				}
				if index, ok := byName[name]; ok {
					if additions[index].Type != d.Type {
						additions[index].Type = lockfile.Production
					}
					continue
				}
				version, exists := p.Dependencies[name]
				if !exists {
					var highest *semver.Version
					for _, key := range packageKeys {
						candidate := g.Packages[key]
						if candidate.Name != name {
							continue
						}
						v, err := semver.ParseEngineVersion(candidate.Version)
						if err == nil && (highest == nil || v.Compare(highest) >= 0) {
							highest, version, exists = v, candidate.Version, true
						}
					}
				}
				if !exists {
					continue
				}
				depPath := name + "@" + canonicalTail(version)
				if _, ok := g.Packages[depPath]; !ok {
					continue
				}
				rangeSpec := p.PeerDependencies[name]
				byName[name] = len(additions)
				additions = append(additions, lockfile.DirectDep{Name: name, DepPath: depPath, Type: d.Type, Specifier: &rangeSpec})
			}
		}
		if len(additions) == 0 {
			continue
		}
		names := lockfile.Set{}
		for _, d := range additions {
			names.Add(d.Name)
		}
		hoisted[importer] = names
		deps = append(deps, additions...)
		slices.SortStableFunc(deps, func(a, b lockfile.DirectDep) int { return cmp.Compare(a.Name, b.Name) })
		g.Importers[importer] = deps
	}
	return hoisted
}

func RemoveAutoInstalledPeers(g *lockfile.Graph, hoisted AutoInstalledPeers) {
	for importer, names := range hoisted {
		if deps, ok := g.Importers[importer]; ok {
			g.Importers[importer] = slices.DeleteFunc(deps, func(d lockfile.DirectDep) bool { return names.Has(d.Name) })
		}
	}
}
