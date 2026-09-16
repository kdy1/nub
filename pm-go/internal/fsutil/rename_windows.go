package fsutil

import (
	"context"
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// Windows readers, scanners and concurrent renames can temporarily deny
// replacement. Keep the old file intact while retrying those sharing failures.
func renameAtomic(source, target string) error {
	return RetryTransient(context.Background(), func() error { return os.Rename(source, target) })
}

// RetryTransient bounds sharing retries and observes cancellation between them.
func RetryTransient(ctx context.Context, operation func() error) error {
	for attempt := range 5 {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := operation()
		transient := errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION)
		if !transient || attempt == 4 {
			return err
		}
		timer := time.NewTimer(20 * time.Millisecond << attempt)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
	return nil
}
