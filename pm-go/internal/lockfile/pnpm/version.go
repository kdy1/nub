package pnpm

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v4"
)

func versionMajor(n *yaml.Node) (uint64, bool) {
	if n == nil || n.Kind != yaml.ScalarNode {
		return 0, false
	}
	if n.Tag == "!!str" {
		parts := strings.Split(strings.TrimSpace(n.Value), ".")
		for _, p := range parts[1:] {
			if p == "" {
				return 0, false
			}
			for _, c := range p {
				if c < '0' || c > '9' {
					return 0, false
				}
			}
		}
		major, err := strconv.ParseUint(strings.TrimPrefix(parts[0], "+"), 10, 64)
		return major, err == nil
	}
	if n.Tag == "!!int" {
		var v uint64
		if n.Decode(&v) == nil {
			return v, true
		}
	}
	if n.Tag == "!!float" || n.Tag == "!!int" {
		var v float64
		if n.Decode(&v) != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return 0, false
		}
		if v >= float64(math.MaxUint64) {
			return math.MaxUint64, true
		}
		return uint64(v), true
	}
	return 0, false
}
func versionText(n *yaml.Node) string {
	if n == nil || n.Kind != yaml.ScalarNode {
		return "(unreadable)"
	}
	if n.Tag == "!!str" {
		return n.Value
	}
	if n.Tag == "!!int" {
		var v any
		if n.Decode(&v) == nil {
			return fmt.Sprint(v)
		}
	}
	if n.Tag == "!!float" {
		var v float64
		if n.Decode(&v) == nil {
			return strconv.FormatFloat(v, 'f', -1, 64)
		}
	}
	return "(unreadable)"
}
