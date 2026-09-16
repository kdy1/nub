package linker

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/nubjs/nub/pm-go/internal/fsutil"
	"golang.org/x/sys/windows"
)

func transientPublishError(err error) bool {
	return os.IsExist(err) || os.IsPermission(err) || errors.Is(err, windows.ERROR_OPERATION_ABORTED) ||
		errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}

func reconcileDependencyLink(link, target string) (bool, error) {
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(link), target)
	}
	canonicalLink, linkErr := fsutil.Canonicalize(link)
	canonicalTarget, targetErr := fsutil.Canonicalize(target)
	if linkErr == nil && targetErr == nil && canonicalLink == canonicalTarget {
		return true, nil
	}
	if _, err := os.Lstat(link); err != nil {
		return false, nil
	}
	err := os.Remove(link)
	if os.IsNotExist(err) {
		err = nil
	}
	return false, err
}
