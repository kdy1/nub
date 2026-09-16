//go:build !windows

package linker

import (
	"os/exec"
	"testing"
)

func cmdShimCommand(t *testing.T, shim string, args ...string) *exec.Cmd {
	t.Helper()
	t.Fatal("cmd shim execution is Windows-only")
	return nil
}
