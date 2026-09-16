package gitcache

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/filelock"
	"lukechampine.com/blake3"
)

func cacheKey(url, commit string, integrity *string) string {
	h := blake3.New(32, nil)
	h.Write([]byte(url))
	h.Write([]byte{0})
	h.Write([]byte(commit))
	if integrity != nil {
		h.Write([]byte{0})
		h.Write([]byte(*integrity))
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}

func (c *Cache) prepare() error {
	if !filepath.IsAbs(c.Root) {
		return fmt.Errorf("git cache requires an absolute root")
	}
	if err := os.MkdirAll(c.Root, 0700); err != nil {
		return err
	}
	return os.Chmod(c.Root, 0700)
}

// Cache entries are never followed if replaced with a symlink.
func realDirectory(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir()
}

func cloneMatches(actual, requested string) bool {
	return actual == requested || hexCommit(requested, 7, 39) && strings.HasPrefix(actual, requested)
}

func (c *Cache) cachedHead(ctx context.Context, path, commit string) (string, bool) {
	if !realDirectory(path) || !realDirectory(filepath.Join(path, ".git")) {
		return "", false
	}
	out, err := c.output(ctx, path, "rev-parse", "HEAD")
	head := strings.TrimSpace(out)
	return head, err == nil && cloneMatches(head, commit)
}

func (c *Cache) clonePath(url, commit string) string {
	// Only hex IDs reach this function. This keeps every cache leaf local even
	// if Clone is called directly with an untrusted committish.
	return filepath.Join(c.Root, "nub-pm-go-git-"+cacheKey(url, commit, nil)+"-"+commit[:min(12, len(commit))])
}

// Clone publishes a checkout only after verifying HEAD. An abbreviated input
// gets promoted to its full SHA cache entry so the installer can reuse it.
// Network access is optional; an offline miss never starts a fetch.
func (c *Cache) Clone(ctx context.Context, url, commit string, shallow, offline bool) (string, string, error) {
	for _, item := range []struct{ value, kind string }{{url, "git url"}, {commit, "git commit"}} {
		if err := validatePositional(item.value, item.kind); err != nil {
			return "", "", err
		}
	}
	if !hexCommit(commit, 7, 40) {
		return "", "", &Error{"git clone requires a resolved commit SHA: " + commit}
	}
	if err := c.prepare(); err != nil {
		return "", "", err
	}
	target := c.clonePath(url, commit)
	// One repository lease also serializes abbreviated/full-key promotion.
	lease, err := filelock.Acquire(ctx, filepath.Join(c.Root, ".locks", cacheKey(url, "", nil)+".lock"), false)
	if err != nil {
		return "", "", err
	}
	defer lease.Close()
	if head, ok := c.cachedHead(ctx, target, commit); ok {
		return target, head, nil
	}
	if offline {
		return "", "", &Error{"git checkout is not cached (offline): " + commit}
	}
	scratch, err := os.MkdirTemp(c.Root, ".git-clone-*")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(scratch)
	run := func(args ...string) error {
		_, err := c.output(ctx, scratch, args...)
		return err
	}
	if err := run("init", "-q"); err != nil {
		return "", "", err
	}
	if err := run("remote", "add", "--", "origin", url); err != nil {
		return "", "", err
	}
	if !shallow || run("fetch", "--depth", "1", "-q", "--", "origin", commit) != nil {
		if err := run("fetch", "-q", "--", "origin"); err != nil {
			return "", "", err
		}
	}
	if err := run("checkout", "-q", commit); err != nil {
		return "", "", err
	}
	out, err := c.output(ctx, scratch, "rev-parse", "HEAD")
	if err != nil {
		return "", "", err
	}
	head := strings.TrimSpace(out)
	if !cloneMatches(head, commit) {
		return "", "", &Error{fmt.Sprintf("git clone HEAD %s does not match requested commit %s", head, commit)}
	}
	canonical := c.clonePath(url, head)
	if cached, ok := c.cachedHead(ctx, canonical, head); ok {
		return canonical, cached, nil
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	// RemoveAll removes a symlink itself, never its referent. All mutations are
	// beneath the private cache and guarded by the same repository lease.
	if err := os.RemoveAll(canonical); err != nil {
		return "", "", err
	}
	if err := os.Rename(scratch, canonical); err != nil {
		return "", "", &Error{"rename clone into place: " + err.Error()}
	}
	return canonical, head, nil
}
