package linker

import "golang.org/x/sys/windows"

func removeBinFile(path string) {
	if ptr, err := windows.UTF16PtrFromString(path); err == nil {
		_ = windows.DeleteFile(ptr)
	}
}
