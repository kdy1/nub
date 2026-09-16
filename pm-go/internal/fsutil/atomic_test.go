package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultWriteAndFailureCleanup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	if err := WriteDefault(path, []byte("old")); err != nil {
		t.Fatal(err)
	}
	if err := WriteDefault(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "new" {
		t.Fatalf("%s: %v", data, err)
	}
	blocked := filepath.Join(dir, "directory")
	if err := os.Mkdir(blocked, 0755); err != nil {
		t.Fatal(err)
	}
	if err := WriteDefault(blocked, []byte("fail")); err == nil {
		t.Fatal("replaced a directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatal("temporary file leaked", entries)
	}
}
