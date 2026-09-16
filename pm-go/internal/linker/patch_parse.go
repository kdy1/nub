package linker

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

type patchSection struct {
	path     *string
	body     string
	deletion bool
}

func splitPatchSections(text string) []patchSection {
	var out []patchSection
	var path, oldPath *string
	var body strings.Builder
	inBody, deletion := false, false
	var remaining *[2]int
	flush := func() {
		if body.Len() > 0 || deletion {
			out = append(out, patchSection{path, body.String(), deletion})
			body.Reset()
			deletion = false
		}
		path = nil
	}
	for _, line := range strings.SplitAfter(text, "\n") {
		if line == "" {
			continue
		}
		line = strings.TrimRight(line, "\r\n")
		if rest, ok := strings.CutPrefix(line, "diff --git "); ok {
			flush()
			inBody, remaining, oldPath = false, nil, nil
			path = diffGitPath(rest)
			continue
		}
		if header, ok := strings.CutPrefix(line, "--- "); ok && (!inBody || remaining == nil) {
			if inBody {
				flush()
			}
			inBody, remaining, oldPath = true, nil, unifiedHeaderPath(header)
			body.WriteString(line + "\n")
			continue
		}
		if !inBody {
			continue
		}
		if header, ok := strings.CutPrefix(line, "+++ "); ok {
			if path == nil {
				path = unifiedHeaderPath(header)
				if path == nil {
					path = oldPath
				}
			}
			if header == "/dev/null" {
				deletion = true
				continue
			}
			if oldPath == nil && path != nil {
				body.Reset()
				body.WriteString("--- a/" + *path + "\n")
			}
		}
		body.WriteString(line + "\n")
		if counts := hunkLineCounts(line); counts != nil {
			remaining = counts
			if *counts == [2]int{} {
				remaining = nil
			}
		} else if remaining != nil {
			if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "-") {
				remaining[0] = max(0, remaining[0]-1)
			}
			if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "+") {
				remaining[1] = max(0, remaining[1]-1)
			}
			if *remaining == [2]int{} {
				remaining = nil
			}
		}
	}
	flush()
	return out
}

func diffGitPath(rest string) *string {
	if after, ok := strings.CutPrefix(rest, `"a/`); ok {
		end := strings.Index(after, `" "b/`)
		if end < 0 {
			return nil
		}
		after = after[end+5:]
		close := strings.LastIndexByte(after, '"')
		if close < 0 {
			return nil
		}
		if decoded, ok := unescapeGitQuoted(after[:close]); ok {
			return &decoded
		}
		return nil
	}
	body, ok := strings.CutPrefix(rest, "a/")
	if !ok {
		return nil
	}
	for start := 0; start < len(body); {
		index := strings.Index(body[start:], " b/")
		if index < 0 {
			break
		}
		index += start
		a, b := body[:index], body[index+3:]
		if a == b {
			return &b
		}
		start = index + 1
	}
	if _, after, ok := strings.Cut(body, " b/"); ok {
		return &after
	}
	return nil
}

func unescapeGitQuoted(s string) (string, bool) {
	var out []byte
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			out = append(out, s[i])
			continue
		}
		i++
		if i == len(s) {
			return "", false
		}
		var ch byte
		switch s[i] {
		case '\\', '"':
			ch = s[i]
		case 'n':
			ch = '\n'
		case 'r':
			ch = '\r'
		case 't':
			ch = '\t'
		case 'a':
			ch = 7
		case 'b':
			ch = 8
		case 'f':
			ch = 12
		case 'v':
			ch = 11
		default:
			if s[i] < '0' || s[i] > '3' || i+2 >= len(s) || s[i+1] < '0' || s[i+1] > '7' || s[i+2] < '0' || s[i+2] > '7' {
				return "", false
			}
			ch = (s[i]-'0')<<6 | (s[i+1]-'0')<<3 | (s[i+2] - '0')
			i += 2
		}
		out = append(out, ch)
	}
	return string(out), utf8.Valid(out)
}

func unifiedHeaderPath(header string) *string {
	path, _, _ := strings.Cut(header, "\t")
	if path == "/dev/null" {
		return nil
	}
	if strings.HasPrefix(path, "a/") || strings.HasPrefix(path, "b/") {
		path = path[2:]
	}
	return &path
}

func patchCount(value string) (int, error) {
	value = strings.TrimPrefix(value, "+")
	n, err := strconv.ParseUint(value, 10, strconv.IntSize-1)
	return int(n), err
}

func hunkLineCounts(header string) *[2]int {
	ranges, ok := strings.CutPrefix(header, "@@ -")
	if !ok {
		return nil
	}
	ranges, _, ok = strings.Cut(ranges, " @@")
	if !ok {
		return nil
	}
	old, new, ok := strings.Cut(ranges, " +")
	if !ok {
		return nil
	}
	counts := &[2]int{1, 1}
	for i, span := range []string{old, new} {
		if _, count, ok := strings.Cut(span, ","); ok {
			n, err := patchCount(count)
			if err != nil {
				return nil
			}
			counts[i] = n
		}
	}
	return counts
}

type hunkPart struct {
	kind    byte
	lines   []string
	noFinal bool
}

type patchHunk struct {
	start, length int
	parts         []hunkPart
}

func parsePatchHunks(body string) ([]patchHunk, error) {
	var hunks []patchHunk
	inHunk := false
	lines := strings.Split(body, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "@@") {
			hunk, err := parseHunkHeader(line)
			if err != nil {
				return nil, err
			}
			hunks = append(hunks, hunk)
			inHunk = true
			continue
		}
		if !inHunk {
			continue
		}
		hunk := &hunks[len(hunks)-1]
		if strings.HasPrefix(line, "\\") {
			if !strings.HasPrefix(line, "\\ No newline at end of file") {
				return nil, fmt.Errorf("unrecognized pragma in patch: %q", line)
			}
			if len(hunk.parts) == 0 {
				return nil, fmt.Errorf("no-newline pragma without a preceding line")
			}
			hunk.parts[len(hunk.parts)-1].noFinal = true
			continue
		}
		kind, text := byte(' '), line
		if line != "" {
			switch line[0] {
			case '-', '+', ' ':
				kind, text = line[0], line[1:]
			case '\r':
			default:
				inHunk = false
				continue
			}
		}
		last := len(hunk.parts) - 1
		if last >= 0 && hunk.parts[last].kind == kind && !hunk.parts[last].noFinal {
			hunk.parts[last].lines = append(hunk.parts[last].lines, text)
		} else {
			hunk.parts = append(hunk.parts, hunkPart{kind: kind, lines: []string{text}})
		}
	}
	return hunks, nil
}

func parseHunkHeader(line string) (patchHunk, error) {
	body, ok := strings.CutPrefix(strings.TrimSpace(line), "@@ -")
	if !ok {
		return patchHunk{}, fmt.Errorf("bad hunk header: %q", line)
	}
	original, _, ok := strings.Cut(body, " +")
	if !ok {
		return patchHunk{}, fmt.Errorf("bad hunk header: %q", line)
	}
	start, length, hasLength := strings.Cut(original, ",")
	n, err := patchCount(start)
	if err != nil {
		return patchHunk{}, fmt.Errorf("bad hunk header start: %q", line)
	}
	count := 1
	if hasLength {
		count, err = patchCount(length)
		if err != nil {
			return patchHunk{}, fmt.Errorf("bad hunk header length: %q", line)
		}
	}
	return patchHunk{start: max(n, 1), length: count}, nil
}
