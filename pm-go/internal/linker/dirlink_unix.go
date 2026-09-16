//go:build !windows

package linker

import (
	"context"
	"os"
)

func createDirLink(ctx context.Context, target, link string) error { return os.Symlink(target, link) }
