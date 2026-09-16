package registry

import (
	"fmt"
	"strings"

	whatwg "github.com/nlnwa/whatwg-url/url"
)

// HostKey is shared by resolution and frozen-fetch URL verification. The
// reference URL parser follows WHATWG rules, including IDNA, IPv6 brackets,
// numeric IPv4 and default ports; net/url's unnormalized Host differs here.
func HostKey(raw string) (string, bool) {
	u, err := whatwg.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "", false
	}
	return strings.ToLower(u.Host()), true
}

func SameRegistryHost(a, b string) bool {
	x, okX := HostKey(a)
	y, okY := HostKey(b)
	return okX == okY && x == y
}

func (c *Client) TarballURL(name, version string) string {
	unscoped := name
	if rest, scoped := strings.CutPrefix(name, "@"); scoped {
		if _, tail, ok := strings.Cut(rest, "/"); ok {
			unscoped, _, _ = strings.Cut(tail, "/")
		} else {
			unscoped = rest
		}
	}
	return fmt.Sprintf("%s/%s/-/%s-%s.tgz", strings.TrimRight(c.Config.RegistryFor(name), "/"), name, unscoped, version)
}

// LockfileURLMatches allows the public npm URL to retain its package archive
// path when a configured mirror serves the same archive. Other hosts require
// the exact metadata URL, including the query and fragment.
func LockfileURLMatches(locked, expected string) bool {
	if locked == expected {
		return true
	}
	a, errA := whatwg.Parse(locked)
	b, errB := whatwg.Parse(expected)
	if errA != nil || errB != nil || strings.ToLower(a.Hostname()) != "registry.npmjs.org" {
		return false
	}
	left, right := a.Pathname(), b.Pathname()
	return strings.Contains(left, "/-/") && strings.Contains(right, "/-/") && strings.TrimLeft(left, "/") == strings.TrimLeft(right, "/")
}
