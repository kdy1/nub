//go:build !windows

package linker

import "os"

func patchPathIsLink(info os.FileInfo) bool { return info.Mode()&os.ModeSymlink != 0 }
