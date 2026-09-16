// Package gitcache resolves Git refs and keeps commit-addressed source trees.
// It runs the invocation's Git executable, never the Rust package manager.
package gitcache

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/processenv"
	"github.com/nubjs/nub/pm-go/internal/registry"
)

type Error struct{ Message string }

func (e *Error) Error() string { return "git error: " + e.Message }
func (e *Error) Code() string  { return "ERR_AUBE_GIT_ERROR" }

type Cache struct {
	// Root is the absolute Go cache's git directory, not the Rust cache.
	Root string
	Env  processenv.Environment
}

func validatePositional(value, kind string) error {
	if strings.HasPrefix(value, "-") {
		return &Error{fmt.Sprintf("refusing to pass %s starting with `-` to git: %q", kind, registry.RedactURL(value))}
	}
	if strings.ContainsRune(value, 0) {
		return &Error{"refusing to pass " + kind + " containing NUL byte to git"}
	}
	return nil
}

func hexCommit(value string, min, max int) bool {
	if len(value) < min || len(value) > max {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func redactArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = `"` + registry.RedactURL(arg) + `"`
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func (c *Cache) output(ctx context.Context, dir string, args ...string) (string, error) {
	env := c.Env.With("GIT_TERMINAL_PROMPT", "0")
	caption := redactArgs(args)
	if len(args) == 3 && args[0] == "ls-remote" {
		caption = "ls-remote " + registry.RedactURL(args[2])
	}
	// Resolve a relative PATH against the original invocation, not the checkout.
	cmd, err := env.Command(ctx, "git", args...)
	if err != nil {
		return "", &Error{fmt.Sprintf("spawn git %s: %s", caption, err)}
	}
	cmd.Dir = dir
	cmd.Stdin = nil
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return "", &Error{fmt.Sprintf("git %s failed: %s", caption, registry.RedactURL(detail))}
	}
	return strings.ToValidUTF8(out.String(), "\uFFFD"), nil
}

// ResolveRef prefers tags to same-named branches. Annotated tags use the
// advertised peeled object; dangling HEAD falls back to main, master, then the
// first advertised ref. An abbreviated object ID is expanded during checkout.
func (c *Cache) ResolveRef(ctx context.Context, url string, committish *string) (string, error) {
	if err := validatePositional(url, "git url"); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if committish != nil && hexCommit(*committish, 40, 40) {
		return strings.ToLower(*committish), nil
	}
	out, err := c.output(ctx, c.Env.Dir, "ls-remote", "--", url)
	if err != nil {
		return "", err
	}
	return resolveAdvertisedRefs(url, committish, out)
}

func resolveAdvertisedRefs(url string, committish *string, out string) (string, error) {
	var head, main, master, tag, branch, first string
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		sha, name := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if sha == "" || name == "" {
			continue
		}
		if first == "" {
			first = sha
		}
		switch name {
		case "HEAD":
			head = sha
		case "refs/heads/main":
			main = sha
		case "refs/heads/master":
			master = sha
		}
		if committish != nil {
			want := *committish
			if name == "refs/tags/"+want || name == "refs/tags/"+want+"^{}" {
				tag = sha
			} else if name == "refs/heads/"+want {
				branch = sha
			}
		}
	}
	if committish != nil {
		if tag != "" {
			return tag, nil
		}
		if branch != "" {
			return branch, nil
		}
		if hexCommit(*committish, 7, 39) {
			return strings.ToLower(*committish), nil
		}
		return "", &Error{fmt.Sprintf("git ls-remote %s: no ref matched %s", registry.RedactURL(url), *committish)}
	}
	for _, sha := range []string{head, main, master, first} {
		if sha != "" {
			return sha, nil
		}
	}
	return "", &Error{"git ls-remote " + registry.RedactURL(url) + ": no refs advertised"}
}

func Host(url string) string {
	rest := strings.TrimPrefix(url, "git+")
	_, after, scheme := strings.Cut(rest, "://")
	if !scheme {
		userHost, _, colon := strings.Cut(rest, ":")
		if !colon {
			return ""
		}
		host := userHost[strings.LastIndexByte(userHost, '@')+1:]
		if strings.Contains(host, "/") {
			return ""
		}
		return host
	}
	authority, _, _ := strings.Cut(after, "/")
	host := authority[strings.LastIndexByte(authority, '@')+1:]
	if inner, ok := strings.CutPrefix(host, "["); ok {
		host, _, _ = strings.Cut(inner, "]")
	} else if colon := strings.LastIndexByte(host, ':'); colon >= 0 {
		host = host[:colon]
	}
	return host
}

func HostInList(url string, hosts []string) bool {
	host := Host(url)
	if host == "" {
		return false
	}
	for _, candidate := range hosts {
		if host == candidate {
			return true
		}
	}
	return false
}
