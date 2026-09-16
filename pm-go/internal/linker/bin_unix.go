//go:build !windows

package linker

import "syscall"

func unlinkBinFile(path string) error { return syscall.Unlink(path) }

func removeBinFile(path string) { _ = unlinkBinFile(path) }
