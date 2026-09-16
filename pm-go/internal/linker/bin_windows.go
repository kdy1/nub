package linker

import "golang.org/x/sys/windows"

func unlinkBinFile(path string) error {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.DeleteFile(ptr)
}

func removeBinFile(path string) { _ = unlinkBinFile(path) }
