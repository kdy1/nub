// Package workspace implements member discovery and selection semantics.
package workspace

import (
	"runtime"
	"strings"
)

func MatchesMember(path string, patterns []string) bool {
	for _, part := range strings.Split(path, "/") {
		if part == "node_modules" {
			return false
		}
	}
	matched := false
	for _, raw := range patterns {
		pattern, negated := strings.CutPrefix(raw, "!")
		for _, expanded := range ExpandBraces(pattern) {
			if negated {
				if Match(expanded, path, false) {
					return false
				}
				if parent, ok := strings.CutSuffix(expanded, "/**"); ok && Match(parent, path, false) {
					return false
				}
			} else {
				recursive := strings.Contains(expanded, "**")
				if !recursive {
					expanded = normalize(expanded)
				}
				matched = matched || Match(expanded, path, !recursive)
			}
		}
	}
	return matched
}

func normalize(pattern string) string {
	var components []string
	for _, part := range strings.Split(pattern, "/") {
		if part == "" || part == "." {
			continue
		}
		if part == ".." && len(components) > 0 && components[len(components)-1] != ".." {
			components = components[:len(components)-1]
		} else {
			components = append(components, part)
		}
	}
	return strings.Join(components, "/")
}
func ExpandBraces(pattern string) []string {
	chars := []rune(pattern)
	depth, open := 0, -1
	var commas []int
	for i, char := range chars {
		switch char {
		case '{':
			if depth == 0 {
				open = i
				commas = nil
			}
			depth++
		case ',':
			if depth == 1 {
				commas = append(commas, i)
			}
		case '}':
			if depth == 0 {
				continue
			}
			depth--
			if depth != 0 || open < 0 {
				continue
			}
			if len(commas) == 0 {
				open = -1
				continue
			}
			boundaries := append(append([]int{open}, commas...), i)
			var out []string
			for j := 0; j+1 < len(boundaries); j++ {
				expanded := string(chars[:open]) + string(chars[boundaries[j]+1:boundaries[j+1]]) + string(chars[i+1:])
				out = append(out, ExpandBraces(expanded)...)
			}
			return out
		}
	}
	return []string{pattern}
}

type charRange struct{ low, high rune }
type token struct {
	kind    byte
	literal rune
	ranges  []charRange
}

func separator(r rune) bool { return r == '/' || runtime.GOOS == "windows" && r == '\\' }
func equal(a, b rune) bool {
	return a == b || runtime.GOOS == "windows" && separator(a) && separator(b)
}

func tokenize(pattern string) ([]token, bool) {
	chars := []rune(pattern)
	var tokens []token
	for i := 0; i < len(chars); {
		c := chars[i]
		switch c {
		case '?':
			tokens = append(tokens, token{kind: '?'})
			i++
		case '*':
			start := i
			for i < len(chars) && chars[i] == '*' {
				i++
			}
			if i-start > 2 {
				return nil, false
			}
			if i-start == 1 {
				tokens = append(tokens, token{kind: '*'})
				continue
			}
			if start > 0 && !separator(chars[start-1]) {
				return nil, false
			}
			if i < len(chars) {
				if !separator(chars[i]) {
					return nil, false
				}
				i++
			}
			tokens = append(tokens, token{kind: 'R'})
		case '[':
			start := i + 1
			kind := byte('[')
			if start < len(chars) && chars[start] == '!' {
				kind = '!'
				start++
			}
			// The first ']' in a class is a literal member.
			end := start + 1
			for end < len(chars) && chars[end] != ']' {
				end++
			}
			if end >= len(chars) {
				return nil, false
			}
			t := token{kind: kind}
			for j := start; j < end; {
				if j+2 < end && chars[j+1] == '-' {
					t.ranges = append(t.ranges, charRange{chars[j], chars[j+2]})
					j += 3
				} else {
					t.ranges = append(t.ranges, charRange{chars[j], chars[j]})
					j++
				}
			}
			tokens = append(tokens, t)
			i = end + 1
		default:
			tokens = append(tokens, token{kind: 'c', literal: c})
			i++
		}
	}
	return tokens, true
}

// Match mirrors glob::Pattern's case-sensitive matching. Separators may be
// wildcard-matched only when literalSeparator is false. A recursive wildcard
// occupies a whole component and consumes directory prefixes or a final tail.
func Match(pattern, path string, literalSeparator bool) bool {
	tokens, ok := tokenize(pattern)
	if !ok {
		return false
	}
	chars := []rune(path)
	type position struct{ token, char int }
	memo := map[position]bool{}
	seen := map[position]bool{}
	var match func(int, int) bool
	match = func(ti, ci int) bool {
		key := position{ti, ci}
		if seen[key] {
			return memo[key]
		}
		seen[key] = true
		result := false
		defer func() { memo[key] = result }()
		if ti == len(tokens) {
			result = ci == len(chars)
			return result
		}
		t := tokens[ti]
		switch t.kind {
		case '*', 'R':
			if match(ti+1, ci) {
				result = true
				return result
			}
			if t.kind == 'R' && ti == len(tokens)-1 {
				result = true
				return result
			}
			for i := ci; i < len(chars); i++ {
				if t.kind == '*' && literalSeparator && separator(chars[i]) {
					break
				}
				if t.kind == 'R' && !separator(chars[i]) {
					continue
				}
				if match(ti+1, i+1) {
					result = true
					return result
				}
			}
		default:
			if ci == len(chars) {
				return false
			}
			c := chars[ci]
			accept := false
			if t.kind == 'c' {
				accept = equal(c, t.literal)
			} else if !(literalSeparator && separator(c)) {
				if t.kind == '?' {
					accept = true
				} else {
					for _, r := range t.ranges {
						if r.low == r.high && equal(c, r.low) || r.low <= c && c <= r.high {
							accept = true
							break
						}
					}
					if t.kind == '!' {
						accept = !accept
					}
				}
			}
			if accept {
				result = match(ti+1, ci+1)
			}
		}
		return result
	}
	return match(0, 0)
}
