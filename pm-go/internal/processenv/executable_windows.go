package processenv

import (
	"io/fs"
	"os"
)

func executable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fs.ErrPermission
	}
	return nil
}
