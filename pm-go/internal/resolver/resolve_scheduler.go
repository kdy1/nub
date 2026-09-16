package resolver

import (
	"context"
	"sync"

	"github.com/nubjs/nub/pm-go/internal/registry"
)

type metadataKey struct{ name, exact string }

func (k metadataKey) String() string { return k.name + "\x00" + k.exact }

type metadataRequest struct {
	key             metadataKey
	client          *registry.Client
	route, cacheDir string
	mode            registry.NetworkMode
	full, refresh   bool
}

type metadataResult struct {
	key       metadataKey
	packument *registry.Packument
	history   *TrustHistory
	err       error
}

// Only the BFS driver reads or changes active and the graph caches. Workers
// own their request snapshot and return a result; hooks and graph mutations
// never run on those goroutines. Network capacity is separate from task order.
type fetchScheduler struct {
	ctx    context.Context
	cancel context.CancelFunc
	slots  chan struct{}
	done   chan metadataResult
	active map[metadataKey]struct{}
	wg     sync.WaitGroup
}

func newFetchScheduler(ctx context.Context, limit int) *fetchScheduler {
	if limit <= 0 {
		limit = 256
	}
	ctx, cancel := context.WithCancel(ctx)
	return &fetchScheduler{ctx: ctx, cancel: cancel, slots: make(chan struct{}, max(4, limit)), done: make(chan metadataResult), active: map[metadataKey]struct{}{}}
}

func (s *fetchScheduler) ensure(input metadataRequest) {
	if _, active := s.active[input.key]; active {
		return
	}
	s.active[input.key] = struct{}{}
	s.wg.Go(func() {
		select {
		case s.slots <- struct{}{}:
		case <-s.ctx.Done():
			return
		}
		result := fetchMetadata(s.ctx, input)
		<-s.slots
		select {
		case s.done <- result:
		case <-s.ctx.Done():
		}
	})
}

func (s *fetchScheduler) hasExact(name string) bool {
	for key := range s.active {
		if key.name == name && key.exact != "" {
			return true
		}
	}
	return false
}

func (s *fetchScheduler) next(ctx context.Context) (metadataResult, error) {
	if len(s.active) == 0 {
		return metadataResult{}, &RegistryFailure{"(resolver)", "packument fetch disappeared before completing"}
	}
	select {
	case result := <-s.done:
		delete(s.active, result.key)
		return result, nil
	case <-ctx.Done():
		return metadataResult{}, ctx.Err()
	}
}

// Successful resolution waits for speculative cache writes, including peers
// that reused a resolved version. A failed invocation cancels and joins them.
func (s *fetchScheduler) drain(ctx context.Context) error {
	for len(s.active) > 0 {
		if _, err := s.next(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (s *fetchScheduler) close() {
	s.cancel()
	s.wg.Wait()
}
