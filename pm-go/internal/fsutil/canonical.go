package fsutil

import (
	"fmt"
	"path/filepath"
)

// Canonicalize resolves an existing absolute path through symlinks and Windows
// junctions. It uses the opened file's final path on Windows: EvalSymlinks does
// not resolve junctions represented as ModeIrregular by recent Go releases.
func Canonicalize(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("canonicalization requires an absolute path")
	}
	return canonicalize(path)
}
