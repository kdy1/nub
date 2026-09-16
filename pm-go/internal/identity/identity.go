// Package identity preserves Nub's distinction between declaration and format.
package identity

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
)

type Declaration struct{ Name, Version, Field string }

// Declared reads the frontend's identity, including the last named devEngines
// entry. The lockfile selector uses UnanimousDeclaration instead.
func Declared(root *jsonvalue.Value) Declaration {
	if v := root.Get("packageManager"); v != nil && v.Kind == 's' {
		name, version, _ := strings.Cut(strings.TrimSpace(v.Text()), "@")
		version, _, _ = strings.Cut(version, "+")
		return Declaration{name, version, "packageManager"}
	}
	dev := root.Get("devEngines").Get("packageManager")
	if dev == nil {
		return Declaration{}
	}
	if dev.Kind == '[' {
		for i := len(dev.Array) - 1; i >= 0; i-- {
			if n := dev.Array[i].Get("name"); n != nil && n.Kind == 's' {
				dev = dev.Array[i]
				break
			}
		}
	}
	version, _, _ := strings.Cut(dev.Get("version").Text(), "+")
	return Declaration{strings.TrimSpace(dev.Get("name").Text()), version, "devEngines.packageManager"}
}

func UnanimousDeclaration(root *jsonvalue.Value) Declaration {
	if v := root.Get("packageManager"); v != nil && v.Kind == 's' {
		name, _, _ := strings.Cut(v.Text(), "@")
		if name != "" {
			return Declaration{Name: name, Field: "packageManager"}
		}
	}
	dev := root.Get("devEngines").Get("packageManager")
	if dev == nil {
		return Declaration{}
	}
	entries := []*jsonvalue.Value{dev}
	if dev.Kind == '[' {
		entries = dev.Array
	}
	var out Declaration
	seen := false
	for _, entry := range entries {
		n := entry.Get("name")
		if n == nil || n.Kind != 's' {
			continue
		}
		if seen && out.Name != n.Text() {
			return Declaration{}
		}
		out = Declaration{Name: n.Text(), Field: "devEngines.packageManager"}
		seen = true
	}
	return out
}

type Kind string

const (
	Nub        Kind = "nub"
	Pnpm       Kind = "pnpm"
	Npm        Kind = "npm"
	Shrinkwrap Kind = "npm-shrinkwrap"
	Yarn       Kind = "yarn"
	YarnBerry  Kind = "yarn-berry"
	Bun        Kind = "bun"
)

func (k Kind) Filename() string {
	return map[Kind]string{Nub: "nub.lock", Pnpm: "pnpm-lock.yaml", Npm: "package-lock.json", Shrinkwrap: "npm-shrinkwrap.json", Yarn: "yarn.lock", YarnBerry: "yarn.lock", Bun: "bun.lock"}[k]
}

func (k Kind) Family() string {
	switch k {
	case Shrinkwrap:
		return "npm"
	case YarnBerry:
		return "yarn"
	default:
		return string(k)
	}
}

type Lockfile struct {
	Kind     Kind
	Path     string
	Existing bool
	Declared bool
}

func Detect(root string, manifest *jsonvalue.Value) (Lockfile, error) {
	return DetectWithBranch(root, manifest, "")
}

// DetectWithBranch uses the already-resolved Git branch. The settings owner
// decides whether branch lockfiles are enabled for this identity.
func DetectWithBranch(root string, manifest *jsonvalue.Value, branch string) (Lockfile, error) {
	var existing []Lockfile
	for _, candidate := range Candidates(root, true, branch) {
		if _, err := os.Stat(candidate.Path); err == nil {
			candidate.Existing = true
			existing = append(existing, candidate)
		}
	}
	decl := UnanimousDeclaration(manifest)
	want := Kind(decl.Name)
	known := want == Npm || want == Pnpm || want == Yarn || want == Bun
	fresh := func(kind Kind) Lockfile {
		return Lockfile{Kind: kind, Path: filepath.Join(root, kind.BranchFilename(branch)), Declared: decl.Name != ""}
	}
	finish := func(lock Lockfile) (Lockfile, error) {
		lock.Declared = decl.Name != ""
		if lock.Kind == Yarn && IsBerryPath(lock.Path) {
			lock.Kind = YarnBerry
		}
		return lock, nil
	}
	var names []string
	for _, lock := range existing {
		names = append(names, filepath.Base(lock.Path))
	}
	if known {
		for _, lock := range existing {
			if lock.Kind.Family() == decl.Name {
				return finish(lock)
			}
		}
		if len(existing) == 0 {
			return fresh(want), nil
		}
		return Lockfile{}, fmt.Errorf("ERR_NUB_LOCKFILE_DECLARATION_MISMATCH: package.json declares `%s` (via `%s`), but %s is missing — found %s instead", decl.Name, decl.Field, want.Filename(), strings.Join(names, ", "))
	}
	if len(existing) == 0 {
		return fresh(Nub), nil
	}
	if decl.Name == "nub" {
		for _, lock := range existing {
			if lock.Kind == Nub {
				return finish(lock)
			}
		}
		return finish(existing[0])
	}
	for _, lock := range existing[1:] {
		if lock.Kind.Family() != existing[0].Kind.Family() {
			return Lockfile{}, fmt.Errorf("ERR_NUB_LOCKFILE_AMBIGUOUS: multiple lockfiles found: %s — cannot tell which package manager owns this project", strings.Join(names, ", "))
		}
	}
	return finish(existing[0])
}

func Role(decl Declaration, lock Lockfile) string {
	switch decl.Name {
	case "nub", "npm", "pnpm", "yarn", "bun":
		return decl.Name
	}
	return lock.Kind.Family()
}
