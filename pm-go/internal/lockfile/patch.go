package lockfile

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/nubjs/nub/pm-go/internal/semver"
)

type PatchForm uint8

const (
	PatchExact PatchForm = iota
	PatchRange
	PatchAll
)

type PatchKey struct {
	Name, Selector string
	Form           PatchForm
}
type InvalidPatchRange struct{ Range string }

func (e *InvalidPatchRange) Error() string {
	return e.Range + " is not a valid semantic version range."
}

type PatchKeyConflict struct {
	PackageID string
	Ranges    []string
}

func (e *PatchKeyConflict) Error() string {
	return fmt.Sprintf("Unable to choose between %d version ranges to patch %s: %s", len(e.Ranges), e.PackageID, strings.Join(e.Ranges, ", "))
}
func (e *PatchKeyConflict) Hint() string {
	return "Explicitly set the exact version (" + e.PackageID + ") to resolve conflict"
}
func splitPatchKey(key string) (name, selector string, ok bool) {
	// The Rust reference splits its UTF-8 string at byte one; a multibyte
	// initial character makes that split unavailable.
	if len(key) < 2 || !utf8.RuneStart(key[1]) {
		return "", "", false
	}
	i := strings.IndexByte(key[1:], '@')
	if i < 0 || i+2 == len(key) {
		return "", "", false
	}
	i++
	return key[:i], key[i+1:], true
}

// ClassifyPatchKey permits source identity selectors from merged/Bun data.
// pnpm declarations must pass pnpmOnly=true before merging configuration.
func ClassifyPatchKey(key string, pnpmOnly bool) (PatchKey, error) {
	name, selector, ok := splitPatchKey(key)
	if !ok {
		return PatchKey{Name: key, Form: PatchAll}, nil
	}
	if _, err := semver.ParseEngineVersion(selector); err == nil {
		return PatchKey{name, selector, PatchExact}, nil
	}
	if _, err := semver.ParseEngineRange(selector); err == nil {
		form := PatchRange
		if strings.TrimSpace(selector) == "*" {
			form = PatchAll
		}
		return PatchKey{name, selector, form}, nil
	}
	if !pnpmOnly && sourceProtocol(selector) {
		return PatchKey{name, selector, PatchExact}, nil
	}
	return PatchKey{}, &InvalidPatchRange{selector}
}
func sourceProtocol(s string) bool {
	_, ok := VersionProtocol(s)
	return ok
}

// VersionProtocol recognizes only protocol tokens that cannot be registry versions.
func VersionProtocol(s string) (string, bool) {
	token, _, ok := strings.Cut(s, ":")
	if !ok || token == "" {
		return "", false
	}
	alpha := func(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
	if !alpha(token[0]) {
		return "", false
	}
	for i := 1; i < len(token); i++ {
		c := token[i]
		if !alpha(c) && !(c >= '0' && c <= '9') && c != '+' && c != '.' && c != '-' {
			return "", false
		}
	}
	return token, true
}

type patchRange struct {
	display, source string
	parsed          *semver.EngineRange
}
type patchGroup struct {
	exact  map[string]string
	ranges []patchRange
	all    *string
}
type PatchGroups struct{ groups map[string]*patchGroup }

// NewPatchGroups preserves supplied order. Callers merging selector maps must
// deduplicate their union; duplicate ranges would be a real ambiguous group.
func NewPatchGroups(keys []string) (*PatchGroups, error) {
	out := &PatchGroups{map[string]*patchGroup{}}
	for _, key := range keys {
		form, err := ClassifyPatchKey(key, false)
		if err != nil {
			return nil, err
		}
		g := out.groups[form.Name]
		if g == nil {
			g = &patchGroup{exact: map[string]string{}}
			out.groups[form.Name] = g
		}
		switch form.Form {
		case PatchExact:
			g.exact[form.Selector] = key
		case PatchAll:
			g.all = &key
		case PatchRange:
			parsed, err := semver.ParseEngineRange(form.Selector)
			if err != nil {
				return nil, err
			}
			g.ranges = append(g.ranges, patchRange{form.Selector, key, parsed})
		}
	}
	return out, nil
}
func (g *PatchGroups) Resolve(name, version string) (string, bool, error) {
	group := g.groups[name]
	if group == nil {
		return "", false, nil
	}
	if key, ok := group.exact[version]; ok {
		return key, true, nil
	}
	if v, err := semver.ParseEngineVersion(version); err == nil {
		var matches []patchRange
		for _, r := range group.ranges {
			if r.parsed.Contains(v) {
				matches = append(matches, r)
			}
		}
		if len(matches) > 1 {
			ranges := make([]string, len(matches))
			for i, m := range matches {
				ranges[i] = m.display
			}
			return "", false, &PatchKeyConflict{name + "@" + version, ranges}
		}
		if len(matches) == 1 {
			return matches[0].source, true, nil
		}
	}
	if group.all != nil {
		return *group.all, true, nil
	}
	return "", false, nil
}
func (g *PatchGroups) ResolvePackage(p *Package) (string, bool, error) {
	return g.Resolve(p.RegistryName(), p.Version)
}

type UnusedPatchKey struct {
	SourceKey            string
	RegistryNameSpelling *string
}

func UnusedPatchKeys(keys []string, packages map[string]*Package) ([]UnusedPatchKey, error) {
	groups, err := NewPatchGroups(keys)
	if err != nil {
		return nil, err
	}
	matched := Set{}
	for _, p := range packages {
		if key, ok, err := groups.ResolvePackage(p); err == nil && ok {
			matched.Add(key)
		}
	}
	out := []UnusedPatchKey{}
	for _, key := range keys {
		if matched.Has(key) {
			continue
		}
		unused := UnusedPatchKey{SourceKey: key}
		for _, k := range slices.Sorted(maps.Keys(packages)) {
			p := packages[k]
			if p.AliasOf == nil {
				continue
			}
			hit, ok, err := groups.Resolve(p.Name, p.Version)
			if err == nil && ok && hit == key {
				spelling := *p.AliasOf + key[len(p.Name):]
				unused.RegistryNameSpelling = &spelling
				break
			}
		}
		out = append(out, unused)
	}
	return out, nil
}
func ResolvePatchValues[V any](source map[string]V, packages map[string]*Package) (map[string]V, error) {
	out := map[string]V{}
	groups, err := NewPatchGroups(slices.Sorted(maps.Keys(source)))
	if err != nil {
		return nil, err
	}
	for _, key := range slices.Sorted(maps.Keys(packages)) {
		p := packages[key]
		spec := p.SpecKey()
		if _, ok := out[spec]; ok {
			continue
		}
		key, ok, err := groups.ResolvePackage(p)
		if err != nil {
			return nil, err
		}
		if ok {
			out[spec] = source[key]
		}
	}
	return out, nil
}
