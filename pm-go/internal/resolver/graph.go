package resolver

import "github.com/nubjs/nub/pm-go/internal/lockfile"

// FilterGraph prunes optional edges, then unreachable packages. Platform
// constraints never prune a required edge, even if another path is optional.
func FilterGraph(graph *lockfile.Graph, host Platform, supported Architectures, ignored lockfile.Set) {
	mismatched := lockfile.Set{}
	for key, pkg := range graph.Packages {
		if !Supported(pkg.OS, pkg.CPU, pkg.Libc, host, supported) {
			mismatched.Add(key)
		}
	}
	for importer, deps := range graph.Importers {
		retained := make([]lockfile.DirectDep, 0, len(deps))
		for _, dep := range deps {
			if dep.Type == lockfile.Optional && (ignored.Has(dep.Name) || mismatched.Has(dep.DepPath)) {
				continue
			}
			retained = append(retained, dep)
		}
		graph.Importers[importer] = retained
	}
	for _, pkg := range graph.Packages {
		for name, tail := range pkg.OptionalDependencies {
			child, ok := graph.Child(name, tail)
			if ignored.Has(name) || ok && mismatched.Has(child) {
				delete(pkg.OptionalDependencies, name)
				delete(pkg.Dependencies, name)
			}
		}
		for _, name := range pkg.BundledDependencies {
			delete(pkg.Dependencies, name)
			delete(pkg.OptionalDependencies, name)
		}
	}
	var roots []string
	for _, deps := range graph.Importers {
		for _, dep := range deps {
			roots = append(roots, dep.DepPath)
		}
	}
	reachable := graph.Reachable(roots, true)
	for key := range graph.Packages {
		if !reachable.Has(key) {
			delete(graph.Packages, key)
		}
	}
}

func OptionalOnlyPackages(graph *lockfile.Graph) lockfile.Set {
	required := lockfile.Set{}
	queue := []string{}
	for _, deps := range graph.Importers {
		for _, dep := range deps {
			if dep.Type != lockfile.Optional && required.Add(dep.DepPath) {
				queue = append(queue, dep.DepPath)
			}
		}
	}
	for next := 0; next < len(queue); next++ {
		pkg := graph.Packages[queue[next]]
		if pkg == nil {
			continue
		}
		for name, tail := range pkg.Dependencies {
			if _, optional := pkg.OptionalDependencies[name]; optional {
				continue
			}
			if child, ok := graph.Child(name, tail); ok && required.Add(child) {
				queue = append(queue, child)
			}
		}
	}
	optional := lockfile.Set{}
	for key := range graph.Packages {
		if !required.Has(key) {
			optional.Add(key)
		}
	}
	return optional
}
func MarkOptionalPackages(graph *lockfile.Graph) {
	optional := OptionalOnlyPackages(graph)
	for key, pkg := range graph.Packages {
		pkg.Optional = optional.Has(key)
	}
}

func MarkTransitivePeers(graph *lockfile.Graph) {
	parents := map[string]lockfile.Set{}
	unresolved := map[string]lockfile.Set{}
	for key, pkg := range graph.Packages {
		visit := func(edges map[string]string) {
			for name, tail := range edges {
				if _, peer := pkg.PeerDependencies[name]; peer {
					continue
				}
				if _, peer := pkg.PeerDependenciesMeta[name]; peer {
					continue
				}
				if child, ok := graph.Child(name, tail); ok {
					if parents[child] == nil {
						parents[child] = lockfile.Set{}
					}
					parents[child].Add(key)
				}
			}
		}
		visit(pkg.Dependencies)
		visit(pkg.OptionalDependencies)
		for name := range pkg.DeclaredPeers() {
			if _, resolved := pkg.Dependencies[name]; !resolved {
				if unresolved[key] == nil {
					unresolved[key] = lockfile.Set{}
				}
				unresolved[key].Add(name)
			}
		}
	}
	accumulated := map[string]lockfile.Set{}
	for origin, peers := range unresolved {
		visited := lockfile.Set{origin: {}}
		queue := parents[origin].Sorted()
		for next := 0; next < len(queue); next++ {
			key := queue[next]
			if !visited.Add(key) {
				continue
			}
			if accumulated[key] == nil {
				accumulated[key] = lockfile.Set{}
			}
			for peer := range peers {
				accumulated[key].Add(peer)
			}
			queue = append(queue, parents[key].Sorted()...)
		}
	}
	for key, pkg := range graph.Packages {
		pkg.TransitivePeerDependencies = accumulated[key].Sorted()
	}
}
