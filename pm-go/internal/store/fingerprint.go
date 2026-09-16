package store

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"lukechampine.com/blake3"
)

type directoryFile struct {
	path, content string
	executable    bool
	size          uint64
	seconds       int64
	nanos         uint32
}

// DirectoryFingerprints observes the same files as ImportDirectory without
// populating the CAS. Metadata is captured before content is read.
func DirectoryFingerprints(ctx context.Context, directory string) (content, metadata string, err error) {
	entries, err := directoryFiles(ctx, directory, true)
	if err != nil {
		return "", "", err
	}
	return fingerprintContent(entries), fingerprintMetadata(entries), nil
}

func DirectoryContentFingerprint(ctx context.Context, directory string) (string, error) {
	entries, err := directoryFiles(ctx, directory, true)
	if err != nil {
		return "", err
	}
	return fingerprintContent(entries), nil
}

func DirectoryMetadataFingerprint(ctx context.Context, directory string) (string, error) {
	entries, err := directoryFiles(ctx, directory, false)
	if err != nil {
		return "", err
	}
	return fingerprintMetadata(entries), nil
}

func directoryFiles(ctx context.Context, directory string, content bool) ([]directoryFile, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var entries []directoryFile
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
		f := directoryFile{path: strings.ReplaceAll(strings.ToValidUTF8(path, "\ufffd"), "\\", "/"),
			executable: runtime.GOOS != "windows" && info.Mode()&0111 != 0, size: uint64(info.Size())}
		if t := info.ModTime(); t.Unix() >= 0 {
			f.seconds, f.nanos = t.Unix(), uint32(t.Nanosecond())
		}
		if content {
			data, err := root.ReadFile(filepath.FromSlash(path))
			if err != nil {
				return err
			}
			h := blake3.Sum256(data)
			f.content = hex.EncodeToString(h[:])
		}
		entries = append(entries, f)
		return nil
	})
	slices.SortFunc(entries, func(a, b directoryFile) int { return strings.Compare(a.path, b.path) })
	return entries, err
}

func fingerprintContent(entries []directoryFile) string {
	// The reference sorts complete tuples, including collisions introduced by
	// lossy UTF-8 conversion or Unix filenames containing backslashes.
	entries = slices.Clone(entries)
	slices.SortFunc(entries, func(a, b directoryFile) int {
		if n := strings.Compare(a.path, b.path); n != 0 {
			return n
		}
		if n := strings.Compare(a.content, b.content); n != 0 {
			return n
		}
		if a.executable == b.executable {
			return 0
		}
		if a.executable {
			return 1
		}
		return -1
	})
	h := blake3.New(32, nil)
	for _, entry := range entries {
		h.Write([]byte(entry.path))
		h.Write([]byte{0})
		h.Write([]byte(entry.content))
		bit := byte(0)
		if entry.executable {
			bit = 1
		}
		h.Write([]byte{bit})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func fingerprintMetadata(entries []directoryFile) string {
	h := blake3.New(32, nil)
	for _, entry := range entries {
		h.Write([]byte(entry.path))
		h.Write([]byte{0})
		var data [21]byte
		binary.LittleEndian.PutUint64(data[0:8], entry.size)
		binary.LittleEndian.PutUint64(data[8:16], uint64(entry.seconds))
		binary.LittleEndian.PutUint32(data[16:20], entry.nanos)
		if entry.executable {
			data[20] = 1
		}
		h.Write(data[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}
