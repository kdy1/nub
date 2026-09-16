package yarn

import (
	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"strings"
)

func splitBerryHeader(header string) []string {
	var out []string
	for _, part := range strings.Split(header, ", ") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
func parseBerrySpec(spec string) (name, protocol, body string, ok bool) {
	start := 0
	if strings.HasPrefix(spec, "@") {
		slash := strings.IndexByte(spec, '/')
		if slash < 0 {
			return
		}
		start = slash + 1
	}
	at := strings.IndexByte(spec[start:], '@')
	if at < 0 {
		return
	}
	name = spec[:start+at]
	protocol, body, ok = strings.Cut(spec[start+at+1:], ":")
	return
}
func berryHasProtocol(text string) bool {
	head, _, ok := strings.Cut(text, ":")
	if !ok || head == "" {
		return false
	}
	for _, c := range head {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '+') {
			return false
		}
	}
	return true
}
func berryCandidates(name, rangeText string, resolved *string) []string {
	var out []string
	push := func(r string) {
		out = append(out, name+"@"+r)
		if !berryHasProtocol(r) {
			out = append(out, name+"@npm:"+r)
		}
	}
	if resolved != nil && *resolved != rangeText {
		push(*resolved)
	}
	push(rangeText)
	return out
}

// The count distinguishes a builtin/empty selector from an unsupported list.
func berryPatchPath(body string) (path string, count int) {
	_, selector, ok := strings.Cut(body, "#")
	if !ok {
		return
	}
	selector, _, _ = strings.Cut(selector, "::")
	for _, part := range strings.Split(selector, "&") {
		if i := strings.LastIndexByte(part, '!'); i >= 0 {
			part = part[i+1:]
		}
		if rest, ok := strings.CutPrefix(part, "~/"); ok {
			part = rest
		} else {
			part = strings.TrimPrefix(part, "~")
		}
		if part == "" || strings.HasPrefix(part, "builtin<") && strings.HasSuffix(part, ">") {
			continue
		}
		if count == 0 {
			path = part
		}
		count++
	}
	return
}
func berryGit(url string) *lockfile.Source {
	source := &lockfile.Source{Kind: lockfile.Git, URL: stripHash(url)}
	if _, fragment, ok := strings.Cut(url, "#"); ok {
		if ref, _ := lockfile.ParseGitFragment(fragment); ref != nil {
			source.Resolved = *ref
		}
	}
	return source
}
