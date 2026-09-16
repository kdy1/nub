package resolver

import (
	"context"
	"fmt"
	"maps"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/registry"
	"github.com/nubjs/nub/pm-go/internal/semver"
)

func (d *driver) metadata(ctx context.Context, task resolveTask) (*registry.Packument, error) {
	name := task.registryName()
	full := d.keepTimes() && !d.r.Options.RegistrySupportsTime
	exact := ""
	if _, err := semver.ParseEngineVersion(task.Range); full && task.Type == lockfile.Optional && err == nil {
		exact = task.Range
	}
	_, compact := d.histories[name]
	if p := d.packuments[name]; p != nil {
		if exact != "" && p.Versions[exact] != nil || exact == "" && !compact {
			return p, nil
		}
	}
	key := name + "\x00" + exact
	if exact == "" && compact {
		delete(d.fetchErrors, key)
	}
	if err := d.fetchErrors[key]; err != nil {
		return nil, err
	}
	if d.r.Client == nil {
		return nil, &RegistryFailure{name, "registry client is unavailable"}
	}
	route := d.routes[name]
	if route == "" {
		route = d.r.Client.Config.RegistryFor(name)
	}
	var p *registry.Packument
	var history *TrustHistory
	var err error
	if exact != "" {
		var selected *registry.ExactPackument
		selected, err = d.r.Client.ExactMetadataAt(ctx, name, exact, route, d.r.Options.Network)
		if err == nil {
			p = &registry.Packument{Name: name, Versions: map[string]*registry.Version{exact: selected.Metadata}, Time: selected.Time, Tags: map[string]string{}}
			history = &TrustHistory{Time: selected.Time, Evidence: map[string]TrustEvidence{}}
			for version, meta := range selected.History {
				history.Evidence[version] = EvidenceFor(meta)
			}
		} else if ctx.Err() == nil {
			p, err = d.r.Client.MetadataAt(ctx, name, route, d.r.CacheDir, d.r.Options.Network, full)
			if err == nil && p.Versions[exact] == nil {
				if d.r.Options.Network != registry.Offline {
					p, err = d.r.Client.RefreshMetadataAt(ctx, name, route, d.r.CacheDir, d.r.Options.Network, full)
				}
				if err == nil && p.Versions[exact] == nil {
					err = fmt.Errorf("version %s is missing from the full packument", exact)
				}
			}
		}
	} else if compact {
		p, err = d.r.Client.RefreshMetadataAt(ctx, name, route, d.r.CacheDir, d.r.Options.Network, full)
	} else {
		p, err = d.r.Client.MetadataAt(ctx, name, route, d.r.CacheDir, d.r.Options.Network, full)
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		err = &RegistryFailure{name, err.Error()}
		d.fetchErrors[key] = err
		return nil, err
	}
	d.mergeMetadata(name, p, history)
	return d.packuments[name], nil
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
