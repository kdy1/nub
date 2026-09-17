package installconfig

import (
	"strconv"

	"github.com/nubjs/nub/pm-go/internal/registry"
	"github.com/nubjs/nub/pm-go/internal/settings"
)

type FrozenMode string

const (
	Frozen FrozenMode = "frozen"
	Prefer FrozenMode = "prefer"
	No     FrozenMode = "no"
	Fix    FrozenMode = "fix"
)

// LockfileFlags are parsed command flags, not resolved scalar settings. The
// parser rejects conflicts; precedence here also matches direct embedded calls.
type LockfileFlags struct{ Frozen, NoFrozen, PreferFrozen bool }

func (f LockfileFlags) Override() FrozenMode {
	switch {
	case f.Frozen:
		return Frozen
	case f.NoFrozen:
		return No
	case f.PreferFrozen:
		return Prefer
	default:
		return ""
	}
}

func (m FrozenMode) Flag() string {
	switch m {
	case Frozen:
		return "--frozen-lockfile"
	case No:
		return "--no-frozen-lockfile"
	case Prefer:
		return "--prefer-frozen-lockfile"
	default:
		return ""
	}
}

func (m FrozenMode) FlagEntry() (settings.Entry, bool) {
	switch m {
	case Frozen:
		return settings.Entry{"frozen-lockfile", "true"}, true
	case No:
		return settings.Entry{"frozen-lockfile", "false"}, true
	case Prefer:
		return settings.Entry{"prefer-frozen-lockfile", "true"}, true
	default:
		return settings.Entry{}, false
	}
}

func ResolveFrozen(override FrozenMode, prefer *bool, lockfileOnly bool, env map[string]string) FrozenMode {
	if override != "" {
		return override
	}
	if prefer != nil {
		if *prefer {
			return Prefer
		}
		return No
	}
	if _, ci := env["CI"]; ci && !lockfileOnly {
		return Frozen
	}
	return Prefer
}

type DepSelection struct{ ProdOnly, DevOnly, SkipOptional bool }

func SelectDeps(prod, dev, noOptional bool) DepSelection {
	return DepSelection{ProdOnly: prod && !dev, DevOnly: dev, SkipOptional: noOptional}
}

func (d DepSelection) ProdOrDevAxis() bool { return d.ProdOnly || d.DevOnly }
func (d DepSelection) IsFiltered() bool    { return d.ProdOrDevAxis() || d.SkipOptional }

func (d DepSelection) Label() string {
	label := ""
	if d.DevOnly {
		label = "--dev"
	} else if d.ProdOnly {
		label = "--prod"
	}
	if d.SkipOptional {
		if label != "" {
			label += " "
		}
		label += "--no-optional"
	}
	return label
}

// InstallRequest holds flags supplied to an install operation. Config adapters
// merge their signals before resolution; unset flags never shadow file sources.
type InstallRequest struct {
	Lockfile                                               LockfileFlags
	Force, FixLockfile, LockfileOnly                       bool
	Prod, Dev, NoOptional                                  bool
	Offline, PreferOffline                                 bool
	ResolutionMode, NodeLinker, LockfileDir                *string
	PackageImportMethod                                    *string
	PublicHoistPattern                                     []string
	ShamefullyHoist, EnableGlobalStore, DisableGlobalStore bool
	NetworkConcurrency                                     *uint64
	VerifyStoreIntegrity, NoVerifyStoreIntegrity           bool
	SideEffectsCache, NoSideEffectsCache                   bool
	AllowAllBuilds                                         bool
}

// CLIEntries is ordered like InstallArgs::to_cli_flag_bag, including repeated
// patterns and inverse flags. Generic --config overrides remain a separate tier.
func (r InstallRequest) CLIEntries() []settings.Entry {
	out := []settings.Entry{}
	for _, item := range []struct {
		key   string
		value *string
	}{{"resolution-mode", r.ResolutionMode}, {"node-linker", r.NodeLinker}, {"lockfile-dir", r.LockfileDir}, {"package-import-method", r.PackageImportMethod}} {
		if item.value != nil {
			out = append(out, settings.Entry{item.key, *item.value})
		}
	}
	for _, pattern := range r.PublicHoistPattern {
		out = append(out, settings.Entry{"public-hoist-pattern", pattern})
	}
	add := func(present bool, key, value string) {
		if present {
			out = append(out, settings.Entry{key, value})
		}
	}
	add(r.ShamefullyHoist, "shamefully-hoist", "true")
	add(r.EnableGlobalStore, "enable-global-virtual-store", "true")
	add(r.DisableGlobalStore, "disable-global-virtual-store", "false")
	if entry, ok := r.Lockfile.Override().FlagEntry(); ok {
		out = append(out, entry)
	}
	if r.NetworkConcurrency != nil {
		out = append(out, settings.Entry{"network-concurrency", strconv.FormatUint(*r.NetworkConcurrency, 10)})
	}
	add(r.VerifyStoreIntegrity, "verify-store-integrity", "true")
	add(r.NoVerifyStoreIntegrity, "verify-store-integrity", "false")
	add(r.SideEffectsCache, "side-effects-cache", "true")
	add(r.NoSideEffectsCache, "side-effects-cache", "false")
	add(r.AllowAllBuilds, "dangerously-allow-all-builds", "true")
	return out
}

type Policy struct {
	Mode                                FrozenMode
	StrictNoLockfile                    bool
	Network                             registry.NetworkMode
	Deps                                DepSelection
	SkipRootLifecycle, RunDevPreinstall bool
}

func (r InstallRequest) Resolve(preferFrozen *bool, env map[string]string) Policy {
	override := r.Lockfile.Override()
	mode := ResolveFrozen(override, preferFrozen, r.LockfileOnly, env)
	if r.FixLockfile {
		mode = Fix
	} else if r.Force && override == "" {
		mode = No
	}
	network := registry.Normal
	if r.Offline {
		network = registry.Offline
	} else if r.PreferOffline {
		network = registry.PreferOffline
	}
	return Policy{Mode: mode, StrictNoLockfile: override == Frozen, Network: network, Deps: SelectDeps(r.Prod, r.Dev, r.NoOptional), RunDevPreinstall: true}
}

// Chained installs keep their caller's default mode and skip root lifecycles.
// CI and preferFrozenLockfile do not silently replace an add/update policy.
func ChainedPolicy(defaultMode FrozenMode, flags LockfileFlags) Policy {
	mode := flags.Override()
	if mode == "" {
		mode = defaultMode
	}
	return Policy{Mode: mode, Network: registry.Normal, SkipRootLifecycle: true}
}
