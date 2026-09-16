package resolver

import (
	"maps"
	"slices"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
)

type AncestorFrame struct{ Name, Version string }

type resolveTask struct {
	Name, Range, Importer                                          string
	Type                                                           lockfile.DepType
	Root                                                           bool
	Parent, OriginalSpecifier, LockfileOverrideSpecifier, RealName *string
	Ancestors                                                      []AncestorFrame
	RangeFromOverride                                              bool
}

func (t resolveTask) registryName() string {
	if t.RealName != nil {
		return *t.RealName
	}
	return t.Name
}
func (t resolveTask) lockfileSpecifier() *string {
	if t.LockfileOverrideSpecifier != nil {
		return t.LockfileOverrideSpecifier
	}
	return t.OriginalSpecifier
}
func rootTask(name, requested string, kind lockfile.DepType, importer string) resolveTask {
	return resolveTask{Name: name, Range: requested, Type: kind, Importer: importer, Root: true, OriginalSpecifier: new(requested)}
}
func seedDirectDependencies(manifests []lockfile.ImporterManifest, ignored lockfile.Set, autoPeers bool) ([]resolveTask, map[string][]lockfile.DirectDep) {
	var queue []resolveTask
	importers := map[string][]lockfile.DirectDep{}
	for _, importer := range manifests {
		p := importer.Package
		importers[importer.Path] = nil
		seen := lockfile.Set{}
		for _, section := range []struct {
			Kind lockfile.DepType
			Deps map[string]string
		}{{lockfile.Production, p.Dependencies}, {lockfile.Dev, p.DevDependencies}, {lockfile.Optional, p.OptionalDependencies}} {
			for _, name := range slices.Sorted(maps.Keys(section.Deps)) {
				if seen.Has(name) {
					continue
				}
				seen.Add(name)
				if section.Kind == lockfile.Optional && ignored.Has(name) {
					continue
				}
				queue = append(queue, rootTask(name, section.Deps[name], section.Kind, importer.Path))
			}
		}
		if autoPeers {
			for _, name := range slices.Sorted(maps.Keys(p.PeerDependencies)) {
				if p.OptionalPeer(name) || seen.Has(name) {
					continue
				}
				queue = append(queue, rootTask(name, p.PeerDependencies[name], lockfile.Production, importer.Path))
			}
		}
	}
	return queue, importers
}
