package lockfile

import (
	"github.com/nubjs/nub/pm-go/internal/identity"
	"github.com/nubjs/nub/pm-go/internal/manifest"
	"maps"
	"strings"
)

type ImporterManifest struct {
	Path    string
	Package *manifest.Package
}
type DriftOptions struct {
	// Empty Kind compares resolution metadata, as the reference default does.
	Kind identity.Kind
	// Non-nil values carry the caller's identity-scoped source selection.
	ManifestOverrides        map[string]string
	ManifestIgnoredOptional  Set
	WorkspaceOverrides       map[string]string
	WorkspaceIgnoredOptional []string
	WorkspaceCatalogs        map[string]map[string]string
	WorkspaceInstall         bool
}

func (g *Graph) CheckDrift(project *manifest.Package, options DriftOptions) DriftStatus {
	options.WorkspaceInstall = false
	return g.CheckWorkspaceDrift([]ImporterManifest{{".", project}}, options)
}
func (g *Graph) CheckWorkspaceDrift(projects []ImporterManifest, options DriftOptions) DriftStatus {
	effective := map[string]string{}
	for _, p := range projects {
		if p.Path != "." {
			continue
		}
		effective = effectiveDriftOverrides(p.Package, options)
		if options.Kind == "" || recordsResolutionMetadata(options.Kind) {
			if status := g.resolutionMetadataDrift(p.Package, effective, options); !status.Fresh() {
				return status
			}
		}
		break
	}
	links := Set{}
	current := Set{}
	for _, p := range projects {
		current.Add(p.Path)
		if p.Path != "." && p.Package.Name != nil {
			links.Add(*p.Package.Name)
		}
	}
	for _, p := range projects {
		if status := g.CheckImporterDrift(p.Path, p.Package, effective, links); !status.Fresh() {
			return status
		}
	}
	if options.WorkspaceInstall {
		for _, path := range driftKeys(g.Importers) {
			if !current.Has(path) {
				return drift("workspace importer %s is in the lockfile but not in the workspace", path)
			}
		}
	}
	return DriftStatus{}
}
func effectiveDriftOverrides(p *manifest.Package, options DriftOptions) map[string]string {
	values := maps.Clone(options.ManifestOverrides)
	if values == nil {
		values = manifest.FlattenOverrides(p.Raw.Get("resolutions"), p.Raw.Get("pnpm").Get("overrides"), p.Raw.Get("aube").Get("overrides"), p.Raw.Get("overrides"))
	}
	maps.Copy(values, options.WorkspaceOverrides)
	values = ResolveCatalogOverrides(values, options.WorkspaceCatalogs)
	p.ResolveOverrideRefs(values)
	return values
}

func ResolveCatalogOverrides(values map[string]string, catalogs map[string]map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range values {
		if name, ok := strings.CutPrefix(value, "catalog:"); ok {
			if name == "" {
				name = "default"
			}
			if target, ok := OverrideTarget(key); ok {
				if spec, ok := catalogs[name][target]; ok {
					value = spec
				}
			}
		}
		out[key] = value
	}
	return out
}
func (g *Graph) resolutionMetadataDrift(project *manifest.Package, effective map[string]string, options DriftOptions) DriftStatus {
	locked := ResolveCatalogOverrides(g.Overrides, options.WorkspaceCatalogs)
	for _, key := range driftKeys(effective) {
		value := effective[key]
		if old, ok := locked[key]; !ok {
			return drift("overrides: manifest adds %s@%s", key, value)
		} else if old != value {
			return drift("overrides: %s changed (%s → %s)", key, old, value)
		}
	}
	for _, key := range driftKeys(locked) {
		if _, ok := effective[key]; !ok {
			return drift("overrides: manifest removes %s", key)
		}
	}
	ignored := maps.Clone(options.ManifestIgnoredOptional)
	if ignored == nil {
		ignored = Set{}
		for _, name := range []string{"pnpm", "aube"} {
			v := project.Raw.Get(name).Get("ignoredOptionalDependencies")
			if v != nil && v.Kind == '[' {
				for _, s := range v.Array {
					if s.Kind == 's' {
						ignored.Add(s.Text())
					}
				}
			}
		}
	}
	for _, name := range options.WorkspaceIgnoredOptional {
		ignored.Add(name)
	}
	for _, name := range ignored.Sorted() {
		if !g.IgnoredOptionalDependencies.Has(name) {
			return drift("ignoredOptionalDependencies: manifest adds %s", name)
		}
	}
	for _, name := range g.IgnoredOptionalDependencies.Sorted() {
		if !ignored.Has(name) {
			return drift("ignoredOptionalDependencies: manifest removes %s", name)
		}
	}
	runtimes := project.EngineDependencies("runtime")
	for _, name := range driftKeys(g.Runtimes) {
		pin := g.Runtimes[name]
		var entry *manifest.EngineDependency
		for i := range runtimes {
			if runtimes[i].Name == name {
				entry = &runtimes[i]
				break
			}
		}
		if entry == nil {
			return drift("devEngines.runtime: manifest no longer pins %s (lockfile records %s)", name, pin.Version)
		}
		if entry.Version != nil && *entry.Version != pin.Specifier {
			return drift("devEngines.runtime: %s changed (%s → %s)", name, pin.Specifier, *entry.Version)
		}
	}
	return DriftStatus{}
}
