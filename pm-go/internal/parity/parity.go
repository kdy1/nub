// Package parity runs the same project fixture against independent executables.
package parity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
)

type Entry struct {
	Kind    string
	Mode    uint32
	Content string
}

type Result struct {
	Code int
	Out  string
	Err  string
	Tree map[string]Entry
}

func Snapshot(root string) (map[string]Entry, error) {
	tree := map[string]Entry{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		entry := Entry{Mode: uint32(info.Mode().Perm())}
		switch {
		case d.Type()&os.ModeSymlink != 0:
			entry.Kind = "symlink"
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			entry.Content = strings.ReplaceAll(target, root, "<project>")
		case d.IsDir():
			entry.Kind = "directory"
		case info.Mode().IsRegular():
			entry.Kind = "file"
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			entry.Content = hex.EncodeToString(sum[:])
		default:
			return fmt.Errorf("unsupported fixture entry: %s", path)
		}
		tree[filepath.ToSlash(rel)] = entry
		return nil
	})
	return tree, err
}

// Run isolates state from both the host and the other implementation. Paths are
// normalized in output, but file bytes and exit statuses are never normalized.
func Run(ctx context.Context, binary, project, state string, args, extraEnv []string) (Result, error) {
	var result Result
	for _, dir := range []string{state, filepath.Join(state, "cache"), filepath.Join(state, "data"), filepath.Join(state, "config")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return result, err
		}
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = project
	for _, key := range []string{"PATH", "SystemRoot", "COMSPEC", "PATHEXT", "TEMP", "TMP", "TMPDIR"} {
		if value, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	cmd.Env = append(cmd.Env, "HOME="+state, "USERPROFILE="+state, "XDG_CACHE_HOME="+filepath.Join(state, "cache"), "XDG_DATA_HOME="+filepath.Join(state, "data"), "XDG_CONFIG_HOME="+filepath.Join(state, "config"), "APPDATA="+filepath.Join(state, "config"), "LOCALAPPDATA="+filepath.Join(state, "data"), "NO_COLOR=1", "CI=true")
	cmd.Env = append(cmd.Env, extraEnv...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return result, err
		}
		result.Code = exit.ExitCode()
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	normalize := strings.NewReplacer(project, "<project>", state, "<state>", "nub-pm-go", "nub")
	result.Out, result.Err = normalize.Replace(stdout.String()), normalize.Replace(stderr.String())
	result.Tree, err = Snapshot(project)
	return result, err
}

func Compare(reference, candidate Result) error {
	if reference.Code != candidate.Code {
		return fmt.Errorf("exit code: Rust %d, Go %d", reference.Code, candidate.Code)
	}
	if reference.Out != candidate.Out {
		return fmt.Errorf("stdout differs: Rust %q, Go %q", reference.Out, candidate.Out)
	}
	if reference.Err != candidate.Err {
		return fmt.Errorf("stderr differs: Rust %q, Go %q", reference.Err, candidate.Err)
	}
	if !reflect.DeepEqual(reference.Tree, candidate.Tree) {
		return fmt.Errorf("project files differ: Rust %v, Go %v", reference.Tree, candidate.Tree)
	}
	return nil
}
