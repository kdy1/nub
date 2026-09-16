package resolver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/npmconfig"
	"github.com/nubjs/nub/pm-go/internal/processenv"
	"github.com/nubjs/nub/pm-go/internal/registry"
)

func schedulerResolver(t *testing.T, endpoint string) *Resolver {
	t.Helper()
	root := t.TempDir()
	config := npmconfig.Resolve([]npmconfig.Entry{{Source: npmconfig.User, Key: "registry", Value: endpoint}}, nil)
	policy := registry.DefaultFetchPolicy()
	policy.Retries = 0
	client := registry.NewClient(config, registry.ClientOptions{Dir: root, Policy: policy})
	t.Cleanup(client.Close)
	r := New(client, processenv.Environment{Dir: root}, t.TempDir())
	r.Options.PackumentNetworkConcurrency = 4
	return r
}

func schedulerPackument(w http.ResponseWriter, name string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"name": name, "versions": map[string]any{"1.0.0": map[string]any{"name": name, "version": "1.0.0"}},
		"dist-tags": map[string]string{"latest": "1.0.0"}, "time": map[string]string{"1.0.0": "2020-01-01T00:00:00.000Z"},
	})
}

func TestResolutionPrefetchIsBoundedDeduplicatedAndOrdered(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	started := make(chan struct{}, 8)
	release := make(chan struct{})
	var mu sync.Mutex
	active, peak := 0, 0
	counts := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		active++
		peak = max(peak, active)
		counts[req.URL.Path]++
		mu.Unlock()
		defer func() { mu.Lock(); active--; mu.Unlock() }()
		started <- struct{}{}
		select {
		case <-release:
			schedulerPackument(w, strings.TrimPrefix(req.URL.Path, "/"))
		case <-req.Context().Done():
		}
	}))
	defer server.Close()
	r := schedulerResolver(t, server.URL)
	deps := map[string]string{}
	wantOrder := []string{}
	for i := range 8 {
		name := fmt.Sprintf("package-%d", i)
		deps[name] = "*"
		wantOrder = append(wantOrder, name)
	}
	authored, err := json.Marshal(map[string]any{"dependencies": deps})
	if err != nil {
		t.Fatal(err)
	}
	projects := []lockfile.ImporterManifest{importer(t, ".", string(authored)), importer(t, "member", string(authored))}
	var order []string
	r.OnResolved = func(_ context.Context, p ResolvedPackage) error { order = append(order, p.Package.Name); return nil }
	ready := make(chan struct{})
	go func() {
		defer close(ready)
		for range 4 {
			select {
			case <-started:
			case <-ctx.Done():
				return
			}
		}
		close(release)
	}()
	graph, err := r.Resolve(ctx, projects, nil, nil)
	<-ready
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Packages) != 8 || !reflect.DeepEqual(order, wantOrder) {
		t.Fatal(graph.Packages, order)
	}
	mu.Lock()
	defer mu.Unlock()
	if peak != 4 || len(counts) != 8 {
		t.Fatal("network concurrency", peak, counts)
	}
	for name, count := range counts {
		if count != 1 {
			t.Fatal("duplicate prefetch", name, count)
		}
	}
}

func TestResolutionErrorCancelsOutstandingPrefetch(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	started, stopped := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/a-missing" {
			select {
			case <-started:
				http.NotFound(w, req)
			case <-req.Context().Done():
			}
			return
		}
		close(started)
		<-req.Context().Done()
		close(stopped)
	}))
	defer server.Close()
	r := schedulerResolver(t, server.URL)
	_, err := r.Resolve(ctx, []lockfile.ImporterManifest{importer(t, ".", `{"dependencies":{"a-missing":"*","z-slow":"*"}}`)}, nil, nil)
	var failure *RegistryFailure
	if !errors.As(err, &failure) || !strings.Contains(err.Error(), "a-missing") {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-ctx.Done():
		t.Fatal("speculative request survived resolver failure")
	}
}

func TestResolutionCancellationJoinsQueuedFetches(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		cancel()
		<-req.Context().Done()
	}))
	defer server.Close()
	r := schedulerResolver(t, server.URL)
	deps := map[string]string{}
	for i := range 32 {
		deps[fmt.Sprintf("p-%d", i)] = "*"
	}
	authored, err := json.Marshal(map[string]any{"dependencies": deps})
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Resolve(ctx, []lockfile.ImporterManifest{importer(t, ".", string(authored))}, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestExactPrefetchSupersedesFullFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		schedulerPackument(w, "p")
	}))
	defer server.Close()
	r := schedulerResolver(t, server.URL)
	d := newDriver(r, nil, nil, nil)
	d.fetcher = newFetchScheduler(ctx, 4)
	defer d.fetcher.close()
	d.fetchErrors[(metadataKey{name: "p"}).String()] = &RegistryFailure{"p", "earlier optional fetch failed"}
	d.ensureFetch(metadataKey{name: "p", exact: "1.0.0"})
	p, err := d.metadata(ctx, resolveTask{Name: "p", Range: "*"})
	if err != nil || p.Versions["1.0.0"] == nil {
		t.Fatal(p, err)
	}
	if _, compact := d.histories["p"]; compact {
		t.Fatal("full refresh retained compact marker")
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 2 {
		t.Fatal("expected exact response followed by fresh full response", requests)
	}
}
