package npmconfig

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"
)

type Files struct {
	Dir, Home, OS               string
	Env                         map[string]string
	Pnpm, Pnpm11                bool
	UserEntries, ProjectEntries []Entry
}

// LoadFiles returns the npmrc cascade with provenance intact. Missing and
// unreadable files are ignored, as in the reference loader. Incumbent-specific
// adapters and CLI/environment entries are layered by the caller afterwards.
func LoadFiles(f Files) []Entry {
	var out []Entry
	read := func(path string, source Source) {
		if path == "" {
			return
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(f.Dir, path)
		}
		data, err := os.ReadFile(path)
		if err == nil && utf8.Valid(data) {
			out = append(out, Parse(string(data), source, f.Env)...)
		}
	}
	first := func(names ...string) string {
		for _, name := range names {
			if value := strings.TrimSpace(f.Env[name]); value != "" {
				return value
			}
		}
		return ""
	}
	platform := f.OS
	if platform == "" {
		platform = runtime.GOOS
	}
	prefix := first("NPM_CONFIG_PREFIX", "npm_config_prefix", "PREFIX")
	builtin := first("NPM_CONFIG_BUILTIN_CONFIG", "npm_config_builtin_config")
	global := first("NPM_CONFIG_GLOBALCONFIG", "npm_config_globalconfig")
	if prefix != "" {
		if global == "" {
			global = filepath.Join(prefix, "etc", "npmrc")
		}
		if builtin == "" {
			if platform == "windows" {
				builtin = filepath.Join(prefix, "node_modules", "npm", "npmrc")
			} else {
				builtin = filepath.Join(prefix, "lib", "node_modules", "npm", "npmrc")
			}
		}
	}
	read(builtin, Builtin)
	read(global, Global)
	for _, e := range f.UserEntries {
		e.Source = User
		out = append(out, e)
	}
	for _, e := range f.ProjectEntries {
		e.Source = Project
		out = append(out, e)
	}
	user := UserFile(f)
	read(user, User)
	if f.Pnpm && f.Pnpm11 {
		if dir := PnpmConfigDir(platform, f.Home, f.Env); dir != "" {
			read(filepath.Join(dir, "auth.ini"), PnpmAuth)
		}
	}
	if path, trusted := authFile(f, out); path != "" && trusted {
		read(path, UserAuthFile)
	}
	project := filepath.Join(f.Dir, ".npmrc")
	if !sameFile(project, user) {
		read(project, Project)
	}
	if path, trusted := authFile(f, out); path != "" && !trusted && !sameFile(path, user) {
		read(path, ProjectAuthFile)
	}
	return out
}

func UserFile(f Files) string {
	names := []string{"NPM_CONFIG_USERCONFIG", "npm_config_userconfig"}
	if f.Pnpm {
		names = append([]string{"PNPM_CONFIG_USERCONFIG", "pnpm_config_userconfig"}, names...)
	}
	for _, name := range names {
		if raw, ok := f.Env[name]; ok {
			if path := expandPath(strings.TrimSpace(raw), f.Home); path != "" {
				if !filepath.IsAbs(path) {
					path = filepath.Join(f.Dir, path)
				}
				return path
			}
			break
		}
	}
	if f.Home == "" {
		return ""
	}
	return filepath.Join(f.Home, ".npmrc")
}

func expandPath(raw, home string) string {
	if raw == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(raw, "~/"); ok {
		if home == "" {
			return ""
		}
		return filepath.Join(home, rest)
	}
	return raw
}

func authFile(f Files, entries []Entry) (string, bool) {
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Key != "npmrc-auth-file" && e.Key != "npmrcAuthFile" {
			continue
		}
		path := expandPath(e.Value, f.Home)
		if path == "" && strings.HasPrefix(e.Value, "~") {
			return "", false
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(f.Dir, path)
		}
		return path, e.Source.Trusted()
	}
	return "", false
}

func sameFile(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	x, ea := filepath.EvalSymlinks(a)
	y, eb := filepath.EvalSymlinks(b)
	if ea == nil && eb == nil {
		return x == y
	}
	return a == b
}

func PnpmConfigDir(platform, home string, env map[string]string) string {
	if xdg := env["XDG_CONFIG_HOME"]; xdg != "" {
		return filepath.Join(xdg, "pnpm")
	}
	switch platform {
	case "windows":
		if local := env["LOCALAPPDATA"]; local != "" {
			return filepath.Join(local, "pnpm", "config")
		}
	case "darwin":
		if home != "" {
			return filepath.Join(home, "Library", "Preferences", "pnpm")
		}
	}
	if home != "" {
		return filepath.Join(home, ".config", "pnpm")
	}
	return ""
}
