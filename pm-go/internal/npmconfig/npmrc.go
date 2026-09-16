// Package npmconfig resolves registry configuration without reading process
// globals. Source tags survive parsing so project files cannot grant themselves
// permission to execute credential helpers or disable transport checks.
package npmconfig

import "strings"

type Source string

const (
	Builtin         Source = "Builtin"
	Global          Source = "Global"
	User            Source = "User"
	PnpmAuth        Source = "PnpmAuth"
	Project         Source = "Project"
	UserAuthFile    Source = "UserNpmrcAuthFile"
	ProjectAuthFile Source = "ProjectNpmrcAuthFile"
	Env             Source = "Env"
)

func (s Source) Trusted() bool {
	switch s {
	case Builtin, Global, User, PnpmAuth, UserAuthFile, Env:
		return true
	}
	return false
}

type Entry struct {
	Source     Source
	Key, Value string
}

func Parse(data string, source Source, env map[string]string) []Entry {
	var out []Entry
	var pending string
	lines := strings.Split(strings.TrimPrefix(data, "\ufeff"), "\n")
	for i, line := range lines {
		// Rust str::lines strips CR only as part of CRLF.
		if i < len(lines)-1 {
			line = strings.TrimSuffix(line, "\r")
		}
		if strings.HasSuffix(line, "\\") {
			pending += strings.TrimSuffix(line, "\\")
			if i < len(lines)-1 {
				continue
			}
		} else {
			pending += line
		}
		line, pending = strings.TrimSpace(pending), ""
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '\'' || value[0] == '"') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		if source.Trusted() {
			key, value = Expand(key, env), Expand(value, env)
		}
		out = append(out, Entry{source, key, value})
	}
	return out
}

// Expand follows the reference parser: unknown and unterminated references
// become empty strings, and replacements are not expanded recursively.
func Expand(value string, env map[string]string) string {
	var out strings.Builder
	for {
		prefix, rest, found := strings.Cut(value, "${")
		out.WriteString(prefix)
		if !found {
			break
		}
		name, after, _ := strings.Cut(rest, "}")
		out.WriteString(env[name])
		value = after
	}
	return out.String()
}
