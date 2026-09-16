// Package testutil contains fixtures and projections used only by tests.
package testutil

import (
	"encoding/json"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"reflect"
)

// Empty Go containers and Rust's empty BTreeMaps/Vecs have one test projection.
// Optional scalars stay null; package keys, metadata, ranges and sources are
// compared without path or version normalization.
func GraphJSON(g *lockfile.Graph) ([]byte, error) {
	g = g.Clone()
	for _, p := range g.Packages {
		if p.Source != nil && p.Source.Kind == lockfile.RemoteTarball && p.Source.Integrity == nil {
			s := ""
			p.Source.Integrity = &s
		}
	}
	return json.Marshal(containers(reflect.ValueOf(g)))
}
func containers(v reflect.Value) any {
	if !v.IsValid() {
		return nil
	}
	if (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) && v.IsNil() {
		return nil
	}
	if v.CanInterface() {
		if _, ok := v.Interface().(json.Marshaler); ok {
			return v.Interface()
		}
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		return containers(v.Elem())
	case reflect.Map:
		out := map[string]any{}
		it := v.MapRange()
		for it.Next() {
			out[it.Key().String()] = containers(it.Value())
		}
		return out
	case reflect.Slice, reflect.Array:
		out := make([]any, v.Len())
		for i := range out {
			out[i] = containers(v.Index(i))
		}
		return out
	case reflect.Struct:
		out := map[string]any{}
		for i := 0; i < v.NumField(); i++ {
			out[v.Type().Field(i).Name] = containers(v.Field(i))
		}
		return out
	default:
		return v.Interface()
	}
}
