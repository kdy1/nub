package manifest

import (
	"maps"
	"slices"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
)

// FlattenOverrides translates npm nested objects into ancestor selectors. The
// caller selects the permitted config sources before passing them here.
func FlattenOverrides(sources ...*jsonvalue.Value) map[string]string {
	out := map[string]string{}
	var nested func(string, *jsonvalue.Value)
	nested = func(chain string, value *jsonvalue.Value) {
		if chain == "" {
			return
		}
		for _, entry := range orderedOverrideFields(value) {
			if entry.Key == "." {
				if entry.Value.Kind == 's' {
					out[chain] = entry.Value.Text()
				}
				continue
			}
			key := chain + ">" + entry.Key
			if entry.Value.Kind == 's' {
				out[key] = entry.Value.Text()
			} else if entry.Value.Kind == '{' {
				nested(key, entry.Value)
			}
		}
	}
	for _, source := range sources {
		for _, entry := range orderedOverrideFields(source) {
			if entry.Value.Kind == 's' && entry.Key != "" {
				out[entry.Key] = entry.Value.Text()
			} else if entry.Value.Kind == '{' {
				nested(entry.Key, entry.Value)
			}
		}
	}
	return out
}

func (p *Package) DirectDependencyRange(name string) *string {
	for _, section := range []map[string]string{p.Dependencies, p.DevDependencies, p.OptionalDependencies} {
		if spec, ok := section[name]; ok {
			return &spec
		}
	}
	return nil
}

// ResolveOverrideRefs performs the reference's single substitution pass.
// Missing sibling references are removed and returned in selector order.
func (p *Package) ResolveOverrideRefs(overrides map[string]string) []string {
	var missing []string
	for _, key := range slices.Sorted(maps.Keys(overrides)) {
		name, ok := strings.CutPrefix(overrides[key], "$")
		if !ok {
			continue
		}
		if spec := p.DirectDependencyRange(name); spec != nil {
			overrides[key] = *spec
		} else {
			delete(overrides, key)
			missing = append(missing, key)
		}
	}
	return missing
}

func orderedOverrideFields(v *jsonvalue.Value) []jsonvalue.Field {
	if v == nil || v.Kind != '{' {
		return nil
	}
	fields := map[string]*jsonvalue.Value{}
	for _, field := range v.Object {
		fields[field.Key] = field.Value
	}
	var out []jsonvalue.Field
	for _, key := range slices.Sorted(maps.Keys(fields)) {
		out = append(out, jsonvalue.Field{Key: key, Value: fields[key]})
	}
	return out
}
