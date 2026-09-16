// Package processenv runs tools using an invocation's directory and environment.
package processenv

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type Environment struct {
	Dir      string
	Vars     []string
	In       io.Reader
	Out, Err io.Writer
}

func sameKey(a, b string) bool { return a == b || runtime.GOOS == "windows" && strings.EqualFold(a, b) }
func (e Environment) Lookup(key string) string {
	for i := len(e.Vars) - 1; i >= 0; i-- {
		name, value, ok := strings.Cut(e.Vars[i], "=")
		if ok && sameKey(name, key) {
			return value
		}
	}
	return ""
}
func (e Environment) With(key, value string) Environment {
	vars := make([]string, 0, len(e.Vars)+1)
	for _, entry := range e.Vars {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || !sameKey(name, key) {
			vars = append(vars, entry)
		}
	}
	e.Vars = append(vars, key+"="+value)
	return e
}

// LookPath searches only the supplied PATH, resolving its relative entries
// against Dir. It does not mutate or fall back to the host process environment.
func (e Environment) LookPath(name string) (string, error) {
	if !filepath.IsAbs(e.Dir) {
		return "", fmt.Errorf("tool execution requires an absolute working directory")
	}
	if name == "" || strings.ContainsRune(name, 0) {
		return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
	var extensions []string
	if runtime.GOOS == "windows" {
		raw := e.Lookup("PATHEXT")
		if raw == "" {
			raw = ".COM;.EXE;.BAT;.CMD"
		}
		for _, ext := range strings.Split(raw, ";") {
			if ext != "" {
				if ext[0] != '.' {
					ext = "." + ext
				}
				extensions = append(extensions, strings.ToLower(ext))
			}
		}
	}
	find := func(path string) (string, error) {
		if !filepath.IsAbs(path) {
			path = filepath.Join(e.Dir, path)
		}
		if len(extensions) == 0 || filepath.Ext(path) != "" {
			if err := executable(path); err == nil {
				return path, nil
			}
		}
		for _, ext := range extensions {
			candidate := path + ext
			if err := executable(candidate); err == nil {
				return candidate, nil
			}
		}
		return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
	if strings.ContainsRune(name, filepath.Separator) || strings.ContainsRune(name, '/') || runtime.GOOS == "windows" && strings.ContainsRune(name, ':') {
		return find(name)
	}
	for _, dir := range filepath.SplitList(e.Lookup("PATH")) {
		if runtime.GOOS == "windows" && dir == "" {
			continue
		}
		if found, err := find(filepath.Join(dir, name)); err == nil {
			return found, nil
		}
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}
func (e Environment) Command(ctx context.Context, name string, args ...string) (*exec.Cmd, error) {
	path, err := e.LookPath(name)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = e.Dir
	// A non-nil empty environment prevents os/exec from inheriting global vars.
	cmd.Env = append([]string{}, e.Vars...)
	cmd.Stdin = e.In
	cmd.Stdout = e.Out
	cmd.Stderr = e.Err
	return cmd, nil
}
