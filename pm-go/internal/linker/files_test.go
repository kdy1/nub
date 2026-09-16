package linker

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/store"
)

func TestFillFilesStrategiesBytesModesAndDestinationPreservation(t *testing.T) {
	for _, strategy := range []Strategy{Copy, Hardlink, Reflink, ReflinkAuto} {
		root := t.TempDir()
		s := store.New(filepath.Join(root, "store", "v1", "files"), filepath.Join(root, "cache"))
		t.Cleanup(func() { s.Close() })
		index := store.PackageIndex{}
		contents := map[string][]byte{"index.js": []byte("module.exports = 42"), "nested/binary": bytes.Repeat([]byte{1, 2, 3, 0}, 16384), "empty": {}}
		for name, data := range contents {
			file, err := s.ImportBytes(t.Context(), data, name == "nested/binary")
			if err != nil {
				t.Fatal(err)
			}
			index[name] = file
		}
		destination := filepath.Join(root, "installed", "package")
		if err := FillFiles(t.Context(), index, destination, strategy); err != nil {
			t.Fatal(strategy, err)
		}
		for name, want := range contents {
			path := filepath.Join(destination, filepath.FromSlash(name))
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatal(strategy, name, err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if runtime.GOOS != "windows" && (info.Mode()&0111 != 0) != index[name].Executable {
				t.Fatal(strategy, name, info.Mode())
			}
			if strategy == Hardlink {
				source, err := os.Stat(index[name].Path)
				if err != nil || !os.SameFile(source, info) {
					t.Fatal("same-device hardlink copied bytes", err)
				}
			}
		}
		if err := FillFiles(t.Context(), index, destination, strategy); !os.IsExist(err) {
			t.Fatal("existing tree overwritten", err)
		}
		if _, err := os.Stat(filepath.Join(destination, "index.js")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFillFilesRejectsUnsafeIndexAndRemovesFailedTrees(t *testing.T) {
	root := t.TempDir()
	for _, key := range []string{"", "../outside", "nested/../../outside", "/absolute", `nested\outside`, "nul\x00file"} {
		destination := filepath.Join(root, "invalid")
		err := FillFiles(t.Context(), store.PackageIndex{key: {}}, destination, Copy)
		var unsafe *UnsafeIndexKey
		if !errors.As(err, &unsafe) {
			t.Fatal(key, err)
		}
		if _, err := os.Stat(destination); !os.IsNotExist(err) {
			t.Fatal("invalid key created a tree", key, err)
		}
	}
	for _, strategy := range []Strategy{Copy, Hardlink, Reflink, ReflinkAuto} {
		destination := filepath.Join(root, "missing")
		err := FillFiles(t.Context(), store.PackageIndex{"file": {Path: filepath.Join(root, "absent")}}, destination, strategy)
		var missing *MissingStoreFile
		if !errors.As(err, &missing) || missing.Code() != "ERR_AUBE_MISSING_STORE_FILE" {
			t.Fatal(err)
		}
		if _, err := os.Stat(destination); !os.IsNotExist(err) {
			t.Fatal("failed tree retained", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := FillFiles(ctx, nil, filepath.Join(root, "cancelled"), Copy); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestDetectStrategyUsesBothPathsAndCleansProbes(t *testing.T) {
	root := t.TempDir()
	want := Hardlink
	if runtime.GOOS == "darwin" {
		want = ReflinkAuto
	}
	if got := DetectStrategy(root, root); got != want {
		t.Fatal(got, want)
	}
	if got := DetectStrategy(root, filepath.Join(root, "missing")); got != Copy {
		t.Fatal(got)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal(entries, err)
	}
}
