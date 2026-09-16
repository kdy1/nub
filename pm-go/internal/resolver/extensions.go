package resolver

import (
	"strings"

	"github.com/nubjs/nub/pm-go/internal/registry"
	"github.com/nubjs/nub/pm-go/internal/semver"
)

type PackageExtension struct {
	Selector                                             string
	Dependencies, OptionalDependencies, PeerDependencies map[string]string
	PeerOptional                                         map[string]bool
}

func PackageSelectorMatches(selector, name, version string) bool {
	selector = strings.TrimSpace(selector)
	if selector == name {
		return true
	}
	at := strings.LastIndexByte(selector, '@')
	if at <= 0 {
		return false
	}
	if strings.HasPrefix(selector, "@") {
		slash := strings.IndexByte(selector, '/')
		if slash < 0 || at <= slash {
			return false
		}
	}
	selected, requested := selector[:at], selector[at+1:]
	if selected != name || requested == "" {
		return false
	}
	// A wildcard also applies to non-registry packages with non-semver versions.
	if requested == "*" || strings.TrimSpace(requested) == "" {
		return true
	}
	return semver.Satisfies(version, requested)
}
func ApplyPackageExtensions(p *registry.Version, extensions []PackageExtension) {
	for _, extension := range extensions {
		if !PackageSelectorMatches(extension.Selector, p.Name, p.Version) {
			continue
		}
		extendMissing(&p.Dependencies, extension.Dependencies)
		extendMissing(&p.OptionalDependencies, extension.OptionalDependencies)
		extendMissing(&p.PeerDependencies, extension.PeerDependencies)
		extendMissing(&p.PeerOptional, extension.PeerOptional)
	}
}
func ApplyLocalExtensions(name, version string, deps map[string]string, extensions []PackageExtension) map[string]string {
	for _, extension := range extensions {
		if PackageSelectorMatches(extension.Selector, name, version) {
			extendMissing(&deps, extension.Dependencies)
		}
	}
	return deps
}
func extendMissing[V any](target *map[string]V, additions map[string]V) {
	for key, value := range additions {
		if *target == nil {
			*target = map[string]V{}
		}
		if _, present := (*target)[key]; !present {
			(*target)[key] = value
		}
	}
}
func IsDeprecationAllowed(name, version string, allowed map[string]string) bool {
	rangeText, ok := allowed[name]
	return ok && semver.Satisfies(version, rangeText)
}
