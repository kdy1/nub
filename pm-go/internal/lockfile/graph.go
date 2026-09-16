package lockfile

import (
	"maps"
	"slices"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
)

type DepType uint8

const (
	Production DepType = iota
	Dev
	Optional
)

func (t DepType) Label() string {
	switch t {
	case Production:
		return "dependencies"
	case Dev:
		return "devDependencies"
	case Optional:
		return "optionalDependencies"
	}
	panic("invalid dependency type")
}

type DirectDep struct {
	Name, DepPath string
	Type          DepType
	Specifier     *string
}
type PeerMeta struct{ Optional bool }
type CatalogEntry struct{ Specifier, Version string }
type Settings struct{ AutoInstallPeers, ExcludeLinksFromLockfile, IncludeTarballURL bool }
type Set map[string]struct{}

func (s Set) Has(key string) bool { _, ok := s[key]; return ok }
func (s Set) Add(key string) bool {
	if s.Has(key) {
		return false
	}
	s[key] = struct{}{}
	return true
}
func (s Set) Sorted() []string { return slices.Sorted(maps.Keys(s)) }

type RuntimeTarget struct {
	OS, CPU string
	Libc    *string
}
type RuntimeVariant struct {
	Targets                 []RuntimeTarget
	Archive, URL, Integrity string
	Bin                     map[string]string
	BinIsBareString         bool
	Prefix                  *string
}
type RuntimePin struct {
	Specifier, Version string
	Dev, HasBin        bool
	Variants           []RuntimeVariant
}

type Graph struct {
	Importers                                    map[string][]DirectDep
	Packages                                     map[string]*Package
	Settings                                     Settings
	Overrides                                    map[string]string
	PackageExtensionsChecksum, PnpmfileChecksum  *string
	IgnoredOptionalDependencies                  Set
	Times                                        map[string]string
	SkippedOptionalDependencies                  map[string]map[string]string
	Catalogs                                     map[string]map[string]CatalogEntry
	BunConfigVersion                             *uint32
	PatchedDependencies, PatchedDependencyHashes map[string]string
	TrustedDependencies                          []string
	// Runtime pins are preserved metadata. Provisioning is outside the Go PM.
	Runtimes             map[string]RuntimePin
	ExtraFields          map[string]*jsonvalue.Value
	WorkspaceExtraFields map[string]map[string]*jsonvalue.Value
}

// Dependencies contain either a full graph key or its name-relative tail.
// Active optional dependencies may also be present in Dependencies; Berry
// readers retain them separately, so callers choose which edges to traverse.
type Package struct {
	Name, Version, DepPath                               string
	Integrity                                            *string
	Dependencies, OptionalDependencies, PeerDependencies map[string]string
	PeerDependenciesMeta                                 map[string]PeerMeta
	Source                                               *Source
	OS, CPU, Libc, BundledDependencies                   []string
	TarballURL                                           *string
	RegistryGitHosted, ForceTarballURL                   bool
	AliasOf, YarnChecksum                                *string
	Engines, Bin, DeclaredDependencies                   map[string]string
	License, FundingURL                                  *string
	Optional                                             bool
	TransitivePeerDependencies                           []string
	ExtraMeta                                            map[string]*jsonvalue.Value
	HasInstallScript, HasShrinkwrap, InBundle            bool
	Deprecated                                           *string
}

func NewGraph() *Graph {
	return &Graph{Importers: map[string][]DirectDep{}, Packages: map[string]*Package{}, Settings: Settings{AutoInstallPeers: true}}
}
func NewPackage(name, version string) *Package {
	return &Package{Name: name, Version: version, DepPath: name + "@" + version, Dependencies: map[string]string{}, OptionalDependencies: map[string]string{}, PeerDependencies: map[string]string{}, PeerDependenciesMeta: map[string]PeerMeta{}}
}
func (p *Package) RegistryName() string {
	if p.AliasOf != nil {
		return *p.AliasOf
	}
	return p.Name
}
func (p *Package) SpecKey() string { return p.Name + "@" + p.Version }
func LookupPatch[V any](p *Package, values map[string]V) (string, V, bool) {
	key := p.SpecKey()
	if value, ok := values[key]; ok {
		return key, value, true
	}
	key = p.RegistryName() + "@" + p.Version
	value, ok := values[key]
	return key, value, ok
}
func (p *Package) SourceApprovalKey() (string, bool) {
	if p.Source == nil {
		return "", false
	}
	return p.RegistryName() + "@" + p.Source.Specifier(), true
}
func (p *Package) GitRepositoryApprovalKey() (string, bool) {
	if p.Source == nil || p.Source.Kind != Git {
		return "", false
	}
	return p.RegistryName() + "@git+" + strings.TrimPrefix(p.Source.URL, "git+"), true
}
func (p *Package) DeclaredPeers() map[string]string {
	peers := maps.Clone(p.PeerDependencies)
	if peers == nil {
		peers = map[string]string{}
	}
	for name := range p.PeerDependenciesMeta {
		if _, ok := peers[name]; !ok {
			peers[name] = "*"
		}
	}
	return peers
}
func (g *Graph) RootDeps() []DirectDep { return g.Importers["."] }
func (g *Graph) Child(name, tail string) (string, bool) {
	return ResolveEdge(name, tail, func(key string) bool { _, ok := g.Packages[key]; return ok })
}

func LinkFromImporter(importer, target string) string {
	if importer == "." {
		return target
	}
	parts := func(s string) []string {
		out := []string{}
		for _, p := range strings.Split(s, "/") {
			if p != "" && p != "." {
				out = append(out, p)
			}
		}
		return out
	}
	from, to := parts(importer), parts(target)
	common := 0
	for common < min(len(from), len(to)) && from[common] == to[common] {
		common++
	}
	out := make([]string, len(from)-common)
	for i := range out {
		out[i] = ".."
	}
	out = append(out, to[common:]...)
	if len(out) == 0 {
		return "."
	}
	return strings.Join(out, "/")
}

func (g *Graph) Reachable(roots []string, includeOptional bool) Set {
	visited := Set{}
	queue := []string{}
	for _, root := range roots {
		if visited.Add(root) {
			queue = append(queue, root)
		}
	}
	for next := 0; next < len(queue); next++ {
		pkg := g.Packages[queue[next]]
		if pkg == nil {
			continue
		}
		visit := func(edges map[string]string) {
			for name, tail := range edges {
				if key, ok := g.Child(name, tail); ok && visited.Add(key) {
					queue = append(queue, key)
				}
			}
		}
		visit(pkg.Dependencies)
		if includeOptional {
			visit(pkg.OptionalDependencies)
		}
	}
	return visited
}
func (g *Graph) ImporterClosure(seeds []string) Set {
	parents := map[string][]string{}
	for parent, pkg := range g.Packages {
		for name, tail := range pkg.Dependencies {
			if child, ok := g.Child(name, tail); ok {
				parents[child] = append(parents[child], parent)
			}
		}
	}
	visited := Set{}
	queue := []string{}
	for _, seed := range seeds {
		if visited.Add(seed) {
			queue = append(queue, seed)
		}
	}
	for next := 0; next < len(queue); next++ {
		for _, parent := range parents[queue[next]] {
			if visited.Add(parent) {
				queue = append(queue, parent)
			}
		}
	}
	return visited
}
func (g *Graph) DependencyDepths() map[string]int {
	depths := map[string]int{}
	queue := []string{}
	for _, deps := range g.Importers {
		for _, dep := range deps {
			if _, ok := g.Packages[dep.DepPath]; ok {
				if _, seen := depths[dep.DepPath]; !seen {
					depths[dep.DepPath] = 0
					queue = append(queue, dep.DepPath)
				}
			}
		}
	}
	for next := 0; next < len(queue); next++ {
		key := queue[next]
		pkg := g.Packages[key]
		depth := depths[key]
		visit := func(edges map[string]string) {
			for name, tail := range edges {
				child, ok := g.Child(name, tail)
				if !ok {
					continue
				}
				if _, seen := depths[child]; !seen {
					depths[child] = depth + 1
					queue = append(queue, child)
				}
			}
		}
		visit(pkg.Dependencies)
		visit(pkg.OptionalDependencies)
	}
	return depths
}
func (g *Graph) FilterDeps(keep func(DirectDep) bool) *Graph {
	out := g.Clone()
	var roots []string
	for importer, deps := range out.Importers {
		filtered := make([]DirectDep, 0, len(deps))
		for _, dep := range deps {
			if keep(dep) {
				filtered = append(filtered, dep)
				roots = append(roots, dep.DepPath)
			}
		}
		out.Importers[importer] = filtered
	}
	reachable := g.Reachable(roots, false)
	for key := range out.Packages {
		if !reachable.Has(key) {
			delete(out.Packages, key)
		}
	}
	return out
}
func (g *Graph) SubsetToImporter(importer string, keep func(DirectDep) bool) (*Graph, bool) {
	deps, ok := g.Importers[importer]
	if !ok {
		return nil, false
	}
	out := g.Clone()
	out.Importers = map[string][]DirectDep{".": nil}
	var roots []string
	for _, dep := range deps {
		if keep(dep) {
			dep.Specifier = clonePtr(dep.Specifier)
			out.Importers["."] = append(out.Importers["."], dep)
			roots = append(roots, dep.DepPath)
		}
	}
	reachable := g.Reachable(roots, false)
	for key := range out.Packages {
		if !reachable.Has(key) {
			delete(out.Packages, key)
		}
	}
	out.SkippedOptionalDependencies = map[string]map[string]string{}
	if skipped, ok := g.SkippedOptionalDependencies[importer]; ok {
		out.SkippedOptionalDependencies["."] = maps.Clone(skipped)
	}
	return out, true
}
