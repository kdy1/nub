// Package lockfileio composes format adapters without introducing an import
// cycle between those adapters and the common graph model.
package lockfileio

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nubjs/nub/pm-go/internal/identity"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/lockfile/bun"
	"github.com/nubjs/nub/pm-go/internal/lockfile/npm"
	"github.com/nubjs/nub/pm-go/internal/lockfile/pnpm"
	"github.com/nubjs/nub/pm-go/internal/lockfile/yarn"
	"github.com/nubjs/nub/pm-go/internal/manifest"
)

type Warning struct{ Code, Message string }
type ReadOptions struct {
	AllowMissingIntegrity   bool
	AllowUnsupportedSources bool
	Overrides               map[string]string
}
type WriteOptions struct {
	// The Nub write path preserves an unchanged graph by default.
	RewriteUnchanged                 bool
	EnforcePackageExtensionsChecksum bool
	ReadOptions                      ReadOptions
}
type WriteResult struct {
	Path     string
	Written  bool
	Warnings []Warning
}

func Read(path string, kind identity.Kind, project *manifest.Package, options ReadOptions) (*lockfile.Graph, []Warning, error) {
	var warnings []Warning
	switch kind {
	case identity.Nub, identity.Pnpm:
		g, w, err := pnpm.Read(path, pnpm.Options{AllowMissingIntegrity: options.AllowMissingIntegrity})
		for _, v := range w {
			warnings = append(warnings, Warning{v.Code, v.Message})
		}
		return g, warnings, err
	case identity.Npm, identity.Shrinkwrap:
		g, w, err := npm.Read(path, project)
		for _, v := range w {
			warnings = append(warnings, Warning{v.Code, v.Message})
		}
		return g, warnings, err
	case identity.Bun:
		g, w, err := bun.Read(path, bun.Options{AllowUnsupportedSources: options.AllowUnsupportedSources})
		for _, v := range w {
			warnings = append(warnings, Warning{v.Code, v.Message})
		}
		return g, warnings, err
	case identity.Yarn, identity.YarnBerry:
		// The reference dispatch reparses yarn.lock's actual marker even when
		// a caller supplied the classic kind for a Berry file.
		g, w, err := yarn.Read(path, project, yarn.Options{AllowUnsupportedSources: options.AllowUnsupportedSources, Overrides: options.Overrides})
		for _, v := range w {
			warnings = append(warnings, Warning{v.Code, v.Message})
		}
		return g, warnings, err
	default:
		return nil, nil, fmt.Errorf("unsupported lockfile kind %q", kind)
	}
}

// Write retains bytes and mtime for graph-equal files. Legacy Nub lockfiles
// migrate only when a real write occurs. Callers choose the active filename,
// including any branch-specific name, before entering this layer.
func Write(path string, kind identity.Kind, g *lockfile.Graph, project *manifest.Package, options WriteOptions) (WriteResult, error) {
	result := WriteResult{Path: path}
	var legacy []string
	if kind == identity.Nub {
		old := filepath.Join(filepath.Dir(path), "lock.yaml")
		if old != path {
			if stat, err := os.Stat(old); err == nil && stat.Mode().IsRegular() {
				legacy = append(legacy, old)
			}
		}
	}
	_, err := os.Stat(path)
	currentExists := err == nil
	if !options.RewriteUnchanged {
		compare := path
		if !currentExists && len(legacy) > 0 {
			compare = legacy[0]
		}
		readOptions := options.ReadOptions
		readOptions.AllowMissingIntegrity = false
		existing, warnings, err := Read(compare, kind, project, readOptions)
		result.Warnings = append(result.Warnings, warnings...)
		if err == nil && Equivalent(g, existing, options.EnforcePackageExtensionsChecksum) {
			if currentExists {
				for _, old := range legacy {
					_ = os.Remove(old)
				}
			}
			return result, nil
		}
	}
	switch kind {
	case identity.Nub, identity.Pnpm:
		warnings, err := pnpm.Write(path, g, project)
		for _, v := range warnings {
			result.Warnings = append(result.Warnings, Warning{v.Code, v.Message})
		}
		if err != nil {
			return result, err
		}
	case identity.Npm, identity.Shrinkwrap:
		if err := npm.Write(path, g, project); err != nil {
			return result, err
		}
	case identity.Bun:
		if err := bun.Write(path, g, project); err != nil {
			return result, err
		}
	case identity.Yarn:
		if err := yarn.WriteClassic(path, g, project); err != nil {
			return result, err
		}
	case identity.YarnBerry:
		if err := yarn.WriteBerry(path, g, project); err != nil {
			return result, err
		}
	default:
		return result, fmt.Errorf("unsupported lockfile kind %q", kind)
	}
	result.Written = true
	for _, old := range legacy {
		_ = os.Remove(old)
	}
	return result, nil
}

func Equivalent(a, b *lockfile.Graph, enforceExtensions bool) bool {
	if enforceExtensions && !sameOptional(a.PackageExtensionsChecksum, b.PackageExtensionsChecksum) {
		return false
	}
	aPatches, err := lockfile.ResolvePatchValues(a.PatchedDependencyHashes, a.Packages)
	if err != nil {
		return false
	}
	bPatches, err := lockfile.ResolvePatchValues(b.PatchedDependencyHashes, b.Packages)
	if err != nil {
		return false
	}
	callback := func(values map[string]string) func(string, string) *string {
		return func(name, version string) *string {
			if v, ok := values[name+"@"+version]; ok {
				return &v
			}
			return nil
		}
	}
	return a.IdentityHash(callback(aPatches)) == b.IdentityHash(callback(bPatches))
}
func sameOptional(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
