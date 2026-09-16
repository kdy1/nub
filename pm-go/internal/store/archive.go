package store

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"
)

type ArchiveLimits struct {
	DecompressedBytes, EntryBytes int64
	Entries                       int
}

func DefaultArchiveLimits() ArchiveLimits { return ArchiveLimits{1 << 30, 512 << 20, 200000} }

type ExtractedFile struct {
	Size       int64
	Executable bool
}
type ArchiveIndex map[string]ExtractedFile

// ExtractTarball creates a new destination and removes it on failure. It never
// merges into a pre-existing tree. Archive paths are interpreted independently
// of the host before os.Root confines all filesystem writes to the destination.
func ExtractTarball(ctx context.Context, compressed io.Reader, destination string, limits ArchiveLimits) (ArchiveIndex, error) {
	if limits.DecompressedBytes <= 0 || limits.EntryBytes <= 0 || limits.Entries <= 0 {
		return nil, fmt.Errorf("archive limits must be positive")
	}
	if err := os.Mkdir(destination, 0755); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(destination)
	if err != nil {
		os.Remove(destination)
		return nil, err
	}
	success := false
	defer func() {
		root.Close()
		if !success {
			os.RemoveAll(destination)
		}
	}()
	gz, err := gzip.NewReader(compressed)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	// The reference importer reads one gzip member.
	gz.Multistream(false)
	limited := &archiveReader{ctx: ctx, r: gz, remaining: limits.DecompressedBytes, limit: limits.DecompressedBytes}
	reader := tar.NewReader(limited)
	index := ArchiveIndex{}
	entries := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		entries++
		if entries > limits.Entries {
			return nil, fmt.Errorf("tarball exceeds entry cap of %d", limits.Entries)
		}
		switch header.Typeflag {
		case tar.TypeDir, tar.TypeXHeader, tar.TypeXGlobalHeader, tar.TypeGNULongName, tar.TypeGNULongLink:
			continue
		case tar.TypeReg, tar.TypeCont:
		default:
			return nil, fmt.Errorf("tarball entry type %q is not allowed", header.Typeflag)
		}
		if header.Size > limits.EntryBytes {
			return nil, fmt.Errorf("tarball entry exceeds per-entry cap: %d bytes > %d", header.Size, limits.EntryBytes)
		}
		name, err := NormalizeTarPath(header.Name, runtime.GOOS)
		if err != nil {
			return nil, err
		}
		if name == "" {
			continue
		}
		if err := root.MkdirAll(filepath.FromSlash(path.Dir(name)), 0755); err != nil {
			return nil, err
		}
		f, err := root.OpenFile(filepath.FromSlash(name), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			return nil, err
		}
		n, copyErr := io.Copy(f, reader)
		mode := os.FileMode(0644)
		executable := header.Mode&0111 != 0
		if executable {
			mode = 0755
		}
		if copyErr == nil {
			copyErr = f.Chmod(mode)
		}
		closeErr := f.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if n != header.Size {
			return nil, fmt.Errorf("tarball entry size mismatch: %s", name)
		}
		index[name] = ExtractedFile{n, executable}
	}
	// Reading to the end validates gzip's checksum/trailer as well as its
	// headers. A tar end marker alone does not establish gzip integrity.
	if _, err := io.Copy(io.Discard, limited); err != nil {
		return nil, err
	}
	success = true
	return index, nil
}

type archiveReader struct {
	ctx              context.Context
	r                io.Reader
	remaining, limit int64
}

func (r *archiveReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.remaining < int64(len(p)) {
		p = p[:r.remaining+1]
	}
	n, err := r.r.Read(p)
	r.remaining -= int64(n)
	if r.remaining < 0 {
		return n, fmt.Errorf("tarball decompression exceeds archive cap of %d bytes", r.limit)
	}
	return n, err
}

func NormalizeTarPath(raw, platform string) (string, error) {
	name := raw
	if platform == "windows" {
		name = strings.ReplaceAll(name, `\`, "/")
		if len(name) >= 2 && name[1] == ':' {
			return "", fmt.Errorf("tarball entry path has a Windows drive prefix: %q", raw)
		}
	}
	if strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("tarball entry path is absolute: %q", raw)
	}
	var components []string
	for _, component := range strings.Split(name, "/") {
		if component != "" && component != "." {
			components = append(components, component)
		}
	}
	if len(components) == 0 {
		return "", nil
	}
	if components[0] == ".." {
		return "", fmt.Errorf("tarball entry path escapes package root via `..`: %q", raw)
	}
	components = components[1:]
	for _, s := range components {
		if s == ".." {
			return "", fmt.Errorf("tarball entry path escapes package root via `..`: %q", raw)
		}
		if !utf8.ValidString(s) {
			return "", fmt.Errorf("tarball entry path contains non-UTF-8 bytes: %q", raw)
		}
		if strings.ContainsAny(s, "\x00\\/") {
			return "", fmt.Errorf("tarball entry path contains a malformed component: %q", raw)
		}
		if platform == "windows" {
			if strings.Contains(s, ":") {
				return "", fmt.Errorf("tarball entry path contains a malformed component: %q", raw)
			}
			if windowsReserved(s) {
				return "", fmt.Errorf("tarball entry path contains a Windows reserved device name: %q", raw)
			}
			if strings.HasSuffix(s, ".") || strings.HasSuffix(s, " ") {
				return "", fmt.Errorf("tarball entry path has a trailing dot or space which Windows strips: %q", raw)
			}
			for _, ch := range s {
				if ch < 0x20 {
					return "", fmt.Errorf("tarball entry path contains control characters: %q", raw)
				}
			}
		}
	}
	return strings.Join(components, "/"), nil
}

func windowsReserved(name string) bool {
	stem, _, _ := strings.Cut(strings.ToUpper(name), ".")
	switch stem {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	return len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9'
}
