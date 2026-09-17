package settings

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v4"
)

// Entries retain duplicate keys and physical order. CLI flags and file entries
// scan newest first; environment aliases have their own declared priority.
type Entry [2]string

type Context struct {
	CLI, ConfigOverrides, Env                            []Entry
	ProjectConfig, ProjectNpmrc, UserNpmrc               []Entry
	ProjectToolConfig, UserToolConfig, Managed, Defaults []Entry
	WorkspaceYAML, GlobalYAML                            *yaml.Node
	// Pnpm gates its named environment aliases and YAML sources. Nub owns
	// layout under every identity, so YAML layout fields are always ignored.
	Pnpm bool
	Warn func(code, message string)
}

// Resolve returns bool, uint64, string, []string, or nil for unknown/unset and
// complex settings. Built-in defaults are folded in after the source walk.
func (c Context) Resolve(name string) any { return c.resolve(name, false) }

// Explicit omits the built-in default (used to distinguish requested hoisting).
func (c Context) Explicit(name string) any { return c.resolve(name, true) }

func (c Context) Bool(name string) *bool {
	v, ok := c.Resolve(name).(bool)
	if !ok {
		return nil
	}
	return &v
}
func (c Context) Uint64(name string) *uint64 {
	v, ok := c.Resolve(name).(uint64)
	if !ok {
		return nil
	}
	return &v
}
func (c Context) String(name string) *string {
	v, ok := c.Resolve(name).(string)
	if !ok {
		return nil
	}
	return &v
}
func (c Context) Strings(name string) []string { v, _ := c.Resolve(name).([]string); return v }

func (c Context) resolve(name string, explicit bool) any {
	d, ok := definitions[name]
	if !ok || d.Kind == "unsupported" {
		return nil
	}
	var value any
	for _, source := range d.Precedence {
		switch source {
		case "cli":
			value = fromEntries(d, c.CLI, true)
			if value == nil {
				value = fromEntries(d, c.ConfigOverrides, true)
			}
		case "env":
			value = c.fromEnv(d)
		case "workspaceYaml":
			if c.Pnpm && !d.Layout {
				value = fromYAML(d, c.WorkspaceYAML)
			}
		case "globalConfigYaml":
			if c.Pnpm && !d.Layout {
				value = fromYAML(d, c.GlobalYAML)
			}
		case "projectConfig":
			value = fromEntries(d, c.ProjectConfig, false)
		case "projectAubeConfig":
			value = fromEntries(d, c.ProjectToolConfig, false)
		case "projectNpmrc":
			value = fromEntries(d, c.ProjectNpmrc, false)
		case "userAubeConfig":
			value = fromEntries(d, c.UserToolConfig, false)
		case "userNpmrc":
			value = fromEntries(d, c.UserNpmrc, false)
		case "embedderDefaults":
			value = fromEntries(d, c.Defaults, false)
		default:
			panic("unknown settings source: " + source)
		}
		if value != nil {
			break
		}
	}
	if value == nil && !explicit {
		value = cloneValue(d.Default)
	}
	value = c.harden(d, value)
	if d.Kind == "enum" {
		if s, ok := value.(string); ok {
			s = asciiLower(strings.TrimSpace(s))
			if slices.Contains(d.Variants, s) {
				return s
			}
		}
		if !explicit {
			return cloneValue(d.Default)
		}
		return nil
	}
	return value
}

func fromEntries(d definition, entries []Entry, cli bool) any {
	for i := len(entries) - 1; i >= 0; i-- {
		key, raw := entries[i][0], entries[i][1]
		matches := slices.Contains(d.Npmrc, key)
		if cli {
			matches = slices.Contains(d.CLI, key) || key == d.Name || kebab(key) == kebab(d.Name)
		}
		if matches {
			if value := parse(d, raw); value != nil {
				return value
			}
		}
	}
	return nil
}

func (c Context) fromEnv(d definition) any {
	for i := len(d.Env) - 1; i >= 0; i-- {
		alias := d.Env[i]
		if !envEnabled(alias, c.Pnpm) {
			continue
		}
		for j := len(c.Env) - 1; j >= 0; j-- {
			if c.Env[j][0] == alias {
				return parse(d, c.Env[j][1])
			}
		}
	}
	return nil
}

func envEnabled(alias string, pnpm bool) bool {
	if strings.HasPrefix(alias, "npm_config_") || strings.HasPrefix(alias, "NPM_CONFIG_") {
		return true
	}
	if strings.HasPrefix(alias, "pnpm_config_") || strings.HasPrefix(alias, "PNPM_CONFIG_") {
		return pnpm
	}
	if slices.Contains([]string{"CI", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "PROXY", "NODE_OPTIONS"}, alias) {
		return true
	}
	if head, _, found := strings.Cut(alias, "_"); found && head != "" {
		for _, c := range head {
			if c < 'A' || c > 'Z' {
				return true
			}
		}
		return false // Nub never reads AUBE_* or other branded settings aliases.
	}
	return true
}

func parse(d definition, raw string) any {
	switch d.Kind {
	case "bool":
		switch asciiLower(strings.TrimSpace(raw)) {
		case "true", "1":
			return true
		case "false", "0":
			return false
		}
	case "int":
		return parseUint(raw)
	case "list":
		return parseList(raw)
	case "string", "enum":
		return raw
	}
	return nil
}

func parseUint(raw string) any {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "+")
	if raw == "" {
		return nil
	}
	for _, c := range raw {
		if c < '0' || c > '9' {
			return nil
		}
	}
	v, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return nil
	}
	return v
}

func parseList(raw string) []string {
	raw = strings.TrimSpace(raw)
	bracketed := strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]")
	if bracketed {
		raw = raw[1 : len(raw)-1]
	}
	out := []string{}
	for _, s := range strings.Split(raw, ",") {
		s = strings.TrimSpace(s)
		if bracketed {
			s = strings.Trim(s, "\"'")
		}
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func asciiLower(s string) string {
	return strings.Map(func(c rune) rune {
		if c >= 'A' && c <= 'Z' {
			return c + ('a' - 'A')
		}
		return c
	}, s)
}

func kebab(s string) string {
	var out strings.Builder
	prevLower, dash := false, false
	for _, c := range s {
		switch {
		case c == '_' || c == '-':
			if !dash && out.Len() > 0 {
				out.WriteByte('-')
				dash = true
			}
			prevLower = false
		case c == '.':
			out.WriteByte('.')
			prevLower, dash = false, false
		case c >= 'A' && c <= 'Z':
			if prevLower {
				out.WriteByte('-')
			}
			out.WriteRune(c + ('a' - 'A'))
			prevLower, dash = false, false
		default:
			out.WriteRune(c)
			prevLower = c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
			dash = false
		}
	}
	return out.String()
}

func (c Context) warn(name string) {
	if c.Warn != nil {
		c.Warn("WARN_AUBE_MANAGED_CONFIG_ENFORCED", fmt.Sprintf("managed config enforced `%s` and ignored a weaker local/env/CLI value", name))
	}
}
