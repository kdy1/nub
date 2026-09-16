package resolver

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/registry"
	"github.com/nubjs/nub/pm-go/internal/semver"
)

func (d *driver) fetchKey(task resolveTask) metadataKey {
	key := metadataKey{name: task.registryName()}
	if _, err := semver.ParseEngineVersion(task.Range); d.keepTimes() && !d.r.Options.RegistrySupportsTime && task.Type == lockfile.Optional && err == nil {
		key.exact = task.Range
	}
	return key
}

func (d *driver) cacheSatisfies(key metadataKey) bool {
	p := d.packuments[key.name]
	if p == nil {
		return false
	}
	if key.exact != "" {
		return p.Versions[key.exact] != nil
	}
	_, compact := d.histories[key.name]
	return !compact
}

func (d *driver) ensureFetch(key metadataKey) {
	if d.cacheSatisfies(key) || d.fetchErrors[key.String()] != nil {
		return
	}
	route := d.routes[key.name]
	if route == "" && d.r.Client != nil {
		route = d.r.Client.Config.RegistryFor(key.name)
	}
	_, compact := d.histories[key.name]
	d.fetcher.ensure(metadataRequest{key: key, client: d.r.Client, route: route,
		cacheDir: d.r.CacheDir, mode: d.r.Options.Network,
		full: d.keepTimes() && !d.r.Options.RegistrySupportsTime, refresh: compact})
}

func (d *driver) prefetch(task resolveTask) {
	key := d.fetchKey(task)
	// Exact optionals retain their compact history even when a lockfile or
	// workspace may satisfy the task; this mirrors the reference seed gate.
	if key.exact != "" {
		d.ensureFetch(key)
		return
	}
	for _, prefix := range []string{"workspace:", "catalog:", "npm:", "jsr:"} {
		if strings.HasPrefix(task.Range, prefix) {
			return
		}
	}
	if nonRegistrySpecifier(task.Range) || d.existingNames.Has(task.Name) {
		return
	}
	if _, overridden := d.r.Options.Overrides[task.Name]; overridden {
		return
	}
	if version, ok := d.workspace[task.Name]; ok && semver.EngineSatisfies(version, task.Range) {
		return
	}
	d.ensureFetch(key)
}

func (d *driver) metadata(ctx context.Context, task resolveTask) (*registry.Packument, error) {
	key := d.fetchKey(task)
	if _, compact := d.histories[key.name]; key.exact == "" && compact {
		delete(d.fetchErrors, key.String())
	}
	for !d.cacheSatisfies(key) && (d.fetchErrors[key.String()] == nil || key.exact == "" && d.fetcher.hasExact(key.name)) {
		d.ensureFetch(key)
		result, err := d.fetcher.next(ctx)
		if err != nil {
			return nil, err
		}
		if result.err != nil {
			d.fetchErrors[result.key.String()] = result.err
			continue
		}
		if result.key.exact != "" {
			delete(d.fetchErrors, (metadataKey{name: result.key.name}).String())
		}
		d.mergeMetadata(result.key.name, result.packument, result.history)
	}
	if d.cacheSatisfies(key) {
		delete(d.fetchErrors, key.String())
		return d.packuments[key.name], nil
	}
	return nil, d.fetchErrors[key.String()]
}

func fetchMetadata(ctx context.Context, input metadataRequest) metadataResult {
	name, exact := input.key.name, input.key.exact
	result := metadataResult{key: input.key}
	if input.client == nil {
		result.err = &RegistryFailure{name, "registry client is unavailable"}
		return result
	}
	var p *registry.Packument
	var history *TrustHistory
	var err error
	if exact != "" {
		var selected *registry.ExactPackument
		selected, err = input.client.ExactMetadataAt(ctx, name, exact, input.route, input.mode)
		if err == nil {
			p = &registry.Packument{Name: name, Versions: map[string]*registry.Version{exact: selected.Metadata}, Time: selected.Time, Tags: map[string]string{}}
			history = &TrustHistory{Time: selected.Time, Evidence: map[string]TrustEvidence{}}
			for version, meta := range selected.History {
				history.Evidence[version] = EvidenceFor(meta)
			}
		} else if ctx.Err() == nil {
			p, err = input.client.MetadataAt(ctx, name, input.route, input.cacheDir, input.mode, input.full)
			if err == nil && p.Versions[exact] == nil {
				if input.mode != registry.Offline {
					p, err = input.client.RefreshMetadataAt(ctx, name, input.route, input.cacheDir, input.mode, input.full)
				}
				if err == nil && p.Versions[exact] == nil {
					err = fmt.Errorf("version %s is missing from the full packument", exact)
				}
			}
		}
	} else if input.refresh {
		p, err = input.client.RefreshMetadataAt(ctx, name, input.route, input.cacheDir, input.mode, input.full)
	} else {
		p, err = input.client.MetadataAt(ctx, name, input.route, input.cacheDir, input.mode, input.full)
	}
	if err != nil {
		result.err = &RegistryFailure{name, err.Error()}
	} else {
		result.packument, result.history = p, history
	}
	return result
}

// A full response usually supersedes compact metadata. Preserve exact releases
// proven by a fresher response when a cached full document omits them, retaining
// the historical trust evidence needed to check those releases.
func (d *driver) mergeMetadata(name string, p *registry.Packument, history *TrustHistory) {
	existing := d.packuments[name]
	previous, compact := d.histories[name]
	if history == nil {
		if compact && existing != nil {
			missing := false
			for version, meta := range existing.Versions {
				if _, ok := p.Versions[version]; !ok {
					p.Versions[version] = meta
					missing = true
				}
			}
			if missing {
				if p.Time == nil {
					p.Time = map[string]string{}
				}
				for version, date := range existing.Time {
					if _, ok := p.Time[version]; !ok {
						p.Time[version] = date
					}
				}
				d.packuments[name] = p
				previous.Time = p.Time
				d.histories[name] = previous
				return
			}
		}
		d.packuments[name] = p
		delete(d.histories, name)
		return
	}
	if !compact && existing != nil {
		missing := false
		for version := range p.Versions {
			if _, ok := existing.Versions[version]; !ok {
				missing = true
			}
		}
		if !missing {
			return
		}
		maps.Copy(existing.Versions, p.Versions)
		if existing.Time == nil {
			existing.Time = map[string]string{}
		}
		maps.Copy(existing.Time, p.Time)
		history.Time = existing.Time
		d.histories[name] = *history
		return
	}
	if existing != nil {
		maps.Copy(existing.Versions, p.Versions)
		if existing.Time == nil {
			existing.Time = map[string]string{}
		}
		maps.Copy(existing.Time, p.Time)
	} else {
		d.packuments[name] = p
		existing = p
	}
	if previous.Evidence == nil {
		previous.Evidence = map[string]TrustEvidence{}
	}
	maps.Copy(previous.Evidence, history.Evidence)
	previous.Time = existing.Time
	d.histories[name] = previous
}
