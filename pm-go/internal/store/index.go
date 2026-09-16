package store

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/nubjs/nub/pm-go/internal/fsutil"
	"github.com/nubjs/nub/pm-go/internal/spec"
	"lukechampine.com/blake3"
)

type StoredFile struct {
	Hash       string  `json:"hex_hash"`
	Path       string  `json:"store_path"`
	Executable bool    `json:"executable"`
	Size       *uint64 `json:"size"`
}
type PackageIndex map[string]StoredFile

// Native Unix paths are not necessarily UTF-8; preserve their bytes just as
// the reference index does instead of JSON's replacement-character conversion.
func (f StoredFile) MarshalJSON() ([]byte, error) {
	var path any = f.Path
	if !utf8.ValidString(f.Path) {
		values := make([]uint8, len(f.Path))
		copy(values, f.Path)
		// []byte encodes as base64, so use unsigned integer elements explicitly.
		ints := make([]uint16, len(values))
		for i, v := range values {
			ints[i] = uint16(v)
		}
		path = map[string]any{"unixBytes": ints}
	}
	return json.Marshal(struct {
		Hash       string  `json:"hex_hash"`
		Path       any     `json:"store_path"`
		Executable bool    `json:"executable"`
		Size       *uint64 `json:"size"`
	}{f.Hash, path, f.Executable, f.Size})
}

func (f *StoredFile) UnmarshalJSON(data []byte) error {
	var wire struct {
		Hash       *string         `json:"hex_hash"`
		Path       json.RawMessage `json:"store_path"`
		Executable *bool           `json:"executable"`
		Size       *uint64         `json:"size"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.Hash == nil || wire.Executable == nil || len(wire.Path) == 0 {
		return fmt.Errorf("invalid stored file")
	}
	var path string
	if len(wire.Path) > 0 && wire.Path[0] == '"' {
		if err := json.Unmarshal(wire.Path, &path); err != nil {
			return err
		}
	} else {
		var native struct {
			Unix    []uint16 `json:"unixBytes"`
			Windows []uint16 `json:"windowsWide"`
		}
		if err := json.Unmarshal(wire.Path, &native); err != nil {
			return err
		}
		if native.Unix != nil && runtime.GOOS != "windows" {
			raw := make([]byte, len(native.Unix))
			for i, b := range native.Unix {
				if b > 255 {
					return fmt.Errorf("invalid Unix path byte")
				}
				raw[i] = byte(b)
			}
			path = string(raw)
		} else if native.Windows != nil && runtime.GOOS == "windows" {
			path = string(utf16.Decode(native.Windows))
		} else {
			return fmt.Errorf("stored path belongs to another platform")
		}
	}
	*f = StoredFile{*wire.Hash, path, *wire.Executable, wire.Size}
	return nil
}

func IndexFingerprint(index PackageIndex) string {
	keys := indexKeys(index)
	hasher := blake3.New(32, nil)
	for _, path := range keys {
		file := index[path]
		hasher.Write([]byte(path))
		hasher.Write([]byte{0})
		hasher.Write([]byte(file.Hash))
		bit := byte(0)
		if file.Executable {
			bit = 1
		}
		hasher.Write([]byte{bit})
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func indexKeys(index PackageIndex) []string {
	keys := make([]string, 0, len(index))
	for path := range index {
		keys = append(keys, path)
	}
	sort.Strings(keys)
	return keys
}

func ValidVersion(version string) bool {
	if version == "" || version == "." || version == ".." || len(version) > 256 {
		return false
	}
	for _, b := range []byte(version) {
		if b < 32 || b == 127 || b == '/' || b == '\\' {
			return false
		}
	}
	return true
}

func indexRel(name, version string, integrity *string) (string, error) {
	if !spec.ValidName(name) || !ValidVersion(version) {
		return "", fmt.Errorf("refusing to cache: invalid coordinate %q@%q", name, version)
	}
	filename := strings.ReplaceAll(name, "/", "__") + "@" + version + ".json"
	if integrity == nil {
		return filename, nil
	}
	digest, ok := IntegrityHex(*integrity)
	if !ok {
		return "", fmt.Errorf("refusing to cache: invalid integrity %q", *integrity)
	}
	return filepath.Join(digest[:min(16, len(digest))], filename), nil
}

func (s *Store) indexPath(name, version string, integrity *string, fallback bool) (string, error) {
	rel, err := indexRel(name, version, integrity)
	if err != nil {
		return "", err
	}
	primary := filepath.Join(s.IndexDir(), rel)
	if fallback && s.ReadFallback != "" {
		if _, err := os.Stat(primary); os.IsNotExist(err) {
			candidate := filepath.Join(filepath.Dir(s.ReadFallback), "index", rel)
			if _, err := os.Stat(candidate); err == nil {
				return candidate, nil
			}
		}
	}
	return primary, nil
}

func (s *Store) validIndex(index PackageIndex) bool {
	if index == nil {
		return false
	}
	for path, file := range index {
		normalized, err := NormalizeTarPath("package/"+path, runtime.GOOS)
		if err != nil || normalized == "" || normalized != path || !validHash(file.Hash) {
			return false
		}
		primary, _ := s.writePath(file.Hash)
		if file.Path == primary {
			continue
		}
		if s.ReadFallback == "" || file.Path != filepath.Join(s.ReadFallback, file.Hash[:2], file.Hash[2:]) {
			return false
		}
	}
	return true
}

// LoadIndex treats corrupt, missing, or truncated entries as cache misses.
// The verified path checks every file; the warm path probes one file.
func (s *Store) LoadIndex(name, version string, integrity *string, verifyAll bool) (PackageIndex, bool) {
	path, err := s.indexPath(name, version, integrity, true)
	if err != nil {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var index PackageIndex
	if err := json.Unmarshal(data, &index); err != nil || !s.validIndex(index) {
		return nil, false
	}
	for _, key := range indexKeys(index) {
		file := index[key]
		info, err := os.Lstat(file.Path)
		if err != nil || !info.Mode().IsRegular() || file.Size != nil && uint64(info.Size()) != *file.Size {
			return nil, false
		}
		if !verifyAll {
			break
		}
	}
	return index, true
}

func (s *Store) SaveIndex(ctx context.Context, name, version string, integrity *string, index PackageIndex) error {
	path, err := s.indexPath(name, version, integrity, false)
	if err != nil {
		return err
	}
	if !s.validIndex(index) {
		return fmt.Errorf("invalid package index")
	}
	if err := s.prepare(ctx); err != nil {
		return err
	}
	data, err := json.Marshal(index)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return fsutil.Write(path, data, 0644)
}

func (s *Store) InvalidateIndex(ctx context.Context, name, version string, integrity *string) (bool, error) {
	path, err := s.indexPath(name, version, integrity, false)
	if err != nil {
		return false, nil
	}
	if err := s.prepare(ctx); err != nil {
		return false, err
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
