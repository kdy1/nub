package resolver

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/registry"
)

func cloneVersion(p *registry.Version) *registry.Version {
	out := *p
	out.Dependencies = maps.Clone(p.Dependencies)
	out.DevDependencies = maps.Clone(p.DevDependencies)
	out.OptionalDependencies = maps.Clone(p.OptionalDependencies)
	out.PeerDependencies = maps.Clone(p.PeerDependencies)
	out.PeerOptional = maps.Clone(p.PeerOptional)
	out.Bin = maps.Clone(p.Bin)
	out.Engines = maps.Clone(p.Engines)
	out.OS = slices.Clone(p.OS)
	out.CPU = slices.Clone(p.CPU)
	out.Libc = slices.Clone(p.Libc)
	out.Bundled = slices.Clone(p.Bundled)
	out.Raw = p.Raw.Clone()
	if p.Dist != nil {
		dist := *p.Dist
		dist.Attestations = p.Dist.Attestations.Clone()
		out.Dist = &dist
	}
	return &out
}

func (r *Resolver) hookVersion(ctx context.Context, p *registry.Version) (*registry.Version, error) {
	if r.ReadPackage == nil {
		return p, nil
	}
	after, err := r.ReadPackage(ctx, cloneVersion(p))
	if err != nil {
		return nil, &RegistryFailure{p.Name, "readPackage hook: " + err.Error()}
	}
	if after == nil {
		return nil, &RegistryFailure{p.Name, "readPackage hook: returned no package"}
	}
	if (after.Name != p.Name || after.Version != p.Version) && r.Warn != nil {
		r.Warn("WARN_AUBE_HOOK_IDENTITY_REWRITTEN", fmt.Sprintf("[pnpmfile] readPackage rewrote %s@%s identity to %s@%s; aube ignores identity edits", p.Name, p.Version, after.Name, after.Version))
	}
	after = cloneVersion(after)
	after.Name = p.Name
	after.Version = p.Version
	after.Dist = p.Dist
	after.OS = p.OS
	after.CPU = p.CPU
	after.Libc = p.Libc
	after.Bundled = p.Bundled
	after.BundleAll = p.BundleAll
	after.HasInstallScript = p.HasInstallScript
	after.Deprecated = p.Deprecated
	return after, nil
}

func jsonStringMap(values map[string]string) *jsonvalue.Value {
	v := jsonvalue.Object()
	for _, name := range slices.Sorted(maps.Keys(values)) {
		v.Put(name, jsonvalue.String(values[name]))
	}
	return v
}
func (r *Resolver) hookImporters(ctx context.Context, manifests []lockfile.ImporterManifest) ([]lockfile.ImporterManifest, error) {
	if r.ReadPackage == nil {
		return manifests, nil
	}
	out := slices.Clone(manifests)
	for i, importer := range out {
		p := *importer.Package
		p.Raw = p.Raw.Clone()
		if p.Raw == nil {
			p.Raw = jsonvalue.Object()
		}
		raw := p.Raw.Clone()
		name, version := "", "0.0.0"
		if p.Name != nil {
			name = *p.Name
		}
		if p.Version != nil {
			version = *p.Version
		}
		raw.Put("name", jsonvalue.String(name))
		raw.Put("version", jsonvalue.String(version))
		for _, field := range []struct {
			name   string
			values map[string]string
		}{{"dependencies", p.Dependencies}, {"devDependencies", p.DevDependencies}, {"optionalDependencies", p.OptionalDependencies}, {"peerDependencies", p.PeerDependencies}} {
			raw.Put(field.name, jsonStringMap(field.values))
		}
		meta, err := registry.ParseVersion(raw)
		if err != nil {
			return nil, &RegistryFailure{importer.Path, "readPackage hook: failed to build hook input from importer manifest: " + err.Error()}
		}
		after, err := r.ReadPackage(ctx, meta)
		if err != nil || after == nil {
			label := name
			if label == "" {
				label = importer.Path
			}
			if err == nil {
				err = fmt.Errorf("returned no package")
			}
			return nil, &RegistryFailure{label, "readPackage hook: " + err.Error()}
		}
		if (after.Name != name || after.Version != version) && r.Warn != nil {
			r.Warn("WARN_AUBE_HOOK_IDENTITY_REWRITTEN", fmt.Sprintf("[pnpmfile] readPackage rewrote importer %s@%s identity to %s@%s; aube ignores identity edits", name, version, after.Name, after.Version))
		}
		p.Dependencies = maps.Clone(after.Dependencies)
		p.DevDependencies = maps.Clone(after.DevDependencies)
		p.OptionalDependencies = maps.Clone(after.OptionalDependencies)
		p.PeerDependencies = maps.Clone(after.PeerDependencies)
		peers := jsonvalue.Object()
		for _, name := range slices.Sorted(maps.Keys(after.PeerOptional)) {
			meta := jsonvalue.Object()
			meta.Put("optional", &jsonvalue.Value{Kind: 'b', Scalar: after.PeerOptional[name]})
			peers.Put(name, meta)
		}
		if len(after.PeerOptional) == 0 {
			p.Raw.Remove("peerDependenciesMeta")
		} else {
			p.Raw.Put("peerDependenciesMeta", peers)
		}
		out[i].Package = &p
	}
	return out, nil
}
