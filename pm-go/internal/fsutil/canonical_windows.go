package fsutil

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func canonicalize(path string) (string, error) {
	widePath := filepath.Clean(path)
	if !strings.HasPrefix(widePath, `\\?\`) {
		if strings.HasPrefix(widePath, `\\`) {
			widePath = `\\?\UNC\` + strings.TrimPrefix(widePath, `\\`)
		} else {
			widePath = `\\?\` + widePath
		}
	}
	wide, err := windows.UTF16PtrFromString(widePath)
	if err != nil {
		return "", &os.PathError{Op: "canonicalize", Path: path, Err: err}
	}
	handle, err := windows.CreateFile(wide, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", &os.PathError{Op: "canonicalize", Path: path, Err: err}
	}
	defer windows.CloseHandle(handle)
	buffer := make([]uint16, 256)
	for {
		n, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", &os.PathError{Op: "canonicalize", Path: path, Err: err}
		}
		if n >= uint32(len(buffer)) {
			buffer = make([]uint16, int(n)+1)
			continue
		}
		result := windows.UTF16ToString(buffer[:n])
		if strings.HasPrefix(result, `\\?\UNC\`) {
			return `\\` + strings.TrimPrefix(result, `\\?\UNC\`), nil
		}
		return strings.TrimPrefix(result, `\\?\`), nil
	}
}
