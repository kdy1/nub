// Package fetch imports resolved package contents into the invocation's store.
package fetch

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"

	"github.com/nubjs/nub/pm-go/internal/filelock"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/registry"
	"github.com/nubjs/nub/pm-go/internal/store"
)

type Options struct {
	Network             registry.NetworkMode
	VerifyIntegrity     bool
	StrictIntegrity     bool
	CheckPackageContent bool
	VerifyAllCacheFiles bool
	ArchiveLimits       store.ArchiveLimits
}

func DefaultOptions() Options {
	return Options{VerifyIntegrity: true, CheckPackageContent: true, ArchiveLimits: store.DefaultArchiveLimits()}
}

type Fetcher struct {
	Store         *store.Store
	Client        *registry.Client
	MetadataCache string
	Options       Options
	// Warn may be called concurrently when callers fetch packages in parallel.
	Warn func(code, message string)
}

type Result struct {
	Index             store.PackageIndex
	Cached            bool
	ComputedIntegrity *string
}

func (f *Fetcher) warn(code, message string) {
	if f.Warn != nil {
		f.Warn(code, message)
	}
}

// Registry fetches a registry-backed graph entry. locked enables the frozen
// lockfile URL check; a fresh resolver already obtained that URL from metadata.
// Alias and peer variants share a content-keyed store index under the real name.
func (f *Fetcher) Registry(ctx context.Context, p *lockfile.Package, locked bool) (Result, error) {
	if p == nil || p.Source != nil {
		return Result{}, fmt.Errorf("registry fetch requires a registry package")
	}
	if f.Store == nil || f.Client == nil {
		return Result{}, fmt.Errorf("registry fetch requires a store and registry client")
	}
	if err := f.Store.EnsureLease(ctx); err != nil {
		return Result{}, err
	}
	name, version := p.RegistryName(), p.Version
	if !registry.ValidName(name) || !store.ValidVersion(version) {
		return Result{}, fmt.Errorf("invalid package coordinate %q@%q", name, version)
	}
	configuredURL := f.Client.TarballURL(name, version)
	leaseKey := configuredURL
	if p.Integrity != nil {
		leaseKey += "\x00" + *p.Integrity
	}
	lease, err := filelock.Acquire(ctx, filepath.Join(f.Store.VersionDir(), ".fetch-locks", store.Hash([]byte(leaseKey))+".lock"), false)
	if err != nil {
		return Result{}, err
	}
	defer lease.Close()
	readKey := p.Integrity
	if readKey == nil {
		if sri, ok := f.Store.ReadBinding(configuredURL); ok {
			readKey = &sri
		}
	}
	if readKey != nil {
		if index, ok := f.Store.LoadIndex(name, version, readKey, f.Options.VerifyAllCacheFiles); ok {
			return Result{Index: index, Cached: true}, nil
		}
	}
	url := configuredURL
	if p.TarballURL != nil {
		url = *p.TarballURL
		if locked {
			if registry.SameRegistryHost(url, f.Client.Config.RegistryFor(name)) {
				if err := f.verifyLockedURL(ctx, name, version, url); err != nil {
					return Result{}, err
				}
			} else if p.Integrity == nil {
				return Result{}, &URLMismatch{fmt.Sprintf("%s@%s: lockfile pins tarball %s on a host that differs from its configured registry but records no integrity hash; refusing to fetch an unverifiable tarball (run `nub-pm-go install --no-frozen-lockfile` to refresh the lockfile)", name, version, registry.RedactURL(url))}
			}
		}
	}
	data, err := f.Client.Tarball(ctx, url, f.Options.Network)
	if err != nil {
		return Result{}, fmt.Errorf("failed to fetch %s@%s: %w", p.Name, version, err)
	}
	if f.Options.VerifyIntegrity {
		if p.Integrity != nil {
			if err := store.Verify(data, *p.Integrity); err != nil {
				return Result{}, fmt.Errorf("%s@%s: %w", p.Name, version, err)
			}
		} else if f.Options.StrictIntegrity {
			return Result{}, fmt.Errorf("%s@%s: registry response has no `dist.integrity` and `strict-store-integrity` is on. Refusing to import unverified bytes.", p.Name, version)
		} else {
			f.warn("WARN_AUBE_MISSING_INTEGRITY", fmt.Sprintf("%s@%s: registry response has no `dist.integrity`, importing without content verification. Set `strict-store-integrity=true` to refuse instead.", p.Name, version))
		}
	}
	index, err := f.Store.ImportTarball(ctx, bytes.NewReader(data), f.Options.ArchiveLimits)
	if err != nil {
		return Result{}, fmt.Errorf("failed to import %s@%s: %w", p.Name, version, err)
	}
	if f.Options.CheckPackageContent {
		if err := store.ValidateIndexContent(index, name, version); err != nil {
			return Result{}, fmt.Errorf("%s@%s: %w", p.Name, version, err)
		}
	}
	result := Result{Index: index}
	writeKey := p.Integrity
	if writeKey == nil {
		result.ComputedIntegrity = new(store.Integrity(data))
		writeKey = result.ComputedIntegrity
	}
	if err := f.Store.SaveIndex(ctx, name, version, writeKey, index); err != nil {
		f.warn("WARN_AUBE_CACHE_WRITE_FAILED", fmt.Sprintf("Failed to cache index for %s@%s: %v", p.Name, version, err))
	}
	if result.ComputedIntegrity != nil {
		// A failed binding write only loses the next warm lookup. It cannot
		// authorize reuse of a content-free coordinate in the shared store.
		_ = f.Store.SaveBinding(ctx, configuredURL, *result.ComputedIntegrity)
	}
	return result, nil
}

type URLMismatch struct{ Message string }

func (e *URLMismatch) Error() string { return e.Message }
func (e *URLMismatch) Code() string  { return "ERR_AUBE_TARBALL_URL_MISMATCH" }

func (f *Fetcher) verifyLockedURL(ctx context.Context, name, version, locked string) error {
	p, err := f.Client.Metadata(ctx, name, f.MetadataCache, f.Options.Network, false)
	if err != nil {
		return &URLMismatch{fmt.Sprintf("%s@%s: failed to fetch registry metadata to verify lockfile tarball URL: %v", name, version, err)}
	}
	meta := p.Versions[version]
	if meta == nil {
		return &URLMismatch{fmt.Sprintf("%s@%s: registry metadata did not include this version while lockfile pinned %s", name, version, registry.RedactURL(locked))}
	}
	if meta.Dist == nil {
		return &URLMismatch{fmt.Sprintf("%s@%s: registry metadata did not include dist.tarball while lockfile pinned %s", name, version, registry.RedactURL(locked))}
	}
	if !registry.LockfileURLMatches(locked, meta.Dist.Tarball) {
		return &URLMismatch{fmt.Sprintf("%s@%s: lockfile tarball URL %s does not match registry metadata %s", name, version, registry.RedactURL(locked), registry.RedactURL(meta.Dist.Tarball))}
	}
	return nil
}
