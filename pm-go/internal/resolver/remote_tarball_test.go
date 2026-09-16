package resolver

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/npmconfig"
	"github.com/nubjs/nub/pm-go/internal/registry"
	"github.com/nubjs/nub/pm-go/internal/store"
)

func TestRemoteTarballMetadataIntegrityAndOffline(t *testing.T) {
	data := resolverTarball(t, resolverTarEntry{"owner-repo-sha/package.json", `{"name":"actual","version":"3.0.0","dependencies":{"child":"^1"},"optionalDependencies":{"other":"*"}}`})
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		if r.Header.Get("Authorization") != "Bearer scoped-token" {
			t.Error("missing scoped credential")
		}
		w.Write(data)
	}))
	defer server.Close()
	policy := registry.DefaultFetchPolicy()
	policy.Retries = 0
	config := npmconfig.Resolve([]npmconfig.Entry{{Source: npmconfig.User, Key: npmconfig.URIKey(server.URL+"/packages/") + ":_authToken", Value: "scoped-token"}}, nil)
	client := registry.NewClient(config, registry.ClientOptions{Dir: t.TempDir(), Env: []string{}, Policy: policy})
	defer client.Close()
	source := lockfile.Source{Kind: lockfile.RemoteTarball, URL: server.URL + "/packages/archive", GitHosted: true}
	resolved, meta, err := ResolveRemoteTarball(t.Context(), "alias", source, client, registry.Normal)
	if err != nil || resolved.URL != source.URL || !resolved.GitHosted || resolved.Integrity == nil || !reflect.DeepEqual(meta, LocalManifest{"alias", "3.0.0", map[string]string{"child": "^1"}}) {
		t.Fatal(resolved, meta, err)
	}
	if err := store.Verify(data, *resolved.Integrity); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ResolveRemoteTarball(t.Context(), "alias", source, client, registry.Offline); err == nil || count.Load() != 1 {
		t.Fatal("offline request", err, count.Load())
	}
}
func TestRemoteTarballErrorsRedactCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("invalid gzip")) }))
	defer server.Close()
	policy := registry.DefaultFetchPolicy()
	policy.Retries = 0
	client := registry.NewClient(npmconfig.Resolve(nil, nil), registry.ClientOptions{Dir: t.TempDir(), Env: []string{}, Policy: policy})
	defer client.Close()
	source := lockfile.Source{Kind: lockfile.RemoteTarball, URL: server.URL + "/pkg?token=private-secret"}
	_, _, err := ResolveRemoteTarball(t.Context(), "alias", source, client, registry.Normal)
	if err == nil || strings.Contains(err.Error(), "private-secret") || !strings.Contains(err.Error(), "token=***") {
		t.Fatal(err)
	}
	server.Close()
	_, _, err = ResolveRemoteTarball(t.Context(), "alias", source, client, registry.Normal)
	if err == nil || strings.Contains(err.Error(), "private-secret") {
		t.Fatal("network error exposed URL credentials", err)
	}
}
