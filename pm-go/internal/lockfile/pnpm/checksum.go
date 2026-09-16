package pnpm

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"maps"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
)

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
}

// PackageExtensionsChecksum follows the reference's object-hash options:
// unordered objects and arrays, without synthetic type/prototype properties.
func PackageExtensionsChecksum(extensions map[string]*jsonvalue.Value) *string {
	if len(extensions) == 0 {
		return nil
	}
	v := jsonvalue.Object()
	for _, key := range slices.Sorted(maps.Keys(extensions)) {
		v.Put(key, extensions[key])
	}
	var buf bytes.Buffer
	objectHash(v, &buf)
	hash := digest(buf.Bytes())
	return &hash
}
func hashedString(value string, out *bytes.Buffer) {
	fmt.Fprintf(out, "string:%d:", len(utf16.Encode([]rune(value))))
	out.WriteString(value)
}
func objectHash(v *jsonvalue.Value, out *bytes.Buffer) {
	if v == nil {
		out.WriteString("Null")
		return
	}
	switch v.Kind {
	case 'n':
		out.WriteString("Null")
	case 'b':
		fmt.Fprintf(out, "bool:%t", v.Scalar)
	case 'd':
		out.WriteString("number:")
		out.WriteString(numberString(fmt.Sprint(v.Scalar)))
	case 's':
		hashedString(v.Text(), out)
	case '{':
		fields := slices.Clone(v.Object)
		slices.SortFunc(fields, func(a, b jsonvalue.Field) int { return strings.Compare(a.Key, b.Key) })
		fmt.Fprintf(out, "object:%d:", len(fields))
		for _, f := range fields {
			hashedString(f.Key, out)
			out.WriteByte(':')
			objectHash(f.Value, out)
			out.WriteByte(',')
		}
	case '[':
		fmt.Fprintf(out, "array:%d:", len(v.Array))
		if len(v.Array) <= 1 {
			for _, item := range v.Array {
				objectHash(item, out)
			}
			return
		}
		serialized := make([]string, len(v.Array))
		for i, item := range v.Array {
			var b bytes.Buffer
			objectHash(item, &b)
			serialized[i] = b.String()
		}
		slices.Sort(serialized)
		fmt.Fprintf(out, "array:%d:", len(serialized))
		for _, s := range serialized {
			hashedString(s, out)
		}
	}
}
func numberString(raw string) string {
	if _, err := strconv.ParseUint(raw, 10, 64); err == nil {
		return raw
	}
	if _, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return raw
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return "0"
	}
	if f == 0 {
		return "0"
	}
	negative := f < 0
	mantissa, exponent, _ := strings.Cut(strconv.FormatFloat(math.Abs(f), 'e', -1, 64), "e")
	e, _ := strconv.Atoi(exponent)
	digits := strings.ReplaceAll(mantissa, ".", "")
	n, k := e+1, len(digits)
	var out string
	switch {
	case k <= n && n <= 21:
		out = digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		out = digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		out = "0." + strings.Repeat("0", -n) + digits
	default:
		out = digits[:1]
		if k > 1 {
			out += "." + digits[1:]
		}
		sign := "+"
		if e < 0 {
			sign = "-"
			e = -e
		}
		out += "e" + sign + strconv.Itoa(e)
	}
	if negative {
		out = "-" + out
	}
	return out
}

// PnpmfileChecksum hashes only paths the caller selected as local pnpmfiles.
// It neither discovers pnpm-named files nor adds a global hook implicitly.
func PnpmfileChecksum(paths []string) (*string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	ordered := slices.Clone(paths)
	slices.Sort(ordered)
	hashes := make([]string, len(ordered))
	for i, path := range ordered {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if !utf8.Valid(data) {
			return nil, fmt.Errorf("%s: pnpmfile is not valid UTF-8", path)
		}
		hashes[i] = digest(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")))
	}
	if len(hashes) == 1 {
		return &hashes[0], nil
	}
	hash := digest([]byte(strings.Join(hashes, ",")))
	return &hash, nil
}
