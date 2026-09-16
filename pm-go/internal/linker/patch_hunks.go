package linker

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
)

type patchModification struct {
	kind          byte // splice, pop, push
	index, remove int
	insert        []string
}

func evaluateHunk(hunk patchHunk, lines []string, offset int) ([]patchModification, bool) {
	base := hunk.start - 1 + offset
	if base < 0 || base > len(lines) || len(lines)-base < hunk.length {
		return nil, false
	}
	index := base
	var edits []patchModification
	for _, part := range hunk.parts {
		if part.kind == ' ' || part.kind == '-' {
			for _, line := range part.lines {
				if index >= len(lines) || strings.TrimRightFunc(lines[index], unicode.IsSpace) != strings.TrimRightFunc(line, unicode.IsSpace) {
					return nil, false
				}
				index++
			}
			if part.kind == '-' {
				edits = append(edits, patchModification{kind: 's', index: index - len(part.lines), remove: len(part.lines)})
				if part.noFinal {
					edits = append(edits, patchModification{kind: '+'})
				}
			}
		} else {
			edits = append(edits, patchModification{kind: 's', index: index, insert: part.lines})
			if part.noFinal {
				edits = append(edits, patchModification{kind: '-'})
			}
		}
	}
	return edits, true
}

func applyPatchHunks(original string, hunks []patchHunk) (string, error) {
	lines := strings.Split(original, "\n")
	var edits []patchModification
	for i, hunk := range hunks {
		offset := 0
		for {
			if next, ok := evaluateHunk(hunk, lines, offset); ok {
				edits = append(edits, next...)
				break
			}
			if offset < 0 {
				offset = -offset
			} else {
				offset = -offset - 1
			}
			if offset < -20 || offset > 20 {
				return "", fmt.Errorf("could not apply hunk %d at line %d (searched 20 lines either way for its context and found none).\n  The file does not match what the patch was cut against — regenerate it against this version.", i+1, hunk.start)
			}
		}
	}
	delta := 0
	for _, edit := range edits {
		switch edit.kind {
		case 's':
			at := edit.index + delta
			if at < 0 || at > len(lines) {
				return "", fmt.Errorf("patch hunks overlap outside the file")
			}
			end := min(at+edit.remove, len(lines))
			lines = slices.Replace(lines, at, end, edit.insert...)
			delta += len(edit.insert) - (end - at)
		case '-':
			if len(lines) > 0 {
				lines = lines[:len(lines)-1]
			}
		case '+':
			lines = append(lines, "")
		}
	}
	return strings.Join(lines, "\n"), nil
}
