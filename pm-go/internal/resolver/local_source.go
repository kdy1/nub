package resolver

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/manifest"
)

type RegistryFailure struct{ Package, Message string }

func (e *RegistryFailure) Error() string {
	return fmt.Sprintf("registry error for %s: %s", e.Package, e.Message)
}
func (e *RegistryFailure) Code() string { return "ERR_AUBE_REGISTRY_ERROR" }

type ExoticSubdependencyError struct {
	Name, Spec, Parent, Importer string
	Ancestors                    []AncestorFrame
}

func (e *ExoticSubdependencyError) Error() string {
	return fmt.Sprintf("blocked exotic transitive dependency %s@%s from %s (blockExoticSubdeps=true; set blockExoticSubdeps=false to allow trusted git/file/tarball subdeps)", e.Name, e.Spec, e.Parent)
}
func (e *ExoticSubdependencyError) Code() string { return "ERR_AUBE_BLOCKED_EXOTIC_SUBDEP" }

func sourceJoin(root, local string) string {
	if filepath.IsAbs(local) || filepath.VolumeName(local) != "" {
		return local
	}
	if runtime.GOOS == "windows" && (strings.HasPrefix(local, `\`) || strings.HasPrefix(local, "/")) {
		return filepath.VolumeName(root) + local
	}
	// Keep unresolved '..' components until the caller chooses normalization.
	if root == "" {
		return local
	}
	return root + string(filepath.Separator) + local
}
func normalizeLexical(local string) string {
	volume := filepath.VolumeName(local)
	tail := strings.TrimPrefix(local, volume)
	if runtime.GOOS == "windows" {
		tail = strings.ReplaceAll(tail, `\`, "/")
	}
	absolute := strings.HasPrefix(tail, "/")
	var parts []string
	for _, part := range strings.Split(tail, "/") {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(parts) > 0 && parts[len(parts)-1] != ".." {
				parts = parts[:len(parts)-1]
			} else {
				parts = append(parts, part)
			}
		default:
			parts = append(parts, part)
		}
	}
	prefix := volume
	if absolute {
		prefix += string(filepath.Separator)
	}
	return prefix + strings.Join(parts, string(filepath.Separator))
}
func RebaseLocal(source lockfile.Source, importerRoot, projectRoot string) lockfile.Source {
	if comparablePath(importerRoot) == comparablePath(projectRoot) {
		if source.Kind == lockfile.Exec {
			source.Path = normalizeLexical(source.Path)
		}
		return source
	}
	if source.Kind == lockfile.Git || source.Kind == lockfile.RemoteTarball {
		return source
	}
	abs := normalizeLexical(sourceJoin(importerRoot, source.Path))
	rebased, err := filepath.Rel(projectRoot, abs)
	if err != nil {
		source.Path = abs
	} else {
		source.Path = normalizeLexical(rebased)
	}
	return source
}

func prepareLocalSource(task resolveTask, resolved map[string]*lockfile.Package, projectRoot string, blockExotic bool) (lockfile.Source, string, error) {
	var parent *lockfile.Package
	if task.Parent != nil {
		parent = resolved[*task.Parent]
	}
	parentAllowsLocal := parent != nil && parent.Source != nil && (parent.Source.Kind == lockfile.Directory || parent.Source.Kind == lockfile.Link || parent.Source.Kind == lockfile.Portal || parent.Source.Kind == lockfile.Exec)
	if blockExotic && !task.Root && !task.RangeFromOverride && !parentAllowsLocal {
		parentName := "<unknown>"
		if task.Parent != nil {
			parentName = *task.Parent
		}
		return lockfile.Source{}, "", &ExoticSubdependencyError{task.Name, task.Range, parentName, task.Importer, task.Ancestors}
	}
	parentRoot := ""
	hasParentRoot := !task.Root && parentAllowsLocal && parent.Source.Kind != lockfile.Exec
	if hasParentRoot {
		parentRoot = sourceJoin(projectRoot, parent.Source.Path)
	}
	importerRoot := projectRoot
	if !task.RangeFromOverride {
		if hasParentRoot {
			importerRoot = parentRoot
		} else if task.Importer != "." {
			importerRoot = sourceJoin(projectRoot, task.Importer)
		}
	}
	source := lockfile.ParseSource(task.Range, importerRoot)
	if source == nil {
		return lockfile.Source{}, "", &RegistryFailure{task.Name, "unparseable local specifier: " + task.Range}
	}
	if !task.Root && !hasParentRoot && !task.RangeFromOverride && source.Kind != lockfile.Git && source.Kind != lockfile.RemoteTarball {
		return lockfile.Source{}, "", &RegistryFailure{task.Name, "transitive local specifier " + task.Range + " cannot be resolved without the parent package source root"}
	}
	return *source, importerRoot, nil
}

// ResolveExecScriptPath follows symlinks before checking the project boundary.
// Reading an exec: manifest later requires an explicitly supplied PATH Node.
func ResolveExecScriptPath(source lockfile.Source, projectRoot string) (string, error) {
	if source.Kind != lockfile.Exec {
		return "", fmt.Errorf("resolve_exec_script_path called on non-exec source")
	}
	script := sourceJoin(projectRoot, source.Path)
	info, err := os.Stat(script)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a file", script)
	}
	canonicalRoot, err := filepath.EvalSymlinks(projectRoot)
	if err == nil {
		canonicalRoot, err = filepath.Abs(canonicalRoot)
	}
	if err != nil {
		return "", fmt.Errorf("canonicalize project root %s: %w", projectRoot, err)
	}
	canonicalScript, err := filepath.EvalSymlinks(script)
	if err == nil {
		canonicalScript, err = filepath.Abs(canonicalScript)
	}
	if err != nil {
		return "", fmt.Errorf("canonicalize exec script %s: %w", script, err)
	}
	rel, err := filepath.Rel(canonicalRoot, canonicalScript)
	if err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("%s resolves outside project root %s", script, canonicalRoot)
	}
	return canonicalScript, nil
}

type LocalManifest struct {
	Name, Version string
	Dependencies  map[string]string
}

func parseLocalManifest(content []byte) (LocalManifest, error) {
	p, err := manifest.ParsePackage(content)
	if err != nil {
		return LocalManifest{}, err
	}
	out := LocalManifest{Version: "0.0.0", Dependencies: p.Dependencies}
	if p.Name != nil {
		out.Name = *p.Name
	}
	if p.Version != nil {
		out.Version = *p.Version
	}
	return out, nil
}
func ReadLocalManifest(source lockfile.Source, importerRoot string) (LocalManifest, error) {
	fail := func(err error) (LocalManifest, error) {
		return LocalManifest{}, &RegistryFailure{source.Specifier(), err.Error()}
	}
	if source.Kind == lockfile.Git || source.Kind == lockfile.RemoteTarball {
		return fail(fmt.Errorf("read_local_manifest called on non-path source"))
	}
	path := sourceJoin(importerRoot, source.Path)
	var content []byte
	var err error
	switch source.Kind {
	case lockfile.Directory, lockfile.Link, lockfile.Portal:
		content, err = os.ReadFile(filepath.Join(path, "package.json"))
	case lockfile.Tarball:
		var compressed []byte
		compressed, err = os.ReadFile(path)
		if err == nil {
			content, err = ReadTarballManifest(compressed)
		}
	default:
		return fail(fmt.Errorf("read_local_manifest: generated or remote source handled separately"))
	}
	if err != nil {
		return fail(err)
	}
	out, err := parseLocalManifest(content)
	if err != nil {
		return fail(err)
	}
	return out, nil
}

const maxResolveTarballBytes = 64 << 20
const maxResolveManifestBytes = 8 << 20

// ReadTarballManifest reads metadata only; materialization still uses the store's
// complete integrity and safe-extraction checks. The first two-component
// package.json wins, regardless of the archive's wrapper directory name.
func ReadTarballManifest(compressed []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	gz.Multistream(false)
	reader := tar.NewReader(io.LimitReader(gz, maxResolveTarballBytes))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		name := header.Name
		if runtime.GOOS == "windows" {
			name = strings.ReplaceAll(name, `\`, "/")
		}
		// Rust Path components ignore repeated separators and inner '.', but
		// retain a leading CurDir component and unresolved parent components.
		var components []string
		if strings.HasPrefix(name, "/") {
			components = append(components, "/")
		}
		for i, part := range strings.Split(name, "/") {
			if part == "" || part == "." && i != 0 {
				continue
			}
			components = append(components, part)
		}
		if len(components) != 2 || components[1] != "package.json" {
			continue
		}
		content, err := io.ReadAll(io.LimitReader(reader, maxResolveManifestBytes+1))
		if err != nil {
			return nil, err
		}
		if len(content) > maxResolveManifestBytes {
			return nil, fmt.Errorf("package.json exceeds 8 MiB cap")
		}
		return content, nil
	}
	return nil, fmt.Errorf("tarball has no top-level package.json")
}
