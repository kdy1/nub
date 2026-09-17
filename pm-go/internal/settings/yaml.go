package settings

import (
	"math"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v4"
)

func unalias(n *yaml.Node) *yaml.Node {
	seen := map[*yaml.Node]bool{}
	for n != nil && (n.Kind == yaml.AliasNode || n.Kind == yaml.DocumentNode) {
		if seen[n] {
			return nil
		}
		seen[n] = true
		if n.Kind == yaml.AliasNode {
			n = n.Alias
		} else if len(n.Content) == 1 {
			n = n.Content[0]
		} else {
			return nil
		}
	}
	return n
}

func yamlValue(n *yaml.Node, key string) *yaml.Node {
	for _, part := range strings.Split(key, ".") {
		n = unalias(n)
		if n == nil || n.Kind != yaml.MappingNode || n.Tag != "!!map" {
			return nil
		}
		var next *yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := unalias(n.Content[i])
			if k != nil && k.Tag == "!!str" && k.Value == part {
				next = n.Content[i+1]
			}
		}
		n = next
	}
	return unalias(n)
}

func fromYAML(d definition, root *yaml.Node) any {
	for _, alias := range d.YAML {
		n := yamlValue(root, alias)
		if n == nil {
			continue
		}
		if n.Kind == yaml.ScalarNode && n.Tag == "!!str" {
			if v := parse(d, n.Value); v != nil {
				return v
			}
		}
		switch d.Kind {
		case "bool":
			if n.Tag == "!!bool" {
				var b bool
				if n.Decode(&b) == nil {
					return b
				}
			}
		case "int":
			if n.Tag == "!!int" {
				var v uint64
				if n.Decode(&v) == nil {
					return v
				}
			}
		case "string", "enum":
			if s, ok := yamlScalar(n); ok {
				return s
			}
		case "list":
			if n.Kind == yaml.SequenceNode && n.Tag == "!!seq" {
				list := []string{}
				for _, child := range n.Content {
					v := unalias(child)
					if v != nil && v.Kind == yaml.ScalarNode && v.Tag == "!!str" {
						list = append(list, v.Value)
					}
				}
				return list
			}
		}
	}
	return nil
}

func yamlScalar(n *yaml.Node) (string, bool) {
	if n.Kind != yaml.ScalarNode {
		return "", false
	}
	switch n.Tag {
	case "!!str":
		return n.Value, true
	case "!!bool":
		var v bool
		if n.Decode(&v) == nil {
			return strconv.FormatBool(v), true
		}
	case "!!int":
		var u uint64
		if n.Decode(&u) == nil {
			return strconv.FormatUint(u, 10), true
		}
		var i int64
		if n.Decode(&i) == nil {
			return strconv.FormatInt(i, 10), true
		}
	case "!!float":
		var f float64
		if n.Decode(&f) != nil {
			break
		}
		if math.IsNaN(f) {
			return ".nan", true
		}
		if math.IsInf(f, 1) {
			return ".inf", true
		}
		if math.IsInf(f, -1) {
			return "-.inf", true
		}
		mantissa, exponent, _ := strings.Cut(strconv.FormatFloat(f, 'e', -1, 64), "e")
		e, _ := strconv.Atoi(exponent)
		if e >= -5 && e <= 15 {
			s := strconv.FormatFloat(f, 'f', -1, 64)
			if !strings.Contains(s, ".") {
				s += ".0"
			}
			return s, true
		}
		prefix := "+"
		if e < 0 {
			prefix = ""
		}
		return mantissa + "e" + prefix + strconv.Itoa(e), true
	}
	return "", false
}
