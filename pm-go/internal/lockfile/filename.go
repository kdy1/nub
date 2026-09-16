package lockfile

import (
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"

	"lukechampine.com/blake3"
)

const DefaultVirtualStoreMaxLength = 120

// DepPathFilename encodes the reference virtual-store slot, including its
// BLAKE3 suffix for long or ASCII-uppercase names. maxLength is a byte cap.
func DepPathFilename(depPath string, maxLength int) (string, error) {
	if maxLength <= 33 {
		return "", fmt.Errorf("virtual-store-dir-max-length (%d) must be > 33 to fit the hash suffix", maxLength)
	}
	path := depPath
	if rest, file := strings.CutPrefix(path, "file:"); file {
		path = "file+" + rest
	} else {
		path = strings.TrimPrefix(path, "/")
	}
	filename := strings.Map(func(c rune) rune {
		if strings.ContainsRune(`\/:*?"<>|#`, c) {
			return '+'
		}
		return c
	}, path)
	if strings.Contains(filename, "(") {
		filename = strings.TrimSuffix(filename, ")")
		filename = strings.ReplaceAll(filename, ")(", "_")
		filename = strings.NewReplacer("(", "_", ")", "_").Replace(filename)
	}
	uppercase := strings.ContainsFunc(filename, func(c rune) bool { return c >= 'A' && c <= 'Z' })
	if len(filename) > maxLength || uppercase && !strings.HasPrefix(filename, "file+") {
		digest := blake3.Sum256([]byte(filename))
		end := min(maxLength-33, len(filename))
		for end > 0 && end < len(filename) && !utf8.RuneStart(filename[end]) {
			end--
		}
		filename = filename[:end] + "_" + hex.EncodeToString(digest[:16])
	}
	return filename, nil
}
