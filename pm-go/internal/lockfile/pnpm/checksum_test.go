package pnpm

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
)

func extensions(t *testing.T, s string) map[string]*jsonvalue.Value {
	t.Helper()
	v, err := jsonvalue.Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*jsonvalue.Value{}
	for _, f := range v.Object {
		out[f.Key] = f.Value
	}
	return out
}
func TestExtensionChecksumReferenceVectors(t *testing.T) {
	for _, tc := range []struct{ json, want string }{
		{`{"a":{"dependencies":{"b":"1.0.0"}}}`, "sha256-9yDK//Ix13a8CrWmJGIeVC0z1tCnQxNHOLTw47oh10s="},
		{`{"k":{"optional":true,"count":3}}`, "sha256-EOT4Rq2KGdwdUwAI9FuL2HmoawSWgN2C+QLiGsRhY20="},
		{`{"pkg":{"bundledDependencies":["z","a","m"],"dependencies":{"x":"1"}}}`, "sha256-9nkLQlH+XcJg38ygPgoq2a+Lz8cfE7PtUaAbUzni6oA="},
	} {
		got := PackageExtensionsChecksum(extensions(t, tc.json))
		if got == nil || *got != tc.want {
			t.Fatalf("%s: %v", tc.json, got)
		}
	}
	if PackageExtensionsChecksum(nil) != nil || PackageExtensionsChecksum(map[string]*jsonvalue.Value{}) != nil {
		t.Fatal("empty checksum should be absent")
	}
	a := PackageExtensionsChecksum(extensions(t, `{"a":{"x":"1","y":"2"},"b":["😀","a","z"]}`))
	b := PackageExtensionsChecksum(extensions(t, `{"b":["z","😀","a"],"a":{"y":"2","x":"1"}}`))
	if *a != *b {
		t.Fatal("unordered object/array checksum differs")
	}
}
func TestObjectHashEncoding(t *testing.T) {
	for _, tc := range []struct{ json, want string }{
		{`null`, "Null"}, {`true`, "bool:true"}, {`"😀"`, "string:2:😀"}, {`[]`, "array:0:"},
		{`["😀"]`, "array:1:string:2:😀"}, {`["b","a"]`, "array:2:array:2:string:10:string:1:astring:10:string:1:b"},
		{`{"z":null,"a":1.0}`, "object:2:string:1:a:number:1,string:1:z:Null,"},
	} {
		v, err := jsonvalue.Parse([]byte(tc.json))
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		objectHash(v, &buf)
		if buf.String() != tc.want {
			t.Fatalf("%s: %s", tc.json, buf.String())
		}
	}
}
func TestChecksumNumberNotation(t *testing.T) {
	for raw, want := range map[string]string{
		"0.0": "0", "-0.0": "0", "3.5": "3.5", "0.5": "0.5", "1e-6": "0.000001", "1e-7": "1e-7", "-1.5e-7": "-1.5e-7",
		"123.456": "123.456", "1e20": "100000000000000000000", "1e21": "1e+21", "1.5e21": "1.5e+21", "-1e21": "-1e+21",
		"18446744073709551615": "18446744073709551615", "-9223372036854775808": "-9223372036854775808",
	} {
		if got := numberString(raw); got != want {
			t.Fatalf("%s: %s", raw, got)
		}
	}
}
func TestPnpmfileChecksum(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	lf := write("lf.cjs", "module.exports = { hooks: {} };\n")
	crlf := write("crlf.cjs", "module.exports = { hooks: {} };\r\n")
	a, err := PnpmfileChecksum([]string{lf})
	if err != nil {
		t.Fatal(err)
	}
	b, err := PnpmfileChecksum([]string{crlf})
	if err != nil {
		t.Fatal(err)
	}
	if *a != "sha256-dS522kUCN9FHUHk8JODaJjlMeNKVaNv8hwk1JhcGjEY=" || *a != *b {
		t.Fatal("single-file or CRLF checksum differs", *a, *b)
	}
	first, second := write("a.cjs", "first\n"), write("b.cjs", "second\n")
	c, err := PnpmfileChecksum([]string{second, first})
	if err != nil {
		t.Fatal(err)
	}
	d, err := PnpmfileChecksum([]string{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if *c != "sha256-fXGbJsgRm3+uXgNRo1L/QGJ7yvEL6Ma+HNJPjmp8s1Q=" || *c != *d {
		t.Fatal("multi-file or sorted-path checksum differs", *c, *d)
	}
	if hash, err := PnpmfileChecksum(nil); err != nil || hash != nil {
		t.Fatalf("empty: %v %v", hash, err)
	}
	if _, err := PnpmfileChecksum([]string{filepath.Join(dir, "missing")}); err == nil {
		t.Fatal("missing hook accepted")
	}
	if _, err := PnpmfileChecksum([]string{write("invalid.cjs", string([]byte{0xff}))}); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
}
