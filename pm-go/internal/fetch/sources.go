package fetch

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/processenv"
	"github.com/nubjs/nub/pm-go/internal/registry"
	"github.com/nubjs/nub/pm-go/internal/resolver"
	"github.com/nubjs/nub/pm-go/internal/store"
)

// Path imports file: and portal: contents afresh, so edits are visible without
// a version bump. A link: has no store index and is materialized by the linker.
func (f *Fetcher) Path(ctx context.Context, p *lockfile.Package, projectRoot string) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if p == nil || p.Source == nil || !filepath.IsAbs(projectRoot) || f.Store == nil {
		return Result{}, fmt.Errorf("path import requires a source, store and absolute project root")
	}
	source := p.Source
	if source.Kind == lockfile.Link {
		return Result{}, nil
	}
	path := source.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(projectRoot, path)
	}
	var index store.PackageIndex
	var err error
	switch source.Kind {
	case lockfile.Directory, lockfile.Portal:
		info, statErr := os.Stat(path)
		if statErr != nil || !info.IsDir() {
			return Result{}, fmt.Errorf("local dependency %s: %s is not a directory", source.Specifier(), path)
		}
		index, err = f.Store.ImportDirectory(ctx, path)
	case lockfile.Tarball:
		file, openErr := os.Open(path)
		if openErr != nil {
			return Result{}, fmt.Errorf("read %s: %w", path, openErr)
		}
		defer file.Close()
		index, err = f.Store.ImportTarball(ctx, file, f.Options.ArchiveLimits)
	default:
		return Result{}, fmt.Errorf("path import requires file:, portal: or link:")
	}
	if err != nil {
		return Result{}, fmt.Errorf("failed to import %s: %w", source.Specifier(), err)
	}
	return Result{Index: index}, nil
}

func (f *Fetcher) Generated(ctx context.Context, p *lockfile.Package, env processenv.Environment, tempRoot string, ignoreScripts bool) (Result, error) {
	if p == nil || p.Source == nil || p.Source.Kind != lockfile.Exec || f.Store == nil {
		return Result{}, fmt.Errorf("generated import requires an exec source and store")
	}
	if ignoreScripts {
		return Result{}, fmt.Errorf("%s requires executing its generator, but scripts are disabled", p.Source.Specifier())
	}
	if !filepath.IsAbs(tempRoot) {
		return Result{}, fmt.Errorf("exec dependency requires an absolute temporary directory")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(tempRoot, 0700); err != nil {
		return Result{}, err
	}
	var index store.PackageIndex
	err := resolver.WithExecBuild(ctx, env, tempRoot, p.Name, *p.Source, func(build string) error {
		var err error
		index, err = f.Store.ImportDirectory(ctx, build)
		if err != nil {
			return fmt.Errorf("failed to import generated %s: %w", p.Source.Specifier(), err)
		}
		return nil
	})
	return Result{Index: index}, err
}

// Remote verifies the source pin independently of registry-store settings.
// The reference reimports URL sources without a name/version index cache.
func (f *Fetcher) Remote(ctx context.Context, p *lockfile.Package) (Result, error) {
	if p == nil || p.Source == nil || p.Source.Kind != lockfile.RemoteTarball || f.Store == nil || f.Client == nil {
		return Result{}, fmt.Errorf("remote import requires a tarball source, store and registry client")
	}
	source := p.Source
	data, err := f.Client.Tarball(ctx, source.URL, f.Options.Network)
	if err != nil {
		return Result{}, fmt.Errorf("failed to fetch %s: %w", registry.RedactURL(source.URL), err)
	}
	if source.Integrity != nil && *source.Integrity != "" {
		if err := store.Verify(data, *source.Integrity); err != nil {
			return Result{}, fmt.Errorf("%s: %w", registry.RedactURL(source.URL), err)
		}
	} else {
		f.warn("WARN_AUBE_MISSING_INTEGRITY", "remote tarball lockfile entry has no integrity field; importing fetched bytes without verification (run `nub-pm-go install --no-frozen-lockfile` to refresh the lockfile)")
	}
	index, err := f.Store.ImportTarball(ctx, bytes.NewReader(data), f.Options.ArchiveLimits)
	if err != nil {
		return Result{}, fmt.Errorf("failed to import %s: %w", registry.RedactURL(source.Specifier()), err)
	}
	return Result{Index: index}, nil
}
