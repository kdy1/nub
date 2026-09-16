package lockfile

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type SourceKind uint8

const (
	Directory SourceKind = iota
	Tarball
	Link
	Portal
	Exec
	Git
	RemoteTarball
)

type Source struct {
	Kind                           SourceKind
	Path, URL, Resolved            string
	Committish, Integrity, Subpath *string
	GitHosted                      bool
}

func (s Source) KindName() string {
	switch s.Kind {
	case Directory, Tarball:
		return "file"
	case Link:
		return "link"
	case Portal:
		return "portal"
	case Exec:
		return "exec"
	case Git:
		return "git"
	case RemoteTarball:
		return "url"
	}
	panic("invalid source kind")
}
func (s Source) GloballyShareable() bool { return s.Kind == Git || s.Kind == RemoteTarball }
func (s Source) PathPOSIX() string       { return strings.ReplaceAll(s.Path, "\\", "/") }
func (s Source) Specifier() string {
	switch s.Kind {
	case Git:
		value := s.URL + "#" + s.Resolved
		if s.Subpath != nil {
			value += "&path:/" + *s.Subpath
		}
		return value
	case RemoteTarball:
		return s.URL
	default:
		return s.KindName() + ":" + s.PathPOSIX()
	}
}
func (s Source) DepPath(name string) string {
	input := s.Specifier()
	if s.Kind == RemoteTarball {
		input = s.URL
	} else if s.Kind != Git {
		input = normalizeLocalPath(s.Path)
	}
	hash := sha256.Sum256([]byte(input))
	return name + "@" + s.KindName() + "+" + hex.EncodeToString(hash[:8])
}

func normalizeLocalPath(value string) string {
	if runtime.GOOS == "windows" {
		value = strings.ReplaceAll(value, "\\", "/")
	}
	volume := filepath.VolumeName(value)
	value = strings.TrimPrefix(value, volume)
	absolute := strings.HasPrefix(value, "/")
	var parts []string
	for _, part := range strings.Split(value, "/") {
		switch part {
		case "", ".":
			continue
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
	prefix := volume
	if absolute {
		prefix += "/"
	}
	return strings.ReplaceAll(prefix+strings.Join(parts, "/"), "\\", "/")
}

func ParseSource(raw, projectRoot string) *Source {
	if git, ok := ParseGit(raw); ok {
		return &git
	}
	if strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "http://") {
		return &Source{Kind: RemoteTarball, URL: raw}
	}
	for _, protocol := range []struct {
		name string
		kind SourceKind
	}{{"file:", Directory}, {"link:", Link}, {"portal:", Portal}, {"exec:", Exec}} {
		if rest, ok := strings.CutPrefix(raw, protocol.name); ok {
			kind := protocol.kind
			absolute := rest
			if !filepath.IsAbs(absolute) {
				absolute = filepath.Join(projectRoot, rest)
			}
			if kind == Directory && LooksLikeTarball(rest) {
				if info, err := os.Stat(absolute); err == nil && info.Mode().IsRegular() {
					kind = Tarball
				}
			}
			return &Source{Kind: kind, Path: rest}
		}
	}
	return nil
}
func LooksLikeTarball(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	return strings.HasSuffix(base, ".tgz") || strings.HasSuffix(base, ".tar.gz")
}

func isHex(raw string) bool {
	for _, c := range raw {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}
func GitCommitsMatch(left, right string) bool {
	if strings.EqualFold(left, right) {
		return true
	}
	left, right = strings.TrimSpace(left), strings.TrimSpace(right)
	if min(len(left), len(right)) < 7 || !isHex(left) || !isHex(right) {
		return false
	}
	left, right = strings.ToLower(left), strings.ToLower(right)
	return len(left) == 40 && len(right) < 40 && strings.HasPrefix(left, right) || len(right) == 40 && len(left) < 40 && strings.HasPrefix(right, left)
}

func ParseGit(raw string) (Source, bool) {
	body, fragment, hasFragment := strings.Cut(raw, "#")
	var ref, subpath *string
	if hasFragment {
		ref, subpath = ParseGitFragment(fragment)
	}
	bareTransport := false
	for _, prefix := range []string{"https://", "http://", "ssh://", "file://"} {
		bareTransport = bareTransport || strings.HasPrefix(body, prefix)
	}
	url := ""
	if rest, ok := strings.CutPrefix(body, "git+"); ok {
		url = rest
	} else if strings.HasPrefix(body, "git://") {
		url = body
	} else if scp, ok := parseSCP(body); ok {
		url = scp
	} else if rest, ok := strings.CutPrefix(body, "github:"); ok {
		url = "https://github.com/" + rest + ".git"
	} else if rest, ok := strings.CutPrefix(body, "gitlab:"); ok {
		url = "https://gitlab.com/" + rest + ".git"
	} else if rest, ok := strings.CutPrefix(body, "bitbucket:"); ok {
		url = "https://bitbucket.org/" + rest + ".git"
	} else if bareTransport && (strings.HasSuffix(body, ".git") || ref != nil && len(*ref) == 40 && isHex(*ref)) {
		url = body
	} else if bareGitHub(body) {
		url = "https://github.com/" + body + ".git"
	} else {
		return Source{}, false
	}
	return Source{Kind: Git, URL: url, Committish: ref, Subpath: subpath}, true
}

func bareGitHub(body string) bool {
	owner, repo, ok := strings.Cut(body, "/")
	if !ok || owner == "" || repo == "" || strings.HasPrefix(owner, ".") {
		return false
	}
	for _, part := range []string{owner, repo} {
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-') {
				return false
			}
		}
	}
	return true
}
func knownHost(host string) bool {
	return host == "github.com" || host == "gitlab.com" || host == "bitbucket.org"
}
func parseSCP(body string) (string, bool) {
	if strings.Contains(body, "://") {
		return "", false
	}
	before, path, ok := strings.Cut(body, ":")
	if !ok || before == "" || path == "" || strings.HasPrefix(path, "/") {
		return "", false
	}
	user, host, ok := strings.Cut(before, "@")
	if !ok || user == "" || !knownHost(host) {
		return "", false
	}
	return "ssh://" + user + "@" + host + "/" + path, true
}

func ParseGitFragment(fragment string) (ref, subpath *string) {
	var preferred *string
	for _, part := range strings.Split(fragment, "&") {
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			key, value, ok = strings.Cut(part, ":")
			if !ok || !(key == "commit" || key == "tag" || key == "head" || key == "branch" || key == "path") {
				key = ""
				value = part
			}
		}
		if value == "" {
			continue
		}
		switch key {
		case "commit":
			if preferred == nil {
				preferred = &value
			}
		case "tag", "head", "branch", "":
			if ref == nil {
				ref = &value
			}
		case "path":
			if subpath != nil {
				continue
			}
			value = strings.TrimLeft(value, "/")
			if value == "" {
				continue
			}
			valid := true
			for _, c := range strings.Split(value, "/") {
				if c == "" || c == "." || c == ".." {
					valid = false
				}
			}
			if valid {
				subpath = &value
			}
		}
	}
	if preferred != nil {
		ref = preferred
	}
	return
}

func SharedLocalDepPath(name, tail string) (string, bool) {
	raw, _, _ := strings.Cut(tail, "(")
	if source, ok := ParseGit(raw); ok {
		if source.Committish == nil {
			return "", false
		}
		source.Resolved = *source.Committish
		source.Committish = nil
		return source.DepPath(name), true
	}
	if strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "http://") {
		return (Source{Kind: RemoteTarball, URL: raw}).DepPath(name), true
	}
	return "", false
}
func ResolveEdge(name, tail string, contains func(string) bool) (string, bool) {
	if contains(tail) {
		return tail, true
	}
	if candidate := name + "@" + tail; contains(candidate) {
		return candidate, true
	}
	candidate, ok := SharedLocalDepPath(name, tail)
	return candidate, ok && contains(candidate)
}
