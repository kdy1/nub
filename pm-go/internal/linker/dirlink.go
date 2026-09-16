package linker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// CreateDirLink uses a relative-capable symlink on Unix and an unprivileged
// junction on Windows. It may replace a stale link, file or empty directory,
// but a populated directory remains a conflict and is never removed recursively.
func CreateDirLink(ctx context.Context, target, link string) error {
	if !filepath.IsAbs(link) {
		return fmt.Errorf("directory link requires an absolute destination")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err := createDirLink(ctx, target, link)
	if os.IsExist(err) {
		_ = os.Remove(link)
		if err := ctx.Err(); err != nil {
			return err
		}
		err = createDirLink(ctx, target, link)
	}
	return err
}
