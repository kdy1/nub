package linker

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/nubjs/nub/pm-go/internal/fsutil"
	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
	"github.com/nubjs/nub/pm-go/internal/manifest"
)

// LinkManifest reads only bin declarations. A present bin field (even null)
// takes precedence over directories.bin, matching the reference install pass.
func (m *BinLinks) LinkManifest(binDir, pkgDir, pkgName string, pkg *jsonvalue.Value, opts BinOptions, preserved PreservedBinLinks) error {
	if bin := pkg.Get("bin"); bin != nil {
		return m.LinkEntries(binDir, pkgDir, pkgName, bin, opts, preserved)
	}
	return m.LinkDirectory(binDir, pkgDir, pkg.Get("directories").Get("bin").Text(), opts, preserved)
}

func (m *BinLinks) LinkEntries(binDir, pkgDir, pkgName string, bin *jsonvalue.Value, opts BinOptions, preserved PreservedBinLinks) error {
	if bin == nil {
		return nil
	}
	entries := map[string]string{}
	if bin.Kind == 's' && pkgName != "" {
		parts := strings.Split(pkgName, "/")
		entries[parts[len(parts)-1]] = bin.Text()
	} else if bin.Kind == '{' {
		entries = manifest.Strings(bin)
	}
	for _, name := range slices.Sorted(maps.Keys(entries)) {
		relative := entries[name]
		if ValidateBinName(name) != nil || ValidateBinTarget(relative) != nil {
			continue
		}
		if err := m.create(binDir, name, filepath.Join(pkgDir, filepath.FromSlash(relative)), opts, preserved); err != nil {
			return err
		}
	}
	return nil
}

// LinkDirectory recursively links regular files by basename in lexical order.
// The root must resolve strictly inside the package; child links are skipped.
func (m *BinLinks) LinkDirectory(binDir, pkgDir, relative string, opts BinOptions, preserved PreservedBinLinks) error {
	if relative == "" {
		return nil
	}
	binsRoot := filepath.Join(pkgDir, filepath.FromSlash(relative))
	if filepath.IsAbs(relative) {
		binsRoot = relative
	}
	canonicalRoot, err := fsutil.Canonicalize(pkgDir)
	if err != nil {
		return nil
	}
	canonicalBins, err := fsutil.Canonicalize(binsRoot)
	if err != nil || !strictPathChild(canonicalRoot, canonicalBins) {
		return nil
	}
	var files []string
	if err := collectBinFiles(binsRoot, &files); err != nil {
		return err
	}
	slices.Sort(files)
	for _, file := range files {
		name := filepath.Base(file)
		if !utf8.ValidString(name) || ValidateBinName(name) != nil {
			continue
		}
		if err := m.create(binDir, name, file, opts, preserved); err != nil {
			return err
		}
	}
	return nil
}

func strictPathChild(root, child string) bool {
	relative, err := filepath.Rel(root, child)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func collectBinFiles(dir string, files *[]string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read directories.bin dir %s: %w", dir, err)
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("failed to stat %s: %w", path, err)
		}
		if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			if err := collectBinFiles(path, files); err != nil {
				return err
			}
		} else if info.Mode().IsRegular() {
			*files = append(*files, path)
		}
	}
	return nil
}
