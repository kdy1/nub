package linker

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
)

type HoistingLimits uint8

const (
	HoistNone HoistingLimits = iota
	HoistWorkspaces
	HoistDependencies
)

type hoistedNode struct {
	pkgDir, modulesDir, key string
	parent                  int // -1 is an independent importer root
	children                map[string]int
}

type hoistedTree struct {
	nodes     []hoistedNode
	importers []int
}

type hoistRequest struct {
	requester, floor int
	name, key        string
}

type importerSeed struct {
	modules string
	deps    []lockfile.DirectDep
}

func newHoistedTree(modules string) *hoistedTree {
	return &hoistedTree{nodes: []hoistedNode{{modulesDir: modules, parent: -1, children: map[string]int{}}}, importers: []int{0}}
}

func (p *hoistedTree) addPackage(parent int, name, key string) int {
	dir := filepath.Join(p.nodes[parent].modulesDir, filepath.FromSlash(name))
	idx := len(p.nodes)
	p.nodes = append(p.nodes, hoistedNode{pkgDir: dir, modulesDir: filepath.Join(dir, "node_modules"), key: key, parent: parent, children: map[string]int{}})
	p.nodes[parent].children[name] = idx
	return idx
}

func (p *hoistedTree) place(r hoistRequest) (int, bool, error) {
	if err := ValidatePackageLinkName(r.name); err != nil {
		return 0, false, err
	}
	// Reuse the nearest visible matching identity, including above a hoist
	// boundary. A nearer conflicting name prevents looking past that node.
	for cursor := r.requester; cursor >= 0; cursor = p.nodes[cursor].parent {
		if existing, ok := p.nodes[cursor].children[r.name]; ok {
			if p.nodes[existing].key == r.key {
				return existing, false, nil
			}
			break
		}
	}
	candidate := r.requester
	for cursor := r.requester; cursor >= 0; cursor = p.nodes[cursor].parent {
		if _, occupied := p.nodes[cursor].children[r.name]; occupied {
			break
		}
		candidate = cursor
		if cursor == r.floor {
			break
		}
	}
	if _, occupied := p.nodes[candidate].children[r.name]; occupied {
		return 0, false, fmt.Errorf("conflicting hoisted placement for %s at %s", r.name, p.nodes[candidate].modulesDir)
	}
	return p.addPackage(candidate, r.name, r.key), true, nil
}

func enqueueHoisted(queue []hoistRequest, node, floor int, pkg *lockfile.Package, graph *lockfile.Graph) []hoistRequest {
	if pkg == nil || pkg.Source != nil && pkg.Source.Kind == lockfile.Link {
		return queue
	}
	for _, name := range slices.Sorted(maps.Keys(pkg.Dependencies)) {
		if key, ok := graph.Child(name, pkg.Dependencies[name]); ok {
			queue = append(queue, hoistRequest{node, floor, name, key})
		}
	}
	return queue
}

func preferredHoistedVersions(root, other []lockfile.DirectDep, graph *lockfile.Graph) map[string]string {
	counts := map[string]map[string]uint64{}
	seen := lockfile.Set{}
	var queue []string
	count := func(name, key string, weight uint64) {
		if graph.Packages[key] == nil {
			return
		}
		if counts[name] == nil {
			counts[name] = map[string]uint64{}
		}
		counts[name][key] += weight
		if seen.Add(key) {
			queue = append(queue, key)
		}
	}
	for _, dep := range root {
		count(dep.Name, dep.DepPath, 1<<60)
	}
	for _, dep := range other {
		count(dep.Name, dep.DepPath, 1<<40)
	}
	for head := 0; head < len(queue); head++ {
		pkg := graph.Packages[queue[head]]
		if pkg.Source != nil && pkg.Source.Kind == lockfile.Link {
			continue
		}
		for _, name := range slices.Sorted(maps.Keys(pkg.Dependencies)) {
			if key, ok := graph.Child(name, pkg.Dependencies[name]); ok {
				count(name, key, 1)
			}
		}
	}
	out := map[string]string{}
	for name, versions := range counts {
		if len(versions) < 2 {
			continue
		}
		best := ""
		for key, count := range versions {
			if best == "" || count > versions[best] || count == versions[best] && key > best {
				best = key
			}
		}
		out[name] = best
	}
	return out
}

func (p *hoistedTree) preplace(root, other []lockfile.DirectDep, graph *lockfile.Graph) ([]hoistRequest, error) {
	winners := preferredHoistedVersions(root, other, graph)
	var queue []hoistRequest
	for _, name := range slices.Sorted(maps.Keys(winners)) {
		if err := ValidatePackageLinkName(name); err != nil {
			return nil, err
		}
		key := winners[name]
		node := p.addPackage(0, name, key)
		queue = enqueueHoisted(queue, node, 0, graph.Packages[key], graph)
	}
	return queue, nil
}

func planHoistedImporter(seed importerSeed, graph *lockfile.Graph, limits HoistingLimits) (*hoistedTree, error) {
	p := newHoistedTree(seed.modules)
	var queue []hoistRequest
	if limits != HoistDependencies {
		var err error
		queue, err = p.preplace(seed.deps, nil, graph)
		if err != nil {
			return nil, err
		}
	}
	for _, dep := range seed.deps {
		if graph.Packages[dep.DepPath] != nil {
			queue = append(queue, hoistRequest{0, 0, dep.Name, dep.DepPath})
		}
	}
	for head := 0; head < len(queue); head++ {
		r := queue[head]
		node, created, err := p.place(r)
		if err != nil {
			return nil, err
		}
		if created {
			floor := 0
			if limits == HoistDependencies {
				floor = node
			}
			queue = enqueueHoisted(queue, node, floor, graph.Packages[r.key], graph)
		}
	}
	return p, nil
}

func planHoistedWorkspace(seeds []importerSeed, graph *lockfile.Graph) (*hoistedTree, error) {
	p := newHoistedTree(seeds[0].modules)
	root := filepath.Dir(seeds[0].modules)
	var memberDeps []lockfile.DirectDep
	reachable := []bool{true}
	for _, seed := range seeds[1:] {
		within := insidePath(root, filepath.Dir(seed.modules), true)
		parent := -1
		if within {
			parent = 0
			memberDeps = append(memberDeps, seed.deps...)
		}
		reachable = append(reachable, within)
		p.importers = append(p.importers, len(p.nodes))
		p.nodes = append(p.nodes, hoistedNode{modulesDir: seed.modules, parent: parent, children: map[string]int{}})
	}
	queue, err := p.preplace(seeds[0].deps, memberDeps, graph)
	if err != nil {
		return nil, err
	}
	for i, seed := range seeds {
		for _, dep := range seed.deps {
			pkg := graph.Packages[dep.DepPath]
			if pkg == nil {
				continue
			}
			floor := 0
			if !reachable[i] || pkg.Source != nil && pkg.Source.Kind == lockfile.Link {
				floor = p.importers[i]
			}
			queue = append(queue, hoistRequest{p.importers[i], floor, dep.Name, dep.DepPath})
		}
	}
	for head := 0; head < len(queue); head++ {
		r := queue[head]
		node, created, err := p.place(r)
		if err != nil {
			return nil, err
		}
		if created {
			queue = enqueueHoisted(queue, node, r.floor, graph.Packages[r.key], graph)
		}
	}
	return p, nil
}

func hoistedPlans(project, modules string, graph *lockfile.Graph, workspace bool, limits HoistingLimits) ([]*hoistedTree, error) {
	if !filepath.IsAbs(project) || graph == nil || limits > HoistDependencies {
		return nil, fmt.Errorf("hoisted planning requires an absolute project, graph and valid hoisting limit")
	}
	if modules == "" {
		modules = "node_modules"
	}
	rootNM, err := CheckedModulesDir(project, modules)
	if err != nil {
		return nil, err
	}
	seeds := []importerSeed{{rootNM, graph.RootDeps()}}
	if workspace {
		for _, key := range slices.Sorted(maps.Keys(graph.Importers)) {
			if key == "." || !IsPhysicalImporter(key) {
				continue
			}
			nm, err := CheckedModulesDir(sourcePath(project, key), modules)
			if err != nil {
				return nil, err
			}
			seeds = append(seeds, importerSeed{nm, graph.Importers[key]})
		}
	}
	if workspace && limits == HoistNone && len(seeds) > 1 {
		plan, err := planHoistedWorkspace(seeds, graph)
		return []*hoistedTree{plan}, err
	}
	var plans []*hoistedTree
	for _, seed := range seeds {
		plan, err := planHoistedImporter(seed, graph, limits)
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

func (p *hoistedTree) byDepth() [][]int {
	depths := make([]int, len(p.nodes))
	var levels [][]int
	for idx, node := range p.nodes {
		if node.parent >= 0 {
			depths[idx] = depths[node.parent] + 1
		}
		if node.pkgDir == "" {
			continue
		}
		for len(levels) <= depths[idx] {
			levels = append(levels, nil)
		}
		levels[depths[idx]] = append(levels[depths[idx]], idx)
	}
	return levels
}
