package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestURLBindingsPartitionStoresAndPreserveWarmFiles(t *testing.T) {
	global, local := testStore(t), testStore(t)
	local.ReadFallback = global.Root
	url := "https://registry.example/pkg/-/pkg-1.tgz"
	first, second := Integrity([]byte("first")), Integrity([]byte("second"))
	if err := global.SaveBinding(t.Context(), url, first); err != nil {
		t.Fatal(err)
	}
	if got, ok := local.ReadBinding(url); !ok || got != first {
		t.Fatal(got, ok)
	}
	if _, ok := local.ReadBinding("https://other.example/pkg/-/pkg-1.tgz"); ok {
		t.Fatal("registry binding crossed origins")
	}
	if err := local.SaveBinding(t.Context(), url, second); err != nil {
		t.Fatal(err)
	}
	path := bindingPath(local.VersionDir(), url)
	stamp := time.Unix(1000000000, 0)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if err := local.SaveBinding(t.Context(), url, second); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || !info.ModTime().Equal(stamp) {
		t.Fatal(info, err)
	}
	if got, ok := local.ReadBinding(url); !ok || got != second {
		t.Fatal(got, ok)
	}
	if err := os.WriteFile(path, []byte(`{"url":"different","sha512":"forged"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if got, ok := local.ReadBinding(url); !ok || got != first {
		t.Fatal("corrupt primary must fall back", got, ok)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := local.SaveBinding(ctx, url, first); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestURLBindingsConcurrentWriters(t *testing.T) {
	s := testStore(t)
	url := "https://registry.example/p/-/p.tgz"
	values := []string{Integrity([]byte("one")), Integrity([]byte("two"))}
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Go(func() {
			if err := s.SaveBinding(t.Context(), url, values[i%2]); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	got, ok := s.ReadBinding(url)
	if !ok || got != values[0] && got != values[1] {
		t.Fatal(got, ok)
	}
	entries, err := os.ReadDir(filepath.Join(s.VersionDir(), "no-integrity"))
	if err != nil || len(entries) != 1 {
		t.Fatal(entries, err)
	}
}
