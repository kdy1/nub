package lockfile

import (
	"maps"
	"slices"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
)

func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	value := *p
	return &value
}
func cloneNested[V any](src map[string]map[string]V) map[string]map[string]V {
	if src == nil {
		return nil
	}
	out := make(map[string]map[string]V, len(src))
	for key, values := range src {
		out[key] = maps.Clone(values)
	}
	return out
}
func cloneValues(src map[string]*jsonvalue.Value) map[string]*jsonvalue.Value {
	if src == nil {
		return nil
	}
	out := make(map[string]*jsonvalue.Value, len(src))
	for key, value := range src {
		out[key] = value.Clone()
	}
	return out
}
func (p *Package) Clone() *Package {
	if p == nil {
		return nil
	}
	out := *p
	out.Integrity = clonePtr(p.Integrity)
	out.Dependencies = maps.Clone(p.Dependencies)
	out.OptionalDependencies = maps.Clone(p.OptionalDependencies)
	out.PeerDependencies = maps.Clone(p.PeerDependencies)
	out.PeerDependenciesMeta = maps.Clone(p.PeerDependenciesMeta)
	if p.Source != nil {
		out.Source = clonePtr(p.Source)
		out.Source.Committish = clonePtr(p.Source.Committish)
		out.Source.Integrity = clonePtr(p.Source.Integrity)
		out.Source.Subpath = clonePtr(p.Source.Subpath)
	}
	out.OS = slices.Clone(p.OS)
	out.CPU = slices.Clone(p.CPU)
	out.Libc = slices.Clone(p.Libc)
	out.BundledDependencies = slices.Clone(p.BundledDependencies)
	out.TarballURL = clonePtr(p.TarballURL)
	out.AliasOf = clonePtr(p.AliasOf)
	out.YarnChecksum = clonePtr(p.YarnChecksum)
	out.Engines = maps.Clone(p.Engines)
	out.Bin = maps.Clone(p.Bin)
	out.DeclaredDependencies = maps.Clone(p.DeclaredDependencies)
	out.License = clonePtr(p.License)
	out.FundingURL = clonePtr(p.FundingURL)
	out.TransitivePeerDependencies = slices.Clone(p.TransitivePeerDependencies)
	out.ExtraMeta = cloneValues(p.ExtraMeta)
	out.Deprecated = clonePtr(p.Deprecated)
	return &out
}
func (g *Graph) Clone() *Graph {
	out := *g
	out.Importers = maps.Clone(g.Importers)
	for key, deps := range g.Importers {
		copy := slices.Clone(deps)
		for i := range copy {
			copy[i].Specifier = clonePtr(copy[i].Specifier)
		}
		out.Importers[key] = copy
	}
	out.Packages = maps.Clone(g.Packages)
	for key, pkg := range g.Packages {
		out.Packages[key] = pkg.Clone()
	}
	out.Overrides = maps.Clone(g.Overrides)
	out.PackageExtensionsChecksum = clonePtr(g.PackageExtensionsChecksum)
	out.PnpmfileChecksum = clonePtr(g.PnpmfileChecksum)
	out.IgnoredOptionalDependencies = maps.Clone(g.IgnoredOptionalDependencies)
	out.Times = maps.Clone(g.Times)
	out.SkippedOptionalDependencies = cloneNested(g.SkippedOptionalDependencies)
	out.Catalogs = cloneNested(g.Catalogs)
	out.BunConfigVersion = clonePtr(g.BunConfigVersion)
	out.PatchedDependencies = maps.Clone(g.PatchedDependencies)
	out.PatchedDependencyHashes = maps.Clone(g.PatchedDependencyHashes)
	out.TrustedDependencies = slices.Clone(g.TrustedDependencies)
	out.Runtimes = maps.Clone(g.Runtimes)
	for key, pin := range g.Runtimes {
		pin.Variants = slices.Clone(pin.Variants)
		for i := range pin.Variants {
			v := &pin.Variants[i]
			v.Targets = slices.Clone(v.Targets)
			for j := range v.Targets {
				v.Targets[j].Libc = clonePtr(v.Targets[j].Libc)
			}
			v.Bin = maps.Clone(v.Bin)
			v.Prefix = clonePtr(v.Prefix)
		}
		out.Runtimes[key] = pin
	}
	out.ExtraFields = cloneValues(g.ExtraFields)
	out.WorkspaceExtraFields = maps.Clone(g.WorkspaceExtraFields)
	for key, values := range g.WorkspaceExtraFields {
		out.WorkspaceExtraFields[key] = cloneValues(values)
	}
	return &out
}
