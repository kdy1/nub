package linker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/fsutil"
	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
)

// AppliedPatchesFilename is the existing Nub project sidecar. Only global
// cache/store paths use the independent Go executable's namespace.
const AppliedPatchesFilename = ".nub-applied-patches.json"

func CurrentPatchHashes(patches map[string]string) map[string]string {
	out := make(map[string]string, len(patches))
	for key, text := range patches {
		hash := sha256.Sum256([]byte(strings.ReplaceAll(text, "\r\n", "\n")))
		out[key] = hex.EncodeToString(hash[:])
	}
	return out
}

// ReadAppliedPatches treats missing, unreadable and malformed state as an
// empty map, conservatively requiring currently patched entries to be rebuilt.
func ReadAppliedPatches(modulesDir string) map[string]string {
	out := map[string]string{}
	if !filepath.IsAbs(modulesDir) {
		return out
	}
	data, err := os.ReadFile(filepath.Join(modulesDir, AppliedPatchesFilename))
	if err != nil {
		return out
	}
	value, err := jsonvalue.Parse(data)
	if err != nil || value.Kind != '{' {
		return out
	}
	for _, field := range value.Object {
		if field.Value.Kind != 's' {
			return map[string]string{}
		}
		out[field.Key] = field.Value.Scalar.(string)
	}
	return out
}

// WriteAppliedPatches publishes tracking state after linking succeeds. Errors
// are returned so the install driver can report the reference tracking error.
func WriteAppliedPatches(modulesDir string, hashes map[string]string) error {
	if !filepath.IsAbs(modulesDir) {
		return fmt.Errorf("patch tracking requires an absolute modules directory")
	}
	path := filepath.Join(modulesDir, AppliedPatchesFilename)
	if len(hashes) == 0 {
		err := unlinkBinFile(path)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	value := jsonvalue.Object()
	for _, key := range slices.Sorted(maps.Keys(hashes)) {
		value.Put(key, jsonvalue.String(hashes[key]))
	}
	data, err := value.MarshalJSON()
	if err != nil {
		return err
	}
	return fsutil.WriteDefault(path, data)
}

// WipeChangedPatchedEntries invalidates project-local entries whose selector
// changed. Global entries instead encode patch hashes in their graph identity.
// Removal is best-effort, matching the reference; the caller holds the project
// install lease, and publishes current fingerprints only after linking.
func WipeChangedPatchedEntries(ctx context.Context, virtualStore string, graph *lockfile.Graph, previous, current map[string]string, maxLength int) error {
	if !filepath.IsAbs(virtualStore) {
		return fmt.Errorf("patch reconciliation requires an absolute virtual store")
	}
	affected := map[string]bool{}
	for _, values := range []map[string]string{previous, current} {
		for key := range values {
			before, had := previous[key]
			after, has := current[key]
			if had != has || before != after {
				affected[key] = true
			}
		}
	}
	if len(affected) == 0 {
		return ctx.Err()
	}
	m := Materializer{Root: virtualStore, MaxFilenameLength: maxLength}
	for _, key := range slices.Sorted(maps.Keys(graph.Packages)) {
		if err := ctx.Err(); err != nil {
			return err
		}
		pkg := graph.Packages[key]
		if affected[pkg.SpecKey()] || affected[pkg.RegistryName()+"@"+pkg.Version] {
			entry, err := m.EntryName(key)
			if err != nil {
				return err
			}
			_ = os.RemoveAll(filepath.Join(virtualStore, entry))
		}
	}
	return nil
}
