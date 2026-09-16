package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/nubjs/nub/pm-go/internal/filelock"
	"github.com/nubjs/nub/pm-go/internal/fsutil"
)

type urlBinding struct {
	URL    string `json:"url"`
	SHA512 string `json:"sha512"`
}

func bindingPath(versionDir, url string) string {
	return filepath.Join(versionDir, "no-integrity", Hash([]byte(url))+".json")
}

func readBinding(versionDir, url string) (string, bool) {
	data, err := os.ReadFile(bindingPath(versionDir, url))
	if err != nil {
		return "", false
	}
	var wire struct{ URL, SHA512 *string }
	if json.Unmarshal(data, &wire) != nil || wire.URL == nil || wire.SHA512 == nil || *wire.URL != url {
		return "", false
	}
	return *wire.SHA512, true
}

// ReadBinding is keyed by the configured registry's archive URL, never a
// name/version alone or an unverified URL copied from another lockfile.
func (s *Store) ReadBinding(url string) (string, bool) {
	if sri, ok := readBinding(s.VersionDir(), url); ok {
		return sri, true
	}
	if s.ReadFallback != "" {
		return readBinding(filepath.Dir(s.ReadFallback), url)
	}
	return "", false
}

func (s *Store) SaveBinding(ctx context.Context, url, sri string) error {
	if err := s.prepare(ctx); err != nil {
		return err
	}
	lease, err := filelock.Acquire(ctx, filepath.Join(s.VersionDir(), ".binding-locks", Hash([]byte(url))+".lock"), false)
	if err != nil {
		return err
	}
	defer lease.Close()
	if previous, ok := readBinding(s.VersionDir(), url); ok && previous == sri {
		return nil
	}
	data, err := json.Marshal(urlBinding{url, sri})
	if err != nil {
		return err
	}
	path := bindingPath(s.VersionDir(), url)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return fsutil.Write(path, data, 0644)
}

// EnsureLease protects cache classification and subsequent materialization
// against store maintenance. The lease remains held until Store.Close.
func (s *Store) EnsureLease(ctx context.Context) error { return s.prepare(ctx) }
