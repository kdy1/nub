package linker

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
)

// Quarantine belongs to one install invocation. Its shared warning latch
// prevents concurrent package materializers from flooding the diagnostic sink.
// A nil receiver still performs the platform operation, without diagnostics.
type Quarantine struct {
	Warn   func(code, message string)
	warned atomic.Bool
}

func isGatekeeperGuarded(relative string, executable bool) bool {
	if executable {
		return true
	}
	base := filepath.Base(relative)
	dot := strings.LastIndexByte(base, '.')
	if dot <= 0 {
		return false
	}
	switch base[dot+1:] {
	case "node", "dylib", "so":
		return true
	}
	return false
}

func (q *Quarantine) warn(path string, err error) {
	if q != nil && q.Warn != nil && !q.warned.Swap(true) {
		q.Warn("WARN_AUBE_QUARANTINE_STRIP_FAILED", fmt.Sprintf("could not remove com.apple.quarantine from %s: %v; macOS may refuse to load or run native binaries from this install", path, err))
	}
}
