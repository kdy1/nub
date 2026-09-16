package parity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectsFileChanges(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "package.json")
	if err := os.WriteFile(path, []byte(`{"dependencies":{}}`), 0644); err != nil {
		t.Fatal(err)
	}
	before, err := Snapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := Compare(Result{Tree: before}, Result{Tree: before}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"dependencies":{"added":"1"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	after, err := Snapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if Compare(Result{Tree: before}, Result{Tree: after}) == nil {
		t.Fatal("missed manifest mutation")
	}
	if Compare(Result{Code: 1}, Result{}) == nil {
		t.Fatal("missed status mismatch")
	}
	if Compare(Result{Out: "result"}, Result{}) == nil {
		t.Fatal("missed output mismatch")
	}
}
