package pnpm

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
)

// Zero options keep the reference's strict remote-integrity check.
type Options struct{ AllowMissingIntegrity bool }
type Warning struct{ Code, Message string }

func Read(path string, options Options) (*lockfile.Graph, []Warning, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	graph, warnings, err := Parse(data, options)
	if err != nil {
		return nil, warnings, fmt.Errorf("%s: %w", path, err)
	}
	return graph, warnings, nil
}
func Parse(data []byte, options Options) (*lockfile.Graph, []Warning, error) {
	raw, err := parseRaw(data)
	if err != nil {
		return nil, nil, err
	}
	raw.stripPatchMarkers()
	major, ok := versionMajor(raw.version)
	if !ok || major < 9 {
		return nil, nil, fmt.Errorf("unsupported pnpm lockfileVersion %q (requires version 9 or newer)", versionText(raw.version))
	}
	if len(raw.legacy) > 0 {
		return nil, nil, fmt.Errorf("pnpm lockfileVersion %q has legacy root-level `%s:` block", versionText(raw.version), raw.legacy[0])
	}
	for _, key := range sortedKeys(raw.packages) {
		if strings.HasPrefix(key, "/") {
			return nil, nil, fmt.Errorf("pnpm lockfileVersion %q has legacy package key `%s`", versionText(raw.version), key)
		}
	}
	r := reader{raw: raw, g: lockfile.NewGraph(), locals: map[string]*lockfile.Package{}, localImporters: map[string]string{}, localSnapshotKeys: map[string]string{}, allLocalKeys: lockfile.Set{}, runtimeImports: map[string]lockfile.RuntimePin{}}
	r.readImporters()
	r.refineLocals()
	if err := r.readPackages(options); err != nil {
		return nil, r.warnings, err
	}
	if err := r.synthesizeAliases(); err != nil {
		return nil, r.warnings, err
	}
	r.normalizeSources()
	r.readHeader()
	return r.g, r.warnings, nil
}
func sortedKeys[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }
func textOr(s *string, fallback string) string {
	if s != nil {
		return *s
	}
	return fallback
}
func suffix(s string) string {
	if i := strings.IndexByte(s, '('); i >= 0 {
		return s[i:]
	}
	return ""
}
func setFirst[V any](m map[string]V, key string, v V) {
	if _, ok := m[key]; !ok {
		m[key] = v
	}
}

type reader struct {
	raw                                            *rawLock
	g                                              *lockfile.Graph
	locals                                         map[string]*lockfile.Package
	localImporters, localSnapshotKeys, localRekeys map[string]string
	allLocalKeys                                   lockfile.Set
	aliases                                        []AliasRemap
	runtimeImports                                 map[string]lockfile.RuntimePin
	warnings                                       []Warning
}

func (r *reader) readImporters() {
	r.g.SkippedOptionalDependencies = map[string]map[string]string{}
	for _, key := range sortedKeys(r.raw.importers) {
		importer := key
		if importer == "" {
			importer = "."
		}
		if _, exists := r.g.Importers[importer]; exists {
			continue
		}
		i := r.raw.importers[key]
		deps := []lockfile.DirectDep{}
		for _, section := range []struct {
			deps map[string]rawDep
			kind lockfile.DepType
		}{{i.deps, lockfile.Production}, {i.dev, lockfile.Dev}, {i.optional, lockfile.Optional}} {
			for _, name := range sortedKeys(section.deps) {
				info := section.deps[name]
				if version, ok := strings.CutPrefix(info.version, "runtime:"); ok {
					setFirst(r.runtimeImports, name, lockfile.RuntimePin{Specifier: strings.TrimPrefix(info.specifier, "runtime:"), Version: version, Dev: section.kind == lockfile.Dev})
					continue
				}
				deps = append(deps, r.direct(importer, name, info, section.kind))
			}
		}
		if len(i.skipped) > 0 {
			m := map[string]string{}
			for name, dep := range i.skipped {
				m[name] = dep.specifier
			}
			r.g.SkippedOptionalDependencies[importer] = m
		}
		r.g.Importers[importer] = deps
	}
}
func (r *reader) direct(importer, name string, info rawDep, kind lockfile.DepType) lockfile.DirectDep {
	classify, _, _ := strings.Cut(info.version, "(")
	dep := lockfile.DirectDep{Name: name, Type: kind, Specifier: &info.specifier}
	if local := lockfile.ParseSource(classify, ""); local != nil {
		if local.Kind == lockfile.Directory && lockfile.LooksLikeTarball(local.Path) {
			local.Kind = lockfile.Tarball
		}
		if local.Kind == lockfile.Git && local.Resolved == "" && local.Committish != nil {
			local.Resolved = *local.Committish
			local.Committish = nil
		}
		snapshot := name + "@" + local.Specifier()
		relative := local.Kind != lockfile.Git && local.Kind != lockfile.RemoteTarball && firstComponent(local.Path) == ".."
		rebased := importer != "." && (info.specifier == classify || strings.HasPrefix(info.specifier, "workspace:") || relative)
		if rebased {
			rebase(local, importer)
		}
		dep.DepPath = local.DepPath(name)
		if r.locals[dep.DepPath] == nil {
			p := lockfile.NewPackage(name, "0.0.0")
			p.DepPath = dep.DepPath
			p.Source = local
			r.locals[p.DepPath] = p
		}
		if rebased {
			setFirst(r.localImporters, dep.DepPath, importer)
		}
		setFirst(r.localSnapshotKeys, dep.DepPath, snapshot)
		r.allLocalKeys.Add(snapshot)
	} else {
		if real, version, ok := SplitDepPath(classify); ok && real != name {
			dep.DepPath = name + "@" + version + suffix(info.version)
			r.aliases = append(r.aliases, AliasRemap{dep.DepPath, info.version, name, real})
		} else {
			dep.DepPath = name + "@" + info.version
		}
	}
	return dep
}
func firstComponent(s string) string {
	if filepath.Separator == '\\' {
		s = strings.ReplaceAll(s, "\\", "/")
	}
	for _, p := range strings.Split(s, "/") {
		if p != "" && p != "." {
			return p
		}
	}
	return ""
}
func rebase(local *lockfile.Source, importer string) {
	if importer == "." || local.Kind == lockfile.Git || local.Kind == lockfile.RemoteTarball {
		return
	}
	// Joining does not access the filesystem. Paths above root retain their
	// unresolved parents, matching aube-util's lexical normalization.
	path := local.Path
	if !filepath.IsAbs(path) {
		path = importer + string(filepath.Separator) + path
	}
	volume := filepath.VolumeName(path)
	path = strings.TrimPrefix(path, volume)
	if filepath.Separator == '\\' {
		path = strings.ReplaceAll(path, "\\", "/")
	}
	absolute := strings.HasPrefix(path, "/")
	parts := []string{}
	for _, p := range strings.Split(path, "/") {
		switch p {
		case "", ".":
			continue
		case "..":
			if len(parts) > 0 && parts[len(parts)-1] != ".." {
				parts = parts[:len(parts)-1]
			} else {
				parts = append(parts, p)
			}
		default:
			parts = append(parts, p)
		}
	}
	prefix := volume
	if absolute {
		prefix += "/"
	}
	local.Path = filepath.FromSlash(prefix + strings.Join(parts, "/"))
}
func (r *reader) refineLocals() {
	for _, key := range sortedKeys(r.locals) {
		p := r.locals[key]
		canonical := p.Name + "@" + p.Source.Specifier()
		snapshotKey := r.localSnapshotKeys[key]
		snap := r.raw.snapshots[snapshotKey]
		if snap == nil && snapshotKey != canonical {
			snap = r.raw.snapshots[canonical]
		}
		if snap == nil {
			for _, k := range sortedKeys(r.raw.snapshots) {
				if n, v, ok := SplitDepPath(k); ok && n+"@"+v == canonical {
					snap = r.raw.snapshots[k]
					break
				}
			}
		}
		if snap != nil {
			p.Dependencies = maps.Clone(snap.deps)
			p.OptionalDependencies = maps.Clone(snap.optional)
			r.aliases = append(r.aliases, RewriteAliases(p.Dependencies)...)
			r.aliases = append(r.aliases, RewriteAliases(p.OptionalDependencies)...)
			maps.Copy(p.Dependencies, p.OptionalDependencies)
		}
		info := r.raw.packages[canonical]
		if info == nil && (p.Source.Kind == lockfile.Git || p.Source.Kind == lockfile.RemoteTarball) {
			for _, k := range sortedKeys(r.raw.packages) {
				n, _, ok := SplitDepPath(k)
				if !ok || n != p.Name {
					continue
				}
				candidate := r.raw.packages[k].resolution.source()
				if candidate == nil {
					continue
				}
				match := candidate.Kind == lockfile.Git && p.Source.Kind == lockfile.Git && lockfile.GitCommitsMatch(candidate.Resolved, p.Source.Resolved) && equalPtr(candidate.Subpath, p.Source.Subpath)
				match = match || candidate.Kind == lockfile.RemoteTarball && p.Source.Kind == lockfile.RemoteTarball && candidate.URL == p.Source.URL
				if match {
					info = r.raw.packages[k]
					break
				}
			}
		}
		if info == nil {
			continue
		}
		if info.version != nil && p.Version == "0.0.0" {
			p.Version = *info.version
		}
		source := info.resolution.source()
		if source == nil {
			continue
		}
		if importer, ok := r.localImporters[key]; ok {
			rebase(source, importer)
		}
		if source.Kind == lockfile.Git || source.Kind == lockfile.RemoteTarball {
			p.Integrity = info.resolution.integrity
		}
		if source.Kind == lockfile.Git && p.Source.Kind == lockfile.Git {
			if lockfile.GitCommitsMatch(source.Resolved, p.Source.Resolved) {
				source.Resolved = p.Source.Resolved
			}
			if source.Subpath == nil {
				source.Subpath = p.Source.Subpath
			}
		}
		p.Source = source
	}
	rekeyed := map[string]*lockfile.Package{}
	r.localRekeys = map[string]string{}
	for _, key := range sortedKeys(r.locals) {
		p := r.locals[key]
		newKey := p.Source.DepPath(p.Name)
		p.DepPath = newKey
		rekeyed[newKey] = p
		if key != newKey {
			r.localRekeys[key] = newKey
		}
	}
	r.locals = rekeyed
	for _, deps := range r.g.Importers {
		for i := range deps {
			if key, ok := r.localRekeys[deps[i].DepPath]; ok {
				deps[i].DepPath = key
			}
		}
	}
	for _, p := range r.locals {
		r.allLocalKeys.Add(p.Name + "@" + p.Source.Specifier())
	}
}
func equalPtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
func (r *reader) readPackages(options Options) error {
	keys := sortedKeys(r.raw.snapshots)
	if len(keys) == 0 {
		keys = sortedKeys(r.raw.packages)
	}
	for _, key := range keys {
		if r.allLocalKeys.Has(key) {
			continue
		}
		name, version, ok := SplitDepPath(key)
		if !ok {
			return fmt.Errorf("invalid dep path: %s", key)
		}
		if registry, ok := RegistryAlias(version); ok {
			return fmt.Errorf("unsupported named registry %q in dep path %s", registry, key)
		}
		if strings.HasPrefix(version, "runtime:") || r.allLocalKeys.Has(name+"@"+version) {
			continue
		}
		info := r.raw.packages[name+"@"+version]
		if info == nil {
			info = r.raw.packages[key]
		}
		p := lockfile.NewPackage(name, version)
		p.DepPath = key
		p.Bin = map[string]string{}
		p.ExtraMeta = map[string]*jsonvalue.Value{}
		isURL := isHTTP(version)
		if info != nil {
			p.PeerDependencies = maps.Clone(info.peers)
			p.PeerDependenciesMeta = maps.Clone(info.peerMeta)
			p.OS = slices.Clone(info.os)
			p.CPU = slices.Clone(info.cpu)
			p.Libc = slices.Clone(info.libc)
			p.Engines = maps.Clone(info.engines)
			p.AliasOf = info.aliasOf
			if info.hasBin {
				p.Bin[""] = ""
			}
			if info.deprecated != nil {
				p.ExtraMeta["deprecated"] = jsonvalue.String(*info.deprecated)
			}
			if res := info.resolution; res != nil {
				p.Integrity = res.integrity
				p.RegistryGitHosted = res.gitHosted
				if res.tarball != nil && isHTTP(*res.tarball) {
					p.TarballURL = res.tarball
					if preserveURL(*res.tarball) {
						p.ExtraMeta["__aube_preserve_tarball_url"] = &jsonvalue.Value{Kind: 'b', Scalar: true}
					}
				}
				if isURL && p.TarballURL != nil && info.version != nil {
					p.Version = *info.version
				}
				p.Source = res.source()
				if p.Source != nil && p.Source.Kind == lockfile.Git {
					if commit := gitCommit(version); commit != "" && lockfile.GitCommitsMatch(p.Source.Resolved, commit) {
						p.Source.Resolved = commit
					}
				}
				if p.Source != nil && p.Source.Kind == lockfile.RemoteTarball && !isURL {
					p.Source = nil
				}
				if !options.AllowMissingIntegrity && p.Integrity == nil && requiresIntegrity(res, p.Source) {
					return fmt.Errorf("lockfile entry %q has a remote tarball resolution without integrity", key)
				}
			}
		}
		if snap := r.raw.snapshots[key]; snap != nil {
			p.Dependencies = maps.Clone(snap.deps)
			p.OptionalDependencies = maps.Clone(snap.optional)
			r.aliases = append(r.aliases, RewriteAliases(p.Dependencies)...)
			r.aliases = append(r.aliases, RewriteAliases(p.OptionalDependencies)...)
			maps.Copy(p.Dependencies, p.OptionalDependencies)
			p.BundledDependencies = slices.Clone(snap.bundled)
			p.Optional = snap.isOptional
			p.TransitivePeerDependencies = slices.Clone(snap.transitivePeers)
		}
		r.g.Packages[key] = p
	}
	return nil
}
func gitCommit(version string) string {
	i := strings.LastIndexByte(version, '#')
	if i < 0 {
		return ""
	}
	c, _, _ := strings.Cut(version[i+1:], "&")
	if len(c) != 40 {
		return ""
	}
	for _, b := range c {
		if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F') {
			return ""
		}
	}
	return c
}
func preserveURL(s string) bool {
	rest, ok := strings.CutPrefix(s, "https://")
	if !ok {
		rest, ok = strings.CutPrefix(s, "http://")
	}
	if !ok {
		return false
	}
	rest, _, _ = strings.Cut(rest, "?")
	rest, _, _ = strings.Cut(rest, "#")
	authority, _, _ := strings.Cut(rest, "/")
	if i := strings.LastIndexByte(authority, '@'); i >= 0 {
		authority = authority[i+1:]
	}
	host, _, _ := strings.Cut(authority, ":")
	host = strings.ToLower(host)
	return host != "registry.npmjs.org" && host != "registry.yarnpkg.com"
}
func requiresIntegrity(res *rawResolution, source *lockfile.Source) bool {
	if res == nil || res.integrity != nil || res.gitHosted {
		return false
	}
	if source != nil {
		if source.Kind != lockfile.RemoteTarball {
			return false
		}
		return !source.GitHosted && !HostedGitTarball(source.URL)
	}
	return res.tarball != nil && isHTTP(*res.tarball) && !HostedGitTarball(*res.tarball)
}
func (r *reader) synthesizeAliases() error {
	byCanonical := map[string]string{}
	for _, key := range sortedKeys(r.locals) {
		p := r.locals[key]
		byCanonical[p.Name+"@"+p.Source.Specifier()] = key
	}
	for _, key := range sortedKeys(r.localSnapshotKeys) {
		final := key
		if k, ok := r.localRekeys[key]; ok {
			final = k
		}
		setFirst(byCanonical, r.localSnapshotKeys[key], final)
	}
	maps.Copy(r.g.Packages, r.locals)
	renames := map[string]string{}
	cloneSources := lockfile.Set{}
	for _, a := range r.aliases {
		if r.g.Packages[a.AliasPath] != nil {
			continue
		}
		if _, ok := renames[a.AliasPath]; ok {
			continue
		}
		bare, _, _ := strings.Cut(a.RealPath, "(")
		if key, ok := byCanonical[bare]; ok {
			if p := r.g.Packages[key]; p != nil && p.Source != nil {
				alias := p.Clone()
				alias.Name = a.AliasName
				alias.DepPath = alias.Source.DepPath(alias.Name)
				alias.AliasOf = &a.RealName
				renames[a.AliasPath] = alias.DepPath
				r.g.Packages[alias.DepPath] = alias
				continue
			}
		}
		p := r.g.Packages[a.RealPath]
		if p == nil {
			p = PeerlessAliasTarget(r.g.Packages, a.RealPath)
		}
		if p == nil {
			return fmt.Errorf("npm-alias references missing package %s (alias dep_path: %s)", a.RealPath, a.AliasPath)
		}
		if p.Source == nil {
			cloneSources.Add(p.DepPath)
		}
		alias := p.Clone()
		alias.Name = a.AliasName
		alias.DepPath = a.AliasPath
		alias.AliasOf = &a.RealName
		r.g.Packages[a.AliasPath] = alias
	}
	for _, deps := range r.g.Importers {
		for i := range deps {
			if key, ok := renames[deps[i].DepPath]; ok {
				deps[i].DepPath = key
			}
		}
	}
	for _, p := range r.g.Packages {
		for _, m := range []map[string]string{p.Dependencies, p.OptionalDependencies} {
			for name, v := range m {
				if key, ok := renames[name+"@"+v]; ok {
					m[name] = DepPathTail(key, name)
				}
			}
		}
	}
	referenced := lockfile.Set{}
	for _, deps := range r.g.Importers {
		for _, d := range deps {
			referenced.Add(d.DepPath)
		}
	}
	for _, p := range r.g.Packages {
		for _, m := range []map[string]string{p.Dependencies, p.OptionalDependencies} {
			for name, v := range m {
				if key, ok := r.g.Child(name, v); ok {
					referenced.Add(key)
				}
			}
		}
	}
	for key, p := range r.g.Packages {
		if p.AliasOf == nil && cloneSources.Has(key) && !referenced.Has(key) {
			delete(r.g.Packages, key)
		}
	}
	return nil
}
func (r *reader) normalizeSources() {
	hasLocal := false
	for _, p := range r.g.Packages {
		hasLocal = hasLocal || p.Source != nil && (p.Source.Kind == lockfile.Git || p.Source.Kind == lockfile.RemoteTarball)
	}
	if !hasLocal {
		return
	}
	translate := func(head string) (string, bool) {
		name, v, ok := SplitDepPath(head)
		if !ok {
			return "", false
		}
		return lockfile.SharedLocalDepPath(name, v)
	}
	warn := func(s string) {
		r.warnings = append(r.warnings, Warning{"WARN_AUBE_LOCKFILE_MALFORMED_PEER_SUFFIX", s})
	}
	rewrite := func(s string) string { return RewritePeerSuffix(s, translate, warn) }
	out := map[string]*lockfile.Package{}
	for _, key := range sortedKeys(r.g.Packages) {
		p := r.g.Packages[key]
		if p.Source != nil && (p.Source.Kind == lockfile.Git || p.Source.Kind == lockfile.RemoteTarball) {
			key = p.Source.DepPath(p.Name) + suffix(key)
		}
		key = rewrite(key)
		p.DepPath = key
		for _, m := range []map[string]string{p.Dependencies, p.OptionalDependencies} {
			for _, name := range sortedKeys(m) {
				m[name] = rewrite(m[name])
			}
		}
		out[key] = p
	}
	r.g.Packages = out
	for _, importer := range sortedKeys(r.g.Importers) {
		for i := range r.g.Importers[importer] {
			d := &r.g.Importers[importer][i]
			d.DepPath = rewrite(d.DepPath)
		}
	}
}
func (r *reader) readHeader() {
	g := r.g
	raw := r.raw
	if s := raw.settings; s != nil {
		if s.autoPeers != nil {
			g.Settings.AutoInstallPeers = *s.autoPeers
		}
		if s.excludeLinks != nil {
			g.Settings.ExcludeLinksFromLockfile = *s.excludeLinks
		}
		if s.includeTarball != nil {
			g.Settings.IncludeTarballURL = *s.includeTarball
		}
	}
	g.Overrides = raw.overrides
	g.Times = raw.times
	g.Catalogs = raw.catalogs
	g.PackageExtensionsChecksum = raw.extensionChecksum
	g.PnpmfileChecksum = raw.hookChecksum
	g.IgnoredOptionalDependencies = lockfile.Set{}
	for _, name := range raw.ignored {
		g.IgnoredOptionalDependencies.Add(name)
	}
	g.PatchedDependencies = map[string]string{}
	g.PatchedDependencyHashes = map[string]string{}
	for key, patch := range raw.patches {
		if patch.hash != nil {
			g.PatchedDependencyHashes[key] = *patch.hash
		} else if patch.path != nil {
			g.PatchedDependencies[key] = *patch.path
		}
	}
	g.Runtimes = map[string]lockfile.RuntimePin{}
	for name, pin := range r.runtimeImports {
		p := raw.packages[name+"@runtime:"+pin.Version]
		pin.HasBin = true
		if p != nil {
			pin.HasBin = p.hasBin
			if p.resolution != nil {
				for _, v := range p.resolution.variants {
					variant := lockfile.RuntimeVariant{Targets: v.targets, Archive: textOr(v.archive, "tarball"), URL: v.url, Integrity: textOr(v.integrity, ""), Bin: v.bin, Prefix: v.prefix}
					if v.bareBin != nil {
						variant.Bin = map[string]string{name: *v.bareBin}
						variant.BinIsBareString = true
					}
					pin.Variants = append(pin.Variants, variant)
				}
			}
		}
		g.Runtimes[name] = pin
	}
}
