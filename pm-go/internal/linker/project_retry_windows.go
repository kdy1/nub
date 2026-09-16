package linker

import (
	"context"
	"errors"
	"time"

	"golang.org/x/sys/windows"
)

func retryLinkFS(ctx context.Context, attempts int, operation func() error) error {
	for attempt := range max(attempts, 1) {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := operation()
		if attempt+1 == max(attempts, 1) || !errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			return err
		}
		delay := min(50*time.Millisecond<<min(attempt, 6), 2*time.Second)
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
	return nil
}
