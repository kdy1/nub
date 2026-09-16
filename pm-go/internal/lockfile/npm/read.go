package npm

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/manifest"
	"github.com/nubjs/nub/pm-go/internal/workspace"
)

type Warning struct{ Code, Message string }
type installInfo struct{ name, depPath string }

func Read(path string, project *manifest.Package) (*lockfile.Graph, []Warning, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	g, warnings, err := Parse(data, project)
	if err != nil {
		return nil, warnings, fmt.Errorf("%s: %w", path, err)
	}
	return g, warnings, nil
}

// Parse accepts npm v1/v2/v3 and pre-versioned shrinkwraps. The manifest
// supplies legacy direct dependency sections and fallback workspace patterns.
func Parse(data []byte, project *manifest.Package) (*lockfile.Graph, []Warning, error) {
	v, err := jsonvalue.Parse(data)
	if err != nil {
		return nil, nil, err
	}
	raw, err := parseRaw(v)
	if err != nil {
		return nil, nil, err
	}
	if project == nil {
		project = &manifest.Package{}
	}
	var warnings []Warning
	if raw.version < 2 {
		legacy, err := parseLegacy(v.Get("dependencies"))
		if err != nil {
			return nil, nil, err
		}
		raw, warnings = liftLegacy(legacy, project)
	}
	normalizePaths(raw)
	graph, err := readGraph(raw, project)
	return graph, warnings, err
}

func sorted[V any](values map[string]V) []string { return slices.Sorted(maps.Keys(values)) }
func value(p *string, fallback string) string {
	if p != nil {
		return *p
	}
	return fallback
}
func httpURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

func canonicalPath(path string) string {
	outside := strings.HasPrefix(path, "../") || strings.HasPrefix(path, "/") || strings.HasPrefix(path, "\\\\") || len(path) > 1 && path[1] == ':'
	if !strings.HasPrefix(path, "node_modules/") && outside {
		if i := strings.Index(path, "node_modules/"); i >= 0 {
			return path[i:]
		}
	}
	return path
}
func normalizePaths(raw *rawLock) {
	canonical := map[string]*rawPackage{}
	for _, key := range sorted(raw.packages) {
		p := raw.packages[key]
		if p.link && p.resolved != nil {
			s := canonicalPath(*p.resolved)
			p.resolved = &s
		}
		key = canonicalPath(key)
		if _, exists := canonical[key]; !exists {
			canonical[key] = p
		}
	}
	raw.packages = canonical
}

func packageName(path string) (string, bool) {
	i := strings.LastIndex(path, "node_modules/")
	if i < 0 {
		return "", false
	}
	tail := path[i+len("node_modules/"):]
	if tail == "" {
		return "", false
	}
	if strings.HasPrefix(tail, "@") {
		i := strings.IndexByte(tail, '/')
		if i < 0 {
			return "", false
		}
		if j := strings.IndexByte(tail[i+1:], '/'); j >= 0 {
			return tail[:i+1+j], true
		}
		return tail, true
	}
	name, _, _ := strings.Cut(tail, "/")
	return name, true
}
func resolveNested(base, dep string, paths map[string]installInfo) (installInfo, bool) {
	for {
		candidate := "node_modules/" + dep
		if base != "" {
			candidate = base + "/" + candidate
		}
		if info, ok := paths[candidate]; ok {
			return info, true
		}
		if base == "" {
			return installInfo{}, false
		}
		if i := strings.LastIndex(base, "/node_modules/"); i >= 0 {
			base = base[:i]
		} else if strings.HasPrefix(base, "node_modules/") {
			base = ""
		} else if i := strings.LastIndexByte(base, '/'); i >= 0 {
			base = base[:i]
		} else {
			base = ""
		}
	}
}
func (info installInfo) tail() string { return strings.TrimPrefix(info.depPath, info.name+"@") }

func localSource(entry *rawPackage, remoteSpecs lockfile.Set) *lockfile.Source {
	if entry.resolved == nil {
		return nil
	}
	r := *entry.resolved
	if git, ok := lockfile.ParseGit(r); ok && git.Committish != nil {
		git.Resolved = *git.Committish
		return &git
	}
	if path, ok := strings.CutPrefix(r, "file:"); ok {
		kind := lockfile.Directory
		if lockfile.LooksLikeTarball(path) {
			kind = lockfile.Tarball
		}
		return &lockfile.Source{Kind: kind, Path: path}
	}
	if remoteSpecs.Has(r) {
		return &lockfile.Source{Kind: lockfile.RemoteTarball, URL: r, Integrity: entry.integrity}
	}
	return nil
}

func readGraph(raw *rawLock, project *manifest.Package) (*lockfile.Graph, error) {
	g := lockfile.NewGraph()
	remoteSpecs := lockfile.Set{}
	for _, entry := range raw.packages {
		for _, section := range []map[string]string{entry.dependencies, entry.devDependencies, entry.optionalDependencies} {
			for _, spec := range section {
				if httpURL(spec) {
					remoteSpecs.Add(spec)
				}
			}
		}
	}
	pointerTarget := func(entry *rawPackage) (string, bool) {
		if !entry.link || entry.resolved == nil {
			return "", false
		}
		target := *entry.resolved
		if !slices.Contains(strings.Split(target, "/"), "node_modules") {
			return "", false
		}
		p := raw.packages[target]
		return target, p != nil && !p.link && p.version != nil
	}
	linkTargets := lockfile.Set{}
	for _, entry := range raw.packages {
		if _, pointer := pointerTarget(entry); !pointer && entry.link && entry.resolved != nil {
			linkTargets.Add(*entry.resolved)
		}
	}
	paths := map[string]installInfo{}
	pointers := map[string]string{}
	keys := sorted(raw.packages)
	for _, path := range keys {
		if path == "" || linkTargets.Has(path) {
			continue
		}
		entry := raw.packages[path]
		name, ok := packageName(path)
		if !ok {
			if entry.name == nil {
				return nil, fmt.Errorf("could not determine package name for '%s'", path)
			}
			name = *entry.name
		}
		var alias *string
		if entry.name != nil && *entry.name != name {
			alias = entry.name
		}
		if target, pointer := pointerTarget(entry); pointer {
			pointers[path] = target
			continue
		}
		var source *lockfile.Source
		packageEntry := entry
		version := ""
		if entry.link {
			if entry.resolved == nil {
				return nil, fmt.Errorf("linked package '%s' has no resolved target", name)
			}
			packageEntry = raw.packages[*entry.resolved]
			if packageEntry == nil {
				return nil, fmt.Errorf("linked package '%s' points to missing target '%s'", name, *entry.resolved)
			}
			version = value(packageEntry.version, "0.0.0")
			source = &lockfile.Source{Kind: lockfile.Link, Path: *entry.resolved}
		} else {
			if entry.version == nil {
				return nil, fmt.Errorf("package '%s' has no version", name)
			}
			version = *entry.version
			source = localSource(entry, remoteSpecs)
		}
		depPath := name + "@" + version
		if source != nil {
			depPath = source.DepPath(name)
		}
		paths[path] = installInfo{name, depPath}
		if _, exists := g.Packages[depPath]; exists {
			continue
		}
		p := lockfile.NewPackage(name, version)
		p.DepPath, p.Source, p.AliasOf, p.Integrity = depPath, source, alias, packageEntry.integrity
		p.DeclaredDependencies = maps.Clone(packageEntry.dependencies)
		if p.DeclaredDependencies == nil {
			p.DeclaredDependencies = map[string]string{}
		}
		maps.Copy(p.DeclaredDependencies, packageEntry.optionalDependencies)
		p.PeerDependencies, p.PeerDependenciesMeta = maps.Clone(packageEntry.peerDependencies), maps.Clone(packageEntry.peerMeta)
		p.OS, p.CPU, p.Libc = slices.Clone(packageEntry.os), slices.Clone(packageEntry.cpu), slices.Clone(packageEntry.libc)
		p.Engines, p.Bin = maps.Clone(packageEntry.engines), maps.Clone(packageEntry.bin)
		p.License, p.FundingURL, p.Deprecated = packageEntry.license, packageEntry.funding, packageEntry.deprecated
		p.HasInstallScript, p.HasShrinkwrap, p.InBundle = packageEntry.hasInstallScript, packageEntry.hasShrinkwrap, packageEntry.inBundle
		p.BundledDependencies = slices.Clone(packageEntry.bundled)
		if source == nil && packageEntry.resolved != nil && httpURL(*packageEntry.resolved) {
			p.TarballURL = packageEntry.resolved
		}
		g.Packages[depPath] = p
	}
	for _, path := range sorted(pointers) {
		if info, exists := paths[pointers[path]]; exists {
			paths[path] = info
		}
	}
	resolved := lockfile.Set{}
	for _, path := range keys {
		if path == "" || linkTargets.Has(path) {
			continue
		}
		info, exists := paths[path]
		if !exists || !resolved.Add(info.depPath) {
			continue
		}
		entry := raw.packages[path]
		lookup := path
		if entry.link {
			lookup = *entry.resolved
			entry = raw.packages[lookup]
		}
		p := g.Packages[info.depPath]
		for _, section := range []struct {
			deps     map[string]string
			optional bool
		}{{entry.dependencies, false}, {entry.optionalDependencies, true}} {
			for _, name := range sorted(section.deps) {
				if target, ok := resolveNested(lookup, name, paths); ok {
					p.Dependencies[name] = target.tail()
					if section.optional {
						p.OptionalDependencies[name] = target.tail()
					}
				}
			}
		}
		// Peers use recorded placement without changing declared ranges.
		for _, name := range sorted(entry.peerDependencies) {
			if _, exists := p.Dependencies[name]; exists {
				continue
			}
			if target, ok := resolveNested(lookup, name, paths); ok {
				p.Dependencies[name] = target.tail()
				if entry.peerMeta[name].Optional {
					p.OptionalDependencies[name] = target.tail()
				}
			}
		}
	}
	root := raw.packages[""]
	if root == nil {
		root = &rawPackage{}
	}
	direct := importerDeps(root, "", paths)
	already := lockfile.Set{}
	for _, dep := range direct {
		already.Add(dep.Name)
	}
	var links []lockfile.DirectDep
	for _, path := range keys {
		if !raw.packages[path].link {
			continue
		}
		rest, ok := strings.CutPrefix(path, "node_modules/")
		if !ok || strings.Contains(rest, "/node_modules/") {
			continue
		}
		expected := 1
		if strings.HasPrefix(rest, "@") {
			expected = 2
		}
		if len(strings.Split(rest, "/")) != expected {
			continue
		}
		if info, exists := paths[path]; exists && !already.Has(info.name) {
			links = append(links, lockfile.DirectDep{Name: info.name, DepPath: info.depPath, Type: lockfile.Production})
		}
	}
	slices.SortStableFunc(links, func(a, b lockfile.DirectDep) int { return strings.Compare(a.Name, b.Name) })
	g.Importers["."] = append(direct, links...)
	w := root.workspaces
	if w == nil {
		w = project.Workspaces
	}
	var patterns []string
	if w != nil {
		patterns = w.Patterns
	}
	for _, target := range linkTargets.Sorted() {
		if target == "" || len(patterns) != 0 && !workspace.MatchesMember(target, patterns) {
			continue
		}
		if entry := raw.packages[target]; entry != nil {
			g.Importers[target] = importerDeps(entry, target, paths)
		}
	}
	return g, nil
}

func importerDeps(entry *rawPackage, base string, paths map[string]installInfo) []lockfile.DirectDep {
	direct := []lockfile.DirectDep{}
	seen := lockfile.Set{}
	for i, section := range []map[string]string{entry.dependencies, entry.devDependencies, entry.optionalDependencies, entry.peerDependencies} {
		for _, name := range sorted(section) {
			kind := lockfile.DepType(i)
			if i == 3 {
				_, prod := entry.dependencies[name]
				_, dev := entry.devDependencies[name]
				_, opt := entry.optionalDependencies[name]
				if prod || dev || opt || entry.peerMeta[name].Optional {
					continue
				}
				kind = lockfile.Production
			}
			if info, ok := resolveNested(base, name, paths); ok && seen.Add(info.name) {
				spec := section[name]
				direct = append(direct, lockfile.DirectDep{Name: info.name, DepPath: info.depPath, Type: kind, Specifier: &spec})
			}
		}
	}
	return direct
}

func liftLegacy(legacy map[string]*legacyDep, project *manifest.Package) (*rawLock, []Warning) {
	r := &rawLock{version: 1, packages: map[string]*rawPackage{"": {dependencies: maps.Clone(project.Dependencies), devDependencies: maps.Clone(project.DevDependencies), optionalDependencies: maps.Clone(project.OptionalDependencies)}}}
	var lift func(map[string]*legacyDep, string)
	lift = func(deps map[string]*legacyDep, prefix string) {
		for _, name := range sorted(deps) {
			d := deps[name]
			path := prefix + "/" + name
			p := &rawPackage{version: d.version, resolved: d.resolved, integrity: d.integrity, dependencies: maps.Clone(d.requires), inBundle: d.bundled}
			if p.dependencies == nil {
				p.dependencies = map[string]string{}
			}
			for _, child := range sorted(d.dependencies) {
				if _, exists := p.dependencies[child]; !exists {
					p.dependencies[child] = "*"
				}
				if d.dependencies[child].bundled {
					p.bundled = append(p.bundled, child)
				}
			}
			r.packages[path] = p
			lift(d.dependencies, path+"/node_modules")
		}
	}
	lift(legacy, "node_modules")
	referenced := lockfile.Set{}
	for _, section := range []map[string]string{project.Dependencies, project.DevDependencies, project.OptionalDependencies} {
		for name := range section {
			referenced.Add(name)
		}
	}
	for path, p := range r.packages {
		if path != "" {
			for name := range p.dependencies {
				referenced.Add(name)
			}
		}
	}
	var orphans []string
	for _, name := range sorted(legacy) {
		if !referenced.Has(name) {
			orphans = append(orphans, name)
		}
	}
	var warnings []Warning
	if len(orphans) != 0 {
		warnings = append(warnings, Warning{"WARN_AUBE_LOCKFILE_LEGACY_INCOMPLETE_GRAPH", fmt.Sprintf("legacy lockfile omits dependency edges for %d hoisted package(s) (%s); they are unreachable from the project's dependencies and will not be installed. Re-lock with a modern npm for a complete graph.", len(orphans), strings.Join(orphans, ", "))})
	}
	return r, warnings
}
