package npm

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/fsutil"
	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/manifest"
)

// Write emits npm's v3 format atomically, using an existing file only as a
// best-effort hoist preference. Reading remains responsible for parse errors.
func Write(path string, g *lockfile.Graph, project *manifest.Package) error {
	existing, _ := os.ReadFile(path)
	data, err := Encode(g, project, filepath.Dir(path), existing)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return fsutil.WriteDefault(path, data)
}

// Encode takes the project root explicitly for fresh workspace identities.
// It does not mutate the graph, manifest, or existing lockfile.
func Encode(g *lockfile.Graph, project *manifest.Package, projectDir string, existing []byte) ([]byte, error) {
	if project == nil {
		project = &manifest.Package{}
	}
	canonical := g.CanonicalPackages()
	for _, key := range sorted(g.Packages) {
		p := g.Packages[key]
		if p.Source != nil && (p.Source.Kind == lockfile.Git || p.Source.Kind == lockfile.RemoteTarball) {
			key := lockfile.CanonicalKey(p.DepPath)
			if _, exists := canonical[key]; !exists {
				canonical[key] = p
			}
		}
	}
	linked := map[string]*lockfile.Package{}
	memberManifests := map[string]*manifest.Package{}
	for _, importer := range sorted(g.Importers) {
		if importer == "." {
			continue
		}
		for _, key := range sorted(g.Packages) {
			p := g.Packages[key]
			if p.Source != nil && p.Source.Kind == lockfile.Link && samePath(p.Source.Path, importer) {
				linked[importer] = p
				break
			}
		}
		if linked[importer] == nil {
			m, err := manifest.ReadPackage(filepath.Join(projectDir, filepath.FromSlash(importer), "package.json"))
			if err != nil {
				m = &manifest.Package{}
			}
			memberManifests[importer] = m
		}
	}
	synthesizedPeer := func(importer string, dep lockfile.DirectDep) bool {
		if dep.Type != lockfile.Production {
			return false
		}
		var peerRange string
		var peer, declared bool
		m := project
		if importer != "." {
			if p := linked[importer]; p != nil {
				peerRange, peer = p.PeerDependencies[dep.Name]
				_, ordinary := p.DeclaredDependencies[dep.Name]
				_, optional := p.OptionalDependencies[dep.Name]
				return !ordinary && !optional && peer && dep.Specifier != nil && *dep.Specifier == peerRange
			}
			m = memberManifests[importer]
		}
		if m == nil {
			return false
		}
		peerRange, peer = m.PeerDependencies[dep.Name]
		_, prod := m.Dependencies[dep.Name]
		_, dev := m.DevDependencies[dep.Name]
		_, opt := m.OptionalDependencies[dep.Name]
		declared = prod || dev || opt
		return !declared && peer && dep.Specifier != nil && *dep.Specifier == peerRange
	}
	var allRoots, declaredRoots []lockfile.DirectDep
	for _, importer := range sorted(g.Importers) {
		for _, dep := range g.Importers[importer] {
			allRoots = append(allRoots, dep)
			if !synthesizedPeer(importer, dep) {
				declaredRoots = append(declaredRoots, dep)
			}
		}
	}
	anyReach := lockfile.ReachableCanonical(canonical, allRoots, nil, true)
	nonDev := lockfile.ReachableCanonical(canonical, allRoots, []lockfile.DepType{lockfile.Dev}, true)
	nonOpt := lockfile.ReachableCanonical(canonical, allRoots, []lockfile.DepType{lockfile.Optional}, true)
	prodReach := lockfile.ReachableCanonical(canonical, allRoots, []lockfile.DepType{lockfile.Dev, lockfile.Optional}, true)
	nonPeer := lockfile.ReachableCanonical(canonical, declaredRoots, nil, false)
	roots := nonLinkRoots(g, g.RootDeps())
	preferred := preferredRoots(existing, canonical)
	rootNames := lockfile.Set{}
	for _, dep := range roots {
		rootNames.Add(dep.Name)
	}
	for _, importer := range sorted(g.Importers) {
		if importer == "." {
			continue
		}
		for _, dep := range nonLinkRoots(g, g.Importers[importer]) {
			if preferred[dep.Name] == lockfile.CanonicalKey(dep.DepPath) && rootNames.Add(dep.Name) {
				roots = append(roots, dep)
			}
		}
	}
	placed := map[string]string{}
	for _, p := range lockfile.HoistTree(canonical, roots, preferred) {
		placed[p.InstallPath()] = p.Key
	}
	license, bin := rootMetadata(project)
	packages := map[string]*writePackage{"": {rawPackage: rawPackage{
		name: project.Name, version: project.Version, license: license, bin: bin, engines: project.Engines, workspaces: project.Workspaces,
		dependencies: project.Dependencies, devDependencies: project.DevDependencies, optionalDependencies: project.OptionalDependencies, peerDependencies: project.PeerDependencies,
	}}}
	emitFileLinks(g, g.RootDeps(), ".", nil, packages)
	claimed := lockfile.Set{}
	for path := range placed {
		if name, ok := strings.CutPrefix(path, "node_modules/"); ok && !strings.Contains(name, "/node_modules/") {
			claimed.Add(name)
		}
	}
	for _, importer := range sorted(g.Importers) {
		if p := linked[importer]; p != nil {
			claimed.Add(p.Name)
		} else if m := memberManifests[importer]; m != nil && m.Name != nil {
			claimed.Add(*m.Name)
		}
	}
	for _, importer := range sorted(g.Importers) {
		if importer == "." {
			continue
		}
		deps := g.Importers[importer]
		var name, version *string
		var peers map[string]string
		if p := linked[importer]; p != nil {
			name, version, peers = &p.Name, &p.Version, p.PeerDependencies
		} else if m := memberManifests[importer]; m != nil {
			name, version, peers = m.Name, m.Version, m.PeerDependencies
		}
		if name == nil {
			continue
		}
		sections := directSections(deps)
		for _, dep := range deps {
			if synthesizedPeer(importer, dep) {
				delete(sections[0], dep.Name)
			}
		}
		entryName := name
		dirname := importer
		if i := strings.LastIndexByte(dirname, '/'); i >= 0 {
			dirname = dirname[i+1:]
		}
		if *name == dirname {
			entryName = nil
		}
		packages[importer] = &writePackage{rawPackage: rawPackage{name: entryName, version: version, dependencies: sections[0], devDependencies: sections[1], optionalDependencies: sections[2], peerDependencies: peers}}
		target := importer
		packages["node_modules/"+*name] = &writePackage{rawPackage: rawPackage{resolved: &target, link: true}}
		emitFileLinks(g, deps, importer, claimed, packages)
		tree := lockfile.HoistTree(canonical, nonLinkRoots(g, deps), nil)
		redundant := lockfile.Set{}
		for _, p := range tree {
			if len(p.Segments) == 1 && placed["node_modules/"+p.Segments[0]] == p.Key {
				redundant.Add(p.Segments[0])
			}
		}
		for _, p := range tree {
			if redundant.Has(p.Segments[0]) {
				continue
			}
			path := importer + "/" + p.InstallPath()
			if _, exists := placed[path]; !exists {
				placed[path] = p.Key
			}
		}
	}
	for _, path := range sorted(placed) {
		key := placed[path]
		p := canonical[key]
		if p == nil {
			continue
		}
		deps, opt := map[string]string{}, map[string]string{}
		for i, section := range []map[string]string{p.Dependencies, p.OptionalDependencies} {
			for name, tail := range section {
				_, peer := p.PeerDependencies[name]
				_, declared := p.DeclaredDependencies[name]
				_, optional := p.OptionalDependencies[name]
				if peer && !declared || i == 0 && optional || canonical[lockfile.ChildCanonicalKey(name, tail)] == nil {
					continue
				}
				rendered, ok := p.DeclaredDependencies[name]
				if !ok {
					rendered = lockfile.DependencyVersion(name, tail)
				}
				if i == 0 {
					deps[name] = rendered
				} else {
					opt[name] = rendered
				}
			}
		}
		isReach := anyReach.Has(key)
		dev, optFlag := isReach && !nonDev.Has(key), isReach && !nonOpt.Has(key)
		devOpt := isReach && !prodReach.Has(key)
		bins := maps.Clone(p.Bin)
		delete(bins, "")
		packages[path] = &writePackage{rawPackage: rawPackage{
			name: p.AliasOf, version: &p.Version, resolved: resolvedField(p), integrity: p.Integrity, license: p.License,
			dependencies: deps, optionalDependencies: opt, peerDependencies: p.PeerDependencies, peerMeta: p.PeerDependenciesMeta,
			bin: bins, engines: p.Engines, os: p.OS, cpu: p.CPU, libc: p.Libc, funding: p.FundingURL, bundled: p.BundledDependencies, deprecated: p.Deprecated,
			hasInstallScript: p.HasInstallScript, hasShrinkwrap: p.HasShrinkwrap, inBundle: p.InBundle,
		}, dev: dev && !optFlag, optional: optFlag && !dev, devOptional: dev && optFlag || devOpt && !dev && !optFlag, peer: isReach && !nonPeer.Has(key)}
	}
	doc := jsonvalue.Object()
	putString(doc, "name", project.Name)
	putString(doc, "version", project.Version)
	doc.Put("lockfileVersion", &jsonvalue.Value{Kind: 'd', Scalar: json.Number("3")})
	doc.Put("requires", boolValue(true))
	entries := jsonvalue.Object()
	for _, key := range sorted(packages) {
		entries.Put(key, packages[key].json())
	}
	doc.Put("packages", entries)
	return doc.Pretty()
}

func samePath(a, b string) bool {
	// Rust Path equality removes redundant separators and interior dots,
	// but preserves parent components and a leading relative dot.
	components := func(s string) string {
		if runtime.GOOS == "windows" {
			s = strings.ReplaceAll(s, "\\", "/")
		}
		prefix := ""
		if strings.HasPrefix(s, "/") {
			prefix = "/"
		}
		var parts []string
		for _, p := range strings.Split(s, "/") {
			if p == "" || p == "." && (len(parts) != 0 || prefix != "") {
				continue
			}
			parts = append(parts, p)
		}
		return prefix + strings.Join(parts, "/")
	}
	return components(a) == components(b)
}
func nonLinkRoots(g *lockfile.Graph, roots []lockfile.DirectDep) []lockfile.DirectDep {
	out := []lockfile.DirectDep{}
	for _, dep := range roots {
		p := g.Packages[dep.DepPath]
		if p != nil && p.Source != nil && (p.Source.Kind == lockfile.Link || p.Source.Kind == lockfile.Directory || p.Source.Kind == lockfile.Tarball) {
			continue
		}
		out = append(out, dep)
	}
	return out
}
func preferredRoots(data []byte, canonical map[string]*lockfile.Package) map[string]string {
	out := map[string]string{}
	v, err := jsonvalue.Parse(data)
	if err != nil {
		return out
	}
	r, err := parseRaw(v)
	if err != nil {
		return out
	}
	for _, path := range sorted(r.packages) {
		p := r.packages[path]
		if p.link || p.version == nil {
			continue
		}
		rest, ok := strings.CutPrefix(path, "node_modules/")
		if !ok || strings.Contains(rest, "/node_modules/") {
			continue
		}
		name, ok := packageName(path)
		if !ok {
			continue
		}
		key := name + "@" + *p.version
		if canonical[key] != nil {
			out[name] = key
		}
	}
	return out
}
func directSections(deps []lockfile.DirectDep) [3]map[string]string {
	out := [3]map[string]string{{}, {}, {}}
	for _, dep := range deps {
		rendered := lockfile.DependencyVersion(dep.Name, strings.TrimPrefix(dep.DepPath, dep.Name+"@"))
		if dep.Specifier != nil {
			rendered = *dep.Specifier
		}
		out[dep.Type][dep.Name] = rendered
	}
	return out
}
func emitFileLinks(g *lockfile.Graph, roots []lockfile.DirectDep, importer string, claimed lockfile.Set, packages map[string]*writePackage) {
	for _, dep := range roots {
		p := g.Packages[dep.DepPath]
		if p == nil || p.Source == nil {
			continue
		}
		s := p.Source
		if s.Kind == lockfile.Link {
			if _, member := g.Importers[s.Path]; member {
				continue
			}
		}
		if s.Kind != lockfile.Directory && s.Kind != lockfile.Tarball && s.Kind != lockfile.Link {
			continue
		}
		resolved := strings.TrimPrefix(s.PathPOSIX(), "./")
		packages[resolved] = &writePackage{rawPackage: rawPackage{name: &p.Name, version: &p.Version}}
		root := importer == "." || importer == ""
		key := "node_modules/" + dep.Name
		taken := !root && claimed.Has(dep.Name)
		if existing := packages[key]; existing != nil {
			taken = existing.resolved == nil || *existing.resolved != resolved
		}
		if taken && !root {
			key = importer + "/" + key
		}
		packages[key] = &writePackage{rawPackage: rawPackage{resolved: &resolved, link: true}}
	}
}
