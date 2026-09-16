package pnpm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/fsutil"
	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/manifest"
	"go.yaml.in/yaml/v4"
)

type Projection struct {
	Lockfile *jsonvalue.Value
	// Hook views use pnpm's source spellings; edits need the internal graph key.
	SnapshotKeys map[string]string
	Warnings     []Warning
}

func Write(path string, g *lockfile.Graph, project *manifest.Package) ([]Warning, error) {
	data, warnings, err := Encode(path, g, project)
	if err != nil {
		return warnings, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return warnings, err
	}
	return warnings, fsutil.WriteDefault(path, data)
}
func Encode(path string, g *lockfile.Graph, project *manifest.Package) ([]byte, []Warning, error) {
	p, err := Build(path, g, project)
	if err != nil {
		return nil, nil, err
	}
	node := toYAML(p.Lockfile)
	data, err := yaml.Dump(node, yaml.WithIndent(2), yaml.WithCompactSeqIndent(true), yaml.WithLineWidth(-1), yaml.WithQuotePreference(yaml.QuoteSingle))
	if err != nil {
		return nil, p.Warnings, err
	}
	return []byte(reformat(string(data))), p.Warnings, nil
}
func toYAML(v *jsonvalue.Value) *yaml.Node {
	n := &yaml.Node{}
	switch v.Kind {
	case '{':
		n.Kind = yaml.MappingNode
		n.Tag = "!!map"
		for _, f := range v.Object {
			n.Content = append(n.Content, toYAML(jsonvalue.String(f.Key)), toYAML(f.Value))
		}
	case '[':
		n.Kind = yaml.SequenceNode
		n.Tag = "!!seq"
		for _, item := range v.Array {
			n.Content = append(n.Content, toYAML(item))
		}
	case 'b':
		n.Kind = yaml.ScalarNode
		n.Tag = "!!bool"
		n.Value = fmt.Sprint(v.Scalar)
	default:
		n.Kind = yaml.ScalarNode
		n.Tag = "!!str"
		n.Value = v.Text()
	}
	return n
}
func obj() *jsonvalue.Value           { return jsonvalue.Object() }
func str(s string) *jsonvalue.Value   { return jsonvalue.String(s) }
func boolean(b bool) *jsonvalue.Value { return &jsonvalue.Value{Kind: 'b', Scalar: b} }
func putText(o *jsonvalue.Value, key string, v *string) {
	if v != nil {
		o.Put(key, str(*v))
	}
}
func putTrue(o *jsonvalue.Value, key string, b bool) {
	if b {
		o.Put(key, boolean(true))
	}
}
func stringMap(m map[string]string) *jsonvalue.Value {
	o := obj()
	for _, k := range sortedKeys(m) {
		o.Put(k, str(m[k]))
	}
	return o
}
func putMap(o *jsonvalue.Value, key string, m map[string]string) {
	if len(m) > 0 {
		o.Put(key, stringMap(m))
	}
}
func array(values []string) *jsonvalue.Value {
	a := &jsonvalue.Value{Kind: '[', Array: []*jsonvalue.Value{}}
	for _, v := range values {
		a.Array = append(a.Array, str(v))
	}
	return a
}
func putList(o *jsonvalue.Value, key string, values []string) {
	if len(values) > 0 {
		o.Put(key, array(values))
	}
}
func depSpec(spec, version string) *jsonvalue.Value {
	o := obj()
	o.Put("specifier", str(spec))
	o.Put("version", str(version))
	return o
}
func declares(p *manifest.Package, name string) bool {
	for _, m := range []map[string]string{p.Dependencies, p.DevDependencies, p.OptionalDependencies} {
		if _, ok := m[name]; ok {
			return true
		}
	}
	return false
}

type writer struct {
	g        *lockfile.Graph
	native   bool
	patches  map[string]string
	warnings []Warning
}

func (w *writer) peerToSpec(head string) (string, bool) {
	p := w.g.Packages[head]
	if p == nil || p.Source == nil || !(p.Source.Kind == lockfile.Git || p.Source.Kind == lockfile.RemoteTarball) {
		return "", false
	}
	return p.Name + "@" + p.Source.Specifier(), true
}
func (w *writer) peerSuffix(value string) string {
	return RewritePeerSuffix(value, w.peerToSpec, func(s string) {
		w.warnings = append(w.warnings, Warning{"WARN_AUBE_LOCKFILE_MALFORMED_PEER_SUFFIX", s})
	})
}
func (w *writer) decorate(value string, p *lockfile.Package) string {
	if p == nil {
		return value
	}
	bare := StripPatchHash(value)
	hash, ok := w.patches[p.DepPath]
	if !ok {
		return bare
	}
	i := strings.IndexByte(bare, '(')
	if i < 0 {
		i = len(bare)
	}
	return bare[:i] + "(patch_hash=" + hash + ")" + bare[i:]
}
func (w *writer) target(name, value string) *lockfile.Package {
	p := w.g.Packages[name+"@"+value]
	if p == nil {
		p = w.g.Packages[PeerlessPath(name, value)]
	}
	return p
}
func (w *writer) rewriteDeps(deps map[string]string) map[string]string {
	out := map[string]string{}
	for _, name := range sortedKeys(deps) {
		value := deps[name]
		target := w.target(name, value)
		rewritten := value
		if target != nil && target.Source != nil && target.Source.Kind != lockfile.Link {
			rewritten = target.Source.Specifier()
		} else if w.native && target != nil && target.AliasOf != nil {
			rewritten = *target.AliasOf + "@" + value
		} else {
			rewritten = w.peerSuffix(value)
		}
		out[name] = w.decorate(rewritten, target)
	}
	return out
}
func Build(path string, g *lockfile.Graph, project *manifest.Package) (*Projection, error) {
	if project == nil {
		project = &manifest.Package{}
	}
	w := writer{g: g, native: filepath.Base(path) == "pnpm-lock.yaml", patches: map[string]string{}}
	selectors := lockfile.Set{}
	for key := range g.PatchedDependencies {
		selectors.Add(key)
	}
	for key := range g.PatchedDependencyHashes {
		selectors.Add(key)
	}
	groups, err := lockfile.NewPatchGroups(selectors.Sorted())
	if err != nil {
		return nil, err
	}
	for _, key := range sortedKeys(g.Packages) {
		p := g.Packages[key]
		if p.Source != nil {
			continue
		}
		key, ok, err := groups.ResolvePackage(p)
		if err != nil {
			return nil, err
		}
		if ok {
			if hash, ok := g.PatchedDependencyHashes[key]; ok {
				w.patches[p.DepPath] = hash
			}
		}
	}
	members := map[string]string{}
	for _, importer := range sortedKeys(g.Importers) {
		if importer == "." {
			continue
		}
		pj, err := manifest.ReadPackage(filepath.Join(filepath.Dir(path), importer, "package.json"))
		if err == nil && pj.Name != nil {
			members[*pj.Name+"@"+textOr(pj.Version, "0.0.0")] = importer
		}
	}
	importers := obj()
	for _, importer := range sortedKeys(g.Importers) {
		sections := map[string]map[string]*jsonvalue.Value{}
		set := func(section, name string, spec *jsonvalue.Value) {
			if sections[section] == nil {
				sections[section] = map[string]*jsonvalue.Value{}
			}
			sections[section][name] = spec
		}
		for _, dep := range g.Importers[importer] {
			pkg := g.Packages[dep.DepPath]
			member, workspaceLink := members[dep.DepPath]
			workspaceLink = workspaceLink && pkg == nil
			var local *lockfile.Source
			if pkg != nil {
				local = pkg.Source
			}
			linkedSibling := false
			if local != nil && local.Kind == lockfile.Link {
				_, linkedSibling = g.Importers[local.Path]
			}
			if importer == "." && (workspaceLink || linkedSibling) && !declares(project, dep.Name) {
				continue
			}
			if g.Settings.ExcludeLinksFromLockfile && (workspaceLink || local != nil && local.Kind == lockfile.Link) {
				continue
			}
			spec := "*"
			if dep.Specifier != nil {
				spec = *dep.Specifier
			} else if importer == "." {
				m := project.Dependencies
				if dep.Type == lockfile.Dev {
					m = project.DevDependencies
				} else if dep.Type == lockfile.Optional {
					m = project.OptionalDependencies
				}
				if v, ok := m[dep.Name]; ok {
					spec = v
				}
			}
			version := ""
			switch {
			case local != nil:
				if local.Kind == lockfile.Link && importer != "." {
					version = "link:" + lockfile.LinkFromImporter(importer, local.PathPOSIX())
				} else {
					version = local.Specifier()
				}
			case workspaceLink:
				version = "link:" + lockfile.LinkFromImporter(importer, member)
			case w.native && pkg != nil && pkg.AliasOf != nil:
				version = *pkg.AliasOf + "@" + DepPathTail(dep.DepPath, dep.Name)
			default:
				version = w.peerSuffix(DepPathTail(dep.DepPath, dep.Name))
			}
			target := pkg
			if target == nil {
				target = g.Packages[PeerlessPath(dep.Name, version)]
			}
			version = w.decorate(version, target)
			set(dep.Type.Label(), dep.Name, depSpec(spec, version))
		}
		if importer == "." {
			for _, name := range sortedKeys(g.Runtimes) {
				pin := g.Runtimes[name]
				section := "dependencies"
				if pin.Dev {
					section = "devDependencies"
				}
				set(section, name, depSpec("runtime:"+pin.Specifier, "runtime:"+pin.Version))
			}
		}
		for name, spec := range g.SkippedOptionalDependencies[importer] {
			set("skippedOptionalDependencies", name, depSpec(spec, "0.0.0"))
		}
		out := obj()
		for _, section := range []string{"dependencies", "devDependencies", "optionalDependencies", "skippedOptionalDependencies"} {
			if len(sections[section]) > 0 {
				m := obj()
				for _, name := range sortedKeys(sections[section]) {
					m.Put(name, sections[section][name])
				}
				out.Put(section, m)
			}
		}
		importers.Put(importer, out)
	}
	packages := map[string]*jsonvalue.Value{}
	snapshots := map[string]*jsonvalue.Value{}
	snapshotKeys := map[string]string{}
	for _, key := range sortedKeys(g.Packages) {
		pkg := g.Packages[key]
		if pkg.Source != nil && (pkg.Source.Kind == lockfile.Link || pkg.Source.Kind == lockfile.Exec) {
			continue
		}
		canonical, info := w.packageInfo(pkg)
		packages[canonical] = info
		snapKey := key
		if pkg.Source != nil {
			snapKey = pkg.Name + "@" + pkg.Source.Specifier()
		} else if w.native && pkg.AliasOf != nil {
			snapKey = *pkg.AliasOf + "@" + DepPathTail(key, pkg.Name)
		} else {
			snapKey = w.peerSuffix(key)
		}
		snapKey = w.decorate(snapKey, pkg)
		snapshotKeys[snapKey] = key
		deps, opt := w.rewriteDeps(pkg.Dependencies), w.rewriteDeps(pkg.OptionalDependencies)
		for name := range opt {
			delete(deps, name)
		}
		snap := obj()
		putMap(snap, "dependencies", deps)
		putMap(snap, "optionalDependencies", opt)
		putList(snap, "transitivePeerDependencies", pkg.TransitivePeerDependencies)
		putTrue(snap, "optional", pkg.Optional)
		putList(snap, "bundledDependencies", pkg.BundledDependencies)
		snapshots[snapKey] = snap
	}
	for name, pin := range g.Runtimes {
		key := name + "@runtime:" + pin.Version
		packages[key] = runtimePackage(pin)
		snapshots[key] = obj()
	}
	out := obj()
	out.Put("lockfileVersion", str("9.0"))
	settings := obj()
	settings.Put("autoInstallPeers", boolean(g.Settings.AutoInstallPeers))
	settings.Put("excludeLinksFromLockfile", boolean(g.Settings.ExcludeLinksFromLockfile))
	putTrue(settings, "lockfileIncludeTarballUrl", g.Settings.IncludeTarballURL)
	out.Put("settings", settings)
	if len(g.Catalogs) > 0 {
		catalogs := obj()
		for _, name := range sortedKeys(g.Catalogs) {
			entries := obj()
			for _, key := range sortedKeys(g.Catalogs[name]) {
				e := g.Catalogs[name][key]
				entries.Put(key, depSpec(e.Specifier, e.Version))
			}
			catalogs.Put(name, entries)
		}
		out.Put("catalogs", catalogs)
	}
	putMap(out, "overrides", g.Overrides)
	putText(out, "packageExtensionsChecksum", g.PackageExtensionsChecksum)
	putText(out, "pnpmfileChecksum", g.PnpmfileChecksum)
	if len(selectors) > 0 {
		patches := obj()
		for _, key := range selectors.Sorted() {
			hash, hasHash := g.PatchedDependencyHashes[key]
			path, hasPath := g.PatchedDependencies[key]
			if hasHash {
				v := obj()
				v.Put("hash", str(hash))
				if hasPath {
					v.Put("path", str(path))
				}
				patches.Put(key, v)
			} else {
				patches.Put(key, str(path))
			}
		}
		out.Put("patchedDependencies", patches)
	}
	putMap(out, "time", w.times())
	out.Put("importers", importers)
	ps := obj()
	for _, key := range sortedKeys(packages) {
		ps.Put(key, packages[key])
	}
	out.Put("packages", ps)
	putList(out, "ignoredOptionalDependencies", g.IgnoredOptionalDependencies.Sorted())
	ss := obj()
	for _, key := range sortedKeys(snapshots) {
		ss.Put(key, snapshots[key])
	}
	out.Put("snapshots", ss)
	return &Projection{out, snapshotKeys, w.warnings}, nil
}
func (w *writer) packageInfo(p *lockfile.Package) (string, *jsonvalue.Value) {
	_, v, ok := SplitDepPath(p.DepPath)
	urlKey := p.TarballURL != nil && ok && v == *p.TarballURL
	canonical := p.Name + "@" + p.Version
	if p.Source != nil {
		canonical = p.Name + "@" + p.Source.Specifier()
	} else if w.native && p.AliasOf != nil {
		canonical = *p.AliasOf + "@" + p.Version
	} else if urlKey {
		n, v, _ := SplitDepPath(p.DepPath)
		canonical = n + "@" + v
	}
	out := obj()
	res := w.resolution(p, urlKey)
	if res != nil {
		out.Put("resolution", res)
	}
	if urlKey || p.Source != nil && p.Source.Kind == lockfile.RemoteTarball {
		out.Put("version", str(p.Version))
	}
	engines := map[string]string{}
	for key, v := range p.Engines {
		if v != "*" {
			engines[key] = v
		}
	}
	putMap(out, "engines", engines)
	putList(out, "cpu", p.CPU)
	putList(out, "os", p.OS)
	putList(out, "libc", p.Libc)
	if v := p.ExtraMeta["deprecated"]; v != nil && v.Kind == 's' {
		out.Put("deprecated", v.Clone())
	}
	putTrue(out, "hasBin", len(p.Bin) > 0)
	putMap(out, "peerDependencies", p.DeclaredPeers())
	meta := obj()
	for _, key := range sortedKeys(p.PeerDependenciesMeta) {
		if p.PeerDependenciesMeta[key].Optional {
			value := obj()
			value.Put("optional", boolean(true))
			meta.Put(key, value)
		}
	}
	if len(meta.Object) > 0 {
		out.Put("peerDependenciesMeta", meta)
	}
	if !w.native {
		putText(out, "aliasOf", p.AliasOf)
	}
	return canonical, out
}
func (w *writer) resolution(p *lockfile.Package, urlKey bool) *jsonvalue.Value {
	out := obj()
	if s := p.Source; s != nil {
		switch s.Kind {
		case lockfile.Directory, lockfile.Portal:
			out.Put("directory", str(s.PathPOSIX()))
			out.Put("type", str("directory"))
		case lockfile.Tarball:
			out.Put("tarball", str("file:"+s.PathPOSIX()))
		case lockfile.Git:
			out.Put("commit", str(s.Resolved))
			_, hosted := lockfile.ParseHostedGit(s.URL)
			putTrue(out, "gitHosted", hosted)
			integrity := s.Integrity
			if integrity == nil {
				integrity = p.Integrity
			}
			putText(out, "integrity", integrity)
			if s.Subpath != nil {
				out.Put("path", str("/"+*s.Subpath))
			}
			out.Put("repo", str(s.URL))
			out.Put("type", str("git"))
		case lockfile.RemoteTarball:
			putTrue(out, "gitHosted", s.GitHosted || HostedGitTarball(s.URL))
			if s.Integrity != nil && *s.Integrity != "" {
				out.Put("integrity", str(*s.Integrity))
			}
			out.Put("tarball", str(s.URL))
		default:
			return nil
		}
		return out
	}
	preserve := w.g.Settings.IncludeTarballURL || strings.HasPrefix(p.RegistryName(), "@jsr/") || p.ForceTarballURL || notDerivable(p.RegistryName(), p.Version, p.TarballURL)
	if v := p.ExtraMeta["__aube_preserve_tarball_url"]; v != nil && v.Kind == 'b' && v.Scalar == true {
		preserve = true
	}
	if !urlKey && p.Integrity == nil && !preserve {
		return nil
	}
	hosted := p.RegistryGitHosted
	if p.TarballURL != nil {
		hosted = hosted || HostedGitTarball(*p.TarballURL)
	}
	putTrue(out, "gitHosted", hosted)
	putText(out, "integrity", p.Integrity)
	if urlKey || preserve {
		putText(out, "tarball", p.TarballURL)
	}
	return out
}
func notDerivable(name, version string, url *string) bool {
	if url == nil {
		return false
	}
	base := name[strings.LastIndexByte(name, '/')+1:]
	path, _, _ := strings.Cut(*url, "?")
	path, _, _ = strings.Cut(path, "#")
	return !strings.HasSuffix(path, "/-/"+base+"-"+version+".tgz")
}
func runtimePackage(pin lockfile.RuntimePin) *jsonvalue.Value {
	variants := &jsonvalue.Value{Kind: '[', Array: []*jsonvalue.Value{}}
	for _, v := range pin.Variants {
		res := obj()
		res.Put("archive", str(v.Archive))
		if v.BinIsBareString && len(v.Bin) == 1 {
			for _, bin := range v.Bin {
				res.Put("bin", str(bin))
			}
		} else {
			res.Put("bin", stringMap(v.Bin))
		}
		if v.Integrity != "" {
			res.Put("integrity", str(v.Integrity))
		}
		putText(res, "prefix", v.Prefix)
		res.Put("type", str("binary"))
		res.Put("url", str(v.URL))
		targets := &jsonvalue.Value{Kind: '[', Array: []*jsonvalue.Value{}}
		for _, t := range v.Targets {
			target := obj()
			target.Put("cpu", str(t.CPU))
			putText(target, "libc", t.Libc)
			target.Put("os", str(t.OS))
			targets.Array = append(targets.Array, target)
		}
		variant := obj()
		variant.Put("resolution", res)
		variant.Put("targets", targets)
		variants.Array = append(variants.Array, variant)
	}
	res := obj()
	res.Put("type", str("variations"))
	res.Put("variants", variants)
	out := obj()
	out.Put("resolution", res)
	out.Put("version", str(pin.Version))
	putTrue(out, "hasBin", pin.HasBin)
	return out
}
func (w *writer) times() map[string]string {
	out := map[string]string{}
	for _, importer := range sortedKeys(w.g.Importers) {
		for _, dep := range w.g.Importers[importer] {
			p := w.g.Packages[dep.DepPath]
			if p == nil || p.Source != nil {
				continue
			}
			name := dep.Name
			if w.native && p.AliasOf != nil {
				name = *p.AliasOf
			}
			version, _, _ := strings.Cut(DepPathTail(dep.DepPath, dep.Name), "(")
			key := name + "@" + version
			value, ok := w.g.Times[key]
			if !ok {
				value, ok = w.g.Times[dep.Name+"@"+version]
			}
			if !ok && !w.native && p.AliasOf != nil {
				value, ok = w.g.Times[*p.AliasOf+"@"+version]
			}
			if ok {
				out[key] = value
			}
		}
	}
	return out
}
