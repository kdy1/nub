//go:build !linux && !darwin

package linker

import "errors"

func cloneFile(source, target string) error { return errors.ErrUnsupported }
