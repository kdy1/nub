// Package settings resolves engine settings from invocation-owned sources.
package settings

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"slices"
)

//go:embed catalog.json
var catalogJSON []byte

type definition struct {
	Name, Type, Kind, DefaultText               string
	UnsupportedAdvice                           string
	Default                                     any
	CLI, Env, Npmrc, YAML, Precedence, Variants []string
	Layout, NpmShared, Explicit                 bool
	Managed                                     string
}

var definitions = func() map[string]definition {
	var rows []definition
	d := json.NewDecoder(bytes.NewReader(catalogJSON))
	d.UseNumber()
	if err := d.Decode(&rows); err != nil {
		panic(err)
	}
	out := make(map[string]definition, len(rows))
	for _, row := range rows {
		if n, ok := row.Default.(json.Number); ok {
			row.Default = parseUint(string(n))
		}
		if values, ok := row.Default.([]any); ok {
			list := make([]string, len(values))
			for i, v := range values {
				list[i] = v.(string)
			}
			row.Default = list
		}
		out[row.Name] = row
	}
	return out
}()

// Names returns the catalog in canonical order, including complex settings
// consumed by dedicated manifest and policy readers rather than this resolver.
func Names() []string {
	out := make([]string, 0, len(definitions))
	for name := range definitions {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// UnsupportedAdvice recognizes canonical names and file aliases, including
// settings omitted from the active resolver. Config writers use this to refuse
// an inert key instead of treating it as arbitrary free-form configuration.
func UnsupportedAdvice(key string) string {
	for _, d := range definitions {
		if d.UnsupportedAdvice != "" && (d.Name == key || slices.Contains(d.Npmrc, key) || slices.Contains(d.YAML, key)) {
			return d.UnsupportedAdvice
		}
	}
	return ""
}

func cloneValue(v any) any {
	if list, ok := v.([]string); ok {
		return slices.Clone(list)
	}
	return v
}
