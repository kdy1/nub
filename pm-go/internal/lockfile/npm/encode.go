package npm

import (
	"strings"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/manifest"
)

type writePackage struct {
	rawPackage
	dev, optional, peer, devOptional bool
}

func boolValue(b bool) *jsonvalue.Value { return &jsonvalue.Value{Kind: 'b', Scalar: b} }
func putString(out *jsonvalue.Value, key string, v *string) {
	if v != nil {
		out.Put(key, jsonvalue.String(*v))
	}
}
func putFlag(out *jsonvalue.Value, key string, v bool) {
	if v {
		out.Put(key, boolValue(true))
	}
}
func putList(out *jsonvalue.Value, key string, items []string) {
	if len(items) == 0 {
		return
	}
	v := &jsonvalue.Value{Kind: '['}
	for _, item := range items {
		v.Array = append(v.Array, jsonvalue.String(item))
	}
	out.Put(key, v)
}
func putMap(out *jsonvalue.Value, key string, values map[string]string) {
	if len(values) == 0 {
		return
	}
	v := jsonvalue.Object()
	for _, name := range sorted(values) {
		v.Put(name, jsonvalue.String(values[name]))
	}
	out.Put(key, v)
}

// npm sorts scalar/array keys before object keys, then applies its preferred
// key list and alphabetical ordering within each bucket.
func (p *writePackage) json() *jsonvalue.Value {
	v := jsonvalue.Object()
	putString(v, "name", p.name)
	putString(v, "version", p.version)
	putString(v, "resolved", p.resolved)
	putString(v, "integrity", p.integrity)
	putList(v, "bundleDependencies", p.bundled)
	putList(v, "cpu", p.cpu)
	putString(v, "deprecated", p.deprecated)
	putFlag(v, "dev", p.dev)
	putFlag(v, "devOptional", p.devOptional)
	putFlag(v, "hasInstallScript", p.hasInstallScript)
	putFlag(v, "hasShrinkwrap", p.hasShrinkwrap)
	putFlag(v, "inBundle", p.inBundle)
	putList(v, "libc", p.libc)
	putString(v, "license", p.license)
	putFlag(v, "link", p.link)
	putFlag(v, "optional", p.optional)
	putList(v, "os", p.os)
	putFlag(v, "peer", p.peer)
	if p.workspaces != nil && p.workspaces.Kind != '{' {
		v.Put("workspaces", p.workspaces.Value())
	}
	putMap(v, "dependencies", p.dependencies)
	putMap(v, "bin", p.bin)
	putMap(v, "devDependencies", p.devDependencies)
	putMap(v, "engines", p.engines)
	if p.funding != nil {
		funding := jsonvalue.Object()
		putString(funding, "url", p.funding)
		v.Put("funding", funding)
	}
	putMap(v, "optionalDependencies", p.optionalDependencies)
	putMap(v, "peerDependencies", p.peerDependencies)
	if len(p.peerMeta) != 0 {
		meta := jsonvalue.Object()
		for _, name := range sorted(p.peerMeta) {
			item := jsonvalue.Object()
			putFlag(item, "optional", p.peerMeta[name].Optional)
			meta.Put(name, item)
		}
		v.Put("peerDependenciesMeta", meta)
	}
	if p.workspaces != nil && p.workspaces.Kind == '{' {
		v.Put("workspaces", p.workspaces.Value())
	}
	return v
}

func rootMetadata(m *manifest.Package) (*string, map[string]string) {
	var license *string
	if v := m.Raw.Get("license"); v != nil {
		if v.Kind == '{' {
			v = v.Get("type")
		}
		if v != nil && v.Kind == 's' {
			s := v.Text()
			license = &s
		}
	}
	strip := func(s string) string {
		for strings.HasPrefix(s, "./") {
			s = s[2:]
		}
		return s
	}
	bin := map[string]string{}
	if v := m.Raw.Get("bin"); v != nil {
		if v.Kind == 's' && m.Name != nil {
			name := *m.Name
			if i := strings.LastIndexByte(name, '/'); i >= 0 {
				name = name[i+1:]
			}
			bin[name] = strip(v.Text())
		} else if v.Kind == '{' {
			for _, item := range v.Object {
				if item.Value.Kind == 's' {
					bin[item.Key] = strip(item.Value.Text())
				}
			}
		}
	}
	return license, bin
}

// npm's writer recognizes a narrower archive spelling than the general source
// parser. Keep this inverse separate so query-bearing or unpinned URLs survive.
func hostedArchive(raw string) (lockfile.HostedGit, string, bool) {
	var h lockfile.HostedGit
	sha := ""
	if rest, ok := strings.CutPrefix(raw, "https://codeload.github.com/"); ok {
		parts := strings.Split(rest, "/")
		if len(parts) != 4 || parts[2] != "tar.gz" {
			return h, "", false
		}
		h, sha = lockfile.HostedGit{Host: "github.com", Owner: parts[0], Repo: parts[1]}, parts[3]
	} else if rest, ok := strings.CutPrefix(raw, "https://gitlab.com/"); ok {
		before, after, ok := strings.Cut(rest, "/-/archive/")
		if !ok {
			return h, "", false
		}
		owner, repo, ok := strings.Cut(before, "/")
		if !ok {
			return h, "", false
		}
		commit, _, ok := strings.Cut(after, "/")
		if !ok {
			return h, "", false
		}
		h, sha = lockfile.HostedGit{Host: "gitlab.com", Owner: owner, Repo: repo}, commit
	} else if rest, ok := strings.CutPrefix(raw, "https://bitbucket.org/"); ok {
		before, after, ok := strings.Cut(rest, "/get/")
		if !ok {
			return h, "", false
		}
		owner, repo, ok := strings.Cut(before, "/")
		if !ok {
			return h, "", false
		}
		commit, ok := strings.CutSuffix(after, ".tar.gz")
		if !ok {
			return h, "", false
		}
		h, sha = lockfile.HostedGit{Host: "bitbucket.org", Owner: owner, Repo: repo}, commit
	} else {
		return h, "", false
	}
	if len(sha) != 40 {
		return h, "", false
	}
	for _, c := range sha {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return h, "", false
		}
	}
	return h, strings.ToLower(sha), true
}
func resolvedField(p *lockfile.Package) *string {
	s := p.Source
	if s != nil && s.Kind == lockfile.RemoteTarball && s.GitHosted {
		if hosted, sha, ok := hostedArchive(s.URL); ok {
			v := "git+" + hosted.SSHURL() + "#" + sha
			return &v
		}
	}
	if p.TarballURL != nil {
		return p.TarballURL
	}
	if s == nil {
		return nil
	}
	if s.Kind == lockfile.RemoteTarball {
		return &s.URL
	}
	if s.Kind != lockfile.Git {
		return nil
	}
	url := s.URL
	if hosted, ok := lockfile.ParseHostedGit(url); ok {
		url = "git+" + hosted.SSHURL()
	} else if !strings.HasPrefix(url, "git://") && !strings.HasPrefix(url, "git+") {
		url = "git+" + url
	}
	url += "#" + s.Resolved
	if s.Subpath != nil {
		url += "&path:/" + *s.Subpath
	}
	return &url
}
