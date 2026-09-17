package installconfig

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/linker"
	"github.com/nubjs/nub/pm-go/internal/manifest"
	"github.com/nubjs/nub/pm-go/internal/settings"
)

type LayoutInput struct {
	Settings                   settings.Context
	Project, Home, GlobalStore string
	Manifests                  []*manifest.Package
	Env                        map[string]string
}

type Layout struct {
	NodeLinker                                                NodeLinker
	Materialization                                           MaterializationSelection
	ModulesDirName, VirtualStoreDir, GlobalStore              string
	EnableModulesDir, VirtualStoreOnly                        bool
	MaxFilenameLength                                         int
	HoistingLimits                                            linker.HoistingLimits
	HoistWorkspacePackages, ShamefullyHoist, DedupeDirectDeps bool
	HoistPatterns, PublicHoistPatterns, DiskMaterialize       []string
}

// ResolveLayout folds the same settings into mode selection and linker inputs,
// before any installation mutation. Settings must include NubDefaults; storage
// path selection and workspace discovery are owned by the session caller.
func ResolveLayout(in LayoutInput) (Layout, error) {
	var out Layout
	if !filepath.IsAbs(in.Project) || in.Home != "" && !filepath.IsAbs(in.Home) || in.GlobalStore != "" && !filepath.IsAbs(in.GlobalStore) {
		return out, fmt.Errorf("layout settings require absolute invocation paths")
	}
	c := in.Settings
	mode, err := ResolveNodeLinker(c.CLIString("nodeLinker"), *c.String("nodeLinker"))
	if err != nil {
		return out, err
	}
	hoist, _ := c.Explicit("hoist").(bool)
	var explicitHoist *bool
	if c.Explicit("hoist") != nil {
		explicitHoist = &hoist
	}
	selection, err := SelectMaterialization(MaterializationInput{Linker: mode, EnableGlobalVirtualStore: c.Bool("enableGlobalVirtualStore"), HoistExplicit: explicitHoist, ResolvedHoist: *c.Bool("hoist"), Env: in.Env, Manifests: in.Manifests, DisableForPackages: c.Strings("disableGlobalVirtualStoreForPackages"), VirtualStoreOnly: *c.Bool("virtualStoreOnly")})
	if err != nil {
		return out, err
	}
	out = Layout{NodeLinker: mode, Materialization: selection, ModulesDirName: *c.String("modulesDir"), GlobalStore: in.GlobalStore, EnableModulesDir: *c.Bool("enableModulesDir"), VirtualStoreOnly: *c.Bool("virtualStoreOnly"), MaxFilenameLength: 120, HoistWorkspacePackages: *c.Bool("hoistWorkspacePackages"), ShamefullyHoist: *c.Bool("shamefullyHoist"), DedupeDirectDeps: *c.Bool("dedupeDirectDeps"), HoistPatterns: c.Strings("hoistPattern"), PublicHoistPatterns: c.Strings("publicHoistPattern"), DiskMaterialize: c.Strings("diskMaterializePackages")}
	if max := c.Uint64("virtualStoreDirMaxLength"); max != nil {
		limit := uint64(^uint(0) >> 1)
		if *max < limit {
			limit = *max
		}
		out.MaxFilenameLength = int(limit)
	}
	switch *c.String("hoistingLimits") {
	case "workspaces":
		out.HoistingLimits = linker.HoistWorkspaces
	case "dependencies":
		out.HoistingLimits = linker.HoistDependencies
	}
	out.VirtualStoreDir = resolveVirtualStore(c, in.Project, in.Home, out.ModulesDirName)
	return out, nil
}

// Apply carries the folded GVS/hidden-tree choice into the linker. The caller
// supplies graph-aware phantom closure and exact-version local ejection after
// this step, and handles EnableModulesDir before invoking either linker.
func (l Layout) Apply(p linker.IsolatedPlan) linker.IsolatedPlan {
	p.ModulesDirName, p.VirtualStoreDir, p.GlobalVirtualStoreDir = l.ModulesDirName, l.VirtualStoreDir, l.GlobalStore
	p.MaxFilenameLength = l.MaxFilenameLength
	p.UseGlobalVirtualStore = l.Materialization.Mode.UsesSharedStore()
	hoist, workspace := l.Materialization.Mode.BuildsHiddenTree(), l.HoistWorkspacePackages
	p.Hoist, p.HoistWorkspacePackages = &hoist, &workspace
	p.HoistPatterns, p.PublicHoistPatterns = slices.Clone(l.HoistPatterns), slices.Clone(l.PublicHoistPatterns)
	p.ShamefullyHoist, p.DedupeDirectDeps, p.VirtualStoreOnly = l.ShamefullyHoist, l.DedupeDirectDeps, l.VirtualStoreOnly
	p.DiskMaterialize = slices.Clone(l.DiskMaterialize)
	return p
}

func resolveVirtualStore(c settings.Context, project, home, modules string) string {
	fallback := filepath.Join(project, modules, ".nub")
	explicit := false
	for _, entries := range [][]settings.Entry{c.ProjectToolConfig, c.ProjectNpmrc, c.UserToolConfig, c.UserNpmrc} {
		for _, e := range entries {
			if e[0] == "virtualStoreDir" || e[0] == "virtual-store-dir" {
				explicit = true
			}
		}
	}
	// Keep the reference's explicit-source predicate. In ordinary Nub sessions
	// the native default always selects this branch, including custom modulesDir.
	for _, e := range c.Env {
		if slices.Contains([]string{"npm_config_virtual_store_dir", "NPM_CONFIG_VIRTUAL_STORE_DIR", "AUBE_VIRTUAL_STORE_DIR"}, e[0]) {
			explicit = true
		}
	}
	for _, e := range c.Defaults {
		if e[0] == "virtualStoreDir" {
			explicit = true
		}
	}
	if !explicit {
		return fallback
	}
	if path, ok := expandPath(*c.String("virtualStoreDir"), project, home); ok {
		return path
	}
	return fallback
}

func expandPath(raw, project, home string) (string, bool) {
	if raw == "~" || strings.HasPrefix(raw, "~/") {
		if home == "" {
			return "", false
		}
		if raw == "~" {
			return home, true
		}
		raw = filepath.Join(home, raw[2:])
	}
	if filepath.IsAbs(raw) {
		return raw, true
	}
	return filepath.Join(project, raw), true
}

// ResolveImportStrategy preserves strict CLI validation and the engine's clone
// warning. The caller's auto probe chooses the project or shared-store volume.
func ResolveImportStrategy(c settings.Context, auto func() linker.Strategy) (linker.Strategy, error) {
	method := *c.String("packageImportMethod")
	cli := c.CLIString("packageImportMethod")
	if cli != nil {
		method = asciiLower(strings.TrimSpace(*cli))
	}
	switch method {
	case "", "auto":
		if auto == nil {
			return 0, fmt.Errorf("automatic package import requires a filesystem probe")
		}
		return auto(), nil
	case "hardlink":
		return linker.Hardlink, nil
	case "copy":
		return linker.Copy, nil
	case "clone", "clone-or-copy":
		if method == "clone" && c.Warn != nil {
			c.Warn("WARN_AUBE_CLONE_STRATEGY_FALLBACK", "package-import-method=clone: reflink will silently fall back to copy if the filesystem does not support it (strict enforcement is a known TODO)")
		}
		return linker.Reflink, nil
	default:
		return 0, fmt.Errorf("unknown --package-import-method value `%s`; expected `auto`, `hardlink`, `copy`, `clone`, or `clone-or-copy`", method)
	}
}
