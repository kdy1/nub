package registry

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/npmconfig"
)

func TestExactPackumentRetainsOnlySelectedDependencyMetadata(t *testing.T) {
	data := []byte(`{"name":"p","dist-tags":{"latest":"2.0.0"},"versions":{"1.0.0":{"name":42,"dependencies":false,"approver":["reviewer"],"_npmUser":{"trustedPublisher":{"id":"ci"}},"dist":{"tarball":"unused","attestations":{"provenance":{"predicateType":"https://slsa.dev/provenance/v1"}}}},"2.0.0":{"name":"p","version":"2.0.0","dependencies":{"child":"^1"},"dist":{"tarball":"https://example.invalid/p.tgz"}}},"time":{"1.0.0":"2020-01-01","2.0.0":"2020-02-01","invalid":32}}`)
	p, err := ParseExactPackument(data, "2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if p.Metadata.Version != "2.0.0" || p.Metadata.Dependencies["child"] != "^1" || len(p.History) != 1 || len(p.Time) != 2 {
		t.Fatal(p)
	}
	h := p.History["1.0.0"]
	if h.Raw.Get("dependencies") != nil || h.Raw.Get("name") != nil || h.Raw.Get("approver") == nil || h.Dist.Tarball != "" || h.Dist.Attestations == nil {
		t.Fatal(h)
	}
	if _, err := Parse(data); err == nil {
		t.Fatal("full metadata unexpectedly accepted malformed historical identity")
	}
	for _, invalid := range []string{`{}`, `{"versions":null}`, `{"versions":{"2.0.0":{}}}`, `{"versions":{"2.0.0":{"name":"p","version":"2.0.0"},"1.0.0":{"dist":false}}}`, string(data) + `{}`} {
		if _, err := ParseExactPackument([]byte(invalid), "2.0.0"); err == nil {
			t.Fatal(invalid)
		}
	}
}

func TestExactMetadataUsesScopedAuthAndOfflineGate(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer test-token" || !strings.HasPrefix(r.Header.Get("Accept"), "application/json;") {
			t.Error(r.Header)
		}
		w.Write([]byte(`{"versions":{"1.0.0":{"name":"@scope/p","version":"1.0.0"}},"time":null}`))
	}))
	defer server.Close()
	config := npmconfig.Resolve([]npmconfig.Entry{{Source: npmconfig.User, Key: npmconfig.URIKey(server.URL) + ":_authToken", Value: "test-token"}}, nil)
	policy := DefaultFetchPolicy()
	policy.Retries = 0
	client := NewClient(config, ClientOptions{Dir: t.TempDir(), Env: []string{}, Policy: policy})
	defer client.Close()
	p, err := client.ExactMetadataAt(t.Context(), "@scope/p", "1.0.0", server.URL, Normal)
	if err != nil || p.Metadata.Name != "@scope/p" {
		t.Fatal(p, err)
	}
	if _, err := client.ExactMetadataAt(t.Context(), "@scope/p", "1.0.0", server.URL, Offline); err == nil || requests.Load() != 1 {
		t.Fatal(err, requests.Load())
	}
}

func TestFreshMetadataBypassesValidatorsAndPreservesOfflineCache(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		if r.Header.Get("If-None-Match") != "" {
			t.Error("forced fetch reused an entity validator")
		}
		w.Header().Set("ETag", "same")
		w.Header().Set("Cache-Control", "max-age=86400")
		if n == 1 {
			w.Write([]byte(`{"name":"p","versions":{"1.0.0":{"name":"p","version":"1.0.0"}}}`))
		} else {
			w.Write([]byte(`{"name":"p","versions":{"2.0.0":{"name":"p","version":"2.0.0"}}}`))
		}
	}))
	defer server.Close()
	policy := DefaultFetchPolicy()
	policy.Retries = 0
	client := NewClient(npmconfig.Resolve(nil, nil), ClientOptions{Dir: t.TempDir(), Env: []string{}, Policy: policy})
	defer client.Close()
	cache := t.TempDir()
	if _, err := client.MetadataAt(t.Context(), "p", server.URL, cache, Normal, true); err != nil {
		t.Fatal(err)
	}
	p, err := client.RefreshMetadataAt(t.Context(), "p", server.URL, cache, Normal, true)
	if err != nil || p.Versions["2.0.0"] == nil || requests.Load() != 2 {
		t.Fatal(p, err, requests.Load())
	}
	if _, err := client.RefreshMetadataAt(t.Context(), "p", server.URL, cache, Offline, true); err == nil || requests.Load() != 2 {
		t.Fatal(err, requests.Load())
	}
	if p, err := client.MetadataAt(t.Context(), "p", server.URL, cache, Offline, true); err != nil || p.Versions["2.0.0"] == nil {
		t.Fatal("failed refresh erased cache", p, err)
	}
}
