package store

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// ImportTarball validates the complete archive before importing any files.
// A staging tree allows the same bounded extractor to serve the CAS and
// directory consumers without trusting archive paths in the store itself.
func (s *Store) ImportTarball(ctx context.Context, compressed io.Reader, limits ArchiveLimits) (PackageIndex, error) {
	if err := s.prepare(ctx); err != nil {
		return nil, err
	}
	staging, err := os.MkdirTemp(s.VersionDir(), ".extract-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	destination := filepath.Join(staging, "package")
	extracted, err := ExtractTarball(ctx, compressed, destination, limits)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(destination)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	index := PackageIndex{}
	keys := make([]string, 0, len(extracted))
	for name := range extracted {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		file, err := root.Open(filepath.FromSlash(name))
		if err != nil {
			return nil, err
		}
		stored, err := s.Import(ctx, file, extracted[name].Executable)
		closeErr := file.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		index[name] = stored
	}
	return index, nil
}

// ImportDirectory excludes dependency trees, VCS data, and symlinks, matching
// file: dependency imports. Executable metadata belongs to each index entry.
func (s *Store) ImportDirectory(ctx context.Context, directory string) (PackageIndex, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	index := PackageIndex{}
	err = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == "." {
			return nil
		}
		if entry.Name() == ".git" || entry.Name() == "node_modules" {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		file, err := root.Open(filepath.FromSlash(path))
		if err != nil {
			return err
		}
		stored, err := s.Import(ctx, file, runtime.GOOS != "windows" && info.Mode()&0111 != 0)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		key := strings.ReplaceAll(strings.ToValidUTF8(path, "\ufffd"), "\\", "/")
		index[key] = stored
		return nil
	})
	if err != nil {
		return nil, err
	}
	return index, nil
}

func ValidateIndexContent(index PackageIndex, name, version string) error {
	manifest, ok := index["package.json"]
	if !ok {
		return fmt.Errorf("package.json missing from tarball")
	}
	data, err := os.ReadFile(manifest.Path)
	if err != nil {
		return err
	}
	return ValidateContent(data, name, version)
}
