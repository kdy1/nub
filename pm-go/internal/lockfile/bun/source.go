package bun

import (
	"path/filepath"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
)

func aliasName(key string) string {
	if slash := strings.LastIndexByte(key, '/'); slash >= 0 {
		start := strings.LastIndexByte(key[:slash], '/') + 1
		if strings.HasPrefix(key[start:slash], "@") {
			return key[start:]
		}
		return key[slash+1:]
	}
	return key
}
func splitIdent(ident string) (name, version string, ok bool) {
	start := 0
	if strings.HasPrefix(ident, "@") {
		slash := strings.IndexByte(ident, '/')
		if slash < 0 {
			return "", "", false
		}
		start = slash + 1
	}
	at := strings.IndexByte(ident[start:], '@')
	if at < 0 {
		return "", "", false
	}
	at += start
	return ident[:at], ident[at+1:], true
}
func classify(alias, rawName, version string, integrity *string, workspaceDirs lockfile.Set) (*lockfile.Source, *string) {
	var aliasOf *string
	if alias != rawName {
		aliasOf = &rawName
	}
	if rel, ok := strings.CutPrefix(version, "workspace:"); ok {
		isPath := workspaceDirs.Has(rel) || strings.HasPrefix(rel, ".") || strings.HasPrefix(rel, "/") || strings.Contains(rel, "/")
		if rel == "" || !isPath {
			rel = "."
		}
		return &lockfile.Source{Kind: lockfile.Link, Path: rel}, aliasOf
	}
	if rest, ok := strings.CutPrefix(version, "github:"); ok {
		url := rest
		var ref *string
		if index := strings.LastIndexByte(rest, '#'); index >= 0 {
			url = rest[:index]
			tail := rest[index+1:]
			ref = &tail
		}
		source := &lockfile.Source{Kind: lockfile.Git, URL: "https://github.com/" + url + ".git", Committish: ref}
		if ref != nil {
			source.Resolved = *ref
		}
		return source, aliasOf
	}
	if strings.HasPrefix(version, "git+") || strings.HasPrefix(version, "git://") || strings.HasPrefix(version, "git@") {
		if source, ok := lockfile.ParseGit(version); ok {
			if source.Committish != nil {
				source.Resolved = *source.Committish
			}
			return &source, aliasOf
		}
	}
	if strings.HasPrefix(version, "https://") || strings.HasPrefix(version, "http://") {
		return &lockfile.Source{Kind: lockfile.RemoteTarball, URL: version, Integrity: integrity}, aliasOf
	}
	if rest, ok := strings.CutPrefix(version, "file:"); ok {
		kind := lockfile.Directory
		if lockfile.LooksLikeTarball(rest) {
			kind = lockfile.Tarball
		}
		return &lockfile.Source{Kind: kind, Path: rest}, aliasOf
	}
	if lockfile.LooksLikeTarball(version) {
		return &lockfile.Source{Kind: lockfile.Tarball, Path: version}, aliasOf
	}
	if rest, ok := strings.CutPrefix(version, "link:"); ok {
		return &lockfile.Source{Kind: lockfile.Link, Path: rest}, aliasOf
	}
	return nil, aliasOf
}

type workspaceScope struct{ name, path string }

func rebaseLocal(key string, source *lockfile.Source, scopes []workspaceScope) {
	if source == nil || source.Kind == lockfile.Git || source.Kind == lockfile.RemoteTarball {
		return
	}
	path := source.Path
	if filepath.Separator == '\\' {
		path = strings.ReplaceAll(path, "\\", "/")
	}
	hasParent := false
	for _, part := range strings.Split(path, "/") {
		hasParent = hasParent || part == ".."
	}
	if !hasParent {
		return
	}
	for _, scope := range scopes {
		if !strings.HasPrefix(key, scope.name+"/") {
			continue
		}
		if !filepath.IsAbs(path) {
			path = scope.path + "/" + path
		}
		volume := filepath.VolumeName(path)
		path = strings.TrimPrefix(path, volume)
		absolute := strings.HasPrefix(path, "/")
		var parts []string
		for _, part := range strings.Split(path, "/") {
			switch part {
			case "", ".":
			case "..":
				if len(parts) > 0 && parts[len(parts)-1] != ".." {
					parts = parts[:len(parts)-1]
				} else {
					parts = append(parts, part)
				}
			default:
				parts = append(parts, part)
			}
		}
		if absolute {
			volume += "/"
		}
		source.Path = filepath.FromSlash(volume + strings.Join(parts, "/"))
		return
	}
}
func binMap(name string, v *jsonvalue.Value) map[string]string {
	out := map[string]string{}
	if v == nil {
		return out
	}
	if v.Kind == 's' {
		out[name] = v.Text()
	}
	if v.Kind == '{' {
		for _, f := range v.Object {
			if f.Value.Kind == 's' {
				out[f.Key] = f.Value.Text()
			}
		}
	}
	return out
}
func resolveNested(key, dep string, contains func(string) bool) (string, bool) {
	base := key
	for {
		candidate := dep
		if base != "" {
			candidate = base + "/" + dep
		}
		if contains(candidate) {
			return candidate, true
		}
		if base == "" {
			return "", false
		}
		if slash := strings.LastIndexByte(base, '/'); slash >= 0 {
			start := strings.LastIndexByte(base[:slash], '/') + 1
			if strings.HasPrefix(base[start:slash], "@") {
				base = base[:max(0, start-1)]
			} else {
				base = base[:slash]
			}
		} else {
			base = ""
		}
	}
}
func resolveWorkspace(path string, name *string, dep string, contains func(string) bool) (string, bool) {
	if name != nil && contains(*name+"/"+dep) {
		return *name + "/" + dep, true
	}
	if path != "" && contains(path+"/"+dep) {
		return path + "/" + dep, true
	}
	return dep, contains(dep)
}
