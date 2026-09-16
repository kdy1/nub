package manifest

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"slices"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
)

// Package is the install-facing view; Raw retains fields used by scoped
// configuration, script policies, and writers. Manifest edits use Document.
type Package struct {
	Name, Version                                                         *string
	Dependencies, DevDependencies, OptionalDependencies, PeerDependencies map[string]string
	Scripts, Engines                                                      map[string]string
	Bundled                                                               *Bundled
	Workspaces                                                            *Workspaces
	UpdateIgnoreDependencies                                              []string
	Raw                                                                   *jsonvalue.Value
}
type Bundled struct {
	All   bool
	Names []string
}
type Workspaces struct {
	Kind              byte
	Patterns, Nohoist []string
	Catalog           map[string]string
	Catalogs          map[string]map[string]string
}

func ReadPackage(path string) (*Package, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParsePackage(data)
}
func ParsePackage(data []byte) (*Package, error) {
	raw, err := jsonvalue.Parse(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}))
	if err != nil {
		return nil, err
	}
	return PackageFromValue(raw)
}
func PackageFromValue(raw *jsonvalue.Value) (*Package, error) {
	if raw == nil || raw.Kind != '{' {
		return nil, fmt.Errorf("package.json must contain a JSON object")
	}
	p := &Package{Raw: raw}
	var err error
	p.Name, err = OptionalString(raw.Get("name"))
	if err != nil {
		return nil, fmt.Errorf("name: %w", err)
	}
	p.Version, err = OptionalString(raw.Get("version"))
	if err != nil {
		return nil, fmt.Errorf("version: %w", err)
	}
	p.Dependencies = Strings(raw.Get("dependencies"))
	p.DevDependencies = Strings(raw.Get("devDependencies"))
	p.OptionalDependencies = Strings(raw.Get("optionalDependencies"))
	p.PeerDependencies = Strings(raw.Get("peerDependencies"))
	p.Scripts = Strings(raw.Get("scripts"))
	p.Engines, err = Engines(raw.Get("engines"))
	if err != nil {
		return nil, err
	}
	canonical, err := parseBundled(raw.Get("bundledDependencies"))
	if err != nil {
		return nil, err
	}
	alias, err := parseBundled(raw.Get("bundleDependencies"))
	if err != nil {
		return nil, err
	}
	p.Bundled = canonical
	if p.Bundled == nil {
		p.Bundled = alias
	}
	p.Workspaces, err = ParseWorkspaces(raw.Get("workspaces"))
	if err != nil {
		return nil, err
	}
	if update := raw.Get("updateConfig"); !Absent(update) {
		if update.Kind != '{' {
			return nil, fmt.Errorf("updateConfig must be an object")
		}
		if value := update.Get("ignoreDependencies"); value != nil {
			p.UpdateIgnoreDependencies, err = StringArray(value)
			if err != nil {
				return nil, fmt.Errorf("updateConfig.ignoreDependencies: %w", err)
			}
		}
	}
	return p, nil
}
func Absent(v *jsonvalue.Value) bool { return v == nil || v.Kind == 'n' }
func OptionalString(v *jsonvalue.Value) (*string, error) {
	if Absent(v) {
		return nil, nil
	}
	if v.Kind != 's' {
		return nil, fmt.Errorf("expected string")
	}
	s := v.Text()
	return &s, nil
}
func StringArray(v *jsonvalue.Value) ([]string, error) {
	if v == nil || v.Kind != '[' {
		return nil, fmt.Errorf("expected an array of strings")
	}
	out := make([]string, len(v.Array))
	for i, item := range v.Array {
		if item.Kind != 's' {
			return nil, fmt.Errorf("expected an array of strings")
		}
		out[i] = item.Text()
	}
	return out, nil
}
func StrictStrings(v *jsonvalue.Value) (map[string]string, error) {
	if v == nil || v.Kind != '{' {
		return nil, fmt.Errorf("expected a string map")
	}
	out := make(map[string]string, len(v.Object))
	for _, entry := range v.Object {
		if entry.Value.Kind != 's' {
			return nil, fmt.Errorf("expected string for %s", entry.Key)
		}
		out[entry.Key] = entry.Value.Text()
	}
	return out, nil
}
func Engines(v *jsonvalue.Value) (map[string]string, error) {
	if v != nil && (v.Kind == 'd' || v.Kind == 'b') {
		return nil, fmt.Errorf("engines: expected a map")
	}
	return Strings(v), nil
}
func parseBundled(v *jsonvalue.Value) (*Bundled, error) {
	if Absent(v) {
		return nil, nil
	}
	if v.Kind == 'b' {
		return &Bundled{All: v.Scalar.(bool)}, nil
	}
	names, err := StringArray(v)
	if err != nil {
		return nil, fmt.Errorf("bundledDependencies must be a boolean or string array")
	}
	return &Bundled{Names: names}, nil
}
func (p *Package) BundledNames() []string {
	if p.Bundled == nil {
		return nil
	}
	if p.Bundled.All {
		return slices.Sorted(maps.Keys(p.Dependencies))
	}
	return slices.Clone(p.Bundled.Names)
}
func (p *Package) OptionalPeer(name string) bool {
	return isBool(p.Raw.Get("peerDependenciesMeta").Get(name).Get("optional"), true)
}
func (p *Package) RequiredPeers() map[string]string {
	out := map[string]string{}
	for name, version := range p.PeerDependencies {
		if !p.OptionalPeer(name) {
			out[name] = version
		}
	}
	return out
}
func isBool(v *jsonvalue.Value, want bool) bool {
	return v != nil && v.Kind == 'b' && v.Scalar.(bool) == want
}
func (p *Package) DependencyMeta(field string, value bool) []string {
	meta := p.Raw.Get("dependenciesMeta")
	out := []string{}
	if meta != nil && meta.Kind == '{' {
		for _, entry := range meta.Object {
			if isBool(entry.Value.Get(field), value) {
				out = append(out, entry.Key)
			}
		}
	}
	slices.Sort(out)
	return out
}

func ParseWorkspaces(v *jsonvalue.Value) (*Workspaces, error) {
	if Absent(v) {
		return nil, nil
	}
	w := &Workspaces{Kind: v.Kind}
	switch v.Kind {
	case 's':
		w.Patterns = []string{v.Text()}
	case '[':
		values, err := StringArray(v)
		if err != nil {
			return nil, err
		}
		w.Patterns = values
	case '{':
		var err error
		w.Patterns, err = StringArray(v.Get("packages"))
		if err != nil {
			return nil, fmt.Errorf("workspaces.packages: %w", err)
		}
		if field := v.Get("nohoist"); !Absent(field) {
			w.Nohoist, err = StringArray(field)
			if err != nil {
				return nil, err
			}
		}
		if field := v.Get("catalog"); !Absent(field) {
			w.Catalog, err = StrictStrings(field)
			if err != nil {
				return nil, err
			}
		}
		if field := v.Get("catalogs"); !Absent(field) {
			if field.Kind != '{' {
				return nil, fmt.Errorf("workspaces.catalogs must be an object")
			}
			w.Catalogs = map[string]map[string]string{}
			for _, entry := range field.Object {
				w.Catalogs[entry.Key], err = StrictStrings(entry.Value)
				if err != nil {
					return nil, err
				}
			}
		}
	default:
		return nil, fmt.Errorf("workspaces must be a string, array, or packages object")
	}
	return w, nil
}

func stringValues(values []string) *jsonvalue.Value {
	out := &jsonvalue.Value{Kind: '['}
	for _, s := range values {
		out.Array = append(out.Array, jsonvalue.String(s))
	}
	return out
}
func stringMap(values map[string]string) *jsonvalue.Value {
	out := jsonvalue.Object()
	for _, key := range slices.Sorted(maps.Keys(values)) {
		out.Put(key, jsonvalue.String(values[key]))
	}
	return out
}
func (w *Workspaces) Value() *jsonvalue.Value {
	if w == nil {
		return jsonvalue.Null()
	}
	if w.Kind == 's' {
		return jsonvalue.String(w.Patterns[0])
	}
	if w.Kind == '[' {
		return stringValues(w.Patterns)
	}
	out := jsonvalue.Object()
	out.Put("packages", stringValues(w.Patterns))
	if w.Nohoist != nil {
		out.Put("nohoist", stringValues(w.Nohoist))
	}
	if w.Catalog != nil {
		out.Put("catalog", stringMap(w.Catalog))
	}
	if w.Catalogs != nil {
		catalogs := jsonvalue.Object()
		for _, key := range slices.Sorted(maps.Keys(w.Catalogs)) {
			catalogs.Put(key, stringMap(w.Catalogs[key]))
		}
		out.Put("catalogs", catalogs)
	}
	return out
}
