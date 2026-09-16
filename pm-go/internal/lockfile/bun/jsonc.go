// Package bun reads and writes Bun's text lockfile format.
package bun

// stripJSONC mirrors the reference's byte-preserving comment/trailing-comma
// pass. In particular, comma lookahead skips whitespace but not comments.
func stripJSONC(input []byte) []byte {
	out := append([]byte(nil), input...)
	inString, escape := false, false
	for i := 0; i < len(input); i++ {
		c := input[i]
		if inString {
			if escape {
				escape = false
			} else if c == '\\' {
				escape = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		if c == '/' && i+1 < len(input) && input[i+1] == '/' {
			for i < len(input) && input[i] != '\n' {
				out[i] = ' '
				i++
			}
			i--
			continue
		}
		if c == '/' && i+1 < len(input) && input[i+1] == '*' {
			out[i], out[i+1] = ' ', ' '
			i += 2
			for i < len(input) {
				if i+1 < len(input) && input[i] == '*' && input[i+1] == '/' {
					out[i], out[i+1] = ' ', ' '
					i++
					break
				}
				if input[i] != '\n' {
					out[i] = ' '
				}
				i++
			}
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(input) && (input[j] == ' ' || input[j] >= '\t' && input[j] <= '\r') {
				j++
			}
			if j < len(input) && (input[j] == '}' || input[j] == ']') {
				out[i] = ' '
			}
		}
		if c == '"' {
			inString = true
		}
	}
	return out
}
