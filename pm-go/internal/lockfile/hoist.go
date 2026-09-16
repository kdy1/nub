package lockfile

import (
	"maps"
	"slices"
	"strconv"
	"strings"
)

// CanonicalPackages chooses the first graph entry for each name/version,
// matching the peer-context collapse used by npm and Bun lockfile writers.
func (g *Graph) CanonicalPackages() map[string]*Package {
	out := map[string]*Package{}
	for _, key := range slices.Sorted(maps.Keys(g.Packages)) {
		p := g.Packages[key]
		if _, exists := out[p.SpecKey()]; !exists {
			out[p.SpecKey()] = p
		}
	}
	return out
}
func CanonicalKey(key string) string { key, _, _ = strings.Cut(key, "("); return key }
func ChildCanonicalKey(name, value string) string {
	value = CanonicalKey(value)
	if strings.HasPrefix(value, name+"@") {
		return value
	}
	return name + "@" + value
}
func DependencyVersion(name, value string) string {
	return strings.TrimPrefix(CanonicalKey(value), name+"@")
}

type Placement struct {
	Segments []string
	Key      string
}

func (p Placement) InstallPath() string {
	if len(p.Segments) == 0 {
		return ""
	}
	return "node_modules/" + strings.Join(p.Segments, "/node_modules/")
}
func segmentKey(segs []string) string {
	var b strings.Builder
	for _, seg := range segs {
		b.WriteString(strconv.Itoa(len(seg)))
		b.WriteByte(':')
		b.WriteString(seg)
	}
	return b.String()
}

// ReachableCanonical traverses dependency edges and optionally omits dev,
// optional, or peer-only paths. Dev applies only to importer roots.
func ReachableCanonical(canonical map[string]*Package, roots []DirectDep, exclude []DepType, crossPeers bool) Set {
	visited := Set{}
	var queue []string
	for _, dep := range roots {
		if slices.Contains(exclude, dep.Type) {
			continue
		}
		key := CanonicalKey(dep.DepPath)
		if canonical[key] != nil && visited.Add(key) {
			queue = append(queue, key)
		}
	}
	for next := 0; next < len(queue); next++ {
		p := canonical[queue[next]]
		for name, tail := range p.Dependencies {
			if _, optional := p.OptionalDependencies[name]; optional && slices.Contains(exclude, Optional) {
				continue
			}
			if !crossPeers {
				_, peer := p.PeerDependencies[name]
				_, declared := p.DeclaredDependencies[name]
				if peer && !declared {
					continue
				}
			}
			key := ChildCanonicalKey(name, tail)
			if canonical[key] != nil && visited.Add(key) {
				queue = append(queue, key)
			}
		}
	}
	return visited
}

// HoistTree places roots first, then traverses their dependencies in breadth
// first order. The nearest conflicting ancestor forces a nested placement,
// even when the root has the requested version. Preferred roots are retained
// only while reachable and cannot override an explicit root dependency.
func HoistTree(canonical map[string]*Package, roots []DirectDep, preferred map[string]string) []Placement {
	placed := map[string]Placement{}
	queue := []Placement{}
	place := func(segs []string, key string) {
		id := segmentKey(segs)
		_, exists := placed[id]
		p := Placement{Segments: slices.Clone(segs), Key: key}
		placed[id] = p
		if !exists {
			queue = append(queue, p)
		}
	}
	for _, dep := range roots {
		key := CanonicalKey(dep.DepPath)
		if canonical[key] != nil {
			place([]string{dep.Name}, key)
		}
	}
	if preferred != nil {
		reachable := ReachableCanonical(canonical, roots, nil, true)
		for _, name := range slices.Sorted(maps.Keys(preferred)) {
			key := preferred[name]
			if _, exists := placed[segmentKey([]string{name})]; !exists && reachable.Has(key) {
				place([]string{name}, key)
			}
		}
	}
	for next := 0; next < len(queue); next++ {
		parent := queue[next]
		p := canonical[parent.Key]
		if p == nil {
			continue
		}
		for _, name := range slices.Sorted(maps.Keys(p.Dependencies)) {
			key := ChildCanonicalKey(name, p.Dependencies[name])
			if canonical[key] == nil {
				continue
			}
			hit, matches := false, false
			for i := len(parent.Segments); i >= 0; i-- {
				candidate := append(slices.Clone(parent.Segments[:i]), name)
				if existing, exists := placed[segmentKey(candidate)]; exists {
					hit, matches = true, existing.Key == key
					break
				}
			}
			if matches {
				continue
			}
			if hit {
				place(append(slices.Clone(parent.Segments), name), key)
			} else {
				place([]string{name}, key)
			}
		}
	}
	out := slices.Collect(maps.Values(placed))
	slices.SortFunc(out, func(a, b Placement) int { return slices.Compare(a.Segments, b.Segments) })
	return out
}
