package linker

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

func createDirLink(ctx context.Context, target, link string) error {
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(link), target)
	}
	target = filepath.Clean(target)
	delay := 50 * time.Millisecond
	for attempt := range 10 {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := createJunction(target, link)
		if attempt == 9 || !(errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION)) {
			return err
		}
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
		delay = min(delay*2, 2*time.Second)
	}
	return nil
}

func createJunction(target, link string) error {
	printName := target
	if strings.HasPrefix(printName, `\\?\UNC\`) {
		printName = `\\` + strings.TrimPrefix(printName, `\\?\UNC\`)
	} else {
		printName = strings.TrimPrefix(printName, `\\?\`)
	}
	substitute := `\??\` + printName
	if strings.HasPrefix(printName, `\\`) {
		substitute = `\??\UNC\` + strings.TrimPrefix(printName, `\\`)
	}
	sub, err := windows.UTF16FromString(substitute)
	if err != nil {
		return err
	}
	print, err := windows.UTF16FromString(printName)
	if err != nil {
		return err
	}
	// REPARSE_DATA_BUFFER header (8), mount-point offsets (8), then
	// two NUL-terminated UTF-16 strings. Length fields exclude the NUL.
	length := 16 + (len(sub)+len(print))*2
	if length > windows.MAXIMUM_REPARSE_DATA_BUFFER_SIZE {
		return fmt.Errorf("junction target exceeds reparse buffer limit")
	}
	buffer := make([]byte, length)
	binary.LittleEndian.PutUint32(buffer[0:4], windows.IO_REPARSE_TAG_MOUNT_POINT)
	binary.LittleEndian.PutUint16(buffer[4:6], uint16(length-8))
	binary.LittleEndian.PutUint16(buffer[10:12], uint16((len(sub)-1)*2))
	binary.LittleEndian.PutUint16(buffer[12:14], uint16(len(sub)*2))
	binary.LittleEndian.PutUint16(buffer[14:16], uint16((len(print)-1)*2))
	for i, code := range append(sub, print...) {
		binary.LittleEndian.PutUint16(buffer[16+i*2:], code)
	}
	if err := os.Mkdir(link, 0755); err != nil {
		return err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.Remove(link)
		}
	}()
	wide, err := windows.UTF16PtrFromString(link)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(wide, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	var returned uint32
	if err := windows.DeviceIoControl(handle, windows.FSCTL_SET_REPARSE_POINT, &buffer[0], uint32(len(buffer)), nil, 0, &returned, nil); err != nil {
		return err
	}
	complete = true
	return nil
}
