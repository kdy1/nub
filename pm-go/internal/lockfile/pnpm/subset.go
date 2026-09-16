package pnpm

import (
	"errors"
	"strings"

	"go.yaml.in/yaml/v4"
)

// The reference's native-layout parser is observable: repeated fields it
// handles directly use the last value, whereas delegated YAML structs reject
// duplicates. Preserve its dispatch boundary as well as the resulting graph.
type subsetParser struct {
	lines  []string
	pos    int
	failed bool
}
type subsetLine struct {
	index, indent int
	body          string
}

func (p *subsetParser) peek() (subsetLine, bool) {
	for i := p.pos; i < len(p.lines); i++ {
		line := p.lines[i]
		indent := len(line) - len(strings.TrimLeft(line, " "))
		body := line[indent:]
		if body == "" || body[0] == '#' {
			continue
		}
		return subsetLine{i, indent, body}, true
	}
	return subsetLine{}, false
}
func (p *subsetParser) consume(line subsetLine) { p.pos = line.index + 1 }
func (p *subsetParser) block(indent int) string {
	start, end := p.pos, p.pos
	for {
		line, ok := p.peek()
		if !ok || line.indent < indent {
			break
		}
		p.consume(line)
		end = p.pos
	}
	if end == start {
		return ""
	}
	var out strings.Builder
	for _, line := range p.lines[start:end] {
		if line == "" {
			out.WriteByte('\n')
			continue
		}
		spaces := len(line) - len(strings.TrimLeft(line, " "))
		if spaces < indent {
			p.failed = true
			return ""
		}
		out.WriteString(line[indent:])
		out.WriteByte('\n')
	}
	return out.String()
}
func splitSubsetKey(body string) (string, *string, bool) {
	single, double := false, false
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '\'':
			if !double {
				single = !single
			}
		case '"':
			if !single {
				double = !double
			}
		case ':':
			if !single && !double {
				if i+1 == len(body) {
					return body[:i], nil, true
				}
				if body[i+1] == ' ' {
					v := strings.TrimRight(body[i+2:], " \t")
					return body[:i], &v, true
				}
			}
		}
	}
	return "", nil, false
}
func subsetString(s string) (string, bool) {
	if s == "" {
		return "", true
	}
	switch s[0] {
	case '\'':
		if len(s) < 2 || s[len(s)-1] != '\'' {
			return "", false
		}
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'"), true
	case '"':
		if len(s) < 2 || s[len(s)-1] != '"' || strings.Contains(s[1:len(s)-1], "\\") {
			return "", false
		}
		return s[1 : len(s)-1], true
	case '{', '[', '&', '*':
		return "", false
	}
	for i := 0; i < len(s); i++ {
		if s[i] == '#' && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t') {
			s = strings.TrimRight(s[:i], " \t")
			if s == "" {
				return "", false
			}
			break
		}
	}
	return s, true
}
func (p *subsetParser) string(s *string) string {
	if s == nil {
		p.failed = true
		return ""
	}
	v, ok := subsetString(*s)
	if !ok {
		p.failed = true
	}
	return v
}
func (p *subsetParser) key(s string) string { return p.string(&s) }
func fragment(s string) (*yaml.Node, error) {
	if strings.TrimSpace(s) == "" {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: ""}, nil
	}
	var node yaml.Node
	if err := yaml.Load([]byte(s), &node); err != nil {
		return nil, err
	}
	var err error
	r := (yamlReader{&node, "fragment", &err}).resolve()
	return r.node, err
}
func (p *subsetParser) decode(s string, fn func(yamlReader)) {
	node, err := fragment(s)
	if err == nil {
		fn(yamlReader{node, "fragment", &err})
	}
	if err != nil {
		p.failed = true
	}
}
func (p *subsetParser) strings(inline *string, indent int) map[string]string {
	out := map[string]string{}
	if inline != nil {
		p.failed = true
		return out
	}
	for {
		line, ok := p.peek()
		if !ok || line.indent < indent {
			break
		}
		if line.indent != indent {
			p.failed = true
			break
		}
		key, v, ok := splitSubsetKey(line.body)
		if !ok {
			p.failed = true
			break
		}
		p.consume(line)
		out[p.key(key)] = p.string(v)
	}
	if len(out) == 0 {
		p.failed = true
	}
	return out
}
func (p *subsetParser) sequence(inline *string, indent int) []string {
	s := ""
	if inline != nil {
		s = *inline
	} else {
		s = p.block(indent)
	}
	var out []string
	p.decode(s, func(r yamlReader) {
		for _, v := range r.sequence(false) {
			out = append(out, v.text())
		}
	})
	return out
}
func (p *subsetParser) deps(indent int) map[string]rawDep {
	out := map[string]rawDep{}
	for {
		line, ok := p.peek()
		if !ok || line.indent < indent {
			break
		}
		if line.indent != indent {
			p.failed = true
			break
		}
		key, v, ok := splitSubsetKey(line.body)
		if !ok || v != nil {
			p.failed = true
			break
		}
		p.consume(line)
		name := p.key(key)
		d := rawDep{}
		hasSpec, hasVersion := false, false
		for {
			line, ok := p.peek()
			if !ok || line.indent < indent+2 {
				break
			}
			if line.indent != indent+2 {
				p.failed = true
				break
			}
			key, v, ok := splitSubsetKey(line.body)
			if !ok {
				p.failed = true
				break
			}
			p.consume(line)
			switch key {
			case "specifier":
				d.specifier = p.string(v)
				hasSpec = true
			case "version":
				d.version = p.string(v)
				hasVersion = true
			default:
				p.failed = true
			}
			if p.failed {
				break
			}
		}
		if !hasSpec || !hasVersion {
			p.failed = true
		}
		out[name] = d
		if p.failed {
			break
		}
	}
	if len(out) == 0 {
		p.failed = true
	}
	return out
}
func (p *subsetParser) importer() *rawImporter {
	out := &rawImporter{}
	for {
		line, ok := p.peek()
		if !ok || line.indent < 4 {
			break
		}
		if line.indent != 4 {
			p.failed = true
			break
		}
		key, v, ok := splitSubsetKey(line.body)
		if !ok || v != nil {
			p.failed = true
			break
		}
		p.consume(line)
		deps := p.deps(6)
		switch key {
		case "dependencies":
			out.deps = deps
		case "devDependencies":
			out.dev = deps
		case "optionalDependencies":
			out.optional = deps
		case "skippedOptionalDependencies":
			out.skipped = deps
		default:
			p.failed = true
		}
		if p.failed {
			break
		}
	}
	return out
}
func (p *subsetParser) snapshot() *rawSnapshot {
	out := &rawSnapshot{deps: map[string]string{}, optional: map[string]string{}}
	for {
		line, ok := p.peek()
		if !ok || line.indent < 4 {
			break
		}
		if line.indent != 4 {
			p.failed = true
			break
		}
		key, v, ok := splitSubsetKey(line.body)
		if !ok {
			p.failed = true
			break
		}
		p.consume(line)
		switch key {
		case "dependencies":
			out.deps = p.strings(v, 6)
		case "optionalDependencies":
			out.optional = p.strings(v, 6)
		case "optional":
			if v == nil || (*v != "true" && *v != "false") {
				p.failed = true
			} else {
				out.isOptional = *v == "true"
			}
		case "bundledDependencies":
			out.bundled = p.sequence(v, 6)
		case "transitivePeerDependencies":
			out.transitivePeers = p.sequence(v, 6)
		default:
			p.failed = true
		}
		if p.failed {
			break
		}
	}
	return out
}
func (p *subsetParser) entries(section string, raw *rawLock) {
	for {
		line, ok := p.peek()
		if !ok || line.indent < 2 {
			break
		}
		if line.indent != 2 {
			p.failed = true
			break
		}
		key, v, ok := splitSubsetKey(line.body)
		if !ok || v != nil && *v != "{}" {
			p.failed = true
			break
		}
		p.consume(line)
		name := p.key(key)
		switch section {
		case "importers":
			if v != nil {
				raw.importers[name] = &rawImporter{}
			} else {
				raw.importers[name] = p.importer()
			}
		case "snapshots":
			if v != nil {
				raw.snapshots[name] = &rawSnapshot{deps: map[string]string{}, optional: map[string]string{}}
			} else {
				raw.snapshots[name] = p.snapshot()
			}
		case "packages":
			block := "{}"
			if v == nil {
				block = p.block(4)
				if block == "" {
					block = "{}"
				}
			}
			p.decode(block, func(r yamlReader) { raw.packages[name] = decodePackage(r) })
		}
		if p.failed {
			break
		}
	}
}
func trySubset(data []byte) *rawLock {
	lines := yamlLines(string(data))
	for _, line := range lines {
		if rest, ok := strings.CutPrefix(line, "---"); ok && (rest == "" || strings.ContainsRune(" \t#", rune(rest[0]))) {
			return nil
		}
	}
	p := subsetParser{lines: lines}
	var decodeErr error
	raw := decodeRaw(yamlReader{&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, "lockfile", &decodeErr})
	for {
		line, ok := p.peek()
		if !ok {
			break
		}
		if line.indent != 0 {
			return nil
		}
		key, v, ok := splitSubsetKey(line.body)
		if !ok {
			return nil
		}
		p.consume(line)
		switch key {
		case "lockfileVersion":
			if v == nil {
				return nil
			}
			node, err := fragment(*v)
			if err != nil {
				return nil
			}
			raw.version = node
		case "packageExtensionsChecksum":
			s := p.string(v)
			raw.extensionChecksum = &s
		case "pnpmfileChecksum":
			s := p.string(v)
			raw.hookChecksum = &s
		case "overrides":
			raw.overrides = p.strings(v, 2)
		case "time":
			raw.times = p.strings(v, 2)
		case "ignoredOptionalDependencies":
			raw.ignored = p.sequence(v, 2)
		case "importers", "packages", "snapshots":
			if v != nil {
				return nil
			}
			switch key {
			case "importers":
				raw.importers = map[string]*rawImporter{}
			case "packages":
				raw.packages = map[string]*rawPackage{}
			case "snapshots":
				raw.snapshots = map[string]*rawSnapshot{}
			}
			p.entries(key, raw)
		case "settings", "catalogs", "patchedDependencies":
			if v != nil {
				return nil
			}
			p.decode(p.block(2), func(r yamlReader) {
				if r.absent() {
					*r.err = errors.New("missing block")
					return
				}
				// These blocks use the typed general decoder even on the fast path.
				wrapper := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, r.node}}
				part := decodeRaw(yamlReader{wrapper, "lockfile", r.err})
				switch key {
				case "settings":
					raw.settings = part.settings
				case "catalogs":
					raw.catalogs = part.catalogs
				case "patchedDependencies":
					raw.patches = part.patches
				}
			})
		default:
			return nil
		}
		if p.failed {
			return nil
		}
	}
	if raw.version == nil {
		return nil
	}
	return raw
}
