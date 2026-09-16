package semver

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	base "github.com/Masterminds/semver/v3"
)

// EngineRange preserves node-semver 2.2.0 (the baseline Rust crate) semantics.
// ParseRange remains the strict npm/node-semver grammar. The engine accepts
// recoverable garbage and retains disjoint adjacent bounds as separate sets.
// Keeping the two APIs explicit lets tests record differences without changing
// the independent npm oracle's expectations.
type EngineRange struct{ sets []engineSet }
type engineBound struct {
	version   *Version
	inclusive bool
}
type engineSet struct{ lower, upper engineBound }
type enginePartial struct {
	numbers    [3]uint64
	present    [3]bool
	pre, build string
}

var enginePartialRE = regexp.MustCompile(`^v?[ \t]*([0-9]+|[xX*])(?:\.([0-9]+|[xX*]))?(?:\.([0-9]+|[xX*])(?:-?([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?)?$`)
var engineVersionRE = regexp.MustCompile(`^[vV]?[ \t]*([0-9]+)\.([0-9]+)\.([0-9]+)(?:-?([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

func ParseEngineVersion(raw string) (*Version, error) {
	if len(raw) > 256 {
		return nil, fmt.Errorf("version is too long")
	}
	p, err := enginePartialFromMatch(engineVersionRE.FindStringSubmatch(raw))
	if err != nil {
		return nil, err
	}
	return p.version(false)
}
func enginePartialFromMatch(m []string) (enginePartial, error) {
	var p enginePartial
	if m == nil {
		return p, fmt.Errorf("invalid engine version")
	}
	for i := 0; i < 3; i++ {
		if m[i+1] == "" || strings.ContainsAny(m[i+1], "xX*") {
			continue
		}
		n, err := strconv.ParseUint(m[i+1], 10, 64)
		if err != nil || n > maxSafeInteger {
			return p, fmt.Errorf("invalid engine version component")
		}
		p.numbers[i], p.present[i] = n, true
	}
	p.pre, p.build = engineIdentifiers(m[4]), engineIdentifiers(m[5])
	return p, nil
}
func engineIdentifiers(raw string) string {
	parts := strings.Split(raw, ".")
	for i, part := range parts {
		if n, err := strconv.ParseUint(part, 10, 64); err == nil {
			parts[i] = strconv.FormatUint(n, 10)
		}
	}
	return strings.Join(parts, ".")
}
func (p enginePartial) version(boundary bool) (*Version, error) {
	s := fmt.Sprintf("%d.%d.%d", p.numbers[0], p.numbers[1], p.numbers[2])
	if boundary {
		s += "-0"
	} else if p.pre != "" {
		s += "-" + p.pre
	}
	if p.build != "" {
		s += "+" + p.build
	}
	return base.StrictNewVersion(s)
}
func (p enginePartial) bump(index int) enginePartial {
	p.numbers[index]++
	for i := index + 1; i < 3; i++ {
		p.numbers[i] = 0
	}
	p.pre, p.build = "", ""
	return p
}
func engineInterval(p enginePartial, op string, boundary bool) (engineSet, bool) {
	v, err := p.version(boundary)
	if err != nil {
		return engineSet{}, false
	}
	b := engineBound{v, op == "=" || op == ">=" || op == "<="}
	switch op {
	case "=":
		return engineSet{b, b}, true
	case ">", ">=":
		return engineSet{lower: b}, true
	case "<", "<=":
		return engineSet{upper: b}, true
	}
	return engineSet{}, false
}
func engineWindow(lower, upper enginePartial, includeLower bool) (engineSet, bool) {
	lo, ok := engineInterval(lower, ">=", false)
	if !includeLower {
		lo = engineSet{}
		ok = true
	}
	hi, valid := engineInterval(upper, "<", true)
	if !ok || !valid {
		return engineSet{}, false
	}
	return intersectEngine(lo, hi)
}

func engineSimple(raw string) (engineSet, bool) {
	op, tail := "", raw
	for _, candidate := range []string{"<=", ">=", "~>", "<", ">", "=", "~", "^"} {
		if rest, ok := strings.CutPrefix(raw, candidate); ok {
			op, tail = candidate, rest
			break
		}
	}
	p, err := enginePartialFromMatch(enginePartialRE.FindStringSubmatch(tail))
	if err != nil {
		return engineSet{}, false
	}
	switch op {
	case "", "=":
		if op == "" && !p.present[0] {
			return engineInterval(enginePartial{}, ">=", false)
		}
		if !p.present[0] {
			return engineSet{}, false
		}
		if !p.present[1] {
			p.numbers[1], p.numbers[2], p.pre, p.build = 0, 0, "", ""
			return engineWindow(p, p.bump(0), true)
		}
		if !p.present[2] {
			p.numbers[2], p.pre, p.build = 0, "", ""
			return engineWindow(p, p.bump(1), true)
		}
		return engineInterval(p, "=", false)
	case ">=":
		return engineInterval(p, op, false)
	case ">":
		if p.present[0] && p.present[1] && !p.present[2] {
			return engineInterval(p.bump(1), ">=", false)
		}
		if p.present[0] && !p.present[1] && !p.present[2] {
			return engineInterval(p.bump(0), ">=", false)
		}
		return engineInterval(p, op, false)
	case "<":
		return engineInterval(p, op, p.present[0] && p.present[1] && !p.present[2])
	case "<=":
		return engineInterval(p, op, !p.present[2])
	case "~", "~>", "^":
		if !p.present[0] {
			return engineSet{}, false
		}
		upper := 0
		if p.present[1] {
			if op != "^" || p.numbers[0] == 0 {
				upper = 1
			}
			if op == "^" && p.numbers[0] == 0 && p.numbers[1] == 0 && p.present[2] {
				upper = 2
			}
		}
		includeLower := !(op == "^" && p.numbers[0] == 0 && !p.present[1] && !p.present[2])
		return engineWindow(p, p.bump(upper), includeLower)
	}
	return engineSet{}, false
}
func engineHyphen(lower, upper string) (engineSet, bool) {
	lo, err := enginePartialFromMatch(enginePartialRE.FindStringSubmatch(lower))
	if err != nil {
		return engineSet{}, false
	}
	hi, err := enginePartialFromMatch(enginePartialRE.FindStringSubmatch(upper))
	if err != nil {
		return engineSet{}, false
	}
	l, ok := engineInterval(lo, ">=", false)
	var u engineSet
	var valid bool
	if !hi.present[0] && !hi.present[1] && !hi.present[2] {
		u, valid = engineInterval(enginePartial{}, "<", true)
	} else if hi.present[0] && !hi.present[1] && !hi.present[2] {
		u, valid = engineInterval(hi.bump(0), "<", true)
	} else if hi.present[0] && hi.present[1] && !hi.present[2] {
		u, valid = engineInterval(hi.bump(1), "<", true)
	} else {
		u, valid = engineInterval(hi, "<=", false)
	}
	if !ok || !valid {
		return engineSet{}, false
	}
	return intersectEngine(l, u)
}

func ParseEngineRange(raw string) (*EngineRange, error) {
	r := &EngineRange{}
	for _, part := range strings.Split(raw, "||") {
		fields := strings.FieldsFunc(part, func(c rune) bool { return c == ' ' || c == '\t' })
		var accumulated []engineSet
		for i := 0; i < len(fields); i++ {
			token := fields[i]
			// A comparator, tilde or v prefix can be separated from its partial.
			if token == "~" && i+1 < len(fields) && fields[i+1] == ">" {
				token = "~>"
				i++
			}
			if strings.Contains("|<|>|<=|>=|=|~|~>|^|v|", "|"+token+"|") && i+1 < len(fields) {
				i++
				token += fields[i]
			}
			var set engineSet
			var ok bool
			if i+2 < len(fields) && fields[i+1] == "-" {
				set, ok = engineHyphen(token, fields[i+2])
				i += 2
			} else {
				set, ok = engineSimple(token)
			}
			if !ok {
				continue
			}
			if len(accumulated) > 0 {
				last := len(accumulated) - 1
				if intersection, ok := intersectEngine(accumulated[last], set); ok {
					accumulated[last] = intersection
					continue
				}
			}
			accumulated = append(accumulated, set)
		}
		r.sets = append(r.sets, accumulated...)
	}
	if len(r.sets) == 0 {
		return nil, fmt.Errorf("no valid engine ranges could be parsed")
	}
	return r, nil
}
func intersectEngine(a, b engineSet) (engineSet, bool) {
	lo, hi := a.lower, a.upper
	if lo.version == nil || b.lower.version != nil && (b.lower.version.GreaterThan(lo.version) || b.lower.version.Equal(lo.version) && !b.lower.inclusive) {
		lo = b.lower
	}
	if hi.version == nil || b.upper.version != nil && (b.upper.version.LessThan(hi.version) || b.upper.version.Equal(hi.version) && !b.upper.inclusive) {
		hi = b.upper
	}
	if lo.version != nil && hi.version != nil {
		c := lo.version.Compare(hi.version)
		if c > 0 || c == 0 && !(lo.inclusive && hi.inclusive) {
			return engineSet{}, false
		}
	}
	return engineSet{lo, hi}, true
}
func (r *EngineRange) Contains(v *Version) bool {
	if r == nil || v == nil {
		return false
	}
	for _, set := range r.sets {
		lo, hi := set.lower, set.upper
		if lo.version != nil && (v.LessThan(lo.version) || v.Equal(lo.version) && !lo.inclusive) {
			continue
		}
		if hi.version != nil && (v.GreaterThan(hi.version) || v.Equal(hi.version) && !hi.inclusive) {
			continue
		}
		if v.Prerelease() == "" {
			return true
		}
		for _, bound := range []engineBound{lo, hi} {
			if b := bound.version; b != nil && b.Prerelease() != "" && b.Major() == v.Major() && b.Minor() == v.Minor() && b.Patch() == v.Patch() {
				return true
			}
		}
	}
	return false
}
func (r *EngineRange) AllowsAny(other *EngineRange) bool {
	if r == nil || other == nil {
		return false
	}
	before := func(upper, lower engineBound) bool {
		if upper.version == nil || lower.version == nil {
			return false
		}
		order := upper.version.Compare(lower.version)
		// The reference Bound ordering treats equal exclusive endpoints as
		// overlapping in allows_any, even though intersect rejects them.
		return order < 0 || order == 0 && upper.inclusive != lower.inclusive
	}
	for _, a := range r.sets {
		for _, b := range other.sets {
			if !before(a.upper, b.lower) && !before(b.upper, a.lower) {
				return true
			}
		}
	}
	return false
}
func EngineSatisfies(version, requested string) bool {
	v, err := ParseEngineVersion(version)
	if err != nil {
		return false
	}
	if strings.TrimSpace(requested) == "" {
		requested = "*"
	}
	r, err := ParseEngineRange(requested)
	return err == nil && r.Contains(v)
}
