package resolver

import (
	"context"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/nubjs/nub/pm-go/internal/gitcache"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/processenv"
	"github.com/nubjs/nub/pm-go/internal/registry"
	"github.com/nubjs/nub/pm-go/internal/semver"
)

type ResolutionMode uint8

const (
	Highest ResolutionMode = iota
	TimeBased
	LowestDirect
)

type MinimumReleaseAge struct {
	Minutes uint64
	Exclude PackageVersionPolicy
	Strict  bool
}

type Options struct {
	AutoInstallPeers, ExcludeLinksFromLockfile bool
	PeerContexts                               PeerContextOptions
	Platform                                   Platform
	Architectures                              Architectures
	Overrides                                  map[string]string
	IgnoredOptional                            lockfile.Set
	Mode                                       ResolutionMode
	WorkspaceImporters                         map[string]string
	IgnoreScripts                              bool
	MinimumReleaseAge                          *MinimumReleaseAge
	Catalogs                                   Catalogs
	NamedRegistries                            map[string]string
	PackageExtensions                          []PackageExtension
	AllowedDeprecated                          map[string]string
	TrustOff, BlockExoticSubdeps               bool
	Trust                                      TrustOptions
	VulnerableRanges                           map[string][]string
	GitShallowHosts                            []string
	RegistrySupportsTime                       bool
	Network                                    registry.NetworkMode
}

func DefaultOptions() Options {
	excludes := DefaultTrustExcludes()
	return Options{AutoInstallPeers: true, PeerContexts: DefaultPeerContextOptions(), Platform: HostPlatform(), BlockExoticSubdeps: true, Trust: TrustOptions{Exclude: excludes}}
}

type ResolvedPackage struct {
	Package      *lockfile.Package
	Deprecated   *string
	UnpackedSize *uint64
	Pending      int
}

// Resolver is an invocation-scoped dependency graph builder. Independent
// invocations must own their resolver, hook and callbacks; the registry client
// itself may share its safe transport and disk metadata cache.
type Resolver struct {
	Client            *registry.Client
	Env               processenv.Environment
	CacheDir, TempDir string
	Git               *gitcache.Cache
	Options           Options
	Now               func() time.Time
	ReadPackage       func(context.Context, *registry.Version) (*registry.Version, error)
	OnResolved        func(context.Context, ResolvedPackage) error
	Warn              func(code, message string)
}

func New(client *registry.Client, env processenv.Environment, cacheDir string) *Resolver {
	return &Resolver{Client: client, Env: env, CacheDir: cacheDir, TempDir: filepath.Join(cacheDir, "tmp"), Git: &gitcache.Cache{Root: filepath.Join(cacheDir, "git"), Env: env}, Options: DefaultOptions(), Now: time.Now}
}

func (r *Resolver) Resolve(ctx context.Context, manifests []lockfile.ImporterManifest, existing *lockfile.Graph, workspaceVersions map[string]string) (*lockfile.Graph, error) {
	if !filepath.IsAbs(r.Env.Dir) || !filepath.IsAbs(r.CacheDir) {
		return nil, fmt.Errorf("resolution requires absolute project and cache paths")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	manifests, err := r.hookImporters(ctx, manifests)
	if err != nil {
		return nil, err
	}
	d := newDriver(r, existing, workspaceVersions, manifests)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if d.cutoffPending && d.rootsPending == 0 {
			d.finishDirectCutoff()
		}
		if len(d.queue) == 0 {
			if len(d.deferred) > 0 {
				return nil, &RegistryFailure{"(resolver)", fmt.Sprintf("%d transitives still deferred when resolve completed", len(d.deferred))}
			}
			if n := len(d.peers); n > 0 {
				task := d.peers[n-1]
				d.peers = d.peers[:n-1]
				if !d.reuseVersion(task, true) {
					d.queue = append(d.queue, task)
				}
				continue
			}
			return d.finalize()
		}
		task := d.queue[0]
		d.queue = d.queue[1:]
		if err := d.process(ctx, task); err != nil {
			return nil, err
		}
	}
}

type driver struct {
	r                             *Resolver
	existing                      *lockfile.Graph
	locked                        *LockedIndex
	graph                         *lockfile.Graph
	workspace, workspaceImporters map[string]string
	declared                      map[string]lockfile.Set
	versions                      map[string][]string
	visited                       lockfile.Set
	queue, deferred, peers        []resolveTask
	rootsPending                  int
	cutoffPending                 bool
	publishedBy, timeCutoff       string
	preprocessor                  taskPreprocessor
	packuments                    map[string]*registry.Packument
	fetchErrors                   map[string]error
	routes                        map[string]string
	trust                         TrustOptions
}

func newDriver(r *Resolver, existing *lockfile.Graph, workspace map[string]string, manifests []lockfile.ImporterManifest) *driver {
	queue, importers := seedDirectDependencies(manifests, r.Options.IgnoredOptional, r.Options.AutoInstallPeers)
	g := lockfile.NewGraph()
	g.Importers = importers
	g.Times = map[string]string{}
	g.SkippedOptionalDependencies = map[string]map[string]string{}
	d := &driver{r: r, existing: existing, locked: NewLockedIndex(existing), graph: g, workspace: workspace, workspaceImporters: map[string]string{}, declared: map[string]lockfile.Set{}, versions: map[string][]string{}, visited: lockfile.Set{}, queue: queue, rootsPending: len(queue), cutoffPending: r.Options.Mode == TimeBased, packuments: map[string]*registry.Packument{}, fetchErrors: map[string]error{}, routes: map[string]string{}, trust: r.Options.Trust}
	maps.Copy(d.workspaceImporters, r.Options.WorkspaceImporters)
	for _, importer := range manifests {
		p := importer.Package
		if p.Name != nil {
			d.workspaceImporters[*p.Name] = importer.Path
		}
		names := lockfile.Set{}
		for _, deps := range []map[string]string{p.Dependencies, p.DevDependencies, p.OptionalDependencies} {
			for name := range deps {
				names.Add(name)
			}
		}
		if r.Options.AutoInstallPeers {
			for name := range p.PeerDependencies {
				if !p.OptionalPeer(name) {
					names.Add(name)
				}
			}
		}
		d.declared[importer.Path] = names
	}
	d.preprocessor = taskPreprocessor{Catalogs: r.Options.Catalogs, Overrides: CompileOverrides(r.Options.Overrides), NamedRegistries: r.Options.NamedRegistries}
	now := time.Time{}
	if r.Now != nil {
		now = r.Now()
	}
	d.trust.Now = now
	if m := r.Options.MinimumReleaseAge; m != nil && m.Minutes > 0 && !now.IsZero() && now.Unix() >= 0 {
		seconds := uint64(now.Unix())
		cutoff := uint64(0)
		if m.Minutes <= seconds/60 {
			cutoff = seconds - m.Minutes*60
		}
		d.publishedBy = time.Unix(int64(cutoff), 0).UTC().Format("2006-01-02T15:04:05.000Z")
	}
	return d
}

func (d *driver) done(task resolveTask) {
	if task.Root && d.rootsPending > 0 {
		d.rootsPending--
	}
}
func (d *driver) keepTimes() bool {
	return d.r.Options.Mode == TimeBased || d.r.Options.MinimumReleaseAge != nil || !d.r.Options.TrustOff
}
func (d *driver) warn(code, message string) {
	if d.r.Warn != nil {
		d.r.Warn(code, message)
	}
}
func (d *driver) link(task resolveTask, depPath, tail string) {
	if task.Root {
		d.graph.Importers[task.Importer] = append(d.graph.Importers[task.Importer], lockfile.DirectDep{Name: task.Name, DepPath: depPath, Type: task.Type, Specifier: task.lockfileSpecifier()})
	}
	if task.Parent != nil {
		if parent := d.graph.Packages[*task.Parent]; parent != nil {
			parent.Dependencies[task.Name] = tail
			if task.Type == lockfile.Optional {
				parent.OptionalDependencies[task.Name] = tail
			}
		}
	}
}
func (d *driver) skipOptional(task resolveTask) {
	if task.Root && task.OriginalSpecifier != nil {
		if d.graph.SkippedOptionalDependencies[task.Importer] == nil {
			d.graph.SkippedOptionalDependencies[task.Importer] = map[string]string{}
		}
		d.graph.SkippedOptionalDependencies[task.Importer][task.Name] = *task.OriginalSpecifier
	}
	d.done(task)
}
func (d *driver) emit(ctx context.Context, p *lockfile.Package, deprecated *string, size *uint64) error {
	if d.r.OnResolved == nil {
		return nil
	}
	return d.r.OnResolved(ctx, ResolvedPackage{p.Clone(), deprecated, size, len(d.queue) + len(d.deferred) + len(d.peers)})
}
func childTask(parent resolveTask, p *lockfile.Package, name, requested string, kind lockfile.DepType) resolveTask {
	ancestors := append(slices.Clone(parent.Ancestors), AncestorFrame{parent.Name, p.Version})
	return resolveTask{Name: name, Range: requested, Importer: parent.Importer, Type: kind, Parent: new(p.DepPath), Ancestors: ancestors}
}
func (d *driver) finishDirectCutoff() {
	paths := lockfile.Set{}
	for _, deps := range d.graph.Importers {
		for _, dep := range deps {
			paths.Add(dep.DepPath)
		}
	}
	for _, graph := range []*lockfile.Graph{d.graph, d.existing} {
		if graph != nil {
			for path, t := range graph.Times {
				if paths.Has(path) && t > d.timeCutoff {
					d.timeCutoff = t
				}
			}
		}
	}
	if d.timeCutoff != "" && (d.publishedBy == "" || d.timeCutoff < d.publishedBy) {
		d.publishedBy = d.timeCutoff
	}
	d.cutoffPending = false
	d.queue = append(d.queue, d.deferred...)
	d.deferred = nil
}
func (d *driver) reuseVersion(task resolveTask, highest bool) bool {
	best := ""
	for _, version := range d.versions[task.Name] {
		if !semver.EngineSatisfies(version, task.Range) || IsVulnerable(task.registryName(), version, d.r.Options.VulnerableRanges) {
			continue
		}
		if !highest {
			best = version
			break
		}
		if best == "" {
			best = version
			continue
		}
		a, ea := semver.ParseEngineVersion(version)
		b, eb := semver.ParseEngineVersion(best)
		if ea == nil && eb == nil && a.Compare(b) >= 0 || (ea != nil || eb != nil) && version >= best {
			best = version
		}
	}
	if best == "" {
		return false
	}
	d.link(task, task.Name+"@"+best, best)
	d.done(task)
	return true
}
func (d *driver) finalize() (*lockfile.Graph, error) {
	g := d.graph
	o := d.r.Options
	g.Settings = lockfile.Settings{AutoInstallPeers: o.AutoInstallPeers, ExcludeLinksFromLockfile: o.ExcludeLinksFromLockfile}
	g.Overrides = maps.Clone(o.Overrides)
	g.IgnoredOptionalDependencies = maps.Clone(o.IgnoredOptional)
	for _, rule := range d.preprocessor.Overrides {
		if catalog, ok := strings.CutPrefix(rule.Replacement, "catalog:"); ok {
			if catalog == "" {
				catalog = "default"
			}
			if replacement, ok := o.Catalogs[catalog][rule.Target.Name]; ok && !strings.HasPrefix(replacement, "catalog:") {
				g.Overrides[rule.RawKey] = replacement
			}
		}
	}
	g.Catalogs = MaterializeCatalogPicks(d.preprocessor.CatalogPicks, d.versions)
	var hoisted AutoInstalledPeers
	if o.AutoInstallPeers {
		hoisted = HoistAutoInstalledPeers(g)
	}
	out, err := ApplyPeerContexts(g, o.PeerContexts)
	if err != nil {
		return nil, err
	}
	RemoveAutoInstalledPeers(out, hoisted)
	if len(o.NamedRegistries) > 0 && d.r.Client != nil {
		for _, p := range out.Packages {
			if p.Source == nil && p.TarballURL != nil {
				a, ea := url.Parse(*p.TarballURL)
				b, eb := url.Parse(d.r.Client.Config.RegistryFor(p.RegistryName()))
				if ea == nil && eb == nil && a.Host != b.Host {
					p.ForceTarballURL = true
				}
			}
		}
	}
	return out, nil
}

func (d *driver) local(ctx context.Context, task resolveTask) error {
	raw, anchor, err := prepareLocalSource(task, d.graph.Packages, d.r.Env.Dir, d.r.Options.BlockExoticSubdeps)
	if err != nil {
		return err
	}
	source := raw
	var meta LocalManifest
	var integrity *string
	switch source.Kind {
	case lockfile.Git:
		source, meta, integrity, err = ResolveGitSource(ctx, task.Name, source, d.r.Git, d.r.Client, d.r.Options.Network, gitcache.HostInList(source.URL, d.r.Options.GitShallowHosts))
		if err != nil {
			return &RegistryFailure{task.Name, "git resolve " + registry.RedactURL(task.Range) + ": " + err.Error()}
		}
		if integrity == nil {
			integrity = d.locked.FindLocalIntegrity(task.Name, meta.Version, &source)
		}
		if source.Kind == lockfile.Git && source.Integrity == nil {
			source.Integrity = integrity
		}
	case lockfile.RemoteTarball:
		source, meta, err = ResolveRemoteTarball(ctx, task.Name, source, d.r.Client, d.r.Options.Network)
		if err != nil {
			return &RegistryFailure{task.Name, "remote tarball " + registry.RedactURL(task.Range) + ": " + err.Error()}
		}
		if source.Integrity != nil && *source.Integrity != "" {
			integrity = source.Integrity
		}
	default:
		source = RebaseLocal(source, anchor, d.r.Env.Dir)
		if source.Kind == lockfile.Exec {
			if d.r.Options.IgnoreScripts {
				return &RegistryFailure{task.Name, source.Specifier() + " requires executing its generator, but scripts are disabled"}
			}
			if err := os.MkdirAll(d.r.TempDir, 0700); err != nil {
				return err
			}
			meta, err = ResolveExecManifest(ctx, d.r.Env, d.r.TempDir, task.Name, source)
			if err != nil {
				return err
			}
		} else {
			meta, err = ReadLocalManifest(raw, anchor)
			if err != nil {
				meta = LocalManifest{task.Name, "0.0.0", map[string]string{}}
			}
		}
	}
	deps := ApplyLocalExtensions(task.Name, meta.Version, meta.Dependencies, d.r.Options.PackageExtensions)
	path := source.DepPath(task.Name)
	d.link(task, path, strings.TrimPrefix(path, task.Name+"@"))
	if d.visited.Add(path) {
		p := lockfile.NewPackage(task.Name, meta.Version)
		p.DepPath = path
		p.Source = &source
		p.Integrity = integrity
		d.graph.Packages[path] = p
		if err := d.emit(ctx, p, nil, nil); err != nil {
			return err
		}
		if source.Kind != lockfile.Link {
			for _, name := range slices.Sorted(maps.Keys(deps)) {
				d.queue = append(d.queue, childTask(task, p, name, deps[name], lockfile.Production))
			}
		}
	}
	d.done(task)
	return nil
}
