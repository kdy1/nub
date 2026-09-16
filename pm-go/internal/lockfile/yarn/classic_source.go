package yarn

import (
	"path/filepath"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
)

type classicSource struct {
	local         *lockfile.Source
	unsupported   string
	isUnsupported bool
}

func fileSource(body string) *lockfile.Source {
	path := stripHash(body)
	kind := lockfile.Directory
	ext := filepath.Ext(path)
	if strings.EqualFold(ext, ".tgz") || strings.EqualFold(ext, ".gz") {
		kind = lockfile.Tarball
	}
	return &lockfile.Source{Kind: kind, Path: path}
}
func stripHash(s string) string { s, _, _ = strings.Cut(s, "#"); return s }
func classicGitSource(resolved *string) *lockfile.Source {
	if resolved == nil {
		return nil
	}
	if git, ok := lockfile.ParseGit(*resolved); ok && git.Committish != nil {
		git.Resolved = *git.Committish
		return &git
	}
	if _, _, ok := lockfile.ParseHostedTarball(*resolved); ok {
		return &lockfile.Source{Kind: lockfile.RemoteTarball, URL: *resolved, GitHosted: true}
	}
	return nil
}
func classifyClassic(spec, name string, resolved *string) classicSource {
	rangeText, ok := strings.CutPrefix(spec, name+"@")
	if !ok {
		return classicSource{}
	}
	git := func() classicSource {
		if source := classicGitSource(resolved); source != nil {
			return classicSource{local: source}
		}
		return classicSource{unsupported: "git", isUnsupported: true}
	}
	protocol, body, hasProtocol := strings.Cut(rangeText, ":")
	if hasProtocol {
		switch protocol {
		case "link":
			return classicSource{local: &lockfile.Source{Kind: lockfile.Link, Path: stripHash(body)}}
		case "file":
			return classicSource{local: fileSource(body)}
		case "portal":
			return classicSource{local: &lockfile.Source{Kind: lockfile.Portal, Path: stripHash(body)}}
		case "npm":
			return classicSource{}
		}
		if strings.Contains(protocol, "/") {
			return git()
		}
		if _, ok := lockfile.ParseGit(rangeText); ok {
			return git()
		}
		return classicSource{unsupported: protocol, isUnsupported: true}
	}
	if strings.Contains(rangeText, "/") {
		return git()
	}
	return classicSource{}
}
func resolvedTarball(raw string) *string {
	url := stripHash(raw)
	if _, ok := lockfile.ParseGit(url); ok {
		return nil
	}
	host, ok := httpHost(url)
	if !ok || host == "registry.npmjs.org" || host == "registry.yarnpkg.com" {
		return nil
	}
	return &url
}
func httpHost(url string) (string, bool) {
	rest, ok := strings.CutPrefix(url, "https://")
	if !ok {
		rest, ok = strings.CutPrefix(url, "http://")
	}
	if !ok {
		return "", false
	}
	authority, _, _ := strings.Cut(rest, "/")
	if at := strings.LastIndexByte(authority, '@'); at >= 0 {
		authority = authority[at+1:]
	}
	host, _, _ := strings.Cut(authority, ":")
	return strings.ToLower(host), true
}
