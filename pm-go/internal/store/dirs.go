package store

import (
	"fmt"
	"path/filepath"
	"strings"
)

const Namespace = "nub-pm-go"

// DefaultDirs preserves the reference's platform precedence, with an isolated
// namespace. home and env belong to the invocation, never the process globals.
func DefaultDirs(platform, home string, env map[string]string) (root, cache string, err error) {
	if xdg := strings.TrimSpace(env["XDG_CACHE_HOME"]); xdg != "" {
		cache = filepath.Join(xdg, Namespace)
	} else if local, ok := env["LOCALAPPDATA"]; platform == "windows" && ok {
		cache = filepath.Join(local, Namespace)
	} else if home != "" {
		cache = filepath.Join(home, ".cache", Namespace)
	}
	if local, ok := env["LOCALAPPDATA"]; platform == "windows" && ok {
		root = filepath.Join(local, Namespace, "store", "v1", "files")
	} else if xdg := strings.TrimSpace(env["XDG_DATA_HOME"]); xdg != "" {
		root = filepath.Join(xdg, Namespace, "store", "v1", "files")
	} else if home != "" {
		root = filepath.Join(home, ".local", "share", Namespace, "store", "v1", "files")
	}
	if root == "" || cache == "" {
		return "", "", fmt.Errorf("HOME environment variable not set")
	}
	return root, cache, nil
}
