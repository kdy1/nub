package lockfile

import "strings"

type HostedGit struct{ Host, Owner, Repo string }

func (h HostedGit) HTTPSURL() string {
	return "https://" + h.Host + "/" + h.Owner + "/" + h.Repo + ".git"
}
func (h HostedGit) SSHURL() string {
	return "ssh://git@" + h.Host + "/" + h.Owner + "/" + h.Repo + ".git"
}
func (h HostedGit) TarballURL(commit string) (string, bool) {
	if len(commit) != 40 || !isHex(commit) {
		return "", false
	}
	sha := strings.ToLower(commit)
	switch h.Host {
	case "github.com":
		return "https://codeload.github.com/" + h.Owner + "/" + h.Repo + "/tar.gz/" + sha, true
	case "gitlab.com":
		return "https://gitlab.com/" + h.Owner + "/" + h.Repo + "/-/archive/" + sha + "/" + h.Repo + "-" + sha + ".tar.gz", true
	case "bitbucket.org":
		return "https://bitbucket.org/" + h.Owner + "/" + h.Repo + "/get/" + sha + ".tar.gz", true
	}
	return "", false
}
func ParseHostedGit(raw string) (HostedGit, bool) {
	body := strings.TrimPrefix(raw, "git+")
	var rest string
	for _, prefix := range []string{"https://", "http://", "ssh://", "git://"} {
		if strings.HasPrefix(body, prefix) {
			rest = strings.TrimPrefix(body, prefix)
			break
		}
	}
	if rest == "" {
		if scp, ok := parseSCP(body); ok {
			return ParseHostedGit(scp)
		}
		return HostedGit{}, false
	}
	if _, after, ok := strings.Cut(rest, "@"); ok {
		rest = after
	}
	host, path, ok := strings.Cut(rest, "/")
	if !ok || !knownHost(host) {
		return HostedGit{}, false
	}
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return HostedGit{}, false
	}
	repo := strings.TrimSuffix(parts[1], ".git")
	if repo == "" {
		return HostedGit{}, false
	}
	return HostedGit{host, parts[0], repo}, true
}
func cutLast(s, separator string) (string, string, bool) {
	i := strings.LastIndex(s, separator)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(separator):], true
}
func ParseHostedTarball(raw string) (HostedGit, string, bool) {
	rest, ok := strings.CutPrefix(raw, "https://")
	if !ok {
		rest, ok = strings.CutPrefix(raw, "http://")
	}
	if !ok {
		return HostedGit{}, "", false
	}
	rest, _, _ = strings.Cut(rest, "?")
	host, path, ok := strings.Cut(rest, "/")
	if !ok {
		return HostedGit{}, "", false
	}
	var h HostedGit
	var sha string
	switch strings.ToLower(host) {
	case "codeload.github.com":
		parts := strings.SplitN(path, "/", 4)
		if len(parts) != 4 || parts[2] != "tar.gz" {
			return HostedGit{}, "", false
		}
		h = HostedGit{"github.com", parts[0], parts[1]}
		sha = parts[3]
	case "gitlab.com":
		head, _, ok := cutLast(path, "/")
		if !ok {
			return HostedGit{}, "", false
		}
		repoPath, commit, ok := cutLast(head, "/")
		if !ok {
			return HostedGit{}, "", false
		}
		ownerRepo, ok := strings.CutSuffix(repoPath, "/-/archive")
		if !ok {
			return HostedGit{}, "", false
		}
		owner, repo, ok := cutLast(ownerRepo, "/")
		if !ok {
			return HostedGit{}, "", false
		}
		h = HostedGit{"gitlab.com", owner, repo}
		sha = commit
	case "bitbucket.org":
		archive, ok := strings.CutSuffix(path, ".tar.gz")
		if !ok {
			return HostedGit{}, "", false
		}
		head, commit, ok := cutLast(archive, "/get/")
		if !ok {
			return HostedGit{}, "", false
		}
		owner, repo, ok := cutLast(head, "/")
		if !ok {
			return HostedGit{}, "", false
		}
		h = HostedGit{"bitbucket.org", owner, repo}
		sha = commit
	default:
		return HostedGit{}, "", false
	}
	if h.Owner == "" || h.Repo == "" || len(sha) != 40 || !isHex(sha) {
		return HostedGit{}, "", false
	}
	return h, strings.ToLower(sha), true
}
func (s Source) HostedGitSource() (*Source, bool) {
	if s.Kind != RemoteTarball {
		return nil, false
	}
	h, sha, ok := ParseHostedTarball(s.URL)
	if !ok {
		return nil, false
	}
	return &Source{Kind: Git, URL: h.HTTPSURL(), Resolved: sha, Integrity: s.Integrity}, true
}
