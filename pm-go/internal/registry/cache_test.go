package registry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nubjs/nub/pm-go/internal/npmconfig"
)

const metadataBody = `{"name":"pkg","versions":{"1.0.0":{"name":"pkg","version":"1.0.0"}},"dist-tags":{"latest":"1.0.0"}}`

func TestMetadataFreshOfflineAndRevalidation(t *testing.T) {
	var hits atomic.Int32
	var conditionals atomic.Bool
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("If-None-Match") == `"v1"` && r.Header.Get("If-Modified-Since") == "Tue, 01 Jan 2030 00:00:00 GMT" {
			conditionals.Store(true)
			w.Header().Set("Cache-Control", "max-age=60")
			w.WriteHeader(304)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Last-Modified", "Tue, 01 Jan 2030 00:00:00 GMT")
		w.Header().Set("Cache-Control", "max-age=10")
		fmt.Fprint(w, metadataBody)
	}))
	defer s.Close()
	c := testClient(t, []npmconfig.Entry{{Source: npmconfig.User, Key: "registry", Value: s.URL}})
	now := time.Unix(1000, 0)
	c.options.Now = func() time.Time { return now }
	dir := t.TempDir()
	get := func(mode NetworkMode) {
		t.Helper()
		p, err := c.Metadata(context.Background(), "pkg", dir, mode, false)
		if err != nil {
			t.Fatal(err)
		}
		if p.Tags["latest"] != "1.0.0" {
			t.Fatal(p)
		}
	}
	get(Normal)
	get(Normal)
	if hits.Load() != 1 {
		t.Fatal("fresh cache refetched", hits.Load())
	}
	now = now.Add(10 * time.Second)
	get(Offline)
	get(PreferOffline)
	if hits.Load() != 1 {
		t.Fatal("offline modes accessed network")
	}
	get(Normal)
	if hits.Load() != 2 || !conditionals.Load() {
		t.Fatal("missing revalidation", hits.Load())
	}
	now = now.Add(20 * time.Second)
	get(Normal)
	if hits.Load() != 2 {
		t.Fatal("304 did not extend freshness")
	}
	_, err := c.Metadata(context.Background(), "missing", dir, Offline, false)
	var miss *OfflineMiss
	if !errors.As(err, &miss) || hits.Load() != 2 {
		t.Fatal(err, hits.Load())
	}
}

func TestMetadataCorruptionIsolationAndFailureAtomicity(t *testing.T) {
	var fail atomic.Bool
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if fail.Load() {
			fmt.Fprint(w, `{"bad":`)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		fmt.Fprint(w, metadataBody)
	}))
	defer s.Close()
	c := testClient(t, []npmconfig.Entry{{Source: npmconfig.User, Key: "registry", Value: s.URL}})
	dir := t.TempDir()
	path, _ := MetadataCachePath(dir, "pkg", c.Config.Registry, false)
	if _, err := c.Metadata(context.Background(), "pkg", dir, Normal, false); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fail.Store(true)
	if _, err := c.Metadata(context.Background(), "pkg", dir, Normal, false); err == nil {
		t.Fatal("invalid metadata accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("failed request overwrote cache", err)
	}
	if err := os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	oldHits := hits.Load()
	if _, err := c.Metadata(context.Background(), "pkg", dir, Offline, false); err == nil || hits.Load() != oldHits {
		t.Fatal("corrupt offline cache used", err)
	}
	fail.Store(false)
	if _, err := c.Metadata(context.Background(), "pkg", dir, PreferOffline, false); err != nil {
		t.Fatal(err)
	}
	other := testClient(t, []npmconfig.Entry{{Source: npmconfig.User, Key: "registry", Value: s.URL + "/other"}})
	if _, err := other.Metadata(context.Background(), "pkg", dir, Offline, false); err == nil {
		t.Fatal("metadata crossed registry namespace")
	}
	if _, err := c.Metadata(context.Background(), "pkg", dir, Offline, true); err == nil {
		t.Fatal("abbreviated cache answered full request")
	}
}

func TestMetadataScopedURLAndConcurrentReads(t *testing.T) {
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.EscapedPath() != "/@scope%2Fpkg" {
			t.Error("scope path not encoded", r.URL.EscapedPath())
		}
		fmt.Fprint(w, metadataBody)
	}))
	defer s.Close()
	c := testClient(t, []npmconfig.Entry{{Source: npmconfig.User, Key: "@scope:registry", Value: s.URL}})
	dir := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Go(func() {
			p, err := c.Metadata(context.Background(), "@scope/pkg", dir, Normal, false)
			if err != nil {
				t.Error(err)
				return
			}
			p.Tags["latest"] = "mutated-local-result"
		})
	}
	wg.Wait()
	if hits.Load() != 1 {
		t.Fatal("concurrent metadata fetched repeatedly", hits.Load())
	}
	p, err := c.Metadata(context.Background(), "@scope/pkg", dir, Offline, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Tags["latest"] != "1.0.0" {
		t.Fatal("callers shared mutable metadata", p)
	}
}

func TestMetadataRetriesDecodeFailures(t *testing.T) {
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			fmt.Fprint(w, `{"name":`)
		} else {
			fmt.Fprint(w, metadataBody)
		}
	}))
	defer s.Close()
	c := testClient(t, []npmconfig.Entry{{Source: npmconfig.User, Key: "registry", Value: s.URL}})
	if _, err := c.Metadata(context.Background(), "pkg", t.TempDir(), Normal, false); err != nil || hits.Load() != 2 {
		t.Fatal(err, hits.Load())
	}
}

func TestCacheControlAndNameValidation(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want uint64
		set  bool
	}{{"", 0, false}, {"max-age=10", 10, true}, {"max-age=10, s-maxage=20", 20, true}, {"s-maxage=20, max-age=10", 20, true}, {"MAX-AGE=30, private", 0, true}, {"no-store", 0, true}, {"max-age=30, max-age=bad", 0, false}, {`max-age="10"`, 0, false}} {
		got := CacheMaxAge(tc.raw)
		if (got != nil) != tc.set || got != nil && *got != tc.want {
			t.Fatal(tc, got)
		}
	}
	for _, name := range []string{"JSONStream", ".old", "_legacy", "@Scope/pkg", "a.b-c_1"} {
		if !ValidName(name) {
			t.Fatal(name)
		}
	}
	for _, name := range []string{"", ".", "..", "../escape", "@scope/../../escape", "@/pkg", "@scope/", "pkg\\other", "pkg%2Fother", strings.Repeat("a", 215)} {
		if _, err := MetadataCachePath(t.TempDir(), name, npmconfig.DefaultRegistry, false); err == nil {
			t.Fatal(name)
		}
	}
}
