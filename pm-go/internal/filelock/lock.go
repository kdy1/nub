// Package filelock provides cancellable, process-wide advisory leases. Lock
// files are never removed: unlinking a live lock permits a second lock inode.
package filelock

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

type Lease struct{ lock *flock.Flock }

func Acquire(ctx context.Context, path string, shared bool) (*Lease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	lock := flock.New(path, flock.SetPermissions(0644))
	var err error
	if shared {
		_, err = lock.TryRLockContext(ctx, 10*time.Millisecond)
	} else {
		_, err = lock.TryLockContext(ctx, 10*time.Millisecond)
	}
	if err != nil {
		lock.Close()
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		lock.Close()
		return nil, err
	}
	return &Lease{lock}, nil
}

func (l *Lease) Close() error { return l.lock.Close() }
