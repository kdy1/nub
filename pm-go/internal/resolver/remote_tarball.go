package resolver

import (
	"context"
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/registry"
)

type TarballFetcher interface {
	Tarball(context.Context, string, registry.NetworkMode) ([]byte, error)
}

// ResolveRemoteTarball discovers metadata and pins the fetched bytes by SHA-512.
// The caller retains network/cache policy; final extraction still validates the
// complete archive before materialization in the store.
func ResolveRemoteTarball(ctx context.Context, name string, source lockfile.Source, client TarballFetcher, mode registry.NetworkMode) (lockfile.Source, LocalManifest, error) {
	fail := func(message string) (lockfile.Source, LocalManifest, error) {
		return lockfile.Source{}, LocalManifest{}, &RegistryFailure{name, message}
	}
	if source.Kind != lockfile.RemoteTarball {
		return fail("resolve_remote_tarball called on non-tarball source")
	}
	data, err := client.Tarball(ctx, source.URL, mode)
	if err != nil {
		detail := err.Error()
		var requestError *url.Error
		if errors.As(err, &requestError) {
			safe := *requestError
			safe.URL = registry.RedactURL(safe.URL)
			detail = safe.Error()
		}
		return fail(fmt.Sprintf("fetch %s: %s", registry.RedactURL(source.URL), detail))
	}
	content, err := ReadTarballManifest(data)
	if err != nil {
		return fail(fmt.Sprintf("tarball %s: %s", registry.RedactURL(source.URL), err))
	}
	meta, err := parseLocalManifest(content)
	if err != nil {
		return fail(err.Error())
	}
	meta.Name = name
	digest := sha512.Sum512(data)
	integrity := "sha512-" + base64.StdEncoding.EncodeToString(digest[:])
	return lockfile.Source{Kind: lockfile.RemoteTarball, URL: source.URL, Integrity: &integrity, GitHosted: source.GitHosted}, meta, nil
}
