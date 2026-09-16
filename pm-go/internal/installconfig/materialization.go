package installconfig

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/manifest"
	"github.com/nubjs/nub/pm-go/internal/workspace"
)

// Materialization cannot request a whole-graph hidden hoist inside the shared
// store. Selected project-local copies have a separate, local hidden tree.
type Materialization uint8

const (
	SharedStore Materialization = iota
	DiskWithHiddenTree
	DiskWithoutHiddenTree
)

func (m Materialization) UsesSharedStore() bool  { return m == SharedStore }
func (m Materialization) BuildsHiddenTree() bool { return m == DiskWithHiddenTree }

type MaterializationInput struct {
	Linker                   NodeLinker
	EnableGlobalVirtualStore *bool
	HoistExplicit            *bool
	ResolvedHoist            bool
	Env                      map[string]string
	Manifests                []*manifest.Package
	DisableForPackages       []string
	VirtualStoreOnly         bool
}
type MaterializationSelection struct {
	Mode     Materialization
	Override *bool
	// Nub demotes this automatic compatibility fallback to debug output.
	IncompatiblePackage string
}

// SelectMaterialization follows Nub's embedder policy: default hoist=true lets
// the shared store engage, while explicitly requesting it vetoes sharing.
func SelectMaterialization(input MaterializationInput) (MaterializationSelection, error) {
	if input.Linker == "" {
		input.Linker = Isolated
	}
	if input.Linker != Isolated && input.Linker != Hoisted {
		return MaterializationSelection{}, fmt.Errorf("invalid materialization linker %q", input.Linker)
	}
	if input.EnableGlobalVirtualStore != nil && *input.EnableGlobalVirtualStore {
		conflict := ""
		if input.Linker == Hoisted {
			conflict = "node-linker=hoisted"
		} else if input.HoistExplicit != nil && *input.HoistExplicit {
			conflict = "hoist=true"
		}
		if conflict != "" {
			return MaterializationSelection{}, fmt.Errorf("enableGlobalVirtualStore=true conflicts with %s: the shared global virtual store needs the isolated layout with no hidden hoist tree (a shared-store bare-name alias would be cross-project-mutable state). Set one, not both.", conflict)
		}
	}
	selection := MaterializationSelection{Override: input.EnableGlobalVirtualStore}
	_, ci := input.Env["CI"] // Presence matters, including CI="" and CI="false".
	if selection.Override == nil {
		trigger := FindGVSIncompatibleTrigger(input.Manifests, input.DisableForPackages)
		if trigger != "" && !ci && !input.VirtualStoreOnly {
			disabled := false
			selection.Override = &disabled
			selection.IncompatiblePackage = trigger
		}
	}
	planned := !ci
	if selection.Override != nil {
		planned = *selection.Override
	}
	hoistVeto := input.HoistExplicit != nil && *input.HoistExplicit
	switch {
	case input.Linker == Isolated && planned && !hoistVeto:
		selection.Mode = SharedStore
	case input.ResolvedHoist:
		selection.Mode = DiskWithHiddenTree
	default:
		selection.Mode = DiskWithoutHiddenTree
	}
	return selection, nil
}

func (s MaterializationSelection) PrewarmOverride(linker NodeLinker) *bool {
	if linker == Isolated {
		value := s.Mode.UsesSharedStore()
		return &value
	}
	return s.Override
}

func FindGVSIncompatibleTrigger(manifests []*manifest.Package, patterns []string) string {
	for _, m := range manifests {
		if m == nil {
			continue
		}
		for _, pattern := range patterns {
			for _, deps := range []map[string]string{m.Dependencies, m.DevDependencies, m.OptionalDependencies} {
				for _, name := range slices.Sorted(maps.Keys(deps)) {
					if name == pattern || workspace.Match(pattern, name, false) {
						return name
					}
				}
			}
		}
	}
	return ""
}

// DetectStoreMode prioritizes any symlink/junction in a mixed tree. An empty
// or uninspectable store is unknown, rather than a previous local installation.
func DetectStoreMode(directory string) *bool {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil
	}
	real := false
	for _, entry := range entries {
		name := entry.Name()
		if name == "node_modules" || strings.HasPrefix(name, ".") {
			continue
		}
		path := filepath.Join(directory, name)
		if _, err := os.Readlink(path); err == nil {
			shared := true
			return &shared
		}
		if info, err := os.Lstat(path); err == nil && info.IsDir() {
			real = true
		}
	}
	if real {
		local := false
		return &local
	}
	return nil
}
