//go:build !windows

package linker

import "context"

func sameStoredPath(a, b string) bool { return a == b }

func retryLinkFS(ctx context.Context, _ int, operation func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return operation()
}
