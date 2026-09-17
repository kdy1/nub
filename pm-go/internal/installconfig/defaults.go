package installconfig

import (
	"strconv"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/manifest"
	"github.com/nubjs/nub/pm-go/internal/settings"
)

type DefaultsInput struct {
	TrulyFresh, ProjectLocal bool
	Root                     *manifest.Package
	Members                  []*manifest.Package
	// StoreDir is the resolved Go store setting, before the CAS suffix. An
	// empty value leaves platform directory selection to the storage layer.
	StoreDir string
	Env      map[string]string
}

// NubDefaults supplies the lowest-priority settings tier. CLI, environment and
// authored files still override it. Workspace discovery is invocation-owned.
func NubDefaults(in DefaultsInput) []settings.Entry {
	format := "pnpm"
	if in.TrulyFresh {
		format = "nub"
	}
	disk := ""
	if !in.ProjectLocal && !compatDisabled(in.Env["__NUB_VITE_COMPAT_DISABLE"]) && declaresRaw(in.Root, "vite") {
		disk = "vite"
	}
	gvsOff := []string{"next", "react-native"}
	expo, remix, injected := false, false, false
	manifests := append([]*manifest.Package{in.Root}, in.Members...)
	for _, p := range manifests {
		if p == nil {
			continue
		}
		if value, ok := directRange(p, "expo"); ok {
			n, known := majorFloor(value)
			expo = expo || !known || n < 56
		}
		if value, ok := directRange(p, "remix"); ok {
			n, known := majorFloor(value)
			remix = remix || strings.HasPrefix(strings.TrimSpace(value), ">") || !known || n >= 3
		}
		meta := p.Raw.Get("dependenciesMeta")
		if meta != nil && meta.Kind == '{' {
			for _, f := range meta.Object {
				if v := f.Value.Get("injected"); v != nil && v.Kind == 'b' && v.Scalar == true {
					injected = true
				}
			}
		}
	}
	if expo {
		gvsOff = append(gvsOff, "expo")
	}
	if remix {
		gvsOff = append(gvsOff, "remix")
	}
	defaults := []settings.Entry{
		{"defaultLockfileFormat", format}, {"defaultTrust", "true"},
		{"minimumReleaseAge", "1440"}, {"minimumReleaseAgeStrict", "true"},
		{"virtualStoreDir", "node_modules/.store"}, {"stateDir", "node_modules/.store"},
		{"disableGlobalVirtualStoreForPackages", strings.Join(gvsOff, ",")}, {"diskMaterializePackages", disk},
	}
	if in.StoreDir != "" {
		defaults = append(defaults, settings.Entry{"storeDir", in.StoreDir})
	}
	defaults = append(defaults, settings.Entry{"nodeLinker", "isolated"})
	if injected {
		defaults = append(defaults, settings.Entry{"hoist", "true"})
	}
	if in.ProjectLocal {
		defaults = append(defaults, settings.Entry{"enableGlobalVirtualStore", "false"})
	}
	return defaults
}

func directRange(p *manifest.Package, name string) (string, bool) {
	for _, deps := range []map[string]string{p.Dependencies, p.DevDependencies, p.OptionalDependencies} {
		if value, ok := deps[name]; ok {
			return value, true
		}
	}
	return "", false
}

func declaresRaw(p *manifest.Package, name string) bool {
	if p == nil {
		return false
	}
	for _, section := range []string{"dependencies", "devDependencies", "optionalDependencies"} {
		if p.Raw.Get(section).Get(name) != nil {
			return true
		}
	}
	return false
}

func majorFloor(value string) (uint32, bool) {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, " :|<") {
		return 0, false
	}
	value = strings.TrimLeft(value, "^~vV>=")
	end := 0
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	n, err := strconv.ParseUint(value[:end], 10, 32)
	return uint32(n), err == nil
}

func compatDisabled(value string) bool {
	switch asciiLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
