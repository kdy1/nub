package bun

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
	"github.com/nubjs/nub/pm-go/internal/manifest"
)

type rawLock struct {
	version, configVersion      uint32
	workspaces                  map[string]*rawWorkspace
	packages                    map[string][]*jsonvalue.Value
	overrides, patches, catalog map[string]string
	catalogs                    map[string]map[string]string
	trusted                     []string
	extra                       map[string]*jsonvalue.Value
}
type rawWorkspace struct {
	dependencies, devDependencies, optionalDependencies map[string]string
	extra                                               map[string]*jsonvalue.Value
}
type rawMeta struct {
	dependencies, optionalDependencies, peerDependencies map[string]string
	optionalPeers, os, cpu, libc                         []string
	bin                                                  *jsonvalue.Value
	extra                                                map[string]*jsonvalue.Value
}
type entry struct {
	ident                  string
	meta                   rawMeta
	integrity, registryURL *string
}

// Typed fields reject duplicates; map keys and opaque JSON fields overwrite.
func fields(v *jsonvalue.Value, known ...string) (map[string]*jsonvalue.Value, map[string]*jsonvalue.Value, error) {
	if v == nil || v.Kind != '{' {
		return nil, nil, fmt.Errorf("expected an object")
	}
	isKnown := map[string]bool{}
	for _, key := range known {
		isKnown[key] = true
	}
	values, extras := map[string]*jsonvalue.Value{}, map[string]*jsonvalue.Value{}
	for _, f := range v.Object {
		if isKnown[f.Key] {
			if values[f.Key] != nil {
				return nil, nil, fmt.Errorf("duplicate field %s", f.Key)
			}
			values[f.Key] = f.Value
		} else {
			extras[f.Key] = collapse(f.Value)
		}
	}
	return values, extras, nil
}
func collapse(v *jsonvalue.Value) *jsonvalue.Value {
	out := v.Clone()
	if out.Kind == '{' {
		out.Object = nil
		for _, field := range v.Object {
			out.Put(field.Key, collapse(field.Value))
		}
	} else if out.Kind == '[' {
		for i, item := range v.Array {
			out.Array[i] = collapse(item)
		}
	}
	return out
}
func stringMap(v *jsonvalue.Value) (map[string]string, error) {
	if v == nil {
		return map[string]string{}, nil
	}
	return manifest.StrictStrings(v)
}
func stringList(v *jsonvalue.Value) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	return manifest.StringArray(v)
}
func uint32Value(v *jsonvalue.Value) (uint32, error) {
	if v == nil || v.Kind != 'd' {
		return 0, fmt.Errorf("expected an unsigned integer")
	}
	n, err := strconv.ParseUint(fmt.Sprint(v.Scalar), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("expected an unsigned integer")
	}
	return uint32(n), nil
}
func objectItems(v *jsonvalue.Value) ([]jsonvalue.Field, error) {
	if v == nil {
		return nil, nil
	}
	if v.Kind != '{' {
		return nil, fmt.Errorf("expected an object")
	}
	return v.Object, nil
}
func parseRaw(data []byte) (*rawLock, error) {
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("invalid UTF-8 in bun.lock")
	}
	v, err := jsonvalue.ParseObjectFields(stripJSONC(data))
	if err != nil {
		return nil, err
	}
	f, extra, err := fields(v, "lockfileVersion", "configVersion", "workspaces", "packages", "overrides", "patchedDependencies", "trustedDependencies", "catalog", "catalogs")
	if err != nil {
		return nil, err
	}
	r := &rawLock{configVersion: 1, workspaces: map[string]*rawWorkspace{}, packages: map[string][]*jsonvalue.Value{}, catalogs: map[string]map[string]string{}, extra: extra}
	if r.version, err = uint32Value(f["lockfileVersion"]); err != nil {
		return nil, fmt.Errorf("lockfileVersion: %w", err)
	}
	if f["configVersion"] != nil {
		if r.configVersion, err = uint32Value(f["configVersion"]); err != nil {
			return nil, fmt.Errorf("configVersion: %w", err)
		}
	}
	for _, field := range []struct {
		key string
		dst *map[string]string
	}{{"overrides", &r.overrides}, {"patchedDependencies", &r.patches}, {"catalog", &r.catalog}} {
		if *field.dst, err = stringMap(f[field.key]); err != nil {
			return nil, fmt.Errorf("%s: %w", field.key, err)
		}
	}
	if r.trusted, err = stringList(f["trustedDependencies"]); err != nil {
		return nil, fmt.Errorf("trustedDependencies: %w", err)
	}
	items, err := objectItems(f["workspaces"])
	if err != nil {
		return nil, fmt.Errorf("workspaces: %w", err)
	}
	for _, item := range items {
		ws, err := parseWorkspace(item.Value)
		if err != nil {
			return nil, fmt.Errorf("workspaces[%q]: %w", item.Key, err)
		}
		r.workspaces[item.Key] = ws
	}
	items, err = objectItems(f["packages"])
	if err != nil {
		return nil, fmt.Errorf("packages: %w", err)
	}
	for _, item := range items {
		if item.Value.Kind != '[' {
			return nil, fmt.Errorf("packages[%q]: expected an array", item.Key)
		}
		r.packages[item.Key] = collapse(item.Value).Array
	}
	items, err = objectItems(f["catalogs"])
	if err != nil {
		return nil, fmt.Errorf("catalogs: %w", err)
	}
	for _, item := range items {
		entries, err := stringMap(item.Value)
		if err != nil {
			return nil, fmt.Errorf("catalogs[%q]: %w", item.Key, err)
		}
		r.catalogs[item.Key] = entries
	}
	return r, nil
}
func parseWorkspace(v *jsonvalue.Value) (*rawWorkspace, error) {
	f, extra, err := fields(v, "dependencies", "devDependencies", "optionalDependencies")
	if err != nil {
		return nil, err
	}
	ws := &rawWorkspace{extra: extra}
	for _, field := range []struct {
		key string
		dst *map[string]string
	}{{"dependencies", &ws.dependencies}, {"devDependencies", &ws.devDependencies}, {"optionalDependencies", &ws.optionalDependencies}} {
		if *field.dst, err = stringMap(f[field.key]); err != nil {
			return nil, fmt.Errorf("%s: %w", field.key, err)
		}
	}
	return ws, nil
}
func parseMeta(v *jsonvalue.Value) (rawMeta, error) {
	f, extra, err := fields(v, "dependencies", "optionalDependencies", "peerDependencies", "optionalPeers", "bin", "os", "cpu", "libc")
	if err != nil {
		return rawMeta{}, err
	}
	meta := rawMeta{extra: extra, bin: f["bin"], os: platforms(f["os"]), cpu: platforms(f["cpu"]), libc: platforms(f["libc"])}
	for _, field := range []struct {
		key string
		dst *map[string]string
	}{{"dependencies", &meta.dependencies}, {"optionalDependencies", &meta.optionalDependencies}, {"peerDependencies", &meta.peerDependencies}} {
		if *field.dst, err = stringMap(f[field.key]); err != nil {
			return rawMeta{}, err
		}
	}
	meta.optionalPeers, err = stringList(f["optionalPeers"])
	return meta, err
}
func platforms(v *jsonvalue.Value) []string {
	if v == nil {
		return nil
	}
	if v.Kind == 's' {
		return []string{v.Text()}
	}
	var values []string
	if v.Kind == '[' {
		for _, item := range v.Array {
			if item.Kind == 's' {
				values = append(values, item.Text())
			}
		}
	}
	return values
}
func decodeEntry(key string, values []*jsonvalue.Value) (*entry, error) {
	if len(values) == 0 || values[0].Kind != 's' {
		return nil, fmt.Errorf("package '%s' has no ident string at position 0", key)
	}
	e := &entry{ident: values[0].Text()}
	seenMeta := false
	for _, value := range values[1:] {
		if value.Kind == '{' {
			meta, err := parseMeta(value)
			if err != nil {
				meta = rawMeta{}
			}
			e.meta = meta
			seenMeta = true
		} else if value.Kind == 's' {
			s := value.Text()
			if isIntegrityHash(s) {
				e.integrity = &s
			} else if !seenMeta && s != "" {
				e.registryURL = &s
			}
		}
	}
	return e, nil
}
func isIntegrityHash(s string) bool {
	algo, body, ok := strings.Cut(s, "-")
	if !ok {
		return false
	}
	length := map[string]int{"sha512": 88, "sha384": 64, "sha256": 44, "sha1": 28, "md5": 24}[algo]
	if length == 0 || len(body) != length {
		return false
	}
	for _, c := range []byte(body) {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '+' || c == '/' || c == '=') {
			return false
		}
	}
	return true
}
