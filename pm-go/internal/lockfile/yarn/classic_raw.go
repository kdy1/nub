// Package yarn implements the classic and Berry lockfile formats.
package yarn

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/nubjs/nub/pm-go/internal/identity"
)

type classicBlock struct {
	specs                []string
	fields, dependencies map[string]string
}

func IsBerry(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimLeftFunc(line, unicode.IsSpace), "__metadata:") {
			return true
		}
	}
	return false
}

// IsBerryPath mirrors the reference's bounded byte-level discovery probe.
// Parse-time detection scans the full text and also accepts leading whitespace.
func IsBerryPath(path string) bool {
	return identity.IsBerryPath(path)
}
func tokenizeClassic(content string) ([]classicBlock, error) {
	var blocks []classicBlock
	var current *classicBlock
	inDeps := false
	for index, raw := range strings.Split(content, "\n") {
		line := strings.TrimRightFunc(raw, unicode.IsSpace)
		body := strings.TrimLeftFunc(line, unicode.IsSpace)
		if body == "" || strings.HasPrefix(body, "#") {
			continue
		}
		indent := len(line) - len(body)
		if indent == 0 {
			if current != nil {
				blocks = append(blocks, *current)
			}
			inDeps = false
			if !strings.HasSuffix(line, ":") {
				return nil, fmt.Errorf("line %d: expected block header ending in ':', got '%s'", index+1, line)
			}
			header := strings.TrimSpace(strings.TrimRight(line, ":"))
			specs, err := headerSpecs(header)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", index+1, err)
			}
			current = &classicBlock{specs: specs, fields: map[string]string{}, dependencies: map[string]string{}}
			continue
		}
		if current == nil {
			return nil, fmt.Errorf("line %d: unexpected indented content before any block header", index+1)
		}
		if indent == 2 {
			inDeps = false
			if strings.HasSuffix(body, ":") {
				inDeps = strings.TrimSpace(strings.TrimRight(body, ":")) == "dependencies"
				continue
			}
			key, value, ok := splitKeyValue(body)
			if !ok {
				return nil, fmt.Errorf("line %d: could not parse '%s'", index+1, body)
			}
			current.fields[key] = value
		} else if indent >= 4 && inDeps {
			key, value, ok := splitKeyValue(body)
			if !ok {
				return nil, fmt.Errorf("line %d: could not parse dep '%s'", index+1, body)
			}
			current.dependencies[key] = value
		}
	}
	if current != nil {
		blocks = append(blocks, *current)
	}
	return blocks, nil
}
func headerSpecs(header string) ([]string, error) {
	var specs []string
	for _, part := range strings.Split(header, ",") {
		value := unquote(strings.TrimSpace(part))
		if value == "" {
			return nil, fmt.Errorf("empty spec in header '%s'", header)
		}
		specs = append(specs, value)
	}
	return specs, nil
}
func splitKeyValue(line string) (string, string, bool) {
	i := strings.IndexFunc(line, unicode.IsSpace)
	if i < 0 {
		return "", "", false
	}
	return unquote(line[:i]), unquote(strings.TrimSpace(line[i:])), true
}
func unquote(value string) string {
	if len(value) >= 2 && (value[0] == '"' && value[len(value)-1] == '"' || value[0] == '\'' && value[len(value)-1] == '\'') {
		return value[1 : len(value)-1]
	}
	return value
}
func specName(spec string) (string, bool) {
	start := 0
	if strings.HasPrefix(spec, "@") {
		slash := strings.IndexByte(spec, '/')
		if slash < 0 {
			return "", false
		}
		start = slash + 1
	}
	at := strings.IndexByte(spec[start:], '@')
	if at < 0 {
		return "", false
	}
	return spec[:start+at], true
}
func npmAliasName(spec string) (string, bool) {
	name, ok := specName(spec)
	if !ok {
		return "", false
	}
	body, ok := strings.CutPrefix(spec[len(name)+1:], "npm:")
	if !ok {
		return "", false
	}
	return specName(body)
}
