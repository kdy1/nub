package settings

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v4"
)

// ParseYAMLSource reads the raw string-keyed map consumed by settings. Root
// duplicate keys overwrite; nested YAML Value mappings reject duplicate keys.
// It validates the entire source, including values no known setting consumes.
func ParseYAMLSource(data []byte) (*yaml.Node, error) {
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("invalid UTF-8 in settings YAML")
	}
	loader, err := yaml.NewLoader(bytes.NewReader(data), yaml.WithUniqueKeys(false))
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := loader.Load(&doc); err != nil && err != io.EOF {
		return nil, err
	}
	var extra yaml.Node
	if err := loader.Load(&extra); err != io.EOF {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("settings YAML must contain one document")
	}
	root := unalias(&doc)
	if root == nil || root.Kind == yaml.ScalarNode && root.Value == "" && root.Style&^yaml.TaggedStyle == 0 {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, nil
	}
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("settings YAML requires a mapping")
	}
	var events func(*yaml.Node) int
	events = func(n *yaml.Node) int {
		count := 1
		if n.Kind == yaml.MappingNode || n.Kind == yaml.SequenceNode {
			count++
		}
		for _, child := range n.Content {
			count += events(child)
		}
		return count
	}
	r := yamlSourceReader{aliasLimit: events(&doc) * 100, active: map[*yaml.Node]bool{}}
	result, _, err := r.read(root, 0, true)
	return result, err
}

type yamlSourceReader struct {
	aliases, aliasLimit int
	active              map[*yaml.Node]bool
}

func (r *yamlSourceReader) read(n *yaml.Node, depth int, root bool) (*yaml.Node, string, error) {
	if n == nil || r.active[n] {
		return nil, "", fmt.Errorf("recursive YAML alias")
	}
	r.active[n] = true
	defer delete(r.active, n)
	if n.Kind == yaml.AliasNode {
		r.aliases++
		if r.aliases > r.aliasLimit {
			return nil, "", fmt.Errorf("YAML repetition limit exceeded")
		}
		return r.read(n.Alias, depth, root)
	}
	out := *n
	out.Content = nil
	if n.Kind == yaml.ScalarNode {
		underlying, err := referenceScalar(n)
		if err != nil {
			return nil, "", err
		}
		out = underlying
		tag := ""
		if localYAMLTag(n.Tag) {
			tag = n.Tag
			out = *n
		}
		value := underlying.Value
		if underlying.Tag == "!!float" && (value == "-0" || value == "0") {
			value = "0"
		}
		return &out, yamlKey([]string{tag, underlying.Tag, value}), nil
	}
	if depth >= 128 {
		return nil, "", fmt.Errorf("YAML recursion limit exceeded")
	}
	tag := ""
	if !root && localYAMLTag(n.Tag) {
		tag = n.Tag
	}
	switch n.Kind {
	case yaml.SequenceNode:
		keys := []string{tag, "sequence"}
		for _, child := range n.Content {
			value, key, err := r.read(child, depth+1, false)
			if err != nil {
				return nil, "", err
			}
			out.Content = append(out.Content, value)
			keys = append(keys, key)
		}
		if tag == "" {
			out.Tag = "!!seq"
		}
		return &out, yamlKey(keys), nil
	case yaml.MappingNode:
		positions := map[string]int{}
		pairs := map[string]string{}
		for i := 0; i < len(n.Content); i += 2 {
			var keyNode *yaml.Node
			var key string
			var err error
			if root {
				k := unalias(n.Content[i])
				if k == nil || k.Kind != yaml.ScalarNode {
					return nil, "", fmt.Errorf("settings YAML requires scalar root keys")
				}
				keyNode = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k.Value}
				key = k.Value
			} else {
				keyNode, key, err = r.read(n.Content[i], depth+1, false)
				if err != nil {
					return nil, "", err
				}
				if _, exists := positions[key]; exists {
					return nil, "", fmt.Errorf("duplicate YAML mapping key")
				}
			}
			value, valueKey, err := r.read(n.Content[i+1], depth+1, false)
			if err != nil {
				return nil, "", err
			}
			pairs[key] = yamlKey([]string{key, valueKey})
			if index, exists := positions[key]; exists {
				out.Content[index+1] = value
			} else {
				positions[key] = len(out.Content)
				out.Content = append(out.Content, keyNode, value)
			}
		}
		keys := make([]string, 0, len(pairs))
		for _, pair := range pairs {
			keys = append(keys, pair)
		}
		slices.Sort(keys)
		if tag == "" {
			out.Tag = "!!map"
		}
		return &out, yamlKey(append([]string{tag, "mapping"}, keys...)), nil
	default:
		return nil, "", fmt.Errorf("invalid YAML node")
	}
}

func localYAMLTag(tag string) bool {
	return strings.HasPrefix(tag, "!") && !strings.HasPrefix(tag, "!!")
}
func yamlKey(parts []string) string {
	data, _ := json.Marshal(parts)
	// Child identities must stay bounded. Embedding an escaped JSON string at
	// each parent would double its escapes repeatedly for deeply nested input.
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// referenceScalar applies yaml_serde's Value schema instead of Go's implicit
// octal/timestamp rules, and rejects integers dispatched to unsupported i128 or
// u128 visitors. Values larger than those widths fall through to finite floats.
func referenceScalar(n *yaml.Node) (yaml.Node, error) {
	out := *n
	raw := n.Value
	explicit := n.Style&yaml.TaggedStyle != 0 && !localYAMLTag(n.Tag)
	forced := ""
	if explicit {
		switch n.Tag {
		case "!!int", "!!float", "!!bool", "!!null":
			forced = n.Tag
		default:
			out.Tag = "!!str"
			return out, nil
		}
	} else if n.Style&^(yaml.TaggedStyle) != 0 {
		out.Tag = "!!str"
		return out, nil
	}
	result := func(tag, value string) (yaml.Node, error) {
		out.Tag, out.Value = tag, value
		return out, nil
	}
	if forced == "" || forced == "!!null" {
		if raw == "" && forced == "" || raw == "null" || raw == "Null" || raw == "NULL" || raw == "~" {
			return result("!!null", "null")
		}
	}
	if forced == "" || forced == "!!bool" {
		switch raw {
		case "true", "True", "TRUE":
			return result("!!bool", "true")
		case "false", "False", "FALSE":
			return result("!!bool", "false")
		}
	}
	unsigned := strings.TrimPrefix(strings.TrimPrefix(raw, "+"), "-")
	leadingZero := len(unsigned) > 1 && unsigned[0] == '0' && asciiDigits(unsigned, 10)
	if (forced == "" || forced == "!!int") && !leadingZero {
		digits, base := unsigned, 10
		if len(digits) > 2 {
			switch digits[:2] {
			case "0x":
				digits, base = digits[2:], 16
			case "0o":
				digits, base = digits[2:], 8
			case "0b":
				digits, base = digits[2:], 2
			}
		}
		if asciiDigits(digits, base) && !strings.HasPrefix(raw, "+-") {
			number, ok := new(big.Int).SetString(digits, base)
			if ok {
				if strings.HasPrefix(raw, "-") {
					number.Neg(number)
				}
				if number.IsInt64() || number.Sign() >= 0 && number.IsUint64() {
					return result("!!int", number.String())
				}
				fits128 := number.Sign() >= 0 && number.BitLen() <= 128 || number.Sign() < 0 && new(big.Int).Neg(number).BitLen() <= 127 || number.Cmp(new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), 127))) == 0
				if fits128 {
					return out, fmt.Errorf("YAML integer is outside the Value model")
				}
			}
		}
	}
	if (forced == "" && !leadingZero || forced == "!!float") && !strings.ContainsAny(raw, "_xXoObB \t\r\n") {
		switch strings.TrimPrefix(raw, "+") {
		case ".inf", ".Inf", ".INF":
			return result("!!float", ".inf")
		case "-.inf", "-.Inf", "-.INF":
			if !strings.HasPrefix(raw, "+") {
				return result("!!float", "-.inf")
			}
		}
		if raw == ".nan" || raw == ".NaN" || raw == ".NAN" {
			return result("!!float", ".nan")
		}
		if f, err := strconv.ParseFloat(raw, 64); err == nil && !math.IsInf(f, 0) && !math.IsNaN(f) {
			return result("!!float", strconv.FormatFloat(f, 'g', -1, 64))
		}
	}
	if forced != "" {
		return out, fmt.Errorf("invalid scalar for YAML tag %s", forced)
	}
	return result("!!str", raw)
}

func asciiDigits(s string, base int) bool {
	if s == "" {
		return false
	}
	for _, c := range []byte(s) {
		value := int(c - '0')
		if c >= 'a' && c <= 'f' {
			value = int(c-'a') + 10
		}
		if c >= 'A' && c <= 'F' {
			value = int(c-'A') + 10
		}
		if value < 0 || value >= base {
			return false
		}
	}
	return true
}
