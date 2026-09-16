package jsonvalue

import "fmt"

// encoding/json replaces unpaired UTF-16 escapes with U+FFFD. The reference
// serde reader rejects them, including in keys, before any manifest mutation.
func validateSurrogates(data []byte) error {
	hex4 := func(start int) (uint16, bool) {
		if start+4 > len(data) {
			return 0, false
		}
		var n uint16
		for _, c := range data[start : start+4] {
			n <<= 4
			switch {
			case c >= '0' && c <= '9':
				n += uint16(c - '0')
			case c >= 'a' && c <= 'f':
				n += uint16(c - 'a' + 10)
			case c >= 'A' && c <= 'F':
				n += uint16(c - 'A' + 10)
			default:
				return 0, false
			}
		}
		return n, true
	}
	inString := false
	for i := 0; i < len(data); i++ {
		if data[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || data[i] != '\\' || i+1 >= len(data) {
			continue
		}
		i++
		if data[i] != 'u' {
			continue
		}
		n, ok := hex4(i + 1)
		if !ok { // The JSON decoder reports malformed escape syntax.
			continue
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return fmt.Errorf("unpaired low surrogate in JSON at byte %d", i-5)
		}
		if n < 0xd800 || n > 0xdbff {
			continue
		}
		if i+2 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
			return fmt.Errorf("unpaired high surrogate in JSON at byte %d", i-5)
		}
		low, ok := hex4(i + 3)
		if !ok || low < 0xdc00 || low > 0xdfff {
			return fmt.Errorf("unpaired high surrogate in JSON at byte %d", i-5)
		}
		i += 6
	}
	return nil
}
