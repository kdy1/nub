package resolver

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/identity"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/semver"
)

type PeerContextOptions struct {
	DedupePeerDependents, DedupePeers, ResolveFromWorkspaceRoot bool
	PeersSuffixMaxLength                                        int
}

func DefaultPeerContextOptions() PeerContextOptions {
	return PeerContextOptions{DedupePeerDependents: true, ResolveFromWorkspaceRoot: true, PeersSuffixMaxLength: 1000}
}

type PeerContextDivergence struct{ Iterations int }

func (e *PeerContextDivergence) Error() string {
	return fmt.Sprintf("peer-context fixed-point did not converge after %d iterations; lockfile would be incomplete", e.Iterations)
}
func (e *PeerContextDivergence) Code() string { return "ERR_AUBE_PEER_CONTEXT_NOT_CONVERGED" }

// ApplyPeerContexts returns an independent graph with contextual package keys
// and peer edges. Convergence includes dependency tails, not only package keys.
func ApplyPeerContexts(input *lockfile.Graph, options PeerContextOptions) (*lockfile.Graph, error) {
	current := input.Clone()
	limit := max(len(current.Packages)+1, 16)
	before := peerGraphState(current)
	seen := map[[32]byte]bool{before: true}
	converged := false
	for iteration := 0; iteration < limit; iteration++ {
		next := applyPeerContextsOnce(current, options)
		if options.DedupePeerDependents {
			dedupePeerVariants(next)
		}
		after := peerGraphState(next)
		if before == after {
			current = next
			converged = true
			break
		}
		if seen[after] {
			return nil, &PeerContextDivergence{iteration + 1}
		}
		seen[after] = true
		current = next
		before = after
	}
	if !converged {
		return nil, &PeerContextDivergence{limit}
	}
	propagatePeerSuffixes(current, options)
	if options.DedupePeers {
		dedupePeerSuffixes(current)
	}
	return current, nil
}
func peerGraphState(g *lockfile.Graph) [32]byte {
	var tokens []string
	for _, key := range peerKeys(g.Packages) {
		tokens = append(tokens, key, "\x1f")
		for _, name := range peerKeys(g.Packages[key].Dependencies) {
			tokens = append(tokens, name, g.Packages[key].Dependencies[name])
		}
		tokens = append(tokens, "\x1e")
	}
	data, _ := json.Marshal(tokens)
	return sha256.Sum256(data)
}

func PeerPassForImport(input *lockfile.Graph, kind identity.Kind) (*lockfile.Graph, error) {
	if kind != identity.Npm && kind != identity.Shrinkwrap && kind != identity.Bun {
		return input.Clone(), nil
	}
	g := input.Clone()
	hoisted := HoistAutoInstalledPeers(g)
	out, err := ApplyPeerContexts(g, DefaultPeerContextOptions())
	if err != nil {
		return nil, err
	}
	RemoveAutoInstalledPeers(out, hoisted)
	return out, nil
}

type peerProvider struct{ targetTail, contextTail string }
type peerScope map[string]peerProvider

func providerFor(g *lockfile.Graph, name, tail string) peerProvider {
	provider := peerProvider{tail, tail}
	if key, ok := g.Child(name, tail); ok {
		p := g.Packages[key]
		if p.Source != nil && (p.Source.Kind == lockfile.Directory || p.Source.Kind == lockfile.Link || p.Source.Kind == lockfile.Portal) {
			provider.contextTail = p.Version
		}
	}
	return provider
}
func scopeFromDeps(g *lockfile.Graph, deps []lockfile.DirectDep) peerScope {
	scope := peerScope{}
	for _, d := range deps {
		tail := d.DepPath
		if rest, ok := strings.CutPrefix(tail, d.Name+"@"); ok && rest != "" {
			tail = rest
		}
		scope[d.Name] = providerFor(g, d.Name, tail)
	}
	return scope
}

type peerPass struct {
	graph   *lockfile.Graph
	index   map[string][]*lockfile.Package
	root    peerScope
	out     map[string]*lockfile.Package
	options PeerContextOptions
}

func applyPeerContextsOnce(g *lockfile.Graph, options PeerContextOptions) *lockfile.Graph {
	pass := peerPass{graph: g, index: map[string][]*lockfile.Package{}, root: scopeFromDeps(g, g.Importers["."]), out: map[string]*lockfile.Package{}, options: options}
	for _, key := range peerKeys(g.Packages) {
		p := g.Packages[key]
		pass.index[p.Name] = append(pass.index[p.Name], p)
	}
	importers := map[string][]lockfile.DirectDep{}
	for _, path := range peerKeys(g.Importers) {
		deps := g.Importers[path]
		scope := scopeFromDeps(g, deps)
		newDeps := make([]lockfile.DirectDep, 0, len(deps))
		for _, d := range deps {
			if target, ok := pass.visit(d.DepPath, scope, lockfile.Set{}); ok {
				d.DepPath = target
			}
			newDeps = append(newDeps, d)
		}
		importers[path] = newDeps
	}
	// The caller owns g; all untouched graph metadata carries through.
	g.Importers = importers
	g.Packages = pass.out
	return g
}
func (pass *peerPass) resolve(p *lockfile.Package, name, requested string, ancestor peerScope) (peerProvider, bool) {
	satisfies := func(provider peerProvider) bool {
		return semver.EngineSatisfies(canonicalTail(provider.contextTail), requested)
	}
	a, hasA := ancestor[name]
	var own peerProvider
	tail, hasOwn := p.Dependencies[name]
	if hasOwn {
		own = providerFor(pass.graph, name, tail)
	}
	r, hasRoot := pass.root[name]
	hasRoot = hasRoot && pass.options.ResolveFromWorkspaceRoot
	if hasA && satisfies(a) {
		return a, true
	}
	if hasOwn && satisfies(own) {
		return own, true
	}
	if !p.PeerDependenciesMeta[name].Optional {
		if hasA {
			return a, true
		}
		if hasOwn {
			return own, true
		}
	}
	if hasRoot && satisfies(r) {
		return r, true
	}
	if p.PeerDependenciesMeta[name].Optional {
		return peerProvider{}, false
	}
	var highest *semver.Version
	var selected peerProvider
	for _, candidate := range pass.index[name] {
		if !semver.EngineSatisfies(candidate.Version, requested) {
			continue
		}
		v, err := semver.ParseEngineVersion(candidate.Version)
		if err != nil || highest != nil && v.Compare(highest) < 0 {
			continue
		}
		tail, ok := strings.CutPrefix(candidate.DepPath, candidate.Name+"@")
		if !ok {
			tail = candidate.Version
		}
		highest = v
		selected = providerFor(pass.graph, name, tail)
	}
	if highest != nil {
		return selected, true
	}
	return r, hasRoot
}
func (pass *peerPass) visit(input string, ancestor peerScope, visiting lockfile.Set) (string, bool) {
	p := pass.graph.Packages[input]
	if p == nil {
		return "", false
	}
	base := canonicalTail(input)
	peers := peerScope{}
	var suffix strings.Builder
	for _, name := range peerKeys(p.PeerDependencies) {
		provider, ok := pass.resolve(p, name, p.PeerDependencies[name], ancestor)
		if !ok {
			continue
		}
		peers[name] = provider
		display := provider.contextTail
		if containsCanonicalBackRef(display, base) {
			display = canonicalTail(display)
		}
		suffix.WriteString("(" + name + "@" + display + ")")
	}
	target := base + effectivePeerSuffix(suffix.String(), pass.options.PeersSuffixMaxLength)
	if pass.out[target] != nil || visiting.Has(target) {
		return target, true
	}
	visiting.Add(target)
	childScope := maps.Clone(ancestor)
	for name, tail := range p.Dependencies {
		childScope[name] = providerFor(pass.graph, name, tail)
	}
	for name, provider := range peers {
		childScope[name] = provider
	}
	deps := map[string]string{}
	for _, name := range peerKeys(p.Dependencies) {
		tail := p.Dependencies[name]
		if provider, ok := peers[name]; ok {
			tail = provider.targetTail
		}
		if child, ok := pass.visit(name+"@"+tail, childScope, visiting); ok {
			if newTail, ok := strings.CutPrefix(child, name+"@"); ok {
				tail = newTail
			}
		}
		deps[name] = tail
	}
	for _, name := range peerKeys(peers) {
		if _, ok := deps[name]; ok {
			continue
		}
		tail := peers[name].targetTail
		if child, ok := pass.visit(name+"@"+tail, childScope, visiting); ok {
			if newTail, ok := strings.CutPrefix(child, name+"@"); ok {
				tail = newTail
			}
			deps[name] = tail
		}
	}
	delete(visiting, target)
	optional := map[string]string{}
	for name := range p.OptionalDependencies {
		if tail, ok := deps[name]; ok {
			optional[name] = tail
		}
	}
	clone := p.Clone()
	clone.DepPath = target
	clone.Dependencies = deps
	clone.OptionalDependencies = optional
	pass.out[target] = clone
	return target, true
}
