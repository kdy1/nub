//go:build !windows

package processenv

import (
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

func executable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fs.ErrPermission
	}
	err = unix.Faccessat(unix.AT_FDCWD, path, unix.X_OK, unix.AT_EACCESS)
	if err == nil {
		return nil
	}
	if err != unix.ENOSYS && err != unix.EPERM {
		return err
	}
	if info.Mode()&0111 != 0 {
		return nil
	}
	return fs.ErrPermission
}
