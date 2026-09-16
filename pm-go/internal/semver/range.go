// Package semver implements npm range syntax. Version ordering uses the
// Masterminds parser; ranges have their own expansion and prerelease rules.
package semver

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	base "github.com/Masterminds/semver/v3"
)

type Version = base.Version

const maxSafeInteger = 9007199254740991

func ParseVersion(raw string) (*Version, error) {
	if len(raw) > 256 {
		return nil, fmt.Errorf("version is too long")
	}
	v, err := base.StrictNewVersion(strings.TrimPrefix(strings.TrimSpace(raw), "v"))
	if err != nil {
		return nil, err
	}
	if v.Major() > maxSafeInteger || v.Minor() > maxSafeInteger || v.Patch() > maxSafeInteger {
		return nil, fmt.Errorf("version component exceeds maximum safe integer")
	}
	return v, nil
}

type comparator struct {
	op      string
	version *Version
}
type Range struct{ sets [][]comparator }
type partial struct {
	numbers    [3]uint64
	specified  int
	pre, build string
}

var partialRE = regexp.MustCompile(`^[v=]?(0|[1-9][0-9]*|[xX*])(?:\.(0|[1-9][0-9]*|[xX*]))?(?:\.(0|[1-9][0-9]*|[xX*]))?(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)
var operatorSpace = regexp.MustCompile(`(<=|>=|~>|[<>=~^])\s+`)

func parsePartial(raw string) (partial, error) {
	var p partial
	m := partialRE.FindStringSubmatch(raw)
	if m == nil {
		return p, fmt.Errorf("invalid range version %q", raw)
	}
	for i := 0; i < 3; i++ {
		if m[i+1] == "" || strings.ContainsAny(m[i+1], "xX*") {
			break
		}
		n, err := strconv.ParseUint(m[i+1], 10, 64)
		if err != nil || n > maxSafeInteger {
			return p, fmt.Errorf("invalid range version %q", raw)
		}
		p.numbers[i], p.specified = n, i+1
	}
	p.pre, p.build = m[4], m[5]
	if _, err := ParseVersion(p.text(false)); err != nil {
		return p, err
	}
	return p, nil
}

func (p partial) text(boundary bool) string {
	s := fmt.Sprintf("%d.%d.%d", p.numbers[0], p.numbers[1], p.numbers[2])
	if boundary {
		return s + "-0"
	}
	if p.pre != "" {
		s += "-" + p.pre
	}
	if p.build != "" {
		s += "+" + p.build
	}
	return s
}

func (p partial) bump(index int) partial {
	p.numbers[index]++
	for i := index + 1; i < 3; i++ {
		p.numbers[i] = 0
	}
	p.pre, p.build = "", ""
	p.specified = 3
	return p
}

func cmp(op string, p partial, boundary bool) (comparator, error) {
	v, err := ParseVersion(p.text(boundary))
	return comparator{op, v}, err
}

func ParseRange(raw string) (*Range, error) {
	r := &Range{}
	for _, part := range strings.Split(raw, "||") {
		fields := strings.Fields(strings.TrimSpace(part))
		var set []comparator
		if len(fields) == 3 && fields[1] == "-" {
			lo, err := parsePartial(fields[0])
			if err != nil {
				return nil, err
			}
			hi, err := parsePartial(fields[2])
			if err != nil {
				return nil, err
			}
			if lo.specified > 0 {
				if lo.specified < 3 {
					lo.pre, lo.build = "", ""
				}
				c, err := cmp(">=", lo, false)
				if err != nil {
					return nil, err
				}
				set = append(set, c)
			}
			if hi.specified > 0 {
				op, boundary := "<=", false
				if hi.specified < 3 {
					hi = hi.bump(hi.specified - 1)
					op, boundary = "<", true
				}
				c, err := cmp(op, hi, boundary)
				if err != nil {
					return nil, err
				}
				set = append(set, c)
			}
		} else {
			for _, field := range strings.Fields(operatorSpace.ReplaceAllString(part, "$1")) {
				cs, err := expand(field)
				if err != nil {
					return nil, err
				}
				set = append(set, cs...)
			}
		}
		r.sets = append(r.sets, set)
	}
	return r, nil
}

func expand(raw string) ([]comparator, error) {
	op, version := "", raw
	for _, candidate := range []string{"<=", ">=", "~>", "<", ">", "=", "~", "^"} {
		if rest, ok := strings.CutPrefix(raw, candidate); ok {
			op, version = candidate, rest
			break
		}
	}
	p, err := parsePartial(version)
	if err != nil {
		return nil, err
	}
	var out []comparator
	add := func(op string, p partial, boundary bool) error {
		c, err := cmp(op, p, boundary)
		if err == nil {
			out = append(out, c)
		}
		return err
	}
	if p.specified == 0 {
		if op == "<" || op == ">" {
			err = add("<", partial{}, true)
		}
		return out, err
	}
	if p.specified < 3 {
		p.pre, p.build = "", ""
	}
	switch op {
	case "~", "~>", "^":
		upper := 0
		if p.specified > 1 {
			if op != "^" {
				upper = 1
			} else if p.numbers[0] == 0 {
				upper = 1
				if p.specified == 3 && p.numbers[1] == 0 {
					upper = 2
				}
			}
		}
		if err = add(">=", p, false); err == nil {
			err = add("<", p.bump(upper), true)
		}
	case "", "=":
		if p.specified == 3 {
			err = add("=", p, false)
		} else if err = add(">=", p, false); err == nil {
			err = add("<", p.bump(p.specified-1), true)
		}
	default:
		boundary := false
		if p.specified < 3 {
			switch op {
			case ">":
				op, p = ">=", p.bump(p.specified-1)
			case "<=":
				op, p, boundary = "<", p.bump(p.specified-1), true
			case "<":
				boundary = true
			}
		}
		err = add(op, p, boundary)
	}
	return out, err
}

func (r *Range) Contains(v *Version) bool {
	if r == nil || v == nil {
		return false
	}
	for _, set := range r.sets {
		ok, allowPre := true, v.Prerelease() == ""
		for _, c := range set {
			order := v.Compare(c.version)
			switch c.op {
			case "=":
				ok = order == 0
			case ">":
				ok = order > 0
			case ">=":
				ok = order >= 0
			case "<":
				ok = order < 0
			case "<=":
				ok = order <= 0
			}
			if !ok {
				break
			}
			if c.version.Prerelease() != "" && c.version.Major() == v.Major() && c.version.Minor() == v.Minor() && c.version.Patch() == v.Patch() {
				allowPre = true
			}
		}
		if ok && allowPre {
			return true
		}
	}
	return false
}

func Satisfies(version, rangeText string) bool {
	v, err := ParseVersion(version)
	if err != nil {
		return false
	}
	r, err := ParseRange(rangeText)
	return err == nil && r.Contains(v)
}
