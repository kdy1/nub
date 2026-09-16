package store

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
)

type integrityAlgorithm struct {
	prefix  string
	newHash func() hash.Hash
}

var algorithms = []integrityAlgorithm{{"sha512-", sha512.New}, {"sha384-", sha512.New384}, {"sha256-", sha256.New}, {"sha1-", sha1.New}}

func parseIntegrity(expected string) (integrityAlgorithm, []string, error) {
	tokens := strings.FieldsFunc(expected, func(c rune) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' })
	for _, algorithm := range algorithms {
		var digests []string
		for _, token := range tokens {
			if rest, ok := strings.CutPrefix(token, algorithm.prefix); ok {
				digest, _, _ := strings.Cut(rest, "?")
				digests = append(digests, digest)
			}
		}
		if len(digests) > 0 {
			return algorithm, digests, nil
		}
	}
	return integrityAlgorithm{}, nil, fmt.Errorf("unsupported integrity format (expected sha1/sha256/sha384/sha512-...): %s", expected)
}

func Verify(data []byte, expected string) error {
	algorithm, digests, err := parseIntegrity(expected)
	if err != nil {
		return err
	}
	h := algorithm.newHash()
	h.Write(data)
	actual := h.Sum(nil)
	for _, encoded := range digests {
		digest, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err == nil && bytes.Equal(digest, actual) {
			return nil
		}
	}
	return fmt.Errorf("integrity mismatch: expected %s, got %s%s", expected, algorithm.prefix, base64.StdEncoding.EncodeToString(actual))
}

func Integrity(data []byte) string {
	digest := sha512.Sum512(data)
	return "sha512-" + base64.StdEncoding.EncodeToString(digest[:])
}

func VerifySHA512(actual [64]byte, expected string) (bool, error) {
	algorithm, digests, err := parseIntegrity(expected)
	if err != nil {
		return false, err
	}
	if algorithm.prefix != "sha512-" {
		return false, nil
	}
	var malformed error
	for _, encoded := range digests {
		digest, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil {
			if malformed == nil {
				malformed = fmt.Errorf("integrity field has malformed base64: %s (%w)", expected, err)
			}
			continue
		}
		if len(digest) != 64 {
			if malformed == nil {
				malformed = fmt.Errorf("integrity field decoded to %d bytes, expected 64 for sha512: %s", len(digest), expected)
			}
			continue
		}
		if bytes.Equal(digest, actual[:]) {
			return true, nil
		}
	}
	if malformed != nil {
		return false, malformed
	}
	return false, fmt.Errorf("integrity mismatch: expected %s, got sha512-%s", expected, base64.StdEncoding.EncodeToString(actual[:]))
}

func IntegrityHex(expected string) (string, bool) {
	_, digests, err := parseIntegrity(expected)
	if err != nil {
		return "", false
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(digests[0])
	if err != nil {
		return "", false
	}
	return hex.EncodeToString(decoded), true
}

func ShasumToSRI(shasum string) (string, bool) {
	shasum = strings.TrimSpace(shasum)
	if len(shasum) != 40 {
		return "", false
	}
	digest, err := hex.DecodeString(shasum)
	if err != nil {
		return "", false
	}
	return "sha1-" + base64.StdEncoding.EncodeToString(digest), true
}

// ValidateContent applies tarball-side normalization only. Git and file
// locators cannot be compared to a semver but still require the expected name.
func ValidateContent(manifest []byte, name, version string) error {
	root, err := jsonvalue.Parse(manifest)
	if err != nil {
		return fmt.Errorf("invalid package.json: %w", err)
	}
	actualName, actualVersion := root.Get("name").Text(), root.Get("version").Text()
	normalized := actualVersion
	if len(normalized) >= 2 && normalized[0] == 'v' && normalized[1] >= '0' && normalized[1] <= '9' {
		normalized = normalized[1:]
	}
	withoutBuild, _, hasBuild := strings.Cut(normalized, "+")
	locator := strings.Contains(version, "://") || strings.HasPrefix(version, "git+") || strings.HasPrefix(version, "file:")
	if actualName != name || !(locator || normalized == version || hasBuild && withoutBuild == version) {
		return fmt.Errorf("package content mismatch: %s@%s", actualName, actualVersion)
	}
	return nil
}
