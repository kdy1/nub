package linker

import (
	"context"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/workspace"
)

func hoistMatches(name string, patterns []string) bool {
	positive := false
	for _, raw := range patterns {
		pattern, negated := strings.CutPrefix(raw, "!")
		if workspace.MatchFold(pattern, name) {
			if negated {
				return false
			}
			positive = true
		}
	}
	return positive
}

func (p IsolatedPlan) publicHoist(ctx context.Context, m Materializer, nm string, stats *LinkStats) error {
	direct := map[string]bool{}
	for _, dep := range p.Graph.RootDeps() {
		direct[dep.Name] = true
	}
	for _, all := range []bool{false, true} {
		if all && !p.ShamefullyHoist || !all && len(p.PublicHoistPatterns) == 0 {
			continue
		}
		claimed := map[string]bool{}
		for _, key := range slices.Sorted(maps.Keys(p.Graph.Packages)) {
			pkg := p.Graph.Packages[key]
			if pkg.Source != nil || direct[pkg.Name] || claimed[pkg.Name] || !all && !hoistMatches(pkg.Name, p.PublicHoistPatterns) {
				continue
			}
			claimed[pkg.Name] = true
			source, err := existingPackageDir(m, key, pkg.Name)
			if err != nil {
				return err
			}
			if source == "" {
				continue
			}
			linked, err := ensureTopLink(ctx, filepath.Join(nm, filepath.FromSlash(pkg.Name)), source)
			if err != nil {
				return err
			}
			if linked {
				stats.TopLevelLinked++
			}
		}
	}
	if !p.HasWorkspace || !enabled(p.HoistWorkspacePackages) || !p.ShamefullyHoist && len(p.PublicHoistPatterns) == 0 {
		return nil
	}
	for _, name := range slices.Sorted(maps.Keys(p.WorkspaceDirs)) {
		dir := p.WorkspaceDirs[name]
		if direct[name] || filepath.Dir(nm) == dir || !p.ShamefullyHoist && !hoistMatches(name, p.PublicHoistPatterns) {
			continue
		}
		if err := ValidatePackageLinkName(name); err != nil {
			return err
		}
		linked, err := ensureTopLink(ctx, filepath.Join(nm, filepath.FromSlash(name)), dir)
		if err != nil {
			return err
		}
		if linked {
			stats.TopLevelLinked++
		}
	}
	return nil
}

func (p IsolatedPlan) hiddenHoist(ctx context.Context, m Materializer) error {
	hidden := filepath.Join(m.Root, "node_modules")
	_ = removeEntry(ctx, hidden, 1)
	if !enabled(p.Hoist) {
		return ctx.Err()
	}
	claimed := map[string]bool{}
	var selected []string
	selectPackage := func(key string) {
		pkg := p.Graph.Packages[key]
		if pkg == nil || pkg.Source != nil || claimed[pkg.Name] || !hoistMatches(pkg.Name, p.HoistPatterns) {
			return
		}
		claimed[pkg.Name] = true
		selected = append(selected, key)
	}
	for _, dep := range p.Graph.RootDeps() {
		selectPackage(dep.DepPath)
	}
	depths := p.Graph.DependencyDepths()
	keys := slices.Sorted(maps.Keys(p.Graph.Packages))
	slices.SortStableFunc(keys, func(a, b string) int {
		x, ax := depths[a]
		y, by := depths[b]
		if ax != by {
			if ax {
				return -1
			}
			return 1
		}
		return x - y
	})
	for _, key := range keys {
		selectPackage(key)
	}
	for _, key := range selected {
		pkg := p.Graph.Packages[key]
		source, err := existingPackageDir(m, key, pkg.Name)
		if err != nil {
			return err
		}
		if source != "" {
			if _, err := ensureTopLink(ctx, filepath.Join(hidden, filepath.FromSlash(pkg.Name)), source); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}
