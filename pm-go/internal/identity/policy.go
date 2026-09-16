package identity

import (
	"strconv"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
)

type Version struct{ Major, Minor int }

func ParseVersion(raw string) Version {
	version := Version{-1, -1}
	parts := strings.Split(strings.TrimLeft(raw, "^~>=<v "), ".")
	for i := 0; i < len(parts) && i < 2; i++ {
		p := parts[i]
		j := 0
		for j < len(p) && p[j] >= '0' && p[j] <= '9' {
			j++
		}
		if n, err := strconv.Atoi(p[:j]); err == nil {
			if i == 0 {
				version.Major = n
			} else {
				version.Minor = n
			}
		}
	}
	return version
}

type Ignored struct{ Field, Fix string }

func Overrides(role string, version Version, root *jsonvalue.Value) (map[string]string, []Ignored) {
	type source struct {
		key   string
		value *jsonvalue.Value
		keep  bool
		fix   string
	}
	honorOverrides := role == "nub" || role == "bun" || role == "npm" && (version.Major < 0 || version.Major > 8 || version.Major == 8 && (version.Minor < 0 || version.Minor >= 3))
	honorResolutions := role == "nub" || role == "bun" || role == "yarn" || role == "pnpm" && (version.Major < 0 || version.Major >= 5)
	sources := []source{
		{"resolutions", root.Get("resolutions"), honorResolutions, "move these pins to `overrides`"},
		{"pnpm.overrides", root.Get("pnpm").Get("overrides"), role == "pnpm", ""},
		{"overrides", root.Get("overrides"), honorOverrides, "move these pins to `resolutions`"},
	}
	out := map[string]string{}
	for _, s := range sources {
		if s.keep && s.value != nil && s.value.Kind == '{' {
			for _, f := range s.value.Object {
				if f.Key != "" && f.Value.Kind == 's' {
					out[f.Key] = f.Value.Text()
				}
			}
		}
	}
	var ignored []Ignored
	// Rust emits the neutral-field warnings in this order.
	for _, i := range []int{2, 0} {
		s := sources[i]
		if s.keep || s.value == nil || s.value.Kind != '{' {
			continue
		}
		for _, f := range s.value.Object {
			if f.Key == "" || f.Value.Kind != 's' {
				continue
			}
			v, ok := out[f.Key]
			if !ok || v != f.Value.Text() {
				ignored = append(ignored, Ignored{s.key, s.fix})
				break
			}
		}
	}
	return out, ignored
}

func PackageExtensions(role string, root *jsonvalue.Value) *jsonvalue.Value {
	var value *jsonvalue.Value
	switch role {
	case "nub":
		value = root.Get("packageExtensions")
	case "pnpm":
		value = root.Get("pnpm").Get("packageExtensions")
	}
	if value == nil || value.Kind != '{' {
		return jsonvalue.Object()
	}
	return value
}

// Unknown pnpm versions keep the dominant v10 model; only a proven v11+
// identity can admit pnpm_config_* or move scalar config out of .npmrc.
func PnpmYAMLSettings(role string, version Version) bool {
	return role == "pnpm" && version.Major >= 11
}
