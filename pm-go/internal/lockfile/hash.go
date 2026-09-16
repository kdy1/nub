package lockfile

import (
	"encoding/hex"
	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
	"lukechampine.com/blake3"
	"slices"
	"strings"
)

type HashOptions struct {
	AllowBuild func(*Package) bool
	Engine     *string
	Patch      func(name, version string) *string
	Content    func(depPath string) *string
}
type GraphHashes map[string]string

func (hashes GraphHashes) DepPath(path string) string {
	if hash, ok := hashes[path]; ok {
		if len(hash) > 16 {
			hash = hash[:16]
		}
		return path + "-" + hash
	}
	return path
}
func hashJSON(value *jsonvalue.Value) [32]byte {
	data, err := value.MarshalJSON()
	if err != nil {
		panic(err)
	}
	return blake3.Sum256(data)
}
func hashHex(value *jsonvalue.Value) string {
	sum := hashJSON(value)
	return hex.EncodeToString(sum[:])
}
func hashStringMap(values map[string]string) *jsonvalue.Value {
	out := jsonvalue.Object()
	for _, key := range driftKeys(values) {
		out.Put(key, jsonvalue.String(values[key]))
	}
	return out
}
func (g *Graph) ComputeHashes(options HashOptions) GraphHashes {
	builds := Set{}
	for key, p := range g.Packages {
		if options.AllowBuild != nil && options.AllowBuild(p) {
			builds.Add(key)
		}
	}
	cache := map[string]string{}
	var visit func(string, Set) string
	visit = func(key string, parents Set) string {
		if value, ok := cache[key]; ok {
			return value
		}
		if !parents.Add(key) {
			return ""
		}
		value := ""
		if p := g.Packages[key]; p != nil {
			id := fullPackageID(key, p, options)
			deps := map[string]string{}
			for _, name := range driftKeys(p.Dependencies) {
				if child, ok := g.Child(name, p.Dependencies[name]); ok {
					deps[name] = visit(child, parents)
				}
			}
			input := jsonvalue.Object()
			input.Put("id", jsonvalue.String(id))
			input.Put("deps", hashStringMap(deps))
			value = hashHex(input)
		}
		delete(parents, key)
		cache[key] = value
		return value
	}
	for _, key := range driftKeys(g.Packages) {
		visit(key, Set{})
	}
	tainted := map[string]bool{}
	var needsBuild func(string, Set) bool
	needsBuild = func(key string, parents Set) bool {
		if value, ok := tainted[key]; ok {
			return value
		}
		if builds.Has(key) {
			tainted[key] = true
			return true
		}
		if !parents.Add(key) {
			return false
		}
		value := false
		if p := g.Packages[key]; p != nil {
			for _, name := range driftKeys(p.Dependencies) {
				if child, ok := g.Child(name, p.Dependencies[name]); ok && needsBuild(child, parents) {
					value = true
					break
				}
			}
		}
		delete(parents, key)
		tainted[key] = value
		return value
	}
	for _, key := range driftKeys(g.Packages) {
		needsBuild(key, Set{})
	}
	out := GraphHashes{}
	for _, key := range driftKeys(g.Packages) {
		input := jsonvalue.Object()
		engine := jsonvalue.Null()
		if options.Engine != nil && tainted[key] {
			engine = jsonvalue.String(*options.Engine)
		}
		input.Put("engine", engine)
		input.Put("deps", jsonvalue.String(cache[key]))
		out[key] = hashHex(input)
	}
	return out
}
func fullPackageID(key string, p *Package, options HashOptions) string {
	id := p.Name + "@" + p.Version
	var patch *string
	if options.Patch != nil {
		patch = options.Patch(p.Name, p.Version)
		if patch == nil && p.RegistryName() != p.Name {
			patch = options.Patch(p.RegistryName(), p.Version)
		}
	}
	if patch != nil {
		id += ":patch:" + *patch
	}
	if p.Source != nil {
		id += ":source:" + p.Source.Specifier()
	}
	if options.Content != nil {
		if content := options.Content(key); content != nil {
			id += ":content:" + *content
		}
	}
	integrity := "<no-integrity>"
	if p.Integrity != nil {
		integrity = *p.Integrity
	}
	return id + ":" + integrity
}

// IdentityHash is host-independent and preserves importer section/specifier
// intent. Presentation-only fields are excluded just as in the Rust engine.
func (g *Graph) IdentityHash(patch func(name, version string) *string) [32]byte {
	hashes := g.ComputeHashes(HashOptions{Patch: patch})
	input := jsonvalue.Object()
	input.Put("nodes", hashStringMap(hashes))
	importers := jsonvalue.Object()
	for _, path := range driftKeys(g.Importers) {
		deps := slices.Clone(g.Importers[path])
		slices.SortStableFunc(deps, func(a, b DirectDep) int {
			for _, pair := range [][2]string{{a.Name, b.Name}, {a.DepPath, b.DepPath}, {a.Type.Label(), b.Type.Label()}} {
				if c := strings.Compare(pair[0], pair[1]); c != 0 {
					return c
				}
			}
			if a.Specifier == nil {
				if b.Specifier == nil {
					return 0
				}
				return -1
			}
			if b.Specifier == nil {
				return 1
			}
			return strings.Compare(*a.Specifier, *b.Specifier)
		})
		edges := &jsonvalue.Value{Kind: '['}
		for _, dep := range deps {
			edge := jsonvalue.Object()
			edge.Put("name", jsonvalue.String(dep.Name))
			edge.Put("dep_path", jsonvalue.String(dep.DepPath))
			edge.Put("dep_type", jsonvalue.String(dep.Type.Label()))
			spec := jsonvalue.Null()
			if dep.Specifier != nil {
				spec = jsonvalue.String(*dep.Specifier)
			}
			edge.Put("specifier", spec)
			edges.Array = append(edges.Array, edge)
		}
		importers.Put(path, edges)
	}
	input.Put("importers", importers)
	return hashJSON(input)
}

// ContentAffected finds source-backed nodes and every ancestor, including
// cycles, so prewarm does not cache them before materialized bytes are known.
func (g *Graph) ContentAffected() Set {
	parents := map[string][]string{}
	var stack []string
	for key, p := range g.Packages {
		if p.Source != nil && p.Source.GloballyShareable() {
			stack = append(stack, key)
		}
		for name, value := range p.Dependencies {
			if child, ok := g.Child(name, value); ok {
				parents[child] = append(parents[child], key)
			}
		}
	}
	out := Set{}
	for len(stack) > 0 {
		key := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if out.Add(key) {
			stack = append(stack, parents[key]...)
		}
	}
	return out
}
