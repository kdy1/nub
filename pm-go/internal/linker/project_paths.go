package linker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/fsutil"
)

type UnsafeModulesDir struct{ Path string }

func (e *UnsafeModulesDir) Error() string {
	return "refusing to use a modules directory that is not inside the project: " + e.Path
}
func (*UnsafeModulesDir) Code() string { return "ERR_AUBE_UNSAFE_MODULES_DIR" }

// CheckedModulesDir validates the resolved path and every existing ancestor
// before a cleanup pass can reach it. Workspace siblings are checked against
// their own project directory, so parent-relative importers remain supported.
func CheckedModulesDir(project, name string) (string, error) {
	if !filepath.IsAbs(project) {
		return "", fmt.Errorf("linking requires an absolute project directory")
	}
	project = filepath.Clean(project)
	modules := name
	if !filepath.IsAbs(name) {
		modules = filepath.Join(project, name)
	}
	if !insidePath(project, modules, false) {
		return "", &UnsafeModulesDir{modules}
	}
	if canonical, err := fsutil.Canonicalize(project); err == nil {
		for parent := modules; ; parent = filepath.Dir(parent) {
			if actual, err := fsutil.Canonicalize(parent); err == nil {
				if !insidePath(canonical, actual, parent != modules) {
					return "", &UnsafeModulesDir{modules}
				}
				break
			}
			if filepath.Dir(parent) == parent {
				return "", &UnsafeModulesDir{modules}
			}
		}
	}
	return modules, nil
}

func insidePath(root, child string, allowEqual bool) bool {
	rel, err := filepath.Rel(root, child)
	return err == nil && (allowEqual || rel != ".") && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func mkdirLinkDir(ctx context.Context, path string) error {
	return retryLinkFS(ctx, 10, func() error { return os.MkdirAll(path, 0755) })
}

func removeEntry(ctx context.Context, path string, attempts int) error {
	return retryLinkFS(ctx, attempts, func() error {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.IsDir() && !patchPathIsLink(info) {
			err = os.RemoveAll(path)
		} else {
			err = os.Remove(path)
		}
		if os.IsNotExist(err) {
			return nil
		}
		return err
	})
}

// SweepStaleTemps only targets linker staging names belonging to a different
// PID. The project install lease prevents concurrent sweeps in the same tree.
func SweepStaleTemps(ctx context.Context, root string) {
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if ctx.Err() != nil {
			return
		}
		rest, ok := strings.CutPrefix(entry.Name(), ".tmp-")
		if !ok {
			continue
		}
		pid, _, ok := strings.Cut(rest, "-")
		if !ok || pid == strconv.Itoa(os.Getpid()) || strings.IndexFunc(pid, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			continue
		}
		path := filepath.Join(root, entry.Name())
		if info, err := os.Lstat(path); err == nil && (info.IsDir() || patchPathIsLink(info)) {
			_ = removeEntry(ctx, path, 10)
		}
	}
}

func sweepTopLevel(ctx context.Context, modules string, preserve map[string]bool, storeLeaf string) {
	scopes := map[string]bool{}
	for name := range preserve {
		if scope, _, ok := strings.Cut(name, "/"); ok {
			scopes[scope] = true
		}
	}
	entries, _ := os.ReadDir(modules)
	for _, entry := range entries {
		if ctx.Err() != nil {
			return
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") || name == storeLeaf || preserve[name] {
			continue
		}
		path := filepath.Join(modules, name)
		if info, err := os.Lstat(path); scopes[name] && err == nil && info.IsDir() && !patchPathIsLink(info) {
			children, _ := os.ReadDir(path)
			for _, child := range children {
				if !preserve[name+"/"+child.Name()] {
					_ = removeEntry(ctx, filepath.Join(path, child.Name()), 4)
				}
			}
			_ = os.Remove(path) // Remove only an empty scope directory.
		} else {
			_ = removeEntry(ctx, path, 4)
		}
	}
}

func ensureTopLink(ctx context.Context, link, source string) (bool, error) {
	target, err := filepath.Rel(filepath.Dir(link), source)
	if err != nil {
		target = source
	}
	if runtime.GOOS == "windows" {
		actual, aerr := fsutil.Canonicalize(link)
		expected, eerr := fsutil.Canonicalize(source)
		if aerr == nil && eerr == nil && actual == expected {
			return false, nil
		}
		if err := removeEntry(ctx, link, 10); err != nil {
			return false, err
		}
	} else {
		if actual, err := os.Readlink(link); err == nil && actual == target {
			return false, nil
		}
		_ = removeEntry(ctx, link, 1)
	}
	if err := mkdirLinkDir(ctx, filepath.Dir(link)); err != nil {
		return false, err
	}
	if err := CreateDirLink(ctx, target, link); err != nil {
		return false, err
	}
	return true, nil
}
