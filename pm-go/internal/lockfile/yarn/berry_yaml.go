package yarn

import (
	"bytes"
	"fmt"
	"go.yaml.in/yaml/v4"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

func berryDocument(data []byte) (*yaml.Node, error) {
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("invalid UTF-8 in yarn.lock")
	}
	loader, err := yaml.NewLoader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var doc, extra yaml.Node
	if err := loader.Load(&doc); err != nil {
		return nil, err
	}
	if err := loader.Load(&extra); err != io.EOF {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("multiple YAML documents are not supported")
	}
	n := berryNode(&doc)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("yarn berry lockfile root must be a mapping")
	}
	if err := validateBerryYAML(n, map[*yaml.Node]bool{}); err != nil {
		return nil, err
	}
	return n, nil
}

func validateBerryYAML(n *yaml.Node, active map[*yaml.Node]bool) error {
	if n == nil {
		return nil
	}
	if active[n] {
		return fmt.Errorf("recursive YAML alias")
	}
	active[n] = true
	defer delete(active, n)
	if n.Kind == yaml.AliasNode {
		return validateBerryYAML(n.Alias, active)
	}
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := berryNode(n.Content[i])
			if key != nil && key.Kind == yaml.ScalarNode {
				value := key.Value
				if scalar := berryScalar(key); scalar != nil {
					value = *scalar
				}
				id := key.Tag + "\x00" + value
				if seen[id] {
					return fmt.Errorf("duplicate YAML mapping key %q", key.Value)
				}
				seen[id] = true
			}
		}
	}
	for _, child := range n.Content {
		if err := validateBerryYAML(child, active); err != nil {
			return err
		}
	}
	return nil
}
func berryNode(n *yaml.Node) *yaml.Node {
	seen := map[*yaml.Node]bool{}
	for n != nil {
		if seen[n] {
			return nil
		}
		seen[n] = true
		if n.Kind == yaml.DocumentNode {
			if len(n.Content) == 0 {
				return nil
			}
			n = n.Content[0]
		} else if n.Kind == yaml.AliasNode {
			n = n.Alias
		} else {
			break
		}
	}
	return n
}
func berryField(n *yaml.Node, key string) *yaml.Node {
	n = berryNode(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if s := berryString(n.Content[i]); s != nil && *s == key {
			return berryNode(n.Content[i+1])
		}
	}
	return nil
}
func berryString(n *yaml.Node) *string {
	n = berryNode(n)
	if n == nil || n.Kind != yaml.ScalarNode || n.Tag != "!!str" {
		return nil
	}
	s := n.Value
	return &s
}
func berryScalar(n *yaml.Node) *string {
	n = berryNode(n)
	if n == nil || n.Kind != yaml.ScalarNode {
		return nil
	}
	s := n.Value
	switch n.Tag {
	case "!!str":
		return &s
	case "!!bool":
		s = strings.ToLower(s)
	case "!!int":
		var value any
		if err := n.Decode(&value); err != nil {
			return nil
		}
		s = fmt.Sprint(value)
	case "!!float":
		var f float64
		if err := n.Decode(&f); err != nil {
			return nil
		}
		if math.IsNaN(f) {
			s = ".nan"
		} else if math.IsInf(f, 1) {
			s = ".inf"
		} else if math.IsInf(f, -1) {
			s = "-.inf"
		} else {
			s = strconv.FormatFloat(f, 'g', -1, 64)
			if !strings.ContainsAny(s, ".eE") {
				s += ".0"
			}
		}
	default:
		return nil
	}
	return &s
}
func berryDependencies(n *yaml.Node) map[string]string {
	out := map[string]string{}
	n = berryNode(n)
	if n != nil && n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			name, value := berryString(n.Content[i]), berryScalar(n.Content[i+1])
			if name != nil && value != nil {
				out[*name] = *value
			}
		}
	}
	return out
}
