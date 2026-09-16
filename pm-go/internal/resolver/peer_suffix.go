package resolver

import (
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"slices"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
)

func peerKeys[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }

func hashedPeerSuffix(s string) bool {
	if len(s) != 34 || s[0] != '(' || s[33] != ')' {
		return false
	}
	for _, c := range []byte(s[1:33]) {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func effectivePeerSuffix(s string, maxLength int) string {
	body := s
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		body = s[1 : len(s)-1]
	}
	if len(body) <= maxLength {
		return s
	}
	h := sha256.Sum256([]byte(body))
	return "(" + hex.EncodeToString(h[:16]) + ")"
}
func containsCanonicalBackRef(value, canonical string) bool {
	if canonical == "" {
		return false
	}
	for start := 0; start < len(value); {
		i := strings.Index(value[start:], "("+canonical)
		if i < 0 {
			return false
		}
		i += start + 1 + len(canonical)
		if i == len(value) || value[i] == '(' || value[i] == ')' {
			return true
		}
		start = i
	}
	return false
}
func outerPeerSegments(s string) []string {
	var segments []string
	for i := 0; i < len(s); {
		if s[i] != '(' {
			i++
			continue
		}
		start, depth := i, 0
		for i < len(s) {
			if s[i] == '(' {
				depth++
			} else if s[i] == ')' {
				depth--
			}
			i++
			if depth == 0 {
				segments = append(segments, s[start:i])
				break
			}
		}
		if depth != 0 {
			break
		}
	}
	return segments
}
func peerNameFromSegment(s string) (string, bool) {
	inner, ok := strings.CutPrefix(s, "(")
	if !ok {
		return "", false
	}
	head := canonicalTail(inner)
	if i := strings.LastIndexByte(head, '@'); i >= 0 {
		return head[:i], true
	}
	return "", false
}
func peerNamesRecursive(segments []string) lockfile.Set {
	names := lockfile.Set{}
	for _, s := range segments {
		if name, ok := peerNameFromSegment(s); ok {
			names.Add(name)
		}
		if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
			inner := s[1 : len(s)-1]
			if i := strings.IndexByte(inner, '('); i >= 0 {
				for name := range peerNamesRecursive(outerPeerSegments(inner[i:])) {
					names.Add(name)
				}
			}
		}
	}
	return names
}
func dedupePeerKey(key string) string {
	parts := strings.Split(key, "(")
	for i := 1; i < len(parts); i++ {
		part := parts[i]
		at := strings.LastIndexByte(part, '@')
		close := strings.IndexAny(part, ")(")
		if at >= 0 && (close < 0 || at < close) {
			parts[i] = part[at+1:]
		}
	}
	return strings.Join(parts, "(")
}

// Variant dedupe only rewrites required edge maps, matching the reference pass.
// Later propagation/dedupe passes rewrite both required and optional edge maps.
func dedupePeerVariants(g *lockfile.Graph) {
	groups := map[string][]string{}
	for _, key := range peerKeys(g.Packages) {
		base := canonicalTail(key)
		groups[base] = append(groups[base], key)
	}
	rewrite := map[string]string{}
	for _, keys := range groups {
		if len(keys) < 2 {
			continue
		}
		parent := make([]int, len(keys))
		for i := range parent {
			parent[i] = i
		}
		var find func(int) int
		find = func(i int) int {
			if parent[i] != i {
				parent[i] = find(parent[i])
			}
			return parent[i]
		}
		for i, a := range keys {
			for j := i + 1; j < len(keys); j++ {
				pa, pb := g.Packages[a], g.Packages[keys[j]]
				if pa.Version != pb.Version {
					continue
				}
				names := lockfile.Set{}
				for n := range pa.PeerDependencies {
					names.Add(n)
				}
				for n := range pb.PeerDependencies {
					names.Add(n)
				}
				equivalent := true
				for name := range names {
					va, oka := pa.Dependencies[name]
					vb, okb := pb.Dependencies[name]
					if oka != okb || oka && canonicalTail(va) != canonicalTail(vb) {
						equivalent = false
						break
					}
				}
				if equivalent {
					parent[find(i)] = find(j)
				}
			}
		}
		reps := map[int]string{}
		for i, key := range keys {
			r := find(i)
			if _, ok := reps[r]; !ok {
				reps[r] = key
			}
		}
		for i, key := range keys {
			if target := reps[find(i)]; target != key {
				rewrite[key] = target
			}
		}
	}
	if len(rewrite) == 0 {
		return
	}
	for key, p := range g.Packages {
		if _, ok := rewrite[key]; ok {
			delete(g.Packages, key)
			continue
		}
		for name, tail := range p.Dependencies {
			if target, ok := rewrite[name+"@"+tail]; ok {
				p.Dependencies[name] = strings.TrimPrefix(target, name+"@")
			}
		}
	}
	rewritePeerImporters(g, rewrite, nil)
}
func rewritePeerImporters(g *lockfile.Graph, rewrite map[string]string, fallback func(string) string) {
	for path, deps := range g.Importers {
		for i := range deps {
			if target, ok := rewrite[deps[i].DepPath]; ok {
				deps[i].DepPath = target
			} else if fallback != nil {
				deps[i].DepPath = fallback(deps[i].DepPath)
			}
		}
		g.Importers[path] = deps
	}
}
func rewritePeerEdges(edges map[string]string, rewrite map[string]string, fallback func(string) string) {
	for name, tail := range edges {
		if target, ok := rewrite[name+"@"+tail]; ok {
			if value, ok := strings.CutPrefix(target, name+"@"); ok {
				edges[name] = value
			}
		} else if fallback != nil {
			edges[name] = fallback(tail)
		}
	}
}
func dedupePeerSuffixes(g *lockfile.Graph) {
	rewrite := map[string]string{}
	counts := map[string]int{}
	for key := range g.Packages {
		target := dedupePeerKey(key)
		rewrite[key] = target
		counts[target]++
	}
	for key, target := range rewrite {
		if counts[target] > 1 {
			rewrite[key] = key
		}
	}
	out := map[string]*lockfile.Package{}
	for _, key := range peerKeys(g.Packages) {
		p := g.Packages[key]
		target := rewrite[key]
		rewritePeerEdges(p.Dependencies, rewrite, dedupePeerKey)
		rewritePeerEdges(p.OptionalDependencies, rewrite, dedupePeerKey)
		p.DepPath = target
		out[target] = p
	}
	g.Packages = out
	rewritePeerImporters(g, rewrite, dedupePeerKey)
}

func propagatePeerSuffixes(g *lockfile.Graph, options PeerContextOptions) {
	forward := map[string][]string{}
	for key, p := range g.Packages {
		for _, name := range peerKeys(p.Dependencies) {
			child := name + "@" + p.Dependencies[name]
			if g.Packages[child] != nil {
				forward[key] = append(forward[key], child)
			}
		}
	}
	cumulative := map[string]map[string]string{}
	visiting := lockfile.Set{}
	var collect func(string) map[string]string
	collect = func(key string) map[string]string {
		if cached, ok := cumulative[key]; ok {
			return cached
		}
		if !visiting.Add(key) {
			return nil
		}
		segments := outerPeerSegments(key)
		acc := map[string]string{}
		for _, seg := range segments {
			if name, ok := peerNameFromSegment(seg); ok {
				if _, ok := acc[name]; !ok {
					acc[name] = seg
				}
			}
		}
		suppressed := peerNamesRecursive(segments)
		addName := func(key string) {
			base := canonicalTail(key)
			if i := strings.LastIndexByte(base, '@'); i > 0 {
				suppressed.Add(base[:i])
			}
		}
		addName(key)
		for _, child := range forward[key] {
			addName(child)
		}
		for _, child := range forward[key] {
			for name, seg := range collect(child) {
				if !suppressed.Has(name) {
					if _, ok := acc[name]; !ok {
						acc[name] = seg
					}
				}
			}
		}
		delete(visiting, key)
		cumulative[key] = acc
		return acc
	}
	keys := peerKeys(g.Packages)
	for _, key := range keys {
		collect(key)
	}
	for _, path := range peerKeys(g.Importers) {
		for _, dep := range g.Importers[path] {
			collect(dep.DepPath)
		}
	}
	rewrite := map[string]string{}
	for _, key := range keys {
		p := g.Packages[key]
		if p.Source != nil && p.Source.GloballyShareable() {
			continue
		}
		base := canonicalTail(key)
		if hashedPeerSuffix(key[len(base):]) {
			continue
		}
		var suffix strings.Builder
		for _, name := range peerKeys(cumulative[key]) {
			suffix.WriteString(cumulative[key][name])
		}
		target := base + effectivePeerSuffix(suffix.String(), options.PeersSuffixMaxLength)
		if target != key {
			rewrite[key] = target
		}
	}
	if len(rewrite) == 0 {
		return
	}
	out := map[string]*lockfile.Package{}
	for _, key := range keys {
		p := g.Packages[key]
		target, ok := rewrite[key]
		if !ok {
			target = key
		}
		rewritePeerEdges(p.Dependencies, rewrite, nil)
		rewritePeerEdges(p.OptionalDependencies, rewrite, nil)
		p.DepPath = target
		if out[target] == nil {
			out[target] = p
		}
	}
	g.Packages = out
	rewritePeerImporters(g, rewrite, nil)
}
