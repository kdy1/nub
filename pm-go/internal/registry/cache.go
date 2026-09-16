package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/fsutil"
	"github.com/nubjs/nub/pm-go/internal/spec"
)

type NetworkMode int

const (
	Normal NetworkMode = iota
	PreferOffline
	Offline
)

type OfflineMiss struct{ Name string }

func (e *OfflineMiss) Error() string { return "offline: no cached packument for " + e.Name }

type HTTPError struct {
	Status  int
	Package string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("registry request for %s returned HTTP %d", e.Package, e.Status)
}

type cachedMetadata struct {
	ETag         string          `json:"etag,omitempty"`
	LastModified string          `json:"last_modified,omitempty"`
	FetchedAt    int64           `json:"fetched_at"`
	MaxAge       *uint64         `json:"max_age_secs,omitempty"`
	Body         json.RawMessage `json:"packument"`
}

type metadataFlight struct {
	done chan struct{}
	body []byte
	err  error
}

func ValidName(name string) bool { return spec.ValidName(name) }

func MetadataCachePath(root, name, registry string, full bool) (string, error) {
	if !ValidName(name) {
		return "", fmt.Errorf("invalid package name: %q", name)
	}
	if root == "" {
		return "", nil
	}
	origin := sha256.Sum256([]byte(registry))
	suffix := ".json"
	if full {
		suffix = ".full.json"
	}
	return filepath.Join(root, "origin-"+hex.EncodeToString(origin[:8]), strings.ReplaceAll(name, "/", "__")+suffix), nil
}

func CacheMaxAge(raw string) *uint64 {
	var shared, normal *uint64
	for _, directive := range strings.Split(raw, ",") {
		d := strings.ToLower(strings.TrimSpace(directive))
		if d == "no-store" || d == "no-cache" || d == "private" {
			return new(uint64(0))
		}
		key, value, ok := strings.Cut(d, "=")
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(value, 10, 64)
		var parsed *uint64
		if err == nil {
			parsed = &n
		}
		switch key {
		case "max-age":
			normal = parsed
		case "s-maxage":
			shared = parsed
		}
	}
	if shared != nil {
		return shared
	}
	return normal
}

func (c *Client) fresh(entry *cachedMetadata) bool {
	age := max(c.options.Now().Unix()-entry.FetchedAt, 0)
	budget := uint64(1800)
	if entry.MaxAge != nil {
		budget = *entry.MaxAge
	}
	return uint64(age) < budget
}

func readMetadata(path string) *cachedMetadata {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var entry cachedMetadata
	if json.Unmarshal(data, &entry) != nil {
		return nil
	}
	if _, err := Parse(entry.Body); err != nil {
		return nil
	}
	return &entry
}

func (c *Client) writeMetadata(path string, entry *cachedMetadata) {
	if path == "" {
		return
	}
	data, err := json.Marshal(entry)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(path), 0755)
	}
	if err == nil {
		err = fsutil.Write(path, data, 0600)
	}
	if err != nil {
		c.warn("PACKUMENT_CACHE_WRITE", "failed to write packument cache")
	}
}

func (c *Client) Metadata(ctx context.Context, name, cacheDir string, mode NetworkMode, full bool) (*Packument, error) {
	return c.MetadataAt(ctx, name, c.Config.RegistryFor(name), cacheDir, mode, full)
}

// MetadataAt routes one request without mutating the shared configuration.
// URI-scoped credentials and cache partitions use this effective registry.
func (c *Client) MetadataAt(ctx context.Context, name, registry, cacheDir string, mode NetworkMode, full bool) (*Packument, error) {
	path, err := MetadataCachePath(cacheDir, name, registry, full)
	if err != nil {
		return nil, err
	}
	cached := readMetadata(path)
	if cached != nil && (mode != Normal || c.fresh(cached)) {
		return Parse(cached.Body)
	}
	if mode == Offline {
		return nil, &OfflineMiss{name}
	}
	key := fmt.Sprintf("%t\x00%s\x00%s\x00%s", full, registry, name, cacheDir)
	c.mu.Lock()
	if active := c.flights[key]; active != nil {
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-active.done:
		}
		if active.err != nil {
			return nil, active.err
		}
		return Parse(active.body)
	}
	f := &metadataFlight{done: make(chan struct{})}
	c.flights[key] = f
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.flights, key); close(f.done); c.mu.Unlock() }()
	// A writer may have published between the first read and flight acquisition.
	if recheck := readMetadata(path); recheck != nil {
		cached = recheck
		if mode == PreferOffline || c.fresh(cached) {
			f.body = cached.Body
			return Parse(f.body)
		}
	}
	f.body, f.err = c.fetchMetadata(ctx, name, registry, path, cached, full)
	if f.err != nil {
		return nil, f.err
	}
	return Parse(f.body)
}

func (c *Client) fetchMetadata(ctx context.Context, name, registry, path string, cached *cachedMetadata, full bool) ([]byte, error) {
	header := http.Header{"Accept": []string{"application/vnd.npm.install-v1+json; q=1.0, application/json; q=0.8, */*"}}
	if full {
		header.Set("Accept", "application/json; q=1.0, */*")
	}
	header.Set("Priority", "u=0")
	if cached != nil {
		if cached.ETag != "" {
			header.Set("If-None-Match", cached.ETag)
		}
		if cached.LastModified != "" {
			header.Set("If-Modified-Since", cached.LastModified)
		}
	}
	r, err := c.Do(ctx, Request{URL: strings.TrimRight(registry, "/") + "/" + strings.ReplaceAll(name, "/", "%2F"), Registry: registry, Package: name, Header: header, MaxBytes: c.options.Policy.PackumentMaxBytes, Retry: true, Validate: func(data []byte) error { _, err := Parse(data); return err }})
	if err != nil {
		return nil, err
	}
	if r.Status == http.StatusNotModified && cached != nil {
		cached.FetchedAt = c.options.Now().Unix()
		if maxAge := CacheMaxAge(r.Header.Get("Cache-Control")); maxAge != nil {
			cached.MaxAge = maxAge
		}
		c.writeMetadata(path, cached)
		return cached.Body, nil
	}
	if r.Status < 200 || r.Status >= 300 {
		return nil, &HTTPError{r.Status, name}
	}
	if _, err := Parse(r.Body); err != nil {
		return nil, err
	}
	entry := &cachedMetadata{Body: r.Body, ETag: r.Header.Get("ETag"), LastModified: r.Header.Get("Last-Modified"), FetchedAt: c.options.Now().Unix(), MaxAge: CacheMaxAge(r.Header.Get("Cache-Control"))}
	c.writeMetadata(path, entry)
	return r.Body, nil
}
