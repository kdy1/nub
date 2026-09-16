package linker

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/store"
)

type UnsafePackageName struct{ Name string }

func (e *UnsafePackageName) Error() string {
	return fmt.Sprintf("refusing to create node_modules entry for unsafe package name: %q", e.Name)
}
func (e *UnsafePackageName) Code() string { return "ERR_AUBE_UNSAFE_PACKAGE_NAME" }

func ValidatePackageLinkName(name string) error {
	if name == "" || strings.ContainsAny(name, "\x00\\") || strings.HasPrefix(name, "/") {
		return &UnsafePackageName{name}
	}
	parts := strings.Split(name, "/")
	if len(parts) != 1 && (len(parts) != 2 || !strings.HasPrefix(parts[0], "@") || len(parts[0]) <= 1) {
		return &UnsafePackageName{name}
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || len(part) >= 2 && part[1] == ':' {
			return &UnsafePackageName{name}
		}
	}
	return nil
}

// Materializer publishes complete package entries into a virtual
// store. Root may be a project-local or Go-global store. Hashes are used only
// for global identities; caller-owned graph/index data must remain immutable.
type Materializer struct {
	Root              string
	Strategy          Strategy
	Hashes            lockfile.GraphHashes
	Patches           map[string]string
	Quarantine        *Quarantine
	MaxFilenameLength int
}

type Materialized struct {
	Directory   string
	Cached      bool
	FilesLinked int
}

func (m Materializer) EntryName(depPath string) (string, error) {
	limit := m.MaxFilenameLength
	if limit == 0 {
		limit = 120
	}
	return lockfile.DepPathFilename(m.Hashes.DepPath(depPath), limit)
}

// EnsurePackage fills a private staging entry and atomically publishes it.
// A competing publisher's complete entry wins without being overwritten. The
// caller holds the store maintenance lease while these CAS files are in use.
func (m Materializer) EnsurePackage(ctx context.Context, depPath string, graph *lockfile.Graph, pkg *lockfile.Package, index store.PackageIndex, nestedLinks map[string]string) (Materialized, error) {
	if !filepath.IsAbs(m.Root) || graph == nil || pkg == nil {
		return Materialized{}, fmt.Errorf("materialization requires an absolute store root, graph and package")
	}
	if err := ValidatePackageLinkName(pkg.Name); err != nil {
		return Materialized{}, err
	}
	for _, name := range slices.Sorted(maps.Keys(pkg.Dependencies)) {
		if err := ValidatePackageLinkName(name); err != nil {
			return Materialized{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return Materialized{}, err
	}
	entry, err := m.EntryName(depPath)
	if err != nil {
		return Materialized{}, err
	}
	finalEntry := filepath.Join(m.Root, entry)
	pkgDir := filepath.Join(finalEntry, "node_modules", filepath.FromSlash(pkg.Name))
	result := Materialized{Directory: pkgDir}
	if _, err := os.Stat(pkgDir); err == nil {
		if err := m.linkDependencies(ctx, m.Root, entry, graph, pkg, nestedLinks, true); err != nil {
			return Materialized{}, err
		}
		result.Cached = true
		m.Quarantine.StripIndexed(pkgDir, index)
		return result, nil
	}
	if err := os.MkdirAll(m.Root, 0755); err != nil {
		return Materialized{}, err
	}
	tmp, err := os.MkdirTemp(m.Root, fmt.Sprintf(".tmp-%d-", os.Getpid()))
	if err != nil {
		return Materialized{}, err
	}
	defer os.RemoveAll(tmp)
	stagedPkg := filepath.Join(tmp, entry, "node_modules", filepath.FromSlash(pkg.Name))
	if err := FillFiles(ctx, index, stagedPkg, m.Strategy); err != nil {
		return Materialized{}, err
	}
	if key, patch, ok := lockfile.LookupPatch(pkg, m.Patches); ok {
		if err := ApplyPatch(ctx, stagedPkg, patch); err != nil {
			return Materialized{}, &PatchError{key, err.Error()}
		}
	}
	m.Quarantine.StripIndexed(stagedPkg, index)
	if err := m.linkDependencies(ctx, tmp, entry, graph, pkg, nestedLinks, false); err != nil {
		return Materialized{}, err
	}
	placed, err := publishEntry(ctx, filepath.Join(tmp, entry), finalEntry)
	if err != nil {
		return Materialized{}, err
	}
	result.Cached = !placed
	if placed {
		result.FilesLinked = len(index)
	}
	return result, nil
}

func (m Materializer) linkDependencies(ctx context.Context, base, entry string, graph *lockfile.Graph, pkg *lockfile.Package, nested map[string]string, warm bool) error {
	nm := filepath.Join(base, entry, "node_modules")
	for _, name := range slices.Sorted(maps.Keys(pkg.Dependencies)) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == pkg.Name {
			continue
		}
		tail := pkg.Dependencies[name]
		key, ok := "", false
		// Preserve the reference warm reconciliation convention. The cold
		// materializer additionally resolves full graph-key edge values.
		if !warm {
			key, ok = graph.Child(name, tail)
		}
		if !ok {
			key, ok = lockfile.SharedLocalDepPath(name, tail)
			if !ok {
				key = name + "@" + tail
			}
		}
		link := filepath.Join(nm, filepath.FromSlash(name))
		target, external := nested[key]
		if external {
			if !filepath.IsAbs(target) {
				return fmt.Errorf("nested dependency target must be absolute: %s", target)
			}
		} else {
			sibling, err := m.EntryName(key)
			if err != nil {
				return err
			}
			target = filepath.Join(m.Root, sibling, "node_modules", filepath.FromSlash(name))
			if runtime.GOOS != "windows" {
				// Both entries shift by the same wrapper depth after publication.
				// Windows junctions must persist the final absolute path instead.
				target = filepath.Join(base, sibling, "node_modules", filepath.FromSlash(name))
				if relative, err := filepath.Rel(filepath.Dir(link), target); err == nil {
					target = relative
				}
			}
		}
		if warm {
			current, err := reconcileDependencyLink(ctx, link, target)
			if err != nil {
				return err
			}
			if current {
				continue
			}
		}
		if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
			return err
		}
		if err := CreateDirLink(ctx, target, link); err != nil {
			if warm && os.IsExist(err) {
				if current, repairErr := reconcileDependencyLink(ctx, link, target); repairErr == nil && current {
					continue
				}
			}
			return err
		}
	}
	return nil
}

func publishEntry(ctx context.Context, staged, final string) (bool, error) {
	for attempt := range 5 {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		err := os.Rename(staged, final)
		if err == nil {
			return true, nil
		}
		if _, statErr := os.Stat(final); statErr == nil {
			return false, nil
		}
		if attempt == 4 || !transientPublishError(err) {
			return false, err
		}
		timer := time.NewTimer(20 * time.Millisecond << attempt)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return false, ctx.Err()
		}
	}
	panic("unreachable")
}
