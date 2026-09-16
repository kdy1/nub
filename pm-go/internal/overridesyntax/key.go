// Package overridesyntax parses the shared pnpm/Yarn selector grammar.
package overridesyntax

import "strings"

func Split(key string) ([]string, bool) {
	var pnpm, out []string
	start := 0
	for i := 0; i < len(key); i++ {
		if key[i] != '>' {
			continue
		}
		if i == 0 {
			return nil, false
		}
		if !strings.ContainsRune(" |@", rune(key[i-1])) {
			if start == i {
				return nil, false
			}
			pnpm = append(pnpm, key[start:i])
			start = i + 1
		}
	}
	if start >= len(key) {
		return nil, false
	}
	pnpm = append(pnpm, key[start:])
	for _, part := range pnpm {
		start = 0
		for i := 0; i < len(part); i++ {
			if part[i] != '/' {
				continue
			}
			current := part[start:i]
			scope := strings.HasPrefix(current, "@") && !strings.Contains(current[1:], "/")
			if !scope {
				if current == "" {
					return nil, false
				}
				out = append(out, current)
				start = i + 1
			}
		}
		if start == len(part) {
			return nil, false
		}
		out = append(out, part[start:])
	}
	return out, true
}
func ParseSegment(s string) (string, *string, bool) {
	if s == "**" {
		return "", nil, false
	}
	start := 0
	if strings.HasPrefix(s, "@") {
		slash := strings.IndexByte(s, '/')
		if slash < 0 || slash == len(s)-1 {
			return "", nil, false
		}
		start = slash + 1
	}
	if at := strings.IndexByte(s[start:], '@'); at >= 0 {
		if at == 0 || start+at == len(s)-1 {
			return "", nil, false
		}
		req := s[start+at+1:]
		return s[:start+at], &req, true
	}
	return s, nil, true
}
