package linker

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/nubjs/nub/pm-go/internal/fsutil"
)

type binEntry struct {
	kind byte
	data string
}

// PreservedBinLinks records complete command families replaced by a lifecycle
// script. On Windows a family includes its cmd, PowerShell and shell launchers.
type PreservedBinLinks map[string]map[string]bool

// BinLinks is private to one ordered bin-linking pass. Capture the pre-build
// pass when lifecycle scripts can run, then compare it before relinking.
type BinLinks struct {
	capture bool
	entries map[string]map[string]map[string]binEntry
	seen    PreservedBinLinks
}

func NewBinLinks(capture bool) *BinLinks {
	return &BinLinks{capture: capture, entries: map[string]map[string]map[string]binEntry{}, seen: PreservedBinLinks{}}
}

func readBinEntry(path string) (*binEntry, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	entry := &binEntry{kind: 'o'}
	if info.Mode()&os.ModeSymlink != 0 {
		entry.kind = 'l'
		entry.data, err = os.Readlink(path)
	} else if info.Mode().IsRegular() {
		entry.kind = 'f'
		var data []byte
		data, err = os.ReadFile(path)
		entry.data = string(data)
	}
	return entry, err
}

func (m *BinLinks) create(binDir, name, target string, opts BinOptions, preserved PreservedBinLinks) error {
	if preserved != nil {
		if m.seen[binDir] == nil {
			m.seen[binDir] = map[string]bool{}
		}
		m.seen[binDir][name] = true
		if preserved[binDir][name] {
			return nil
		}
	}
	// Create the physical parent first on Windows, where recursive mkdir
	// through a junction may report AlreadyExists for an absent leaf.
	mkdirRoot := binDir
	if runtime.GOOS == "windows" {
		if canonical, err := fsutil.Canonicalize(filepath.Dir(binDir)); err == nil {
			mkdirRoot = filepath.Join(stripVerbatim(canonical), filepath.Base(binDir))
		}
	}
	mkdirTarget := filepath.Dir(filepath.Join(mkdirRoot, filepath.FromSlash(name)))
	if err := os.MkdirAll(mkdirTarget, 0755); err != nil {
		info, statErr := os.Stat(mkdirTarget)
		if !os.IsExist(err) || statErr != nil || !info.IsDir() {
			return fmt.Errorf("failed to create bin directory %s: %w", binDir, err)
		}
	}
	if err := CreateBinShim(binDir, name, target, opts); err != nil {
		return fmt.Errorf("failed to link bin `%s` at %s -> %s: %w", name, filepath.Join(binDir, name), target, err)
	}
	if !m.capture {
		return nil
	}
	files := map[string]binEntry{}
	for _, path := range binPaths(binDir, name) {
		entry, err := readBinEntry(path)
		if err != nil {
			return err
		}
		if entry != nil {
			files[path] = *entry
		}
	}
	if m.entries[binDir] == nil {
		m.entries[binDir] = map[string]map[string]binEntry{}
	}
	m.entries[binDir][name] = files
	return nil
}

// RemoveUnchanged removes only the exact files/links emitted by this pass.
// Replacing or deleting any launcher preserves the entire command family.
func (m *BinLinks) RemoveUnchanged() (PreservedBinLinks, error) {
	preserved := PreservedBinLinks{}
	for _, binDir := range slices.Sorted(maps.Keys(m.entries)) {
		for _, name := range slices.Sorted(maps.Keys(m.entries[binDir])) {
			files := m.entries[binDir][name]
			paths := slices.Sorted(maps.Keys(files))
			replaced := false
			for _, path := range paths {
				current, err := readBinEntry(path)
				if err != nil {
					return nil, err
				}
				if current == nil || *current != files[path] {
					replaced = true
				}
			}
			if replaced {
				if preserved[binDir] == nil {
					preserved[binDir] = map[string]bool{}
				}
				preserved[binDir][name] = true
			} else {
				for _, path := range paths {
					if err := unlinkBinFile(path); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	return preserved, nil
}

// RemoveUnclaimed drops preserved families that the post-build manifests no
// longer declare. A still-declared family keeps replacements and deletions.
func (m *BinLinks) RemoveUnclaimed(preserved PreservedBinLinks, relinked *BinLinks) error {
	for _, binDir := range slices.Sorted(maps.Keys(preserved)) {
		for _, name := range slices.Sorted(maps.Keys(preserved[binDir])) {
			if relinked.seen[binDir][name] {
				continue
			}
			for _, path := range slices.Sorted(maps.Keys(m.entries[binDir][name])) {
				info, err := os.Lstat(path)
				if os.IsNotExist(err) {
					continue
				}
				if err != nil {
					return err
				}
				if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
					err = os.RemoveAll(path)
				} else {
					err = unlinkBinFile(path)
				}
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}
