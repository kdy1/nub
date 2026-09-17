package installstate

import "golang.org/x/sys/windows"

func unlinkFile(path string) error {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.DeleteFile(ptr)
}
