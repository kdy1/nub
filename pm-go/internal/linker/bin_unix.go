//go:build !windows

package linker

import "syscall"

func removeBinFile(path string) { _ = syscall.Unlink(path) }
