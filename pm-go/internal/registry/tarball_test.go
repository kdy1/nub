package registry

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/npmconfig"
)

func TestTarballAuthEncodingAndRetry(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept-Encoding") != "identity" {
			t.Error("tarball HTTP encoding", r.Header)
		}
		if r.Header.Get("Authorization") != "Bearer private" {
			t.Error("path auth", r.Header)
		}
		if attempts.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		w.Write([]byte{0x1f, 0x8b, 0, 1})
	}))
	defer server.Close()
	client := testClient(t, []npmconfig.Entry{{Source: npmconfig.User, Key: npmconfig.URIKey(server.URL+"/packages/") + ":_authToken", Value: "private"}})
	body, err := client.Tarball(t.Context(), server.URL+"/packages/pkg.tgz", Normal)
	if err != nil || !bytes.Equal(body, []byte{0x1f, 0x8b, 0, 1}) || attempts.Load() != 2 {
		t.Fatal(body, err, attempts.Load())
	}
	if _, err := client.Tarball(t.Context(), server.URL+"/packages/pkg.tgz", Offline); err == nil || attempts.Load() != 2 {
		t.Fatal("offline made a request", err)
	}
	for _, target := range []string{"file:///etc/passwd", "ssh://example.com/pkg", "ftp://example.com/a", "/relative", ":bad"} {
		if _, err := client.Tarball(t.Context(), target, Normal); err == nil {
			t.Fatal("unsafe tarball URL", target)
		}
	}
}

func TestTarballForeignHostAndLimits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("foreign registry token leaked")
		}
		w.Write([]byte("too much data"))
	}))
	defer server.Close()
	client := testClient(t, []npmconfig.Entry{{Source: npmconfig.User, Key: "registry", Value: "https://private.example/"}, {Source: npmconfig.User, Key: "_authToken", Value: "secret"}})
	client.options.Policy.TarballMaxBytes = 4
	if _, err := client.Tarball(t.Context(), server.URL, Normal); err == nil {
		t.Fatal("tarball cap ignored")
	}
}
