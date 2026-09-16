package lockfileio

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/nubjs/nub/pm-go/internal/identity"
	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/manifest"
)

type ProjectReadOptions struct {
	ReadOptions
	// Branch is empty when disabled, outside Git, or on a detached HEAD.
	// The caller owns identity-scoped settings and resolves it per run.
	Branch string
	// ForImport skips current and legacy Nub files and uses strict integrity.
	ForImport bool
}

type ProjectReadResult struct {
	Graph    *lockfile.Graph
	Kind     identity.Kind
	Path     string
	Warnings []Warning
}

func ReadProject(dir string, project *manifest.Package, options ProjectReadOptions) (ProjectReadResult, error) {
	binary := filepath.Join(dir, "bun.lockb")
	if exists(binary) && !exists(filepath.Join(dir, "bun.lock")) {
		return ProjectReadResult{}, &ValidationError{"ERR_AUBE_LOCKFILE_PARSE", binary, "bun.lockb (binary format) is not supported — run `bun install --save-text-lockfile` to generate a bun.lock text file first, or upgrade to bun 1.2+ where text is the default"}
	}
	candidates := identity.Candidates(dir, !options.ForImport, options.Branch)
	if selected, err := identity.DetectWithBranch(dir, projectValue(project), options.Branch); err == nil {
		// Reads retain the reference fallback when a declaration is ambiguous
		// or contradictory. Writes surface that error instead. Family ordering
		// is stable, retaining shrinkwrap-before-package-lock precedence.
		slices.SortStableFunc(candidates, func(a, b identity.Lockfile) int {
			am, bm := a.Kind.Family() == selected.Kind.Family(), b.Kind.Family() == selected.Kind.Family()
			if am == bm {
				return 0
			}
			if am {
				return -1
			}
			return 1
		})
	}
	readOptions := options.ReadOptions
	if options.ForImport {
		readOptions.AllowMissingIntegrity = false
	}
	for _, candidate := range candidates {
		if !exists(candidate.Path) {
			continue
		}
		kind := candidate.Kind
		if kind == identity.Yarn && identity.IsBerryPath(candidate.Path) {
			kind = identity.YarnBerry
		}
		g, warnings, err := Read(candidate.Path, kind, project, readOptions)
		if err != nil {
			return ProjectReadResult{}, err
		}
		return ProjectReadResult{Graph: g, Kind: kind, Path: candidate.Path, Warnings: warnings}, nil
	}
	return ProjectReadResult{}, &NotFoundError{Dir: dir}
}

// WriteProject preserves the declared or existing format and uses that kind's
// current output name. Legacy Nub inputs migrate only on a real write.
func WriteProject(dir string, g *lockfile.Graph, project *manifest.Package, branch string, options WriteOptions) (WriteResult, error) {
	selected, err := identity.DetectWithBranch(dir, projectValue(project), branch)
	if err != nil {
		return WriteResult{}, err
	}
	path := filepath.Join(dir, selected.Kind.BranchFilename(branch))
	return Write(path, selected.Kind, g, project, options)
}

type NotFoundError struct{ Dir string }

func (e *NotFoundError) Error() string { return fmt.Sprintf("no lockfile found in %s", e.Dir) }

func exists(path string) bool { _, err := os.Stat(path); return err == nil }
func projectValue(project *manifest.Package) *jsonvalue.Value {
	if project == nil {
		return nil
	}
	return project.Raw
}
