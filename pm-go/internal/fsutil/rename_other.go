//go:build !windows

package fsutil

import "os"

func renameAtomic(source, target string) error { return os.Rename(source, target) }
