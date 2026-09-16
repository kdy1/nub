// Package npm decodes npm package-lock and shrinkwrap files.
package npm

import (
	"fmt"
	"strconv"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/manifest"
)

type rawLock struct {
	version  uint32
	packages map[string]*rawPackage
}
type rawPackage struct {
	name, version, integrity, resolved, deprecated                        *string
	link, hasInstallScript, hasShrinkwrap, inBundle                       bool
	workspaces                                                            *manifest.Workspaces
	dependencies, devDependencies, optionalDependencies, peerDependencies map[string]string
	peerMeta                                                              map[string]lockfile.PeerMeta
	os, cpu, libc, bundled                                                []string
	engines, bin                                                          map[string]string
	license, funding                                                      *string
}
type legacyDep struct {
	version, resolved, integrity *string
	requires                     map[string]string
	dependencies                 map[string]*legacyDep
	bundled                      bool
}

func parseRaw(v *jsonvalue.Value) (*rawLock, error) {
	if v == nil || v.Kind != '{' {
		return nil, fmt.Errorf("expected a lockfile object")
	}
	r := &rawLock{version: 1, packages: map[string]*rawPackage{}}
	if version := v.Get("lockfileVersion"); !manifest.Absent(version) {
		if version.Kind != 'd' {
			return nil, fmt.Errorf("lockfileVersion: expected an unsigned integer")
		}
		n, err := strconv.ParseUint(fmt.Sprint(version.Scalar), 10, 32)
		if err != nil {
			return nil, fmt.Errorf("lockfileVersion: expected an unsigned integer")
		}
		r.version = uint32(n)
	}
	if packages := v.Get("packages"); packages != nil {
		if packages.Kind != '{' {
			return nil, fmt.Errorf("packages: expected an object")
		}
		for _, item := range packages.Object {
			p, err := parsePackage(item.Value)
			if err != nil {
				return nil, fmt.Errorf("packages[%q]: %w", item.Key, err)
			}
			r.packages[item.Key] = p
		}
	}
	return r, nil
}

func stringsDefault(v *jsonvalue.Value) (map[string]string, error) {
	if v == nil {
		return map[string]string{}, nil
	}
	return manifest.StrictStrings(v)
}
func boolDefault(v *jsonvalue.Value) (bool, error) {
	if v == nil {
		return false, nil
	}
	if v.Kind != 'b' {
		return false, fmt.Errorf("expected a boolean")
	}
	return v.Scalar.(bool), nil
}
func platformList(v *jsonvalue.Value) []string {
	out := []string{}
	if v == nil {
		return out
	}
	if v.Kind == 's' {
		return append(out, v.Text())
	}
	if v.Kind == '[' {
		for _, item := range v.Array {
			if item.Kind == 's' {
				out = append(out, item.Text())
			}
		}
	}
	return out
}

// License and funding arrays must be drained and validated even after a usable
// value was found. Only the top-level Option accepts JSON null.
func firstText(v *jsonvalue.Value, key string) (*string, error) {
	switch v.Kind {
	case 's':
		s := v.Text()
		return &s, nil
	case '{':
		return manifest.OptionalString(v.Get(key))
	case '[':
		var first *string
		for _, item := range v.Array {
			text, err := firstText(item, key)
			if err != nil {
				return nil, err
			}
			if first == nil {
				first = text
			}
		}
		return first, nil
	default:
		return nil, fmt.Errorf("expected a string, object, or array")
	}
}

func parsePackage(v *jsonvalue.Value) (*rawPackage, error) {
	if v == nil || v.Kind != '{' {
		return nil, fmt.Errorf("expected a package object")
	}
	p := &rawPackage{peerMeta: map[string]lockfile.PeerMeta{}}
	for _, field := range []struct {
		name string
		dst  **string
	}{
		{"name", &p.name}, {"version", &p.version}, {"integrity", &p.integrity}, {"resolved", &p.resolved}, {"deprecated", &p.deprecated},
	} {
		value, err := manifest.OptionalString(v.Get(field.name))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", field.name, err)
		}
		*field.dst = value
	}
	for _, field := range []struct {
		name string
		dst  *bool
	}{
		{"link", &p.link}, {"hasInstallScript", &p.hasInstallScript}, {"hasShrinkwrap", &p.hasShrinkwrap}, {"inBundle", &p.inBundle},
	} {
		value, err := boolDefault(v.Get(field.name))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", field.name, err)
		}
		*field.dst = value
	}
	for _, field := range []struct {
		name string
		dst  *map[string]string
	}{
		{"dependencies", &p.dependencies}, {"devDependencies", &p.devDependencies}, {"optionalDependencies", &p.optionalDependencies}, {"peerDependencies", &p.peerDependencies}, {"bin", &p.bin},
	} {
		value, err := stringsDefault(v.Get(field.name))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", field.name, err)
		}
		*field.dst = value
	}
	var err error
	p.workspaces, err = manifest.ParseWorkspaces(v.Get("workspaces"))
	if err != nil {
		return nil, fmt.Errorf("workspaces: %w", err)
	}
	p.engines, err = manifest.Engines(v.Get("engines"))
	if err != nil {
		return nil, err
	}
	p.os, p.cpu, p.libc = platformList(v.Get("os")), platformList(v.Get("cpu")), platformList(v.Get("libc"))
	if meta := v.Get("peerDependenciesMeta"); meta != nil {
		if meta.Kind != '{' {
			return nil, fmt.Errorf("peerDependenciesMeta: expected an object")
		}
		for _, item := range meta.Object {
			if item.Value.Kind != '{' {
				return nil, fmt.Errorf("peerDependenciesMeta[%q]: expected an object", item.Key)
			}
			optional, err := boolDefault(item.Value.Get("optional"))
			if err != nil {
				return nil, fmt.Errorf("peerDependenciesMeta[%q].optional: %w", item.Key, err)
			}
			p.peerMeta[item.Key] = lockfile.PeerMeta{Optional: optional}
		}
	}
	for _, field := range []struct {
		name, key string
		dst       **string
	}{{"license", "type", &p.license}, {"funding", "url", &p.funding}} {
		if value := v.Get(field.name); !manifest.Absent(value) {
			*field.dst, err = firstText(value, field.key)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", field.name, err)
			}
		}
	}
	bundled := v.Get("bundleDependencies")
	if alias := v.Get("bundledDependencies"); alias != nil {
		if bundled != nil {
			return nil, fmt.Errorf("duplicate field bundleDependencies")
		}
		bundled = alias
	}
	if bundled != nil {
		p.bundled, err = manifest.StringArray(bundled)
		if err != nil {
			return nil, fmt.Errorf("bundleDependencies: %w", err)
		}
	}
	return p, nil
}

func parseLegacy(v *jsonvalue.Value) (map[string]*legacyDep, error) {
	out := map[string]*legacyDep{}
	if v == nil {
		return out, nil
	}
	if v.Kind != '{' {
		return nil, fmt.Errorf("dependencies: expected an object")
	}
	for _, item := range v.Object {
		p := item.Value
		if p.Kind != '{' {
			return nil, fmt.Errorf("dependencies[%q]: expected an object", item.Key)
		}
		d := &legacyDep{}
		for _, field := range []struct {
			name string
			dst  **string
		}{{"version", &d.version}, {"resolved", &d.resolved}, {"integrity", &d.integrity}} {
			var err error
			*field.dst, err = manifest.OptionalString(p.Get(field.name))
			if err != nil {
				return nil, fmt.Errorf("dependencies[%q].%s: %w", item.Key, field.name, err)
			}
		}
		var err error
		d.requires, err = stringsDefault(p.Get("requires"))
		if err != nil {
			return nil, err
		}
		d.bundled, err = boolDefault(p.Get("bundled"))
		if err != nil {
			return nil, err
		}
		d.dependencies, err = parseLegacy(p.Get("dependencies"))
		if err != nil {
			return nil, err
		}
		out[item.Key] = d
	}
	return out, nil
}
