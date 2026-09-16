package pnpm

import (
	"fmt"
	"go.yaml.in/yaml/v4"
	"strings"
)

// yamlReader preserves serde's typed scalar rules: a requested String takes
// scalar spelling (including numbers), but booleans and tolerant string lists
// inspect the scalar's YAML type. Unknown fields do not become graph metadata.
type yamlReader struct {
	node *yaml.Node
	path string
	err  *error
}

func (r yamlReader) fail(want string) {
	if *r.err == nil {
		*r.err = fmt.Errorf("%s: expected %s", r.path, want)
	}
}
func (r yamlReader) resolve() yamlReader {
	seen := map[*yaml.Node]bool{}
	for r.node != nil && (r.node.Kind == yaml.AliasNode || r.node.Kind == yaml.DocumentNode) {
		if seen[r.node] {
			r.fail("non-recursive YAML alias")
			r.node = nil
			break
		}
		seen[r.node] = true
		if r.node.Kind == yaml.AliasNode {
			r.node = r.node.Alias
		} else if len(r.node.Content) == 1 {
			r.node = r.node.Content[0]
		} else {
			r.node = nil
		}
	}
	return r
}
func (r yamlReader) absent() bool { r = r.resolve(); return r.node == nil || r.node.Tag == "!!null" }
func (r yamlReader) object(optional bool, known string) map[string]yamlReader {
	r = r.resolve()
	out := map[string]yamlReader{}
	if optional && r.absent() {
		return out
	}
	if r.node == nil || r.node.Kind != yaml.MappingNode {
		r.fail("a mapping")
		return out
	}
	for i := 0; i < len(r.node.Content); i += 2 {
		k := yamlReader{r.node.Content[i], r.path + ".<key>", r.err}.text()
		if _, exists := out[k]; exists && strings.Contains(" "+known+" ", " "+k+" ") {
			r.fail("unique field " + k)
		}
		out[k] = yamlReader{r.node.Content[i+1], r.path + "." + k, r.err}
	}
	return out
}
func field(m map[string]yamlReader, key string, parent yamlReader) yamlReader {
	if r, ok := m[key]; ok {
		return r
	}
	return yamlReader{nil, parent.path + "." + key, parent.err}
}
func (r yamlReader) text() string {
	r = r.resolve()
	if r.node == nil || r.node.Kind != yaml.ScalarNode {
		r.fail("a string")
		return ""
	}
	return r.node.Value
}
func (r yamlReader) optionalText() *string {
	if r.absent() {
		return nil
	}
	s := r.text()
	return &s
}
func (r yamlReader) boolean(optional bool) *bool {
	r = r.resolve()
	if optional && r.absent() {
		return nil
	}
	if r.node == nil || r.node.Kind != yaml.ScalarNode || r.node.Tag != "!!bool" {
		r.fail("a boolean")
		return nil
	}
	b := strings.EqualFold(r.node.Value, "true")
	return &b
}
func (r yamlReader) sequence(optional bool) []yamlReader {
	r = r.resolve()
	if optional && r.absent() {
		return nil
	}
	if r.node == nil || r.node.Kind != yaml.SequenceNode {
		r.fail("a sequence")
		return nil
	}
	out := make([]yamlReader, len(r.node.Content))
	for i, n := range r.node.Content {
		out[i] = yamlReader{n, fmt.Sprintf("%s[%d]", r.path, i), r.err}
	}
	return out
}
func (r yamlReader) strings() map[string]string {
	out := map[string]string{}
	for k, v := range r.object(true, "") {
		out[k] = v.text()
	}
	return out
}
func (r yamlReader) stringList() []string {
	var out []string
	for _, v := range r.sequence(true) {
		out = append(out, v.text())
	}
	return out
}
func (r yamlReader) platforms() []string {
	r = r.resolve()
	if r.node == nil {
		return nil
	}
	if r.node.Kind == yaml.ScalarNode && r.node.Tag == "!!str" {
		return []string{r.node.Value}
	}
	var out []string
	if r.node.Kind == yaml.SequenceNode {
		for _, n := range r.node.Content {
			v := (yamlReader{n, r.path, r.err}).resolve()
			if v.node != nil && v.node.Kind == yaml.ScalarNode && v.node.Tag == "!!str" {
				out = append(out, v.node.Value)
			}
		}
	}
	return out
}
func (r yamlReader) engines() map[string]string {
	r = r.resolve()
	out := map[string]string{}
	if r.node == nil || r.node.Kind != yaml.MappingNode {
		return out
	}
	for k, v := range r.object(false, "") {
		v = v.resolve()
		if v.node != nil && v.node.Kind == yaml.ScalarNode && v.node.Tag == "!!str" {
			out[k] = v.node.Value
		}
	}
	return out
}
