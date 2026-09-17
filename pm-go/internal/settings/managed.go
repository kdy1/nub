package settings

import (
	"slices"
	"strings"
)

func (c Context) harden(d definition, local any) any {
	if d.Managed == "" {
		return local
	}
	var values []any
	for _, entry := range c.Managed {
		if entry[0] == d.Name || slices.Contains(d.Npmrc, entry[0]) {
			if v := parse(d, entry[1]); v != nil {
				values = append(values, v)
			}
		}
	}
	if len(values) == 0 {
		return local
	}
	switch d.Kind {
	case "bool":
		enforced := d.Managed == "trueWins"
		if d.Managed != "trueWins" && d.Managed != "falseWins" {
			return local
		}
		for _, value := range values {
			if value == enforced {
				if local != nil && local != enforced {
					c.warn(d.Name)
				}
				return enforced
			}
		}
	case "int":
		if d.Managed != "max" {
			return local
		}
		max := values[0].(uint64)
		for _, value := range values[1:] {
			if n := value.(uint64); n > max {
				max = n
			}
		}
		if n, ok := local.(uint64); ok {
			if n >= max {
				return local
			}
			c.warn(d.Name)
		}
		return max
	case "list":
		if d.Managed != "managedWins" {
			return local
		}
		managed := values[0].([]string)
		for _, value := range values[1:] {
			managed = slices.DeleteFunc(managed, func(s string) bool { return !slices.Contains(value.([]string), s) })
		}
		if list, ok := local.([]string); ok {
			for _, s := range list {
				if !slices.Contains(managed, s) {
					c.warn(d.Name)
					break
				}
			}
		}
		return managed
	case "string", "enum":
		if d.Managed == "managedWins" {
			if local != nil && local != values[0] {
				c.warn(d.Name)
			}
			return values[0]
		}
		if rankSpec, ok := strings.CutPrefix(d.Managed, "ranked:"); ok {
			ranks := strings.Split(rankSpec, "<")
			for i := range ranks {
				ranks[i] = strings.TrimSpace(ranks[i])
			}
			rank := func(v any) int {
				s, ok := v.(string)
				if !ok {
					return -1
				}
				return slices.Index(ranks, asciiLower(strings.TrimSpace(s)))
			}
			var best any
			bestRank := -1
			for _, v := range values {
				if r := rank(v); r >= 0 && r >= bestRank {
					best, bestRank = v, r
				}
			}
			if bestRank >= 0 && bestRank > rank(local) {
				if local != nil {
					c.warn(d.Name)
				}
				return best
			}
		}
	}
	return local
}
