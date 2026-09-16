package fsutil

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// Windows readers, scanners and concurrent renames can temporarily deny
// replacement. Keep the old file intact while retrying those sharing failures.
func renameAtomic(source, target string) error {
	for attempt := range 5 {
		err := os.Rename(source, target)
		transient := errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION)
		if !transient || attempt == 4 {
			return err
		}
		time.Sleep(20 * time.Millisecond << attempt)
	}
	return nil
}
