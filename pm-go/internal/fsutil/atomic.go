package fsutil

import (
	"crypto/rand"
	"os"
	"path/filepath"
)

// Write publishes only a complete file, retaining the previous file on failure.
func Write(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".pm-write-*")
	if err != nil {
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		os.Remove(f.Name())
		return err
	}
	return publish(f, path, data)
}

// WriteDefault applies the platform's normal creation permissions, including
// umask. Unlike Write, it does not reapply an explicit mode after creation.
func WriteDefault(path string, data []byte) error {
	for range 10 {
		name := filepath.Join(filepath.Dir(path), ".pm-write-"+rand.Text())
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0666)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		return publish(f, path, data)
	}
	return &os.PathError{Op: "create", Path: path, Err: os.ErrExist}
}

func publish(f *os.File, path string, data []byte) error {
	name := f.Name()
	defer os.Remove(name)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return renameAtomic(name, path)
}
