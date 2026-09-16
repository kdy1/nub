package pnpm

import (
	"bytes"
	"fmt"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"go.yaml.in/yaml/v4"
	"io"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"
)

type rawLock struct {
	version                         *yaml.Node
	settings                        *rawSettings
	overrides, times                map[string]string
	extensionChecksum, hookChecksum *string
	catalogs                        map[string]map[string]lockfile.CatalogEntry
	patches                         map[string]rawPatch
	ignored                         []string
	legacy                          []string
	importers                       map[string]*rawImporter
	packages                        map[string]*rawPackage
	snapshots                       map[string]*rawSnapshot
}
type rawSettings struct{ autoPeers, excludeLinks, includeTarball *bool }
type rawPatch struct{ path, hash *string }
type rawImporter struct{ deps, dev, optional, skipped map[string]rawDep }
type rawDep struct{ specifier, version string }
type rawPackage struct {
	resolution                   *rawResolution
	engines, peers               map[string]string
	peerMeta                     map[string]lockfile.PeerMeta
	os, cpu, libc                []string
	hasBin                       bool
	deprecated, aliasOf, version *string
}
type rawResolution struct {
	integrity, directory, tarball, commit, repo, subpath *string
	gitHosted                                            bool
	variants                                             []rawVariant
}
type rawVariant struct {
	targets                    []lockfile.RuntimeTarget
	archive, integrity, prefix *string
	url                        string
	bin                        map[string]string
	bareBin                    *string
}
type rawSnapshot struct {
	deps, optional           map[string]string
	bundled, transitivePeers []string
	isOptional               bool
}

func parseRaw(data []byte) (*rawLock, error) {
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("invalid UTF-8 in lockfile")
	}
	if raw := trySubset(data); raw != nil {
		return raw, nil
	}
	loader, err := yaml.NewLoader(bytes.NewReader(data), yaml.WithUniqueKeys(false))
	if err != nil {
		return nil, err
	}
	var best *rawLock
	bestScore := -1
	for range 16 {
		var node yaml.Node
		if err = loader.Load(&node); err != nil {
			if err == io.EOF {
				break
			}
			if best != nil {
				return best, nil
			}
			return nil, err
		}
		var parseErr error
		raw := decodeRaw(yamlReader{&node, "lockfile", &parseErr})
		if parseErr != nil {
			if best != nil {
				return best, nil
			}
			return nil, parseErr
		}
		score := raw.score()
		if score > bestScore {
			best, bestScore = raw, score
		}
	}
	if best == nil {
		return nil, fmt.Errorf("expected a lockfile mapping")
	}
	return best, nil
}
func (r *rawLock) score() int {
	score := len(r.packages) + len(r.snapshots)
	for _, i := range r.importers {
		score += 1000 * (len(i.deps) + len(i.dev) + len(i.optional))
	}
	if r.settings != nil {
		score += 100
	}
	if len(r.catalogs) > 0 {
		score += 100
	}
	if len(r.overrides) > 0 {
		score += 100
	}
	return score
}
func decodeRaw(r yamlReader) *rawLock {
	m := r.object(false, "lockfileVersion settings overrides packageExtensionsChecksum pnpmfileChecksum catalogs patchedDependencies ignoredOptionalDependencies dependencies devDependencies optionalDependencies specifiers importers packages snapshots time")
	f := func(k string) yamlReader { return field(m, k, r) }
	out := &rawLock{version: f("lockfileVersion").resolve().node, overrides: f("overrides").strings(), times: f("time").strings(), extensionChecksum: f("packageExtensionsChecksum").optionalText(), hookChecksum: f("pnpmfileChecksum").optionalText(), catalogs: map[string]map[string]lockfile.CatalogEntry{}, patches: map[string]rawPatch{}, ignored: f("ignoredOptionalDependencies").stringList(), importers: map[string]*rawImporter{}, packages: map[string]*rawPackage{}, snapshots: map[string]*rawSnapshot{}}
	if s := f("settings"); !s.absent() {
		v := s.object(false, "autoInstallPeers excludeLinksFromLockfile lockfileIncludeTarballUrl")
		out.settings = &rawSettings{field(v, "autoInstallPeers", s).boolean(true), field(v, "excludeLinksFromLockfile", s).boolean(true), field(v, "lockfileIncludeTarballUrl", s).boolean(true)}
	}
	for _, k := range []string{"dependencies", "devDependencies", "optionalDependencies", "specifiers"} {
		if !f(k).absent() {
			out.legacy = append(out.legacy, k)
		}
	}
	for name, catalog := range f("catalogs").object(true, "") {
		entries := map[string]lockfile.CatalogEntry{}
		for name, v := range catalog.object(false, "") {
			d := decodeDep(v)
			entries[name] = lockfile.CatalogEntry{Specifier: d.specifier, Version: d.version}
		}
		out.catalogs[name] = entries
	}
	for name, p := range f("patchedDependencies").object(true, "") {
		p = p.resolve()
		if p.node != nil && p.node.Kind == yaml.ScalarNode && p.node.Tag == "!!str" {
			s := p.text()
			out.patches[name] = rawPatch{hash: &s}
		} else {
			v := p.object(false, "path hash")
			out.patches[name] = rawPatch{field(v, "path", p).optionalText(), field(v, "hash", p).optionalText()}
		}
	}
	for k, v := range f("importers").object(f("importers").node == nil, "") {
		out.importers[k] = decodeImporter(v)
	}
	for k, v := range f("packages").object(f("packages").node == nil, "") {
		out.packages[k] = decodePackage(v)
	}
	for k, v := range f("snapshots").object(f("snapshots").node == nil, "") {
		out.snapshots[k] = decodeSnapshot(v)
	}
	return out
}
func decodeDep(r yamlReader) rawDep {
	m := r.object(false, "specifier version")
	return rawDep{field(m, "specifier", r).text(), field(m, "version", r).text()}
}
func decodeDeps(r yamlReader) map[string]rawDep {
	out := map[string]rawDep{}
	for k, v := range r.object(true, "") {
		out[k] = decodeDep(v)
	}
	return out
}
func decodeImporter(r yamlReader) *rawImporter {
	m := r.object(false, "dependencies devDependencies optionalDependencies skippedOptionalDependencies")
	f := func(k string) yamlReader { return field(m, k, r) }
	return &rawImporter{decodeDeps(f("dependencies")), decodeDeps(f("devDependencies")), decodeDeps(f("optionalDependencies")), decodeDeps(f("skippedOptionalDependencies"))}
}
func decodePackage(r yamlReader) *rawPackage {
	m := r.object(false, "resolution engines peerDependencies peerDependenciesMeta os cpu libc hasBin deprecated aliasOf version")
	f := func(k string) yamlReader { return field(m, k, r) }
	out := &rawPackage{engines: f("engines").engines(), peers: f("peerDependencies").strings(), peerMeta: map[string]lockfile.PeerMeta{}, os: f("os").platforms(), cpu: f("cpu").platforms(), libc: f("libc").platforms(), deprecated: f("deprecated").optionalText(), aliasOf: f("aliasOf").optionalText(), version: f("version").optionalText()}
	if v := f("hasBin"); v.node != nil {
		if b := v.boolean(false); b != nil {
			out.hasBin = *b
		}
	}
	for k, v := range f("peerDependenciesMeta").object(true, "") {
		m := v.object(false, "optional")
		p := field(m, "optional", v)
		meta := lockfile.PeerMeta{}
		if p.node != nil {
			if b := p.boolean(false); b != nil {
				meta.Optional = *b
			}
		}
		out.peerMeta[k] = meta
	}
	if v := f("resolution"); !v.absent() {
		out.resolution = decodeResolution(v)
	}
	return out
}
func decodeResolution(r yamlReader) *rawResolution {
	m := r.object(false, "integrity gitHosted directory tarball commit repo type path variants")
	f := func(k string) yamlReader { return field(m, k, r) }
	out := &rawResolution{integrity: f("integrity").optionalText(), directory: f("directory").optionalText(), tarball: f("tarball").optionalText(), commit: f("commit").optionalText(), repo: f("repo").optionalText(), subpath: f("path").optionalText()}
	_ = f("type").optionalText()
	if v := f("gitHosted"); v.node != nil {
		if b := v.boolean(false); b != nil {
			out.gitHosted = *b
		}
	}
	if out.subpath != nil {
		s := strings.TrimLeft(*out.subpath, "/")
		out.subpath = &s
		for _, p := range strings.Split(s, "/") {
			if p == "" || p == "." || p == ".." {
				out.subpath = nil
			}
		}
	}
	for _, v := range f("variants").sequence(true) {
		out.variants = append(out.variants, decodeVariant(v))
	}
	return out
}
func decodeVariant(r yamlReader) rawVariant {
	m := r.object(false, "targets resolution")
	out := rawVariant{bin: map[string]string{}}
	for _, t := range field(m, "targets", r).sequence(false) {
		v := t.object(false, "os cpu libc")
		out.targets = append(out.targets, lockfile.RuntimeTarget{OS: field(v, "os", t).text(), CPU: field(v, "cpu", t).text(), Libc: field(v, "libc", t).optionalText()})
	}
	b := field(m, "resolution", r)
	v := b.object(false, "type archive url integrity bin prefix")
	f := func(k string) yamlReader { return field(v, k, b) }
	_ = f("type").optionalText()
	out.archive = f("archive").optionalText()
	out.integrity = f("integrity").optionalText()
	out.prefix = f("prefix").optionalText()
	out.url = f("url").text()
	if bin := f("bin").resolve(); !bin.absent() {
		if bin.node != nil && bin.node.Kind == yaml.ScalarNode && bin.node.Tag == "!!str" {
			s := bin.text()
			out.bareBin = &s
		} else {
			out.bin = bin.strings()
		}
	}
	return out
}
func decodeSnapshot(r yamlReader) *rawSnapshot {
	m := r.object(false, "dependencies optionalDependencies bundledDependencies optional transitivePeerDependencies")
	f := func(k string) yamlReader { return field(m, k, r) }
	out := &rawSnapshot{deps: f("dependencies").strings(), optional: f("optionalDependencies").strings(), bundled: f("bundledDependencies").stringList(), transitivePeers: f("transitivePeerDependencies").stringList()}
	if b := f("optional").boolean(true); b != nil {
		out.isOptional = *b
	}
	return out
}
func (r *rawResolution) source() *lockfile.Source {
	if r == nil {
		return nil
	}
	if r.tarball != nil {
		s := *r.tarball
		if p, ok := strings.CutPrefix(s, "file:"); ok {
			return &lockfile.Source{Kind: lockfile.Tarball, Path: p}
		}
		if isHTTP(s) {
			return &lockfile.Source{Kind: lockfile.RemoteTarball, URL: s, Integrity: r.integrity, GitHosted: r.gitHosted}
		}
		return nil
	}
	if r.directory != nil {
		return &lockfile.Source{Kind: lockfile.Directory, Path: *r.directory}
	}
	if r.repo != nil && r.commit != nil {
		return &lockfile.Source{Kind: lockfile.Git, URL: *r.repo, Resolved: *r.commit, Integrity: r.integrity, Subpath: r.subpath}
	}
	return nil
}
func isHTTP(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}
func (r *rawLock) stripPatchMarkers() {
	for _, i := range r.importers {
		for _, deps := range []map[string]rawDep{i.deps, i.dev, i.optional, i.skipped} {
			for k, v := range deps {
				v.version = StripPatchHash(v.version)
				deps[k] = v
			}
		}
	}
	r.packages = rekeyPatches(r.packages)
	r.snapshots = rekeyPatches(r.snapshots)
	for _, s := range r.snapshots {
		for _, m := range []map[string]string{s.deps, s.optional} {
			for k, v := range m {
				m[k] = StripPatchHash(v)
			}
		}
	}
}
func rekeyPatches[V any](m map[string]V) map[string]V {
	out := map[string]V{}
	for _, k := range slices.Sorted(maps.Keys(m)) {
		newKey := StripPatchHash(k)
		if _, exists := out[newKey]; !exists {
			out[newKey] = m[k]
		}
	}
	return out
}
