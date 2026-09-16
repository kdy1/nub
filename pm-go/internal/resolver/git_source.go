package resolver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nubjs/nub/pm-go/internal/gitcache"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/registry"
	"github.com/nubjs/nub/pm-go/internal/store"
)

func hostedGitSource(original lockfile.Source, resolved string, integrity *string, archive string) lockfile.Source {
	if original.Subpath == nil && archive != "" {
		if integrity == nil {
			integrity = new("")
		}
		return lockfile.Source{Kind: lockfile.RemoteTarball, URL: archive, Integrity: integrity, GitHosted: true}
	}
	return lockfile.Source{Kind: lockfile.Git, URL: original.URL, Committish: original.Committish, Resolved: resolved, Subpath: original.Subpath, Integrity: integrity}
}

func readGitManifest(name, tree, location string, subpath *string) (LocalManifest, error) {
	where, local := "", "."
	if subpath != nil {
		where, local = " at /"+*subpath, filepath.FromSlash(*subpath)
		if !filepath.IsLocal(local) {
			return LocalManifest{}, &RegistryFailure{name, "git package subpath escapes checkout"}
		}
	}
	fail := func(action string, err error) (LocalManifest, error) {
		return LocalManifest{}, &RegistryFailure{name, fmt.Sprintf("%s in %s%s: %s", action, location, where, err)}
	}
	root, err := os.OpenRoot(tree)
	if err != nil {
		return fail("stat git package root", err)
	}
	defer root.Close()
	info, err := root.Stat(local)
	if err != nil {
		return fail("stat git package root", err)
	}
	if !info.IsDir() {
		return LocalManifest{}, &RegistryFailure{name, fmt.Sprintf("git package root in %s%s is not a directory: %s", location, where, filepath.Join(tree, local))}
	}
	data, err := root.ReadFile(filepath.Join(local, "package.json"))
	if os.IsNotExist(err) {
		return LocalManifest{Name: name, Version: "0.0.0", Dependencies: map[string]string{}}, nil
	}
	if err != nil {
		return fail("read package.json", err)
	}
	meta, err := parseLocalManifest(data)
	if err != nil {
		return fail("parse package.json", err)
	}
	meta.Name = name
	return meta, nil
}

// ResolveGitSource pins a Git ref, reads the package metadata and returns its
// lockfile identity. Hosted HTTPS archives are preferred; download/extraction
// failures fall back to Git. A mismatched pinned integrity is a hard error.
// The third return value is the SHA-512 of the actual hosted archive bytes.
func ResolveGitSource(ctx context.Context, name string, source lockfile.Source, cache *gitcache.Cache, client TarballFetcher, mode registry.NetworkMode, shallow bool) (lockfile.Source, LocalManifest, *string, error) {
	fail := func(err error) (lockfile.Source, LocalManifest, *string, error) {
		if _, ok := err.(*RegistryFailure); !ok {
			err = &RegistryFailure{name, err.Error()}
		}
		return lockfile.Source{}, LocalManifest{}, nil, err
	}
	if source.Kind != lockfile.Git {
		return fail(fmt.Errorf("resolve_git_source called on non-git source"))
	}
	runtimeURL := source.URL
	hosted, isHosted := lockfile.ParseHostedGit(source.URL)
	if isHosted {
		runtimeURL = hosted.HTTPSURL()
	}
	// Offline resolution cannot discover a moving branch or tag.
	if mode == registry.Offline {
		if source.Committish == nil {
			return fail(fmt.Errorf("offline: git dependency requires a pinned commit"))
		}
		if !gitcache.IsFullCommit(*source.Committish) {
			return fail(fmt.Errorf("offline: git dependency requires a pinned commit"))
		}
	}
	resolved, err := cache.ResolveRef(ctx, runtimeURL, source.Committish)
	if err != nil {
		return fail(err)
	}
	archive := ""
	if isHosted {
		archive, _ = hosted.TarballURL(resolved)
	}
	if archive != "" && source.Integrity != nil {
		if tree, _, actual, ok := cache.LookupCodeload(source.URL, resolved, source.Integrity); ok {
			meta, err := readGitManifest(name, tree, "cached codeload extract", source.Subpath)
			if err != nil {
				return fail(err)
			}
			return hostedGitSource(source, resolved, source.Integrity, archive), meta, actual, nil
		}
	}
	if archive != "" && client != nil {
		if data, err := client.Tarball(ctx, archive, mode); err == nil {
			pin := source.Integrity
			if pin != nil {
				if err := store.Verify(data, *pin); err != nil {
					return fail(err)
				}
			} else {
				pin = new(store.Integrity(data))
			}
			if tree, head, err := cache.ExtractCodeload(ctx, data, source.URL, resolved, pin); err == nil {
				if meta, err := readGitManifest(name, tree, "codeload extract", source.Subpath); err == nil {
					actual := store.Integrity(data)
					return hostedGitSource(source, head, &actual, archive), meta, &actual, nil
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	tree, head, err := cache.Clone(ctx, runtimeURL, resolved, shallow, mode == registry.Offline)
	if err != nil {
		return fail(err)
	}
	meta, err := readGitManifest(name, tree, "clone", source.Subpath)
	if err != nil {
		return fail(err)
	}
	return hostedGitSource(source, head, nil, ""), meta, nil, nil
}
