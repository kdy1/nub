// Package linker materializes package contents and dependency layouts.
package linker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/fsutil"
	"github.com/nubjs/nub/pm-go/internal/store"
)

type Strategy uint8

const (
	Hardlink Strategy = iota
	Copy
	Reflink
	ReflinkAuto
)

type MissingStoreFile struct{ StorePath, RelativePath string }

func (e *MissingStoreFile) Error() string {
	return fmt.Sprintf("cached package index references a missing CAS shard at %s (file: %q). The store and its index cache are out of sync — rerun the install to re-fetch the tarball.", e.StorePath, e.RelativePath)
}
func (e *MissingStoreFile) Code() string { return "ERR_AUBE_MISSING_STORE_FILE" }

type UnsafeIndexKey struct{ Key string }

func (e *UnsafeIndexKey) Error() string {
	return fmt.Sprintf("refusing to materialize unsafe index key: %q", e.Key)
}
func (e *UnsafeIndexKey) Code() string { return "ERR_AUBE_UNSAFE_INDEX_KEY" }

// FillFiles creates a fresh package tree. Failure removes only the tree this
// call created; an existing destination is never overwritten or removed.
func FillFiles(ctx context.Context, index store.PackageIndex, destination string, strategy Strategy) error {
	if !filepath.IsAbs(destination) {
		return fmt.Errorf("materialization requires an absolute destination")
	}
	if strategy > ReflinkAuto {
		return fmt.Errorf("invalid file linking strategy")
	}
	keys := slices.Sorted(maps.Keys(index))
	for _, key := range keys {
		if err := validateIndexKey(key); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	if err := os.Mkdir(destination, 0755); err != nil {
		return err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(destination)
		}
	}()
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		target := filepath.Join(destination, filepath.FromSlash(key))
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		stored := index[key]
		if err := placeFile(ctx, stored, target, strategy); err != nil {
			if _, statErr := os.Stat(stored.Path); os.IsNotExist(statErr) {
				return &MissingStoreFile{stored.Path, key}
			}
			return err
		}
		if stored.Executable && runtime.GOOS != "windows" {
			info, err := os.Stat(target)
			if err != nil {
				return err
			}
			if err := os.Chmod(target, info.Mode().Perm()|0111); err != nil {
				return err
			}
		}
	}
	complete = true
	return nil
}

func validateIndexKey(key string) error {
	if key == "" || strings.HasPrefix(key, "/") || strings.ContainsAny(key, "\\\x00") {
		return &UnsafeIndexKey{key}
	}
	for _, component := range strings.Split(key, "/") {
		if component == ".." || runtime.GOOS == "windows" && strings.Contains(component, ":") {
			return &UnsafeIndexKey{key}
		}
	}
	return nil
}

func placeFile(ctx context.Context, stored store.StoredFile, target string, strategy Strategy) error {
	if strategy == Reflink || strategy == ReflinkAuto {
		if runtime.GOOS == "darwin" && stored.Size != nil && *stored.Size <= 16*1024 {
			return copyFile(ctx, stored.Path, target)
		}
		if err := cloneFile(stored.Path, target); err == nil {
			return nil
		}
		_ = os.Remove(target)
		if strategy == Reflink {
			return copyFile(ctx, stored.Path, target)
		}
	}
	if strategy == Hardlink || strategy == ReflinkAuto {
		if err := os.Link(stored.Path, target); err == nil {
			return nil
		}
	}
	return copyFile(ctx, stored.Path, target)
}

func copyFile(ctx context.Context, source, target string) error {
	return fsutil.RetryTransient(ctx, func() error { return copyFileOnce(ctx, source, target) })
}

func copyFileOnce(ctx context.Context, source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("store source is not a regular file: %s", source)
	}
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, cancellationReader{ctx, in})
	closeErr := out.Close()
	err = errors.Join(copyErr, closeErr)
	if err != nil {
		_ = os.Remove(target)
	}
	return err
}

type cancellationReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r cancellationReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// DetectStrategy probes both locations so a cross-filesystem store chooses
// copying before any package is materialized. Probe files are invocation-local.
func DetectStrategy(sourceDir, targetDir string) Strategy {
	source, err := os.CreateTemp(sourceDir, ".nub-pm-go-link-probe-*")
	if err != nil {
		return Copy
	}
	name := source.Name()
	source.Close()
	defer os.Remove(name)
	target := filepath.Join(targetDir, filepath.Base(name)+".target")
	if err := os.Link(name, target); err != nil {
		return Copy
	}
	defer os.Remove(target)
	if runtime.GOOS == "darwin" {
		return ReflinkAuto
	}
	return Hardlink
}
