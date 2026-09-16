package linker

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestGatekeeperSelection(t *testing.T) {
	for _, name := range []string{"lib/binding.node", "lib/native.so", "lib/libvips.dylib", ".hidden.node"} {
		if !isGatekeeperGuarded(name, false) {
			t.Fatal(name)
		}
	}
	for _, name := range []string{"bin/tool", "index.js", "README.md", "dir.node/index.js", "x.NODE", "x.node.map", ".node", "lib/.so", "file."} {
		if isGatekeeperGuarded(name, false) || !isGatekeeperGuarded(name, true) {
			t.Fatal(name)
		}
	}
}

func TestQuarantineWarningsAreBoundedPerInvocation(t *testing.T) {
	for range 2 {
		var count atomic.Int32
		q := &Quarantine{Warn: func(code, message string) {
			count.Add(1)
			if code != "WARN_AUBE_QUARANTINE_STRIP_FAILED" || !strings.Contains(message, "native.node: denied;") {
				t.Error(code, message)
			}
		}}
		var group sync.WaitGroup
		for range 100 {
			group.Go(func() { q.warn("native.node", errors.New("denied")) })
		}
		group.Wait()
		if count.Load() != 1 {
			t.Fatal(count.Load())
		}
	}
	(*Quarantine)(nil).warn("ignored", errors.New("denied"))
}
