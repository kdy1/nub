//go:build !windows

package fsutil

import "path/filepath"

func canonicalize(path string) (string, error) { return filepath.EvalSymlinks(path) }
