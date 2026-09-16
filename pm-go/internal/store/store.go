package store

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/nubjs/nub/pm-go/internal/filelock"
	"lukechampine.com/blake3"
)

// Store owns a shared maintenance lease after its first write. Close it only
// after its callers have finished; pruning uses a separate Store instance.
// Paths must be resolved from the invocation's directory before construction.
type Store struct {
	Root, CacheDir, ReadFallback string
	mu                           sync.Mutex
	shared                       *filelock.Lease
	closed                       bool
}

func New(root, cache string) *Store {
	return &Store{Root: filepath.Clean(root), CacheDir: filepath.Clean(cache)}
}
func (s *Store) VersionDir() string      { return filepath.Dir(s.Root) }
func (s *Store) IndexDir() string        { return filepath.Join(s.VersionDir(), "index") }
func (s *Store) TreesDir() string        { return filepath.Join(s.VersionDir(), "trees") }
func (s *Store) MaintenancePath() string { return filepath.Join(s.VersionDir(), ".maintenance.lock") }

func (s *Store) prepare(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.closed {
		return os.ErrClosed
	}
	if s.shared != nil {
		return nil
	}
	lease, err := filelock.Acquire(ctx, s.MaintenancePath(), true)
	if err != nil {
		return err
	}
	s.shared = lease
	return nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.shared != nil {
		err := s.shared.Close()
		s.shared = nil
		return err
	}
	return nil
}

func (s *Store) LockMaintenance(ctx context.Context) (*filelock.Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, os.ErrClosed
	}
	if s.shared != nil {
		return nil, fmt.Errorf("this Store already holds a writer lease")
	}
	return filelock.Acquire(ctx, s.MaintenancePath(), false)
}

func Hash(content []byte) string {
	sum := blake3.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func validHash(hash string) bool {
	if len(hash) != 64 {
		return false
	}
	for _, c := range hash {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func (s *Store) writePath(hash string) (string, error) {
	if !validHash(hash) {
		return "", fmt.Errorf("invalid store content hash: %q", hash)
	}
	return filepath.Join(s.Root, hash[:2], hash[2:]), nil
}

func (s *Store) FilePath(hash string) (string, error) {
	primary, err := s.writePath(hash)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(primary); err == nil {
		return primary, nil
	}
	if s.ReadFallback != "" {
		candidate := filepath.Join(s.ReadFallback, hash[:2], hash[2:])
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return primary, nil
}

func matchesSize(path string, size uint64) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && uint64(info.Size()) == size
}

func (s *Store) ImportBytes(ctx context.Context, content []byte, executable bool) (StoredFile, error) {
	return s.Import(ctx, bytes.NewReader(content), executable)
}

// Import hashes into a staging file before taking the shard's writer lease.
// Publication replaces only a missing or truncated entry, never a live inode
// shared by installed packages. An interrupted stream cannot publish a hash.
func (s *Store) Import(ctx context.Context, content io.Reader, executable bool) (StoredFile, error) {
	if err := s.prepare(ctx); err != nil {
		return StoredFile{}, err
	}
	if err := os.MkdirAll(s.Root, 0755); err != nil {
		return StoredFile{}, err
	}
	temp, err := os.CreateTemp(s.Root, ".cas-*")
	if err != nil {
		return StoredFile{}, err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	hasher := blake3.New(32, nil)
	size, err := io.Copy(io.MultiWriter(temp, hasher), contextReader{ctx, content})
	if err != nil {
		return StoredFile{}, err
	}
	if err := temp.Chmod(0644); err != nil {
		return StoredFile{}, err
	}
	if err := temp.Close(); err != nil {
		return StoredFile{}, err
	}
	hash := hex.EncodeToString(hasher.Sum(nil))
	n := uint64(size)
	if s.ReadFallback != "" {
		candidate := filepath.Join(s.ReadFallback, hash[:2], hash[2:])
		if matchesSize(candidate, n) {
			return StoredFile{hash, candidate, executable, &n}, nil
		}
	}
	path, _ := s.writePath(hash)
	lease, err := filelock.Acquire(ctx, filepath.Join(s.VersionDir(), ".write-locks", hash[:2]+".lock"), false)
	if err != nil {
		return StoredFile{}, err
	}
	defer lease.Close()
	if !matchesSize(path, n) {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return StoredFile{}, err
		}
		if err := os.Rename(temp.Name(), path); err != nil {
			return StoredFile{}, err
		}
	}
	if executable {
		marker, err := os.OpenFile(path+"-exec", os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0644)
		if err == nil {
			err = marker.Close()
		}
		if err != nil && !errors.Is(err, os.ErrExist) {
			return StoredFile{}, err
		}
	}
	return StoredFile{hash, path, executable, &n}, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
