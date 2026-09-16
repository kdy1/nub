package fsutil

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestAtomicWriteRetriesWindowsReaderSharingConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "binding.json")
	if err := os.WriteFile(path, []byte("previous"), 0644); err != nil {
		t.Fatal(err)
	}
	wide, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(wide, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			windows.CloseHandle(handle)
		}
	}()
	done := make(chan error, 1)
	go func() { done <- Write(path, []byte("replacement"), 0644) }()
	select {
	case err := <-done:
		t.Fatalf("write returned while replacement was blocked: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	old, err := os.ReadFile(path)
	if err != nil || string(old) != "previous" {
		t.Fatal("old file changed during blocked replacement", string(old), err)
	}
	if err := windows.CloseHandle(handle); err != nil {
		t.Fatal(err)
	}
	closed = true
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "replacement" {
		t.Fatal(string(data), err)
	}
}
