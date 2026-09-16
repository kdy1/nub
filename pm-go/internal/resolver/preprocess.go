package resolver

import (
	"fmt"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/semver"
)

type namedRoute struct{ Name, Registry string }
type taskPreprocessor struct {
	Catalogs        Catalogs
	Overrides       OverrideRules
	NamedRegistries map[string]string
	CatalogPicks    Catalogs
}

func (p *taskPreprocessor) recordCatalog(catalog, name, requested string) {
	if p.CatalogPicks == nil {
		p.CatalogPicks = Catalogs{}
	}
	if p.CatalogPicks[catalog] == nil {
		p.CatalogPicks[catalog] = map[string]string{}
	}
	p.CatalogPicks[catalog][name] = requested
}

// apply retains the manifest specifier separately from overrides and registry
// aliases. A false keep result is the pnpm '-' edge-removal override.
func (p *taskPreprocessor) apply(task *resolveTask) (keep bool, route *namedRoute, err error) {
	if catalog, requested, matched, err := p.Catalogs.Resolve(task.Name, task.Range); err != nil {
		return false, nil, err
	} else if matched {
		p.recordCatalog(catalog, task.Name, requested)
		task.Range = requested
	}
	for range 2 {
		changed := false
		if replacement := p.Overrides.Pick(task.Name, task.Range, task.Ancestors); replacement != nil {
			if *replacement == "-" {
				return false, nil, nil
			}
			effective := *replacement
			catalog, requested, matched, err := p.Catalogs.Resolve(task.Name, effective)
			if err != nil {
				return false, nil, err
			}
			if matched {
				effective = requested
			}
			if task.Root {
				task.LockfileOverrideSpecifier = new(effective)
			}
			if task.Range != effective {
				if matched {
					p.recordCatalog(catalog, task.Name, requested)
				}
				if nonRegistrySpecifier(effective) {
					task.RangeFromOverride = true
				}
				task.Range = effective
				if task.RealName != nil && !strings.HasPrefix(effective, "npm:") && !strings.HasPrefix(effective, "jsr:") {
					task.RealName = nil
				}
				changed = true
			}
		}
		if rest, ok := strings.CutPrefix(task.Range, "npm:"); ok {
			at := strings.IndexByte(rest, '@')
			if strings.HasPrefix(rest, "@") {
				at = -1
				if slash := strings.IndexByte(rest, '/'); slash >= 0 {
					if next := strings.IndexByte(rest[slash+1:], '@'); next >= 0 {
						at = slash + 1 + next
					}
				}
			}
			name, requested := rest, "latest"
			if at >= 0 {
				name, requested = rest[:at], rest[at+1:]
			}
			if task.RealName == nil || *task.RealName != name || requested != task.Range {
				task.RealName = new(name)
				task.Range = requested
				changed = true
			}
		}
		if rest, ok := strings.CutPrefix(task.Range, "jsr:"); ok {
			name, requested := task.Name, rest
			if strings.HasPrefix(rest, "@") {
				name, requested = rest, "latest"
				if at := strings.LastIndexByte(rest[1:], '@'); at >= 0 {
					at++
					name, requested = rest[:at], rest[at+1:]
				}
			}
			mapped, valid := jsrNpmName(name)
			if !valid {
				return false, nil, fmt.Errorf("registry error for %s: invalid jsr: spec `%s` — expected `jsr:@scope/name[@range]`", task.Name, task.Range)
			}
			if task.RealName == nil || *task.RealName != mapped || requested != task.Range {
				task.RealName = new(mapped)
				task.Range = requested
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	if registry, name, requested, ok := parseNamedRegistry(task.Range, task.Name, p.NamedRegistries); ok {
		task.Range = requested
		if name != nil {
			task.RealName = name
		}
		route = &namedRoute{Name: task.registryName(), Registry: registry}
	}
	return true, route, nil
}
func nonRegistrySpecifier(s string) bool {
	if _, ok := lockfile.ParseGit(s); ok {
		return true
	}
	for _, prefix := range []string{"file:", "link:", "portal:", "exec:", "http://", "https://"} {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}
func jsrNpmName(name string) (string, bool) {
	rest, ok := strings.CutPrefix(name, "@")
	if !ok {
		return "", false
	}
	scope, pkg, ok := strings.Cut(rest, "/")
	valid := func(part string) bool {
		if part == "" || part == "." || part == ".." {
			return false
		}
		for _, c := range []byte(part) {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
				return false
			}
		}
		return true
	}
	if !ok || !valid(scope) || !valid(pkg) {
		return "", false
	}
	return "@jsr/" + scope + "__" + pkg, true
}
func parseNamedRegistry(requested, alias string, known map[string]string) (registry string, name *string, selector string, ok bool) {
	scheme, body, has := strings.Cut(requested, ":")
	if !has || scheme == "" {
		return
	}
	for _, reserved := range []string{"npm", "jsr", "catalog", "workspace", "file", "link", "portal", "exec", "git", "github", "http", "https", "node"} {
		if scheme == reserved {
			return
		}
	}
	registry, ok = known[scheme]
	if !ok {
		return
	}
	raw := body
	if strings.TrimSpace(raw) == "" {
		raw = "*"
	}
	pkg := alias
	if _, err := semver.ParseEngineRange(raw); err == nil {
		selector = body
	} else if strings.HasPrefix(body, "@") {
		pkg, selector = body, "latest"
		if at := strings.LastIndexByte(body, '@'); at > 0 {
			pkg, selector = body[:at], body[at+1:]
		}
		if !strings.Contains(pkg, "/") || strings.HasSuffix(pkg, "/") {
			return "", nil, "", false
		}
	} else if strings.HasPrefix(alias, "@") {
		selector = body
	} else {
		pkg, selector = body, "latest"
		if at := strings.LastIndexByte(body, '@'); at >= 1 {
			pkg, selector = body[:at], body[at+1:]
		}
		if pkg == "" {
			return "", nil, "", false
		}
	}
	if selector == "" {
		selector = "latest"
	}
	if pkg != alias {
		name = new(pkg)
	}
	return registry, name, selector, true
}
