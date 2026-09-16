//go:build !windows

package linker

import (
	"errors"
	"os"
	"syscall"
)

func transientPublishError(err error) bool {
	return os.IsExist(err) || os.IsPermission(err) || errors.Is(err, syscall.EINTR) || errors.Is(err, syscall.EAGAIN)
}

func reconcileDependencyLink(link, target string) (bool, error) {
	existing, err := os.Readlink(link)
	if err == nil && existing == target {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	if err == nil {
		_ = os.Remove(link)
	} else {
		_ = os.RemoveAll(link)
	}
	return false, nil
}
