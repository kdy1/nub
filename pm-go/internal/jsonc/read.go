// Package jsonc reads Nub configuration text under the reference resource bounds.
package jsonc

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"unicode/utf8"
)

const MaxFileBytes = 1024 * 1024
const MaxNestingDepth = 64

// Read checks the target type before opening it: a device or writer-less pipe
// must fail rather than block. The bounded read also covers regular files whose
// reported size is smaller than the content they produce.
func Read(path string) (string, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !stat.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > MaxFileBytes {
		return "", fmt.Errorf("larger than the 1 MiB limit")
	}
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	if !utf8.Valid(data) {
		return "", invalidUTF8(data)
	}
	return string(data), nil
}

func invalidUTF8(data []byte) error {
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		if r != utf8.RuneError || size != 1 {
			i += size
			continue
		}
		if !utf8.FullRune(data[i:]) {
			return fmt.Errorf("incomplete utf-8 byte sequence from index %d", i)
		}
		length, first := 1, data[i]
		if first >= 0xc2 && first <= 0xf4 {
			for j := 1; i+j < len(data) && j < 4; j++ {
				b := data[i+j]
				if b < 0x80 || b > 0xbf || j == 1 && (first == 0xe0 && b < 0xa0 || first == 0xed && b > 0x9f || first == 0xf0 && b < 0x90 || first == 0xf4 && b > 0x8f) {
					length = j
					break
				}
			}
		}
		return fmt.Errorf("invalid utf-8 sequence of %d bytes from index %d", length, i)
	}
	return nil
}

// CheckNesting scans raw bytes before recursive parsing. Syntax errors belong
// to the parser: unmatched closers saturate at zero, and unfinished strings or
// comments simply end the scan. Both JSONC quote forms and escapes are handled.
func CheckNesting(text string, maxDepth int) error {
	const (
		code = iota
		quoted
		lineComment
		blockComment
	)
	state, depth := code, 0
	var quote byte
	for i := 0; i < len(text); i++ {
		b := text[i]
		next := byte(0)
		if i+1 < len(text) {
			next = text[i+1]
		}
		switch state {
		case code:
			switch b {
			case '\'', '"':
				state, quote = quoted, b
			case '/':
				if next == '/' {
					state = lineComment
					i++
				} else if next == '*' {
					state = blockComment
					i++
				}
			case '{', '[':
				depth++
				if depth > maxDepth {
					return fmt.Errorf("nesting is deeper than the %d-level limit", maxDepth)
				}
			case '}', ']':
				depth = max(depth-1, 0)
			}
		case quoted:
			if b == '\\' {
				i++
			} else if b == quote {
				state = code
			}
		case lineComment:
			if b == '\n' {
				state = code
			}
		case blockComment:
			if b == '*' && next == '/' {
				state = code
				i++
			}
		}
	}
	return nil
}
