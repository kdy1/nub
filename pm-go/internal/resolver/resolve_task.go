package resolver

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/registry"
	"github.com/nubjs/nub/pm-go/internal/semver"
	"github.com/nubjs/nub/pm-go/internal/spec"
	"github.com/nubjs/nub/pm-go/internal/store"
)

type ResolutionError struct {
	Name, Range, Importer, RegistryName string
	OriginalSpecifier                   *string
	Ancestors                           []AncestorFrame
	AgeGate                             AgeVerdict
	Minutes                             uint64
}

func (e *ResolutionError) Error() string {
	switch e.AgeGate {
	case Undeterminable:
		return fmt.Sprintf("cannot check the publish age of %s@%s — the registry served no publish time for any matching version", e.Name, e.Range)
	case TooNew:
		return fmt.Sprintf("no version of %s matching %s is older than %d minute(s) (minimumReleaseAgeStrict=true)", e.Name, e.Range, e.Minutes)
	default:
		return fmt.Sprintf("no version of %s matches range `%s`", e.Name, e.Range)
	}
}
func (e *ResolutionError) Code() string {
	switch e.AgeGate {
	case Undeterminable:
		return "ERR_AUBE_RELEASE_AGE_MISSING_TIME"
	case TooNew:
		return "ERR_AUBE_NO_MATURE_MATCHING_VERSION"
	default:
		return "ERR_AUBE_NO_MATCHING_VERSION"
	}
}

type WorkspaceError struct {
	Name, Spec, Target, Range, Version, Importer string
	Known                                        []string
	Mismatch                                     bool
}

func (e *WorkspaceError) Error() string {
	if e.Mismatch {
		return fmt.Sprintf("in %s: `\"%s\": \"%s\"` wants `%s` at `%s`, but this workspace has %s@%s", e.Importer, e.Name, e.Spec, e.Target, e.Range, e.Target, e.Version)
	}
	return fmt.Sprintf("in %s: `\"%s\": \"%s\"` names workspace package `%s`, which is not in this workspace", e.Importer, e.Name, e.Spec, e.Target)
}
func (e *WorkspaceError) Code() string {
	if e.Mismatch {
		return "ERR_AUBE_NO_MATCHING_VERSION"
	}
	return "ERR_AUBE_WORKSPACE_PKG_NOT_FOUND"
}

func (d *driver) workspaceError(task resolveTask, target string) *WorkspaceError {
	requested := task.Range
	if task.OriginalSpecifier != nil {
		requested = *task.OriginalSpecifier
	}
	return &WorkspaceError{Name: task.Name, Spec: requested, Target: target, Importer: task.Importer, Known: slices.Sorted(maps.Keys(d.workspace))}
}
func (d *driver) rewriteWorkspace(task *resolveTask) error {
	if !task.Root {
		return nil
	}
	w, ok := spec.ParseWorkspace(task.Range)
	if !ok || w.Kind == "range" {
		return nil
	}
	if w.Kind == "path" {
		task.Range = "link:" + w.Path
		return nil
	}
	v, ok := d.workspace[w.Name]
	if !ok {
		return d.workspaceError(*task, w.Name)
	}
	if !spec.WorkspaceRangeBinds(v, w.Range) {
		e := d.workspaceError(*task, w.Name)
		e.Mismatch = true
		e.Version = v
		e.Range = w.Range
		return e
	}
	importer, ok := d.workspaceImporters[w.Name]
	if !ok {
		return d.workspaceError(*task, w.Name)
	}
	task.Range = "link:" + lockfile.LinkFromImporter(task.Importer, importer)
	return nil
}
func (d *driver) linkWorkspace(task resolveTask) bool {
	version, ok := d.workspace[task.Name]
	if !ok {
		return false
	}
	matched := false
	if rest, ok := strings.CutPrefix(task.Range, "workspace:"); ok {
		matched = spec.WorkspaceRangeBinds(version, rest)
	} else {
		matched = strings.HasPrefix(task.Range, "link:") || strings.HasPrefix(task.Range, "portal:") || task.Range == "" || task.Range == "*" || semver.EngineSatisfies(version, task.Range)
	}
	if !matched {
		return false
	}
	d.link(task, task.Name+"@"+version, version)
	d.done(task)
	return true
}
func (d *driver) reuseLocked(ctx context.Context, task resolveTask) (bool, error) {
	old := d.locked.FindSatisfying(task.Name, task.Range, task.registryName(), d.r.Options.VulnerableRanges)
	if old == nil {
		return false, nil
	}
	if task.Type == lockfile.Optional && !Supported(old.OS, old.CPU, old.Libc, d.r.Options.Platform, d.r.Options.Architectures) {
		d.skipOptional(task)
		return true, nil
	}
	path := task.Name + "@" + old.Version
	d.link(task, path, old.Version)
	if d.visited.Add(path) {
		d.versions[task.Name] = append(d.versions[task.Name], old.Version)
		if d.keepTimes() && d.existing != nil {
			if date, ok := d.existing.Times[path]; ok {
				d.graph.Times[path] = date
			}
		}
		p := old.Clone()
		p.Name = task.Name
		p.DepPath = path
		p.Dependencies = map[string]string{}
		p.OptionalDependencies = map[string]string{}
		d.graph.Packages[path] = p
		if err := d.emit(ctx, p, nil, nil); err != nil {
			return true, err
		}
		for _, name := range slices.Sorted(maps.Keys(old.Dependencies)) {
			if slices.Contains(old.BundledDependencies, name) {
				continue
			}
			value := strings.TrimPrefix(old.Dependencies[name], name+"@")
			value, _, _ = strings.Cut(value, "(")
			kind := lockfile.Production
			if _, optional := old.OptionalDependencies[name]; optional {
				kind = lockfile.Optional
			}
			d.queue = append(d.queue, childTask(task, p, name, value, kind))
		}
	}
	d.done(task)
	return true, nil
}
func (d *driver) process(ctx context.Context, task resolveTask) error {
	keep, route, err := d.preprocessor.apply(&task)
	if err != nil {
		return err
	}
	if !keep {
		d.done(task)
		return nil
	}
	if route != nil {
		d.routes[route.Name] = route.Registry
	}
	if !task.Root && (strings.HasPrefix(task.Range, "link:") || strings.HasPrefix(task.Range, "portal:")) && d.linkWorkspace(task) {
		return nil
	}
	if err := d.rewriteWorkspace(&task); err != nil {
		return err
	}
	if nonRegistrySpecifier(task.Range) {
		return d.local(ctx, task)
	}
	if d.linkWorkspace(task) {
		return nil
	}
	if strings.HasPrefix(task.Range, "workspace:") {
		if _, ok := d.workspace[task.Name]; !ok {
			return d.workspaceError(task, task.Name)
		}
	}
	if d.reuseVersion(task, false) {
		return nil
	}
	if reused, err := d.reuseLocked(ctx, task); reused || err != nil {
		return err
	}
	name := task.registryName()
	packument, err := d.metadata(ctx, task)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if task.Type == lockfile.Optional {
			d.done(task)
			return nil
		}
		return err
	}
	if d.cutoffPending && !task.Root {
		d.deferred = append(d.deferred, task)
		return nil
	}
	opts := PickOptions{Lowest: task.Root && d.r.Options.Mode != Highest, Strict: true, Cutoff: d.publishedBy, ExemptCutoff: d.timeCutoff}
	if p := d.locked.FindFirstInRange(task.Name, task.Range); p != nil && !IsVulnerable(name, p.Version, d.r.Options.VulnerableRanges) {
		opts.Locked = p.Version
	}
	if age := d.r.Options.MinimumReleaseAge; age != nil {
		opts.Strict = age.Strict
		opts.AgeExempt = func(version string, _ *semver.Version) bool { return age.Exclude.Excludes(name, version) }
	}
	result := Pick(packument, task.Range, opts)
	if result.Version == nil {
		if result.AgeGate == Clears && task.Type == lockfile.Optional {
			d.warn("WARN_AUBE_SKIPPED_OPTIONAL_NO_MATCHING_VERSION", fmt.Sprintf("skipping optional dep %s: no version matches `%s`", task.Name, task.Range))
			d.skipOptional(task)
			return nil
		}
		e := &ResolutionError{Name: task.Name, RegistryName: name, Range: task.Range, Importer: task.Importer, Ancestors: task.Ancestors, OriginalSpecifier: task.OriginalSpecifier}
		if age := d.r.Options.MinimumReleaseAge; age != nil {
			e.Minutes = age.Minutes
			e.AgeGate = result.AgeGate
		}
		return e
	}
	picked := PreferNonVulnerable(name, packument, task.Range, result.Version, opts, d.r.Options.VulnerableRanges)
	if !d.r.Options.TrustOff {
		history, compact := d.histories[name]
		if !compact {
			history = HistoryFor(packument)
		}
		if trustErr := history.Check(packument.Name, picked.Version, EvidenceFor(picked), d.trust); trustErr != nil {
			if trustErr.MissingTime {
				return trustErr
			}
			repick := RepickPastTrustDowngrade(packument, name, task.Range, picked.Version, opts, d.trust, d.r.Options.VulnerableRanges)
			if repick == nil {
				return trustErr
			}
			d.warn("WARN_AUBE_TRUST_DOWNGRADE_SKIPPED", fmt.Sprintf("skipped %s@%s (trustPolicy=no-downgrade): earlier published version %s had %s but this version has %s; resolved to %s@%s instead", name, picked.Version, trustErr.PriorVersion, trustErr.PriorEvidence.Label(), trustErr.CurrentEvidence.Label(), name, repick.Version))
			picked = repick
		}
	}
	meta := cloneVersion(picked)
	if !d.visited.Has(task.Name + "@" + picked.Version) {
		ApplyPackageExtensions(meta, d.r.Options.PackageExtensions)
		meta, err = d.r.hookVersion(ctx, meta)
		if err != nil {
			return err
		}
	}
	if !Supported(meta.OS, meta.CPU, meta.Libc, d.r.Options.Platform, d.r.Options.Architectures) {
		if task.Type == lockfile.Optional {
			d.skipOptional(task)
			return nil
		}
		d.warn("WARN_AUBE_UNSUPPORTED_PLATFORM_INSTALL", fmt.Sprintf("required dep %s@%s declares unsupported platform; installing anyway", task.Name, meta.Version))
	}
	path := task.Name + "@" + meta.Version
	if d.keepTimes() {
		if date, ok := packument.Time[picked.Version]; ok {
			d.graph.Times[path] = date
		} else if d.existing != nil {
			if date, ok := d.existing.Times[path]; ok {
				d.graph.Times[path] = date
			}
		}
	}
	d.link(task, path, meta.Version)
	if !d.visited.Add(path) {
		d.done(task)
		return nil
	}
	d.versions[task.Name] = append(d.versions[task.Name], meta.Version)
	p := packageFromVersion(task, meta)
	d.graph.Packages[path] = p
	deprecated := meta.Deprecated
	if IsDeprecationAllowed(task.Name, meta.Version, d.r.Options.AllowedDeprecated) {
		deprecated = nil
	}
	var size *uint64
	if meta.Dist != nil {
		size = meta.Dist.UnpackedSize
	}
	if err := d.emit(ctx, p, deprecated, size); err != nil {
		return err
	}
	if err := d.enqueueRegistry(task, p, meta); err != nil {
		return err
	}
	d.done(task)
	return nil
}

func packageFromVersion(task resolveTask, v *registry.Version) *lockfile.Package {
	p := lockfile.NewPackage(task.Name, v.Version)
	p.AliasOf = task.RealName
	if v.Dist != nil {
		p.Integrity = v.Dist.Integrity
		if p.Integrity == nil && v.Dist.Shasum != nil {
			if integrity, ok := store.ShasumToSRI(*v.Dist.Shasum); ok {
				p.Integrity = &integrity
			}
		}
		p.TarballURL = new(v.Dist.Tarball)
		p.RegistryGitHosted = strings.Contains(v.Dist.Tarball, "://npm.pkg.github.com/")
	}
	p.PeerDependencies = maps.Clone(v.PeerDependencies)
	if p.PeerDependencies == nil {
		p.PeerDependencies = map[string]string{}
	}
	for name, optional := range v.PeerOptional {
		p.PeerDependenciesMeta[name] = lockfile.PeerMeta{Optional: optional}
		if _, ok := p.PeerDependencies[name]; !ok {
			p.PeerDependencies[name] = "*"
		}
	}
	p.OS = slices.Clone(v.OS)
	p.CPU = slices.Clone(v.CPU)
	p.Libc = slices.Clone(v.Libc)
	bundled := lockfile.Set{}
	for _, name := range v.Bundled {
		bundled.Add(name)
	}
	if v.BundleAll {
		for name := range v.Dependencies {
			bundled.Add(name)
		}
	}
	p.BundledDependencies = bundled.Sorted()
	p.Engines = maps.Clone(v.Engines)
	p.Bin = maps.Clone(v.Bin)
	if bin, ok := p.Bin[""]; ok {
		delete(p.Bin, "")
		p.Bin[task.registryName()] = bin
	}
	p.DeclaredDependencies = map[string]string{}
	maps.Copy(p.DeclaredDependencies, v.Dependencies)
	maps.Copy(p.DeclaredDependencies, v.OptionalDependencies)
	p.License = v.License
	p.FundingURL = v.Funding
	p.HasInstallScript = v.HasInstallScript
	p.Deprecated = v.Deprecated
	if v.Deprecated != nil {
		p.ExtraMeta = map[string]*jsonvalue.Value{"deprecated": jsonvalue.String(*v.Deprecated)}
	}
	return p
}
func (d *driver) enqueueRegistry(task resolveTask, p *lockfile.Package, meta *registry.Version) error {
	for _, section := range []struct {
		deps map[string]string
		kind lockfile.DepType
	}{{meta.Dependencies, lockfile.Production}, {meta.OptionalDependencies, lockfile.Optional}} {
		for _, name := range slices.Sorted(maps.Keys(section.deps)) {
			requested := section.deps[name]
			if slices.Contains(p.BundledDependencies, name) || section.kind == lockfile.Optional && d.r.Options.IgnoredOptional.Has(name) {
				continue
			}
			if d.r.Options.BlockExoticSubdeps && nonRegistrySpecifier(requested) {
				if section.kind == lockfile.Production {
					return &RegistryFailure{name, fmt.Sprintf("uses exotic specifier %q which is blocked by blockExoticSubdeps (declared by %s)", registry.RedactURL(requested), task.Name)}
				}
				d.warn("WARN_AUBE_EXOTIC_SUBDEP_SKIPPED", fmt.Sprintf("skipping optional dependency %s of %s — exotic specifier %q blocked by blockExoticSubdeps", name, task.Name, registry.RedactURL(requested)))
				continue
			}
			child := childTask(task, p, name, requested, section.kind)
			d.prefetch(child)
			d.queue = append(d.queue, child)
		}
	}
	if d.r.Options.AutoInstallPeers {
		for _, name := range slices.Sorted(maps.Keys(meta.PeerDependencies)) {
			requested := meta.PeerDependencies[name]
			if meta.PeerOptional[name] || d.declared[task.Importer].Has(name) || task.Importer != "." && d.r.Options.PeerContexts.ResolveFromWorkspaceRoot && d.declared["."].Has(name) {
				continue
			}
			if slices.ContainsFunc(task.Ancestors, func(a AncestorFrame) bool { return a.Name == name }) {
				continue
			}
			if _, ok := meta.Dependencies[name]; ok {
				continue
			}
			if _, ok := meta.OptionalDependencies[name]; ok {
				continue
			}
			if slices.Contains(p.BundledDependencies, name) {
				continue
			}
			if d.r.Options.BlockExoticSubdeps && nonRegistrySpecifier(requested) {
				d.warn("WARN_AUBE_EXOTIC_SUBDEP_SKIPPED", fmt.Sprintf("skipping peer dependency %s of %s — exotic specifier %q blocked by blockExoticSubdeps", name, task.Name, registry.RedactURL(requested)))
				continue
			}
			child := childTask(task, p, name, requested, lockfile.Production)
			d.prefetch(child)
			d.peers = append(d.peers, child)
		}
	}
	return nil
}
