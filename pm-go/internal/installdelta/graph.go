package installdelta

import (
	"encoding/hex"
	"slices"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"lukechampine.com/blake3"
)

type componentGraph struct {
	members           [][]string
	indices           map[string]int
	children, parents []map[int]bool
}

// components uses iterative Tarjan traversal, including unreachable packages.
// Child order matches the reference's name-sorted dependency map.
func components(g *lockfile.Graph) componentGraph {
	nodes := keys(g.Packages)
	indices := map[string]int{}
	for i, key := range nodes {
		indices[key] = i
	}
	index := make([]int, len(nodes))
	low := make([]int, len(nodes))
	on := make([]bool, len(nodes))
	for i := range index {
		index[i] = -1
	}
	children := make([][]int, len(nodes))
	for i, key := range nodes {
		for _, name := range keys(g.Packages[key].Dependencies) {
			if child, ok := g.Child(name, g.Packages[key].Dependencies[name]); ok {
				children[i] = append(children[i], indices[child])
			}
		}
	}
	cursor := make([]int, len(nodes))
	stack := []int{}
	next := 0
	out := componentGraph{indices: map[string]int{}}
	enter := func(v int) { index[v] = next; low[v] = next; next++; stack = append(stack, v); on[v] = true }
	for start := range nodes {
		if index[start] != -1 {
			continue
		}
		calls := []int{start}
		enter(start)
		for len(calls) > 0 {
			v := calls[len(calls)-1]
			if cursor[v] < len(children[v]) {
				w := children[v][cursor[v]]
				cursor[v]++
				if index[w] == -1 {
					enter(w)
					calls = append(calls, w)
				} else if on[w] {
					low[v] = min(low[v], index[w])
				}
				continue
			}
			if low[v] == index[v] {
				members := []string{}
				for {
					w := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					on[w] = false
					members = append(members, nodes[w])
					out.indices[nodes[w]] = len(out.members)
					if w == v {
						break
					}
				}
				slices.Sort(members)
				out.members = append(out.members, members)
			}
			calls = calls[:len(calls)-1]
			if len(calls) > 0 {
				parent := calls[len(calls)-1]
				low[parent] = min(low[parent], low[v])
			}
		}
	}
	out.children = make([]map[int]bool, len(out.members))
	out.parents = make([]map[int]bool, len(out.members))
	for i := range out.members {
		out.children[i] = map[int]bool{}
		out.parents[i] = map[int]bool{}
	}
	for parent, children := range children {
		from := out.indices[nodes[parent]]
		for _, child := range children {
			to := out.indices[nodes[child]]
			if from != to {
				out.children[from][to] = true
				out.parents[to][from] = true
			}
		}
	}
	return out
}

// SubtreeHashes collapses cycles, then hashes each component's sorted leaves
// and child digests. A changed descendant invalidates every affected ancestor.
func SubtreeHashes(g *lockfile.Graph, leaf map[string]string) map[string]string {
	c := components(g)
	remaining := make([]int, len(c.members))
	ready := []int{}
	hashes := make([]string, len(c.members))
	for i, children := range c.children {
		remaining[i] = len(children)
		if len(children) == 0 {
			ready = append(ready, i)
		}
	}
	for len(ready) > 0 {
		i := ready[len(ready)-1]
		ready = ready[:len(ready)-1]
		h := blake3.New(32, nil)
		h.Write([]byte("scc"))
		length(h, len(c.members[i]))
		for _, m := range c.members[i] {
			if value, ok := leaf[m]; ok {
				field(h, "leaf", []byte(value))
			}
		}
		children := []string{}
		for child := range c.children[i] {
			children = append(children, hashes[child])
		}
		slices.Sort(children)
		length(h, len(children))
		for _, child := range children {
			field(h, "child", []byte(child))
		}
		hashes[i] = hex.EncodeToString(h.Sum(nil))
		for parent := range c.parents[i] {
			remaining[parent]--
			if remaining[parent] == 0 {
				ready = append(ready, parent)
			}
		}
	}
	out := map[string]string{}
	for key, i := range c.indices {
		out[key] = hashes[i]
	}
	return out
}

// BuildPhases retains non-building bridges between selected builds. Unrelated
// dependency depth cannot serialize independent builds; cycles share a phase.
func BuildPhases(g *lockfile.Graph, selected lockfile.Set) [][]string {
	c := components(g)
	relevant := map[int]bool{}
	pending := []int{}
	for key := range selected {
		if i, ok := c.indices[key]; ok {
			pending = append(pending, i)
		}
	}
	for len(pending) > 0 {
		i := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if relevant[i] {
			continue
		}
		relevant[i] = true
		for parent := range c.parents[i] {
			pending = append(pending, parent)
		}
	}
	remaining := make([]int, len(c.members))
	ready := map[int]bool{}
	for i, children := range c.children {
		for child := range children {
			if relevant[child] {
				remaining[i]++
			}
		}
		if relevant[i] && remaining[i] == 0 {
			ready[i] = true
		}
	}
	phases := [][]string{}
	for len(ready) > 0 {
		current := ready
		ready = map[int]bool{}
		selectedHere := []string{}
		for i := range current {
			for _, key := range c.members[i] {
				if selected.Has(key) {
					selectedHere = append(selectedHere, key)
				}
			}
			for parent := range c.parents[i] {
				remaining[parent]--
				if remaining[parent] == 0 {
					ready[parent] = true
				}
			}
		}
		if len(selectedHere) > 0 {
			slices.Sort(selectedHere)
			phases = append(phases, selectedHere)
		}
	}
	return phases
}
