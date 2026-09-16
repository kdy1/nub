package linker

import (
	"context"
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

func reconcileDependencyLink(ctx context.Context, link, target string) (bool, error) {
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(link), target)
	}
	// A correctly targeted dangling junction needs no repair. Avoid deleting
	// it while another install materializes the destination or reads the link.
	if raw, err := os.Readlink(link); err == nil && filepath.Clean(stripVerbatim(raw)) == filepath.Clean(stripVerbatim(target)) {
		return true, nil
	}
	canonicalLink, linkErr := fsutil.Canonicalize(link)
	canonicalTarget, targetErr := fsutil.Canonicalize(target)
	if linkErr == nil && targetErr == nil && canonicalLink == canonicalTarget {
		return true, nil
	}
	if _, err := os.Lstat(link); err != nil {
		return false, nil
	}
	err := fsutil.RetryTransient(ctx, func() error {
		err := os.Remove(link)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	})
	return false, err
}
