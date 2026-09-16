package yarn

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/workspace"
)

// Yarn readers expand each positive glob independently. In this reference
// path, a negative pattern is ignored rather than subtracted from the result.
func discoverMembers(projectDir string, patterns []string) []string {
	found := lockfile.Set{}
	for _, pattern := range patterns {
		if strings.HasPrefix(pattern, "!") || pattern == "." {
			continue
		}
		absolute := filepath.Join(projectDir, filepath.FromSlash(pattern))
		if filepath.IsAbs(pattern) {
			absolute = pattern
		}
		volume := filepath.VolumeName(absolute)
		rest := strings.TrimPrefix(absolute, volume)
		base := volume
		if strings.HasPrefix(rest, string(filepath.Separator)) {
			base += string(filepath.Separator)
			rest = strings.TrimLeft(rest, string(filepath.Separator))
		}
		if base == "" {
			base = "."
		}
		components := strings.Split(rest, string(filepath.Separator))
		var walk func(string, int, map[string]bool)
		walk = func(dir string, index int, ancestors map[string]bool) {
			if index == len(components) {
				info, err := os.Stat(dir)
				if err != nil || !info.IsDir() {
					return
				}
				pj, err := os.Stat(filepath.Join(dir, "package.json"))
				if err != nil || !pj.Mode().IsRegular() {
					return
				}
				rel, err := filepath.Rel(projectDir, dir)
				if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					return
				}
				found.Add(filepath.ToSlash(rel))
				return
			}
			part := components[index]
			if part == "**" {
				walk(dir, index+1, ancestors)
				identity, err := filepath.EvalSymlinks(dir)
				if err != nil {
					return
				}
				identity, err = filepath.Abs(identity)
				if err != nil || ancestors[identity] {
					return
				}
				ancestors[identity] = true
				defer delete(ancestors, identity)
				children, err := os.ReadDir(dir)
				if err != nil {
					return
				}
				for _, child := range children {
					p := filepath.Join(dir, child.Name())
					if info, err := os.Stat(p); err == nil && info.IsDir() {
						walk(p, index, ancestors)
					}
				}
				return
			}
			if !strings.ContainsAny(part, "*?[") {
				walk(filepath.Join(dir, part), index+1, ancestors)
				return
			}
			children, err := os.ReadDir(dir)
			if err != nil {
				return
			}
			for _, child := range children {
				if workspace.Match(part, child.Name(), true) {
					walk(filepath.Join(dir, child.Name()), index+1, ancestors)
				}
			}
		}
		walk(base, 0, map[string]bool{})
	}
	return slices.Clip(found.Sorted())
}
