//go:build !windows

package fsutil

import (
	"context"
	"os"
)

func renameAtomic(source, target string) error { return os.Rename(source, target) }

// RetryTransient executes once on platforms without Windows sharing failures.
func RetryTransient(ctx context.Context, operation func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return operation()
}
