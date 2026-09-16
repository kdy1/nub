package registry

import (
	"context"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nubjs/nub/pm-go/internal/npmconfig"
)

func testClient(t *testing.T, entries []npmconfig.Entry) *Client {
	t.Helper()
	p := DefaultFetchPolicy()
	p.RetryMin = time.Millisecond
	p.RetryMax = 5 * time.Millisecond
	p.Timeout = 3 * time.Second
	p.StallTimeout = time.Second
	c := NewClient(npmconfig.Resolve(entries, nil), ClientOptions{Dir: t.TempDir(), Env: []string{}, Policy: p, UserAgent: "test-client"})
	t.Cleanup(c.Close)
	return c
}

func TestTransportAuthScopeAndRedirectChain(t *testing.T) {
	var leaked atomic.Bool
	var visited atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		visited.Add(1)
		if r.Header.Get("Authorization") != "" {
			leaked.Store(true)
		}
		if r.URL.Path == "/first" {
			http.Redirect(w, r, "/second", http.StatusFound)
			return
		}
		fmt.Fprint(w, "ok")
	}))
	defer foreign.Close()
	var own *httptest.Server
	own = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer scoped-token" {
			t.Error("missing scope credential", r.Header.Get("Authorization"))
		}
		if r.Header.Get("User-Agent") != "test-client" {
			t.Error("missing user agent")
		}
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/next", http.StatusFound)
		} else {
			http.Redirect(w, r, foreign.URL+"/first", http.StatusFound)
		}
	}))
	defer own.Close()
	key := npmconfig.URIKey(own.URL + "/")
	c := testClient(t, []npmconfig.Entry{{Source: npmconfig.User, Key: key + ":_authToken", Value: "default-token"}, {Source: npmconfig.User, Key: key + ":@org:_authToken", Value: "scoped-token"}})
	r, err := c.Do(context.Background(), Request{URL: own.URL + "/start", Registry: own.URL + "/", Package: "@org/pkg", MaxBytes: 100})
	if err != nil || r.Status != 200 || string(r.Body) != "ok" || visited.Load() != 2 || leaked.Load() {
		t.Fatal(r, err, "visited", visited.Load(), "leaked", leaked.Load())
	}
}

func TestTransportTLSRootsAndDowngrade(t *testing.T) {
	var contacted atomic.Bool
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { contacted.Store(true) }))
	defer plain.Close()
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, plain.URL, http.StatusFound) }))
	defer tlsServer.Close()
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: tlsServer.Certificate().Raw})
	c := testClient(t, []npmconfig.Entry{{Source: npmconfig.User, Key: "ca", Value: string(cert)}})
	r, err := c.Do(context.Background(), Request{URL: tlsServer.URL, Registry: tlsServer.URL, MaxBytes: 1024})
	if err != nil || r.Status != 302 || contacted.Load() {
		t.Fatal(r, err, "downgrade contacted", contacted.Load())
	}
	untrusted := testClient(t, nil)
	if _, err := untrusted.Do(context.Background(), Request{URL: tlsServer.URL, Registry: tlsServer.URL}); err == nil {
		t.Fatal("untrusted certificate accepted")
	}
	// NODE_EXTRA_CA_CERTS is injected, not read from process environment.
	path := filepath.Join(t.TempDir(), "root.pem")
	if err := os.WriteFile(path, cert, 0600); err != nil {
		t.Fatal(err)
	}
	extras := testClient(t, nil)
	extras.options.Env = []string{"NODE_EXTRA_CA_CERTS=" + path}
	if _, err := extras.Do(context.Background(), Request{URL: tlsServer.URL, Registry: tlsServer.URL}); err != nil {
		t.Fatal(err)
	}
}

func TestTransportRetryAndBodyLimits(t *testing.T) {
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if r.URL.Path == "/retry" && n < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(503)
			return
		}
		if r.URL.Path == "/deny" {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/mutate" {
			w.WriteHeader(503)
			return
		}
		if r.URL.Path == "/chunk" {
			w.(http.Flusher).Flush()
			fmt.Fprint(w, strings.Repeat("x", 100))
			return
		}
		w.Header().Set("Content-Length", "100")
		fmt.Fprint(w, strings.Repeat("x", 100))
	}))
	defer s.Close()
	c := testClient(t, nil)
	r, err := c.Do(context.Background(), Request{URL: s.URL + "/retry", Registry: s.URL, Retry: true, MaxBytes: 100})
	if err != nil || r.Status != 200 || hits.Load() != 3 || len(r.Body) != 100 {
		t.Fatal(r, err, hits.Load())
	}
	for _, path := range []string{"/chunk", "/length"} {
		hits.Store(0)
		_, err := c.Do(context.Background(), Request{URL: s.URL + path, Registry: s.URL, Retry: true, MaxBytes: 50})
		var tooLarge *BodyTooLarge
		if !errors.As(err, &tooLarge) || hits.Load() != 1 {
			t.Fatal(path, err, hits.Load())
		}
	}
	for _, path := range []string{"/deny", "/mutate"} {
		hits.Store(0)
		_, err := c.Do(context.Background(), Request{Method: "POST", URL: s.URL + path, Registry: s.URL, Retry: path == "/deny"})
		if err != nil || hits.Load() != 1 {
			t.Fatal(path, err, hits.Load())
		}
	}
}

func TestTransportStalledBodyAndCancellation(t *testing.T) {
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer s.Close()
	c := testClient(t, nil)
	c.options.Policy.StallTimeout = 100 * time.Millisecond
	c.options.Policy.Retries = 8
	_, err := c.Do(context.Background(), Request{URL: s.URL, Registry: s.URL, Retry: true})
	if !errors.Is(err, errStalled) || hits.Load() != 2 {
		t.Fatal("timeout retry budget", err, hits.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.Do(ctx, Request{URL: s.URL, Registry: s.URL, Retry: true})
	if !errors.Is(err, context.Canceled) || hits.Load() != 2 {
		t.Fatal("cancelled request retried", err, hits.Load())
	}
}

func TestTransportConcurrentRequests(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") }))
	defer s.Close()
	c := testClient(t, nil)
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Go(func() {
			r, err := c.Do(context.Background(), Request{URL: s.URL, Registry: s.URL})
			if err != nil || string(r.Body) != "ok" {
				t.Error(r, err)
			}
		})
	}
	wg.Wait()
}

func TestRetryAfterAndBackoff(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		seconds int
		valid   bool
	}{{"0", 0, true}, {" 5 ", 5, true}, {"999999", 60, true}, {" ", 0, false}, {"-1", 0, false}, {"Tue, 01 Jan 2030 00:00:00 GMT", 0, false}, {"18446744073709551616", 0, false}} {
		d, ok := RetryAfter(tc.raw)
		if ok != tc.valid || int(d/time.Second) != tc.seconds {
			t.Fatal(tc, d, ok)
		}
	}
	p := DefaultFetchPolicy()
	for _, attempt := range []uint32{0, 1, 2, 3, 1000000} {
		want := time.Minute
		if attempt <= 1 {
			want = 10 * time.Second
		}
		if d := p.Backoff(attempt); d != want {
			t.Fatal(attempt, d)
		}
	}
	p.RetryFactor = 1
	if d := p.Backoff(4); d != 10*time.Second {
		t.Fatal(d)
	}
}

func TestProxyConfigurationIsIsolated(t *testing.T) {
	for _, tc := range []struct {
		target, rules string
		want          bool
	}{
		{"https://api.example.test", "example.test", true}, {"https://notexample.test", "example.test", false}, {"http://127.0.0.2", "127.0.0.0/8", true}, {"https://example.test", "example.test:443", true}, {"http://example.test", "example.test:443", false}, {"http://anywhere", "*", true},
	} {
		u, _ := url.Parse(tc.target)
		if got := bypassProxy(u, tc.rules); got != tc.want {
			t.Fatal(tc, got)
		}
	}
	var hits atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); fmt.Fprint(w, r.URL.Host) }))
	defer proxy.Close()
	c := testClient(t, []npmconfig.Entry{{Source: npmconfig.User, Key: "http-proxy", Value: proxy.URL}})
	r, err := c.Do(context.Background(), Request{URL: "http://registry.invalid/path", Registry: "http://registry.invalid/"})
	if err != nil || string(r.Body) != "registry.invalid" || hits.Load() != 1 {
		t.Fatal(r, err, hits.Load())
	}
}
