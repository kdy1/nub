//go:build !windows

package installstate

import "syscall"

func unlinkFile(path string) error { return syscall.Unlink(path) }
