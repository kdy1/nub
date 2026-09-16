package gitcache

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/filelock"
	"github.com/nubjs/nub/pm-go/internal/fsutil"
	"github.com/nubjs/nub/pm-go/internal/store"
)

func (c *Cache) codeloadPath(url, commit string, integrity *string) (string, string, bool) {
	if !filepath.IsAbs(c.Root) || validatePositional(url, "git url") != nil || !hexCommit(commit, 40, 40) {
		return "", "", false
	}
	head := strings.ToLower(commit)
	return filepath.Join(c.Root, "nub-pm-go-codeload-"+cacheKey(url, head, integrity)+"-"+head[:12]), head, true
}

// LookupCodeload is read-only. Atomic publication ensures every visible entry
// is a completed tree. The sidecar records the fetched bytes' SHA-512 SRI.
func (c *Cache) LookupCodeload(url, commit string, integrity *string) (path, head string, actual *string, ok bool) {
	path, head, ok = c.codeloadPath(url, commit, integrity)
	if !ok || !realDirectory(path) {
		return "", "", nil, false
	}
	if data, err := os.ReadFile(path + ".integrity"); err == nil {
		if value := strings.TrimSpace(string(data)); value != "" {
			actual = &value
		}
	}
	return path, head, actual, true
}

func (c *Cache) ExtractCodeload(ctx context.Context, data []byte, url, commit string, integrity *string) (string, string, error) {
	target, head, ok := c.codeloadPath(url, commit, integrity)
	if !ok {
		return "", "", &Error{fmt.Sprintf("extract_codeload_tarball: invalid (url, commit) — commit must be a full 40-char SHA, got %s", commit)}
	}
	// Validate a pin even when the matching tree is already warm.
	if integrity != nil {
		if err := store.Verify(data, *integrity); err != nil {
			return "", "", err
		}
	}
	if err := c.prepare(); err != nil {
		return "", "", err
	}
	lease, err := filelock.Acquire(ctx, filepath.Join(c.Root, ".locks", filepath.Base(target)+".lock"), false)
	if err != nil {
		return "", "", err
	}
	defer lease.Close()
	actual := store.Integrity(data)
	if path, sha, cached, ok := c.LookupCodeload(url, commit, integrity); ok {
		if cached == nil {
			if err := fsutil.Write(path+".integrity", []byte(actual), 0600); err != nil {
				return "", "", err
			}
		}
		return path, sha, nil
	}
	scratch, err := os.MkdirTemp(c.Root, ".codeload-*")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(scratch)
	tree := filepath.Join(scratch, "tree")
	if err := store.ExtractCodeload(ctx, bytes.NewReader(data), tree, store.DefaultArchiveLimits()); err != nil {
		return "", "", err
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if err := os.RemoveAll(target); err != nil {
		return "", "", err
	}
	// Publish the integrity first; readers cannot observe a tree without it.
	if err := fsutil.Write(target+".integrity", []byte(actual), 0600); err != nil {
		return "", "", err
	}
	if err := os.Rename(tree, target); err != nil {
		return "", "", &Error{"rename codeload extract into place: " + err.Error()}
	}
	return target, head, nil
}
