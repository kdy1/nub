package installstate

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
)

// State files use serde's typed semantics: known struct fields are unique and
// case-sensitive, non-optional nulls are invalid, map duplicates are last-wins.
// A malformed sidecar is a cache miss, never a zero-valued successful install.
func decode(data []byte, target any) error {
	v, err := jsonvalue.ParseObjectFields(data)
	if err != nil {
		return err
	}
	v, err = checkedValue(v, reflect.TypeOf(target).Elem())
	if err != nil {
		return err
	}
	data, err = v.MarshalJSON()
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func checkedValue(v *jsonvalue.Value, t reflect.Type) (*jsonvalue.Value, error) {
	if t.Kind() == reflect.Pointer {
		if v.Kind == 'n' {
			return v, nil
		}
		return checkedValue(v, t.Elem())
	}
	bad := func() (*jsonvalue.Value, error) { return nil, fmt.Errorf("invalid install state %s", t) }
	switch t.Kind() {
	case reflect.Struct:
		if v.Kind != '{' {
			return bad()
		}
		fields := map[string]reflect.StructField{}
		for i := range t.NumField() {
			f := t.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			fields[name] = f
		}
		seen := map[string]bool{}
		out := jsonvalue.Object()
		for _, f := range v.Object {
			sf, ok := fields[f.Key]
			if !ok {
				continue
			}
			if seen[f.Key] {
				return bad()
			}
			seen[f.Key] = true
			if allowed := sf.Tag.Get("enum"); allowed != "" && f.Value.Kind != 'n' && !slices.Contains(strings.Split(allowed, "|"), f.Value.Text()) {
				return bad()
			}
			child, err := checkedValue(f.Value, sf.Type)
			if err != nil {
				return nil, err
			}
			out.Put(f.Key, child)
		}
		for name, f := range fields {
			if f.Tag.Get("required") == "true" && !seen[name] {
				return bad()
			}
		}
		return out, nil
	case reflect.Map:
		if v.Kind != '{' {
			return bad()
		}
		out := jsonvalue.Object()
		for _, f := range v.Object {
			child, err := checkedValue(f.Value, t.Elem())
			if err != nil {
				return nil, err
			}
			out.Put(f.Key, child)
		}
		return out, nil
	case reflect.Slice:
		if v.Kind != '[' {
			return bad()
		}
		out := &jsonvalue.Value{Kind: '['}
		for _, item := range v.Array {
			child, err := checkedValue(item, t.Elem())
			if err != nil {
				return nil, err
			}
			out.Array = append(out.Array, child)
		}
		return out, nil
	case reflect.String:
		if v.Kind != 's' {
			return bad()
		}
	case reflect.Bool:
		if v.Kind != 'b' {
			return bad()
		}
	default:
		if v.Kind != 'd' {
			return bad()
		}
	}
	return v, nil
}

func encode(value any) ([]byte, error) { return wireValue(reflect.ValueOf(value)).MarshalJSON() }

func wireValue(v reflect.Value) *jsonvalue.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return jsonvalue.Null()
		}
		return wireValue(v.Elem())
	case reflect.Struct:
		out := jsonvalue.Object()
		t := v.Type()
		for i := range v.NumField() {
			f := v.Field(i)
			tag := t.Field(i).Tag.Get("json")
			name, option, _ := strings.Cut(tag, ",")
			if option == "omitempty" && emptyField(f) {
				continue
			}
			out.Put(name, wireValue(f))
		}
		return out
	case reflect.Map:
		out := jsonvalue.Object()
		keys := v.MapKeys()
		slices.SortFunc(keys, func(a, b reflect.Value) int { return strings.Compare(a.String(), b.String()) })
		for _, key := range keys {
			out.Put(key.String(), wireValue(v.MapIndex(key)))
		}
		return out
	case reflect.Slice:
		out := &jsonvalue.Value{Kind: '['}
		for i := range v.Len() {
			out.Array = append(out.Array, wireValue(v.Index(i)))
		}
		return out
	case reflect.String:
		return jsonvalue.String(v.String())
	default:
		data, _ := json.Marshal(v.Interface())
		value, _ := jsonvalue.Parse(data)
		return value
	}
}

func emptyField(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Slice, reflect.Map, reflect.String:
		return v.Len() == 0
	default:
		return v.IsZero()
	}
}
