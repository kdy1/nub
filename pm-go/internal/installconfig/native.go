package installconfig

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
	"github.com/nubjs/nub/pm-go/internal/settings"
)

// NativeInstall is the validated install block of an existing nub.jsonc file.
// Optional lists distinguish absence from an authored empty list. The enclosing
// file's discovery, scope validation and non-install fields belong to its reader.
type NativeInstall struct {
	linker                         *nativeLinker
	publicHoist, minimumAgeExclude *[]string
	minimumAgeSeconds              *uint64
}

type nativeLinker struct {
	strategy string
	eject    *[]string
	hoist    any // nil, bool, or []string; constructed only by the validator.
}

type NativeConfigError struct {
	Path, Key, Expected, Message, File string
	UnknownKey                         bool
}

func (e *NativeConfigError) Error() string {
	file := e.File
	if file == "" {
		file = "nub.jsonc"
	}
	if e.UnknownKey {
		return fmt.Sprintf("unknown key `%s` in %s of %s", e.Key, e.Path, file)
	}
	if e.Expected != "" {
		return fmt.Sprintf("`%s` in %s must be %s", e.Path, file, e.Expected)
	}
	return fmt.Sprintf("`%s` in %s: %s", e.Path, file, e.Message)
}

func nativeType(path, expected string) error {
	return &NativeConfigError{Path: path, Expected: expected}
}
func nativeValue(path, message string) error { return &NativeConfigError{Path: path, Message: message} }

// ParseNativeInstall validates the install node, including keys that were
// replaced by the discriminated linker shape. It never silently ignores them.
func ParseNativeInstall(v *jsonvalue.Value) (NativeInstall, error) {
	var result NativeInstall
	if v == nil || v.Kind != '{' {
		return result, nativeType("install", "an object")
	}
	for _, f := range v.Object {
		if !slices.Contains([]string{"linker", "publicHoist", "minimumReleaseAge", "minimumReleaseAgeExclude"}, f.Key) {
			return result, &NativeConfigError{Path: "install", Key: f.Key, UnknownKey: true}
		}
	}
	var err error
	if value := v.Get("linker"); value != nil {
		result.linker, err = parseNativeLinker(value, "install.linker")
		if err != nil {
			return NativeInstall{}, err
		}
	}
	if value := v.Get("publicHoist"); value != nil {
		result.publicHoist, err = nativeStrings(value, "install.publicHoist")
		if err != nil {
			return NativeInstall{}, err
		}
	}
	if value := v.Get("minimumReleaseAge"); value != nil {
		if value.Kind != 's' {
			return NativeInstall{}, nativeType("install.minimumReleaseAge", "a string")
		}
		seconds, e := ParseAgeDuration(value.Text(), "install.minimumReleaseAge")
		if e != nil {
			return NativeInstall{}, e
		}
		result.minimumAgeSeconds = &seconds
	}
	if value := v.Get("minimumReleaseAgeExclude"); value != nil {
		result.minimumAgeExclude, err = nativeStrings(value, "install.minimumReleaseAgeExclude")
		if err != nil {
			return NativeInstall{}, err
		}
	}
	return result, nil
}

func nativeStrings(v *jsonvalue.Value, path string) (*[]string, error) {
	if v.Kind != '[' {
		return nil, nativeType(path, "an array of strings")
	}
	result := make([]string, 0, len(v.Array))
	for _, item := range v.Array {
		if item.Kind != 's' {
			return nil, nativeType(path, "a string")
		}
		result = append(result, item.Text())
	}
	return &result, nil
}

func parseNativeLinker(v *jsonvalue.Value, path string) (*nativeLinker, error) {
	strategyPath := path
	strategy := ""
	if v.Kind == 's' {
		strategy = v.Text()
	} else {
		if v.Kind != '{' {
			return nil, nativeType(path, "an object")
		}
		strategyPath += ".strategy"
		s := v.Get("strategy")
		if s == nil {
			return nil, nativeValue(path, "missing `strategy` (one of \"global-virtual-store\", \"isolated\", \"hoisted\", or \"pnp\")")
		}
		if s.Kind != 's' {
			return nil, nativeType(strategyPath, "a string")
		}
		strategy = s.Text()
	}
	if !slices.Contains([]string{"global-virtual-store", "isolated", "hoisted", "pnp"}, strategy) {
		return nil, nativeValue(strategyPath, fmt.Sprintf("unknown strategy `%s` (expected \"global-virtual-store\", \"isolated\", \"hoisted\", or \"pnp\"); `%s` accepts either that string or an object with a `strategy` key", strategy, path))
	}
	result := &nativeLinker{strategy: strategy}
	if v.Kind == 's' {
		return result, nil
	}
	allowed := []string{"strategy"}
	if strategy == "global-virtual-store" {
		allowed = append(allowed, "eject")
	}
	if strategy == "isolated" {
		allowed = append(allowed, "hoist")
	}
	for _, field := range v.Object {
		if slices.Contains(allowed, field.Key) {
			continue
		}
		owner := map[string]string{"eject": "global-virtual-store", "hoist": "isolated"}[field.Key]
		if owner != "" {
			return nil, nativeValue(path+"."+field.Key, fmt.Sprintf("not valid with `strategy: \"%s\"` — it configures the \"%s\" layout. Either switch to `strategy: \"%s\"` or drop this key.", strategy, owner, owner))
		}
		slices.Sort(allowed)
		return nil, nativeValue(path+"."+field.Key, fmt.Sprintf("unknown key (`strategy: \"%s\"` accepts `%s`)", strategy, strings.Join(allowed, "`, `")))
	}
	var err error
	if v := v.Get("eject"); v != nil {
		result.eject, err = nativeStrings(v, path+".eject")
	}
	if v := v.Get("hoist"); v != nil {
		switch v.Kind {
		case 'b':
			result.hoist = v.Scalar.(bool)
		case '[':
			var values *[]string
			values, err = nativeStrings(v, path+".hoist")
			if err == nil {
				result.hoist = *values
			}
		default:
			err = nativeType(path+".hoist", "a boolean or array of strings")
		}
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ParseAgeDuration keeps the file grammar separate from pnpm's CLI allowance
// for bare minute counts. uint64 seconds avoid Go time.Duration's shorter range.
func ParseAgeDuration(raw, path string) (uint64, error) {
	invalid := func(message string) (uint64, error) {
		return 0, nativeValue(path, fmt.Sprintf("invalid duration `%s` — %s", raw, message))
	}
	if raw == "" {
		return invalid("empty")
	}
	multiplier := map[byte]uint64{'s': 1, 'm': 60, 'h': 3600, 'd': 86400, 'w': 604800}[raw[len(raw)-1]]
	if multiplier == 0 {
		return invalid("expected an integer followed by a unit s|m|h|d|w (e.g. \"3d\")")
	}
	digits := raw[:len(raw)-1]
	if digits == "" {
		return invalid("missing the integer amount")
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return invalid("the amount must be a non-negative integer")
		}
	}
	n, err := strconv.ParseUint(digits, 10, 64)
	if err != nil || n > math.MaxUint64/multiplier {
		return invalid("overflows")
	}
	return n * multiplier, nil
}

// OverlayNativeInstall uses replacement at the authored field boundary; a
// project linker object replaces the complete global strategy and its options.
func OverlayNativeInstall(base, overlay NativeInstall) NativeInstall {
	if overlay.linker != nil {
		base.linker = overlay.linker
	}
	if overlay.publicHoist != nil {
		base.publicHoist = overlay.publicHoist
	}
	if overlay.minimumAgeSeconds != nil {
		base.minimumAgeSeconds = overlay.minimumAgeSeconds
	}
	if overlay.minimumAgeExclude != nil {
		base.minimumAgeExclude = overlay.minimumAgeExclude
	}
	return base
}

// Lower emits the projectConfig tier and the native phantom-ejection seed.
// Layout applies under every PM; release age applies only to Nub identity.
func (n NativeInstall) Lower(defaults []settings.Entry, nativeMode, projectLocal bool) ([]settings.Entry, []string, error) {
	entries := []settings.Entry{}
	eject := []string{}
	push := func(k, v string) { entries = append(entries, settings.Entry{k, v}) }
	if l := n.linker; l != nil {
		switch l.strategy {
		case "pnp":
			return nil, nil, fmt.Errorf("nub: `install.linker: \"pnp\"` is reserved and not supported yet [ERR_NUB_CONFIG_UNSUPPORTED]")
		case "hoisted":
			push("nodeLinker", "hoisted")
		case "isolated":
			push("nodeLinker", "isolated")
			push("enableGlobalVirtualStore", "false")
			switch h := l.hoist.(type) {
			case bool:
				push("hoist", strconv.FormatBool(h))
				if h {
					push("hoistPattern", "*")
				}
			case []string:
				push("hoist", "true")
				push("hoistPattern", strings.Join(h, ","))
			}
		case "global-virtual-store":
			push("nodeLinker", "isolated")
			if !slices.Contains(defaults, settings.Entry{"hoist", "true"}) {
				push("enableGlobalVirtualStore", "true")
			}
			if l.eject != nil {
				eject = append(eject, (*l.eject)...)
			}
		}
	}
	if n.publicHoist != nil {
		push("shamefullyHoist", "false")
		push("publicHoistPattern", strings.Join(*n.publicHoist, ","))
	}
	if n.linker != nil && n.linker.eject != nil {
		for _, key := range []string{"disableGlobalVirtualStoreForPackages", "diskMaterializePackages"} {
			merged := []string{}
			for i := len(defaults) - 1; i >= 0; i-- {
				if defaults[i][0] == key {
					for _, v := range strings.Split(defaults[i][1], ",") {
						if v != "" {
							merged = append(merged, v)
						}
					}
					break
				}
			}
			for _, v := range eject {
				if !slices.Contains(merged, v) {
					merged = append(merged, v)
				}
			}
			push(key, strings.Join(merged, ","))
		}
	}
	if nativeMode {
		if s := n.minimumAgeSeconds; s != nil {
			minutes := *s / 60
			if *s%60 != 0 {
				minutes++
			}
			push("minimumReleaseAge", strconv.FormatUint(minutes, 10))
			push("minimumReleaseAgeStrict", "true")
		}
		if n.minimumAgeExclude != nil {
			push("minimumReleaseAgeExclude", strings.Join(*n.minimumAgeExclude, ","))
		}
	}
	if projectLocal {
		entries = slices.DeleteFunc(entries, func(e settings.Entry) bool { return e == (settings.Entry{"enableGlobalVirtualStore", "true"}) })
	}
	return entries, eject, nil
}
