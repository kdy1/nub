package filelock

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestSharedAndExclusiveLeases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "locks", "store.lock")
	first, err := Acquire(t.Context(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Acquire(t.Context(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := Acquire(ctx, path, false); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("exclusive while shared: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	exclusive, err := Acquire(t.Context(), path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer exclusive.Close()
	ctx2, cancel2 := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel2()
	if _, err := Acquire(ctx2, path, true); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shared while exclusive: %v", err)
	}
}

func TestLockProcess(t *testing.T) {
	if path := os.Getenv("PM_GO_LOCK_HELPER"); path != "" {
		ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
		defer cancel()
		lease, err := Acquire(ctx, path, false)
		if os.Getenv("PM_GO_LOCK_EXPECT_BLOCK") == "1" {
			if !errors.Is(err, context.DeadlineExceeded) {
				if lease != nil {
					lease.Close()
				}
				t.Fatalf("child escaped lock: %v", err)
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			lease.Close()
		}
		return
	}
	path := filepath.Join(t.TempDir(), "process.lock")
	lease, err := Acquire(t.Context(), path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	child := func(block bool) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestLockProcess$")
		cmd.Env = append(os.Environ(), "PM_GO_LOCK_HELPER="+path)
		if block {
			cmd.Env = append(cmd.Env, "PM_GO_LOCK_EXPECT_BLOCK=1")
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("child: %v\n%s", err, out)
		}
	}
	child(true)
	lease.Close()
	child(false)
}

func TestCancelledLockDoesNotCreateFiles(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	path := filepath.Join(t.TempDir(), "unused", "lock")
	if _, err := Acquire(ctx, path, false); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}
