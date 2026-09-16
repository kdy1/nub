package identity

import (
	"os"
	"path/filepath"
	"strings"
)

func (k Kind) BranchFilename(branch string) string {
	if branch == "" || k != Nub && k != Pnpm {
		return k.Filename()
	}
	branch = strings.ReplaceAll(branch, "/", "!")
	if k == Nub {
		return "nub." + branch + ".lock"
	}
	return "pnpm-lock." + branch + ".yaml"
}

// Candidates uses Nub's foreign-first precedence. Branch files lead their
// base file; legacy Nub names follow the current canonical name.
func Candidates(root string, includeNub bool, branch string) []Lockfile {
	var out []Lockfile
	add := func(kind Kind, filename string) {
		out = append(out, Lockfile{Kind: kind, Path: filepath.Join(root, filename)})
	}
	addBranch := func(kind Kind) {
		if filename := kind.BranchFilename(branch); filename != kind.Filename() {
			add(kind, filename)
		}
		add(kind, kind.Filename())
	}
	addBranch(Pnpm)
	for _, kind := range []Kind{Bun, Yarn, Shrinkwrap, Npm} {
		add(kind, kind.Filename())
	}
	if includeNub {
		addBranch(Nub)
		add(Nub, "lock.yaml")
	}
	return out
}

// IsBerryPath is the reference's bounded discovery probe, distinct from the
// full-text, whitespace-tolerant detection used during parsing.
func IsBerryPath(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 4096)
	n, _ := f.Read(buf)
	prefix := string(buf[:n])
	return strings.HasPrefix(prefix, "__metadata:") || strings.Contains(prefix, "\n__metadata:")
}
