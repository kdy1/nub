// Package registry contains npm registry metadata and transport operations.
package registry

import (
	"fmt"
	"strconv"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
)

type Packument struct {
	Name       string
	Modified   *string
	Versions   map[string]*Version
	Tags, Time map[string]string
	Raw        *jsonvalue.Value
}

type Version struct {
	Name, Version                                                         string
	Dependencies, DevDependencies, OptionalDependencies, PeerDependencies map[string]string
	PeerOptional                                                          map[string]bool
	Bin, Engines                                                          map[string]string
	OS, CPU, Libc                                                         []string
	Bundled                                                               []string
	BundleAll                                                             bool
	Dist                                                                  *Dist
	HasInstallScript                                                      bool
	Deprecated, License, Funding                                          *string
	Raw                                                                   *jsonvalue.Value
}

type Dist struct {
	Tarball           string
	Integrity, Shasum *string
	UnpackedSize      *uint64
	Attestations      *jsonvalue.Value
}

func Parse(data []byte) (*Packument, error) {
	root, err := jsonvalue.Parse(data)
	if err != nil {
		return nil, err
	}
	name, err := requiredString(root, "name")
	if err != nil {
		return nil, err
	}
	p := &Packument{Name: name, Modified: optionalString(root.Get("modified")), Versions: map[string]*Version{}, Raw: root}
	p.Tags, err = stringMap(root.Get("dist-tags"))
	if err != nil {
		return nil, fmt.Errorf("dist-tags: %w", err)
	}
	p.Time, err = stringMap(root.Get("time"))
	if err != nil {
		return nil, fmt.Errorf("time: %w", err)
	}
	versions := root.Get("versions")
	if versions == nil {
		return p, nil
	}
	if versions.Kind != '{' {
		return nil, fmt.Errorf("versions must be an object")
	}
	for _, f := range versions.Object {
		v, err := ParseVersion(f.Value)
		if err != nil {
			return nil, fmt.Errorf("%s@%s: %w", name, f.Key, err)
		}
		p.Versions[f.Key] = v
	}
	return p, nil
}

func ParseVersion(root *jsonvalue.Value) (*Version, error) {
	name, err := requiredString(root, "name")
	if err != nil {
		return nil, err
	}
	version, err := requiredString(root, "version")
	if err != nil {
		return nil, err
	}
	v := &Version{Name: name, Version: version, Raw: root, PeerOptional: map[string]bool{}, Bin: map[string]string{}, Engines: map[string]string{}}
	for _, field := range []struct {
		key    string
		target *map[string]string
	}{
		{"dependencies", &v.Dependencies}, {"devDependencies", &v.DevDependencies}, {"optionalDependencies", &v.OptionalDependencies}, {"peerDependencies", &v.PeerDependencies},
	} {
		*field.target, err = stringMap(root.Get(field.key))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", field.key, err)
		}
	}
	if peers := root.Get("peerDependenciesMeta"); peers != nil {
		if peers.Kind != '{' {
			return nil, fmt.Errorf("peerDependenciesMeta must be an object")
		}
		for _, p := range peers.Object {
			v.PeerOptional[p.Key] = isTrue(p.Value.Get("optional"))
		}
	}
	for _, field := range []struct {
		key    string
		target *[]string
	}{{"os", &v.OS}, {"cpu", &v.CPU}, {"libc", &v.Libc}} {
		x := root.Get(field.key)
		if x != nil && x.Kind == 's' {
			*field.target = []string{x.Text()}
		}
		if x != nil && x.Kind == '[' {
			for _, item := range x.Array {
				if item.Kind == 's' {
					*field.target = append(*field.target, item.Text())
				}
			}
		}
	}
	if engines := root.Get("engines"); engines != nil && engines.Kind == '{' {
		v.Engines, err = stringMap(engines)
		if err != nil {
			return nil, err
		}
	}
	if bin := root.Get("bin"); bin != nil {
		if bin.Kind == 's' && bin.Text() != "" {
			v.Bin[""] = bin.Text()
		} else if bin.Kind == '{' {
			v.Bin, err = stringMap(bin)
			if err != nil {
				return nil, err
			}
		}
	}
	v.Deprecated = nonemptyString(root.Get("deprecated"))
	v.License = nonemptyString(root.Get("license"))
	if v.License == nil {
		v.License = nonemptyString(root.Get("license").Get("type"))
	}
	v.Funding = funding(root.Get("funding"))
	v.HasInstallScript = isTrue(root.Get("hasInstallScript"))
	bundle := root.Get("bundledDependencies")
	if bundle == nil || bundle.Kind == 'n' {
		bundle = root.Get("bundleDependencies")
	}
	if bundle != nil && bundle.Kind != 'n' {
		switch bundle.Kind {
		case 'b':
			v.BundleAll = isTrue(bundle)
		case '[':
			v.Bundled, err = stringList(bundle)
			if err != nil {
				return nil, fmt.Errorf("bundledDependencies: %w", err)
			}
		default:
			return nil, fmt.Errorf("bundledDependencies must be a boolean or array")
		}
	}
	if dist := root.Get("dist"); dist != nil && dist.Kind != 'n' {
		tarball, err := requiredString(dist, "tarball")
		if err != nil {
			return nil, fmt.Errorf("dist: %w", err)
		}
		v.Dist = &Dist{Tarball: tarball, Attestations: dist.Get("attestations")}
		if a := v.Dist.Attestations; a != nil && a.Kind != 'n' && a.Kind != '{' {
			return nil, fmt.Errorf("dist.attestations must be an object")
		}
		for _, field := range []struct {
			key    string
			target **string
		}{{"integrity", &v.Dist.Integrity}, {"shasum", &v.Dist.Shasum}} {
			x := dist.Get(field.key)
			if x != nil && x.Kind != 'n' && x.Kind != 's' {
				return nil, fmt.Errorf("dist.%s must be a string", field.key)
			}
			*field.target = optionalString(x)
		}
		if size := dist.Get("unpackedSize"); size != nil && size.Kind == 'd' {
			if n, err := strconv.ParseUint(fmt.Sprint(size.Scalar), 10, 64); err == nil {
				v.Dist.UnpackedSize = &n
			}
		}
	}
	return v, nil
}

func requiredString(v *jsonvalue.Value, key string) (string, error) {
	x := v.Get(key)
	if x == nil || x.Kind != 's' {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return x.Text(), nil
}
func optionalString(v *jsonvalue.Value) *string {
	if v == nil || v.Kind != 's' {
		return nil
	}
	s := v.Text()
	return &s
}
func nonemptyString(v *jsonvalue.Value) *string {
	s := optionalString(v)
	if s != nil && *s == "" {
		return nil
	}
	return s
}
func isTrue(v *jsonvalue.Value) bool { return v != nil && v.Kind == 'b' && v.Scalar == true }

func stringMap(v *jsonvalue.Value) (map[string]string, error) {
	out := map[string]string{}
	if v == nil || v.Kind == 'n' {
		return out, nil
	}
	if v.Kind != '{' {
		return nil, fmt.Errorf("expected an object mapping strings to strings")
	}
	for _, f := range v.Object {
		if f.Value.Kind == 's' {
			out[f.Key] = f.Value.Text()
		}
	}
	return out, nil
}

func stringList(v *jsonvalue.Value) ([]string, error) {
	if v == nil || v.Kind == 'n' {
		return nil, nil
	}
	if v.Kind == 's' {
		return []string{v.Text()}, nil
	}
	if v.Kind != '[' {
		return nil, fmt.Errorf("expected a string or array of strings")
	}
	var out []string
	for _, x := range v.Array {
		if x.Kind != 's' {
			return nil, fmt.Errorf("expected a string in array")
		}
		out = append(out, x.Text())
	}
	return out, nil
}

func funding(v *jsonvalue.Value) *string {
	if v == nil {
		return nil
	}
	if v.Kind == '[' {
		for _, x := range v.Array {
			if x.Kind != '[' {
				if value := funding(x); value != nil {
					return value
				}
			}
		}
		return nil
	}
	if value := nonemptyString(v); value != nil {
		return value
	}
	return nonemptyString(v.Get("url"))
}
