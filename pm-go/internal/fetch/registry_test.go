package fetch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/manifest"
	"github.com/nubjs/nub/pm-go/internal/npmconfig"
	"github.com/nubjs/nub/pm-go/internal/processenv"
	"github.com/nubjs/nub/pm-go/internal/registry"
	"github.com/nubjs/nub/pm-go/internal/resolver"
	"github.com/nubjs/nub/pm-go/internal/store"
	"github.com/nubjs/nub/pm-go/internal/testregistry"
)

func registryFixture(t *testing.T, contents string) (*Fetcher, *lockfile.Package, *testregistry.Registry) {
	t.Helper()
	server := testregistry.Start(t, testregistry.Package{Name: "real", Version: "1.2.3", Files: map[string]string{"index.js": contents}})
	root, cache := t.TempDir(), t.TempDir()
	config := npmconfig.Resolve([]npmconfig.Entry{{Source: npmconfig.User, Key: "registry", Value: server.URL}}, nil)
	policy := registry.DefaultFetchPolicy()
	policy.Retries = 0
	client := registry.NewClient(config, registry.ClientOptions{Dir: root, Policy: policy})
	t.Cleanup(client.Close)
	pkg, err := manifest.ParsePackage([]byte(`{"dependencies":{"alias":"npm:real@1.2.3"}}`))
	if err != nil {
		t.Fatal(err)
	}
	r := resolver.New(client, processenv.Environment{Dir: root}, cache)
	graph, err := r.Resolve(t.Context(), []lockfile.ImporterManifest{{Path: ".", Package: pkg}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := store.New(filepath.Join(t.TempDir(), "v1", "files"), cache)
	t.Cleanup(func() { s.Close() })
	f := &Fetcher{Store: s, Client: client, MetadataCache: cache, Options: DefaultOptions()}
	f.Options.VerifyAllCacheFiles = true
	return f, graph.Packages["alias@1.2.3"], server
}

func assertContent(t *testing.T, result Result, want string) {
	t.Helper()
	data, err := os.ReadFile(result.Index["index.js"].Path)
	if err != nil || string(data) != want {
		t.Fatal(string(data), err)
	}
}

func TestResolvedRegistryFetchWarmOfflineAndCacheRepair(t *testing.T) {
	f, p, server := registryFixture(t, "module.exports = 42")
	result, err := f.Registry(t.Context(), p, true)
	if err != nil || result.Cached || result.ComputedIntegrity != nil {
		t.Fatal(result, err)
	}
	assertContent(t, result, "module.exports = 42")
	requests := len(server.Requests())
	f.Options.Network = registry.Offline
	warm, err := f.Registry(t.Context(), p, true)
	if err != nil || !warm.Cached || len(server.Requests()) != requests {
		t.Fatal(warm, err, server.Requests())
	}
	if err := os.WriteFile(result.Index["index.js"].Path, []byte("torn"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Registry(t.Context(), p, true); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatal(err)
	}
	f.Options.Network = registry.Normal
	repaired, err := f.Registry(t.Context(), p, true)
	if err != nil || repaired.Cached {
		t.Fatal(repaired, err)
	}
	assertContent(t, repaired, "module.exports = 42")
}

func TestUnpinnedFetchBindsConfiguredURLAcrossProjects(t *testing.T) {
	first, a, _ := registryFixture(t, "first registry")
	second, b, serverB := registryFixture(t, "second registry")
	second.Store = first.Store
	a.Integrity, b.Integrity = nil, nil
	resultA, err := first.Registry(t.Context(), a, true)
	if err != nil || resultA.ComputedIntegrity == nil {
		t.Fatal(resultA, err)
	}
	if sri, ok := first.Store.ReadBinding(first.Client.TarballURL("real", "1.2.3")); !ok || sri != *resultA.ComputedIntegrity {
		t.Fatal(sri, ok)
	}
	second.Options.Network = registry.Offline
	requests := len(serverB.Requests())
	if _, err := second.Registry(t.Context(), b, true); err == nil {
		t.Fatal("unrelated registry reused unpinned bytes")
	}
	if len(serverB.Requests()) != requests {
		t.Fatal("offline issued network request")
	}
	second.Options.Network = registry.Normal
	resultB, err := second.Registry(t.Context(), b, true)
	if err != nil || resultB.Cached {
		t.Fatal(resultB, err)
	}
	assertContent(t, resultB, "second registry")
	first.Options.Network = registry.Offline
	warm, err := first.Registry(t.Context(), a, true)
	if err != nil || !warm.Cached {
		t.Fatal(warm, err)
	}
	assertContent(t, warm, "first registry")
}

func TestRegistryFetchRejectsIntegrityAndContentBeforeCaching(t *testing.T) {
	for _, kind := range []string{"integrity", "strict", "content"} {
		t.Run(kind, func(t *testing.T) {
			f, p, _ := registryFixture(t, "package contents")
			switch kind {
			case "integrity":
				p.Integrity = new(store.Integrity([]byte("not the archive")))
			case "strict":
				p.Integrity = nil
				f.Options.StrictIntegrity = true
			case "content":
				p.Version = "9.0.0"
			}
			if _, err := f.Registry(t.Context(), p, false); err == nil {
				t.Fatal("invalid content accepted")
			}
			if _, ok := f.Store.LoadIndex(p.RegistryName(), p.Version, p.Integrity, true); ok {
				t.Fatal("failed fetch published index")
			}
			if _, ok := f.Store.ReadBinding(f.Client.TarballURL(p.RegistryName(), p.Version)); ok {
				t.Fatal("failed fetch published binding")
			}
		})
	}
}

func TestFrozenURLChecksAndRegistryChangeWithIntegrity(t *testing.T) {
	f, p, original := registryFixture(t, "verified original")
	other, _, _ := registryFixture(t, "other registry")
	p.TarballURL = new(original.URL + "/tampered?token=secret")
	requests := len(original.Requests())
	_, err := f.Registry(t.Context(), p, true)
	var mismatch *URLMismatch
	if !errors.As(err, &mismatch) || strings.Contains(err.Error(), "secret") {
		t.Fatal(err)
	}
	if len(original.Requests()) != requests+1 {
		t.Fatal("URL mismatch fetched the archive", original.Requests())
	}
	p.TarballURL = new(original.URL + "/tarballs/real-1.2.3.tgz")
	other.Store = f.Store
	without := p.Clone()
	without.Integrity = nil
	requests = len(original.Requests())
	_, err = other.Registry(t.Context(), without, true)
	if !errors.As(err, &mismatch) || len(original.Requests()) != requests {
		t.Fatal("host mismatch without SRI fetched bytes", err)
	}
	result, err := other.Registry(t.Context(), p, true)
	if err != nil {
		t.Fatal(err)
	}
	assertContent(t, result, "verified original")
}

func TestRegistryFetchConcurrentAliasesAndCancellation(t *testing.T) {
	f, p, server := registryFixture(t, "shared bytes")
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			alias := p.Clone()
			alias.Name = "another-alias"
			alias.DepPath = alias.Name + "@1.2.3"
			if i%2 == 0 {
				alias = p
			}
			if _, err := f.Registry(t.Context(), alias, false); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	tarballs := 0
	for _, request := range server.Requests() {
		if strings.Contains(request, ".tgz") {
			tarballs++
		}
	}
	if tarballs != 1 {
		t.Fatal("concurrent aliases did not share content-keyed fetch", tarballs)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f.Registry(ctx, p, false); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
