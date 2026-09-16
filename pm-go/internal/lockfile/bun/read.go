package bun

import (
	"fmt"
	"maps"
	"os"
	"slices"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
)

// The zero value uses Nub's eager unsupported-source policy. Lenient mode is
// retained for comparison with the standalone reference engine.
type Options struct{ AllowUnsupportedSources bool }
type Warning struct{ Code, Message string }
type UnsupportedSource struct{ Ident, Protocol string }

func (e *UnsupportedSource) Error() string {
	return fmt.Sprintf("lockfile entry `%s` uses unsupported source `%s`", e.Ident, e.Protocol)
}
func sortedKeys[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }
func Read(path string, options Options) (*lockfile.Graph, []Warning, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	return Parse(data, options)
}
func Parse(data []byte, options Options) (*lockfile.Graph, []Warning, error) {
	r, err := parseRaw(data)
	if err != nil {
		return nil, nil, err
	}
	if r.version != 1 && r.version != 2 {
		return nil, nil, fmt.Errorf("bun.lock lockfileVersion %d is not supported (expected 1 or 2)", r.version)
	}
	g := lockfile.NewGraph()
	entries := map[string]*entry{}
	for _, key := range sortedKeys(r.packages) {
		entries[key], err = decodeEntry(key, r.packages[key])
		if err != nil {
			return nil, nil, err
		}
	}
	var scopes []workspaceScope
	dirs := lockfile.Set{}
	for _, path := range sortedKeys(r.workspaces) {
		if path == "" {
			continue
		}
		dirs.Add(path)
		if name := r.workspaces[path].extra["name"]; name != nil && name.Kind == 's' {
			scopes = append(scopes, workspaceScope{name.Text(), path})
		}
	}
	slices.SortStableFunc(scopes, func(a, b workspaceScope) int { return len(b.name) - len(a.name) })
	type coordinate struct{ name, version string }
	info := map[string]coordinate{}
	type unsupportedEntry struct {
		name  string
		issue *UnsupportedSource
	}
	unsupported := map[string]unsupportedEntry{}
	var warnings []Warning
	edgeIssue := func(u unsupportedEntry, optional bool) error {
		if !optional {
			return u.issue
		}
		warnings = append(warnings, Warning{"WARN_AUBE_LOCKFILE_UNSUPPORTED_SOURCE", fmt.Sprintf("optional dependency `%s` uses the unsupported `%s` source — skipping it (the install continues)", u.name, u.issue.Protocol)})
		return nil
	}
	for _, key := range sortedKeys(entries) {
		e := entries[key]
		rawName, version, ok := splitIdent(e.ident)
		if !ok {
			return nil, warnings, fmt.Errorf("could not parse ident '%s' for package '%s'", e.ident, key)
		}
		name := aliasName(key)
		source, aliasOf := classify(name, rawName, version, e.integrity, dirs)
		if protocol, ok := lockfile.VersionProtocol(version); !options.AllowUnsupportedSources && source == nil && ok {
			unsupported[key] = unsupportedEntry{name, &UnsupportedSource{e.ident, protocol}}
			continue
		}
		rebaseLocal(key, source, scopes)
		info[key] = coordinate{name, version}
		depPath := name + "@" + version
		if g.Packages[depPath] != nil {
			continue
		}
		p := lockfile.NewPackage(name, version)
		p.Source, p.AliasOf, p.Integrity, p.TarballURL = source, aliasOf, e.integrity, e.registryURL
		p.DeclaredDependencies = map[string]string{}
		for k, v := range e.meta.dependencies {
			p.DeclaredDependencies[k] = v
		}
		for k, v := range e.meta.optionalDependencies {
			p.DeclaredDependencies[k] = v
			p.OptionalDependencies[k] = ""
		}
		p.Bin = binMap(name, e.meta.bin)
		p.ExtraMeta = maps.Clone(e.meta.extra)
		if p.ExtraMeta == nil {
			p.ExtraMeta = map[string]*jsonvalue.Value{}
		}
		if e.meta.bin != nil && e.meta.bin.Kind != 'n' {
			p.ExtraMeta["bin"] = e.meta.bin.Clone()
		}
		if len(e.meta.optionalPeers) != 0 {
			v := &jsonvalue.Value{Kind: '['}
			for _, name := range e.meta.optionalPeers {
				v.Array = append(v.Array, jsonvalue.String(name))
			}
			p.ExtraMeta["optionalPeers"] = v
		}
		p.PeerDependencies = maps.Clone(e.meta.peerDependencies)
		for _, name := range e.meta.optionalPeers {
			p.PeerDependenciesMeta[name] = lockfile.PeerMeta{Optional: true}
		}
		p.OS, p.CPU, p.Libc = e.meta.os, e.meta.cpu, e.meta.libc
		g.Packages[depPath] = p
	}
	contains := func(key string) bool { _, ok := info[key]; _, bad := unsupported[key]; return ok || bad }
	resolved := lockfile.Set{}
	for _, key := range sortedKeys(entries) {
		coord, ok := info[key]
		if !ok {
			continue
		}
		depPath := coord.name + "@" + coord.version
		if !resolved.Add(depPath) {
			continue
		}
		p, e := g.Packages[depPath], entries[key]
		deps := append(sortedKeys(e.meta.dependencies), sortedKeys(e.meta.optionalDependencies)...)
		for _, name := range deps {
			target, ok := resolveNested(key, name, contains)
			if !ok {
				continue
			}
			if coord, ok := info[target]; ok {
				p.Dependencies[name] = coord.version
			} else {
				_, required := e.meta.dependencies[name]
				if err := edgeIssue(unsupported[target], !required); err != nil {
					return nil, warnings, err
				}
			}
		}
		for name := range p.OptionalDependencies {
			if value, ok := p.Dependencies[name]; ok {
				p.OptionalDependencies[name] = value
			} else {
				delete(p.OptionalDependencies, name)
			}
		}
	}
	g.WorkspaceExtraFields = map[string]map[string]*jsonvalue.Value{}
	g.SkippedOptionalDependencies = map[string]map[string]string{}
	for _, path := range sortedKeys(r.workspaces) {
		ws := r.workspaces[path]
		importer := path
		if importer == "" {
			importer = "."
		}
		var wsName *string
		if v := ws.extra["name"]; path != "" && v != nil && v.Kind == 's' {
			name := v.Text()
			wsName = &name
		}
		direct := []lockfile.DirectDep{}
		push := func(name, spec string, kind lockfile.DepType) error {
			target, ok := resolveWorkspace(path, wsName, name, contains)
			if !ok {
				return nil
			}
			if coord, ok := info[target]; ok {
				direct = append(direct, lockfile.DirectDep{Name: coord.name, DepPath: coord.name + "@" + coord.version, Type: kind, Specifier: &spec})
				return nil
			}
			optional := kind == lockfile.Optional
			if err := edgeIssue(unsupported[target], optional); err != nil {
				return err
			}
			if optional {
				if g.SkippedOptionalDependencies[importer] == nil {
					g.SkippedOptionalDependencies[importer] = map[string]string{}
				}
				g.SkippedOptionalDependencies[importer][name] = spec
			}
			return nil
		}
		for i, deps := range []map[string]string{ws.dependencies, ws.devDependencies, ws.optionalDependencies} {
			for _, name := range sortedKeys(deps) {
				if err := push(name, deps[name], lockfile.DepType(i)); err != nil {
					return nil, warnings, err
				}
			}
		}
		optionalPeers := lockfile.Set{}
		if v := ws.extra["optionalPeers"]; v != nil && v.Kind == '[' {
			for _, item := range v.Array {
				if item.Kind == 's' {
					optionalPeers.Add(item.Text())
				}
			}
		}
		if peers := ws.extra["peerDependencies"]; peers != nil && peers.Kind == '{' {
			peerMap := map[string]*jsonvalue.Value{}
			for _, f := range peers.Object {
				peerMap[f.Key] = f.Value
			}
			for _, name := range sortedKeys(peerMap) {
				if optionalPeers.Has(name) || slices.ContainsFunc(direct, func(d lockfile.DirectDep) bool { return d.Name == name }) {
					continue
				}
				if err := push(name, peerMap[name].Text(), lockfile.Production); err != nil {
					return nil, warnings, err
				}
			}
		}
		g.Importers[importer] = direct
		if len(ws.extra) != 0 {
			g.WorkspaceExtraFields[importer] = ws.extra
		}
	}
	if _, ok := g.Importers["."]; !ok {
		g.Importers["."] = nil
	}
	g.Catalogs = map[string]map[string]lockfile.CatalogEntry{}
	catalog := func(name string, entries map[string]string) {
		values := map[string]lockfile.CatalogEntry{}
		for k, v := range entries {
			values[k] = lockfile.CatalogEntry{Specifier: v, Version: v}
		}
		g.Catalogs[name] = values
	}
	if len(r.catalog) != 0 {
		catalog("default", r.catalog)
	}
	for name, entries := range r.catalogs {
		catalog(name, entries)
	}
	g.BunConfigVersion = &r.configVersion
	g.Overrides, g.PatchedDependencies, g.ExtraFields = r.overrides, r.patches, r.extra
	seen := lockfile.Set{}
	for _, name := range r.trusted {
		if seen.Add(name) {
			g.TrustedDependencies = append(g.TrustedDependencies, name)
		}
	}
	return g, warnings, nil
}
