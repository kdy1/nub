//go:build !windows

package fsutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestDefaultWriteRespectsUmask(t *testing.T) {
	if os.Getenv("PM_GO_TEST_UMASK_CHILD") == "1" {
		syscall.Umask(0077)
		path := filepath.Join(os.Getenv("PM_GO_TEST_UMASK_DIR"), "private")
		if err := WriteDefault(path, []byte("data")); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatal(info.Mode())
		}
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestDefaultWriteRespectsUmask$")
	cmd.Env = append(os.Environ(), "PM_GO_TEST_UMASK_CHILD=1", "PM_GO_TEST_UMASK_DIR="+t.TempDir())
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child: %v\n%s", err, output)
	}
}
