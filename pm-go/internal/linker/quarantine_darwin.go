package linker

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/nubjs/nub/pm-go/internal/store"
	"golang.org/x/sys/unix"
)

func (q *Quarantine) remove(path string) {
	err := unix.Lremovexattr(path, "com.apple.quarantine")
	if err != nil && !errors.Is(err, unix.ENOATTR) && !errors.Is(err, unix.ENOENT) {
		q.warn(path, err)
	}
}

// StripIndexed visits only the tarball index's native modules and executables.
// It runs after patches on cold entries, and again on indexed cache hits.
func (q *Quarantine) StripIndexed(packageDir string, index store.PackageIndex) {
	for relative, file := range index {
		if isGatekeeperGuarded(relative, file.Executable) && validateIndexKey(relative) == nil {
			q.remove(filepath.Join(packageDir, filepath.FromSlash(relative)))
		}
	}
}

// StripTree serves build output restored without a tarball index. It inspects
// mode bits and never traverses directory links or strips a linked target.
func (q *Quarantine) StripTree(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			continue
		}
		if info.IsDir() {
			q.StripTree(path)
		} else if info.Mode().IsRegular() && isGatekeeperGuarded(entry.Name(), info.Mode().Perm()&0111 != 0) {
			q.remove(path)
		}
	}
}
