package bun

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
)

func TestJSONCOffsetsAndStrings(t *testing.T) {
	input := []byte("// 한글\r\n{\"url\":\"https://a/*b*/\",/* 😀\n x */\"values\":[1,2,],}\n")
	clean := stripJSONC(input)
	if len(clean) != len(input) {
		t.Fatal("offsets changed")
	}
	for i, c := range input {
		if c == '\n' && clean[i] != c {
			t.Fatal("newline moved")
		}
	}
	v, err := jsonvalue.Parse(clean)
	if err != nil || v.Get("url").Text() != "https://a/*b*/" || len(v.Get("values").Array) != 2 {
		t.Fatal(string(clean), err)
	}
	if _, err := parseRaw([]byte(`{"lockfileVersion":1,/* comment */}`)); err == nil {
		t.Fatal("comma lookahead unexpectedly skipped comment")
	}
	if _, err := parseRaw([]byte(`{"lockfileVersion":1}/* unterminated`)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stripJSONC([]byte("\"\\\"//\"")), []byte("\"\\\"//\"")) {
		t.Fatal("escaped quote lost")
	}
	if _, err := parseRaw([]byte("{\"lockfileVersion\":1}//\xff")); err == nil {
		t.Fatal("invalid UTF8 hidden by comment")
	}
}

func TestTypedFieldsDefaultsAndDuplicates(t *testing.T) {
	for _, input := range []string{
		`{}`, `{"lockfileVersion":null}`, `{"lockfileVersion":1.0}`, `{"lockfileVersion":-1}`, `{"lockfileVersion":4294967296}`,
		`{"lockfileVersion":1,"configVersion":null}`, `{"lockfileVersion":1,"packages":null}`, `{"lockfileVersion":1,"workspaces":[]}`,
		`{"lockfileVersion":1,"packages":{"a":{}}}`, `{"lockfileVersion":1,"overrides":{"a":1}}`, `{"lockfileVersion":1,"trustedDependencies":[false]}`,
		`{"lockfileVersion":1,"catalogs":{"x":null}}`, `{"lockfileVersion":1,"lockfileVersion":2}`,
		`{"lockfileVersion":1,"workspaces":{"":{"dependencies":{},"dependencies":{}}}}`,
		`{"lockfileVersion":1,"workspaces":{"a":{"dependencies":null},"a":{}}}`,
	} {
		if _, err := parseRaw([]byte(input)); err == nil {
			t.Errorf("accepted %s", input)
		}
	}
	r, err := parseRaw([]byte(`{"lockfileVersion":2,"workspaces":{"a":{"name":"old"},"a":{"name":"new","name":"last","peerDependencies":{"x":"1","x":"2"}}},"overrides":{"a":"1","a":"2"},"future":{"a":1,"a":2},"future":{"x":3},"catalogs":{"x":{}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if r.configVersion != 1 || r.version != 2 || r.overrides["a"] != "2" || r.workspaces["a"].extra["name"].Text() != "last" || r.workspaces["a"].extra["peerDependencies"].Get("x").Text() != "2" || r.extra["future"].Get("x") == nil {
		t.Fatalf("%+v", r)
	}
}

func TestTupleDecoding(t *testing.T) {
	sri := "sha512-" + strings.Repeat("A", 88)
	v, _ := jsonvalue.Parse([]byte(`["a@1","https://registry/a",{"dependencies":{"b":"^2"},"os":["linux",false],"bin":"a.js"},"sha1-owner-abc","` + sri + `"]`))
	e, err := decodeEntry("a", v.Array)
	if err != nil || e.registryURL == nil || *e.registryURL != "https://registry/a" || e.integrity == nil || *e.integrity != sri || e.meta.dependencies["b"] != "^2" || len(e.meta.os) != 1 {
		t.Fatal(e, err)
	}
	for _, body := range []string{`{"dependencies":{"b":"2"},"optionalPeers":null}`, `{"dependencies":null,"bin":"x"}`, `{"optionalPeers":[1],"unknown":true}`} {
		v, _ := jsonvalue.Parse([]byte(`["a@1",{"bin":"first"},` + body + `,"after"]`))
		e, err := decodeEntry("a", v.Array)
		if err != nil || len(e.meta.dependencies) != 0 || len(e.meta.extra) != 0 || e.meta.bin != nil || e.registryURL != nil {
			t.Fatal("invalid metadata was not discarded", e, err)
		}
	}
	for _, body := range []string{`[]`, `[1]`} {
		v, _ := jsonvalue.Parse([]byte(body))
		if _, err := decodeEntry("a", v.Array); err == nil {
			t.Fatal(body)
		}
	}
}

func TestIntegrityTupleRecognition(t *testing.T) {
	for algo, length := range map[string]int{"sha512": 88, "sha384": 64, "sha256": 44, "sha1": 28, "md5": 24} {
		if !isIntegrityHash(algo + "-" + strings.Repeat("A", length-3) + "+/=") {
			t.Fatal(algo)
		}
		for _, body := range []string{strings.Repeat("A", length-1), strings.Repeat("A", length-1) + "-", strings.Repeat("A", length-1) + "é"} {
			if isIntegrityHash(algo + "-" + body) {
				t.Fatal(body)
			}
		}
	}
	for _, input := range []string{"sha1-owner-abc", "unknown-", "sha512-", "foo"} {
		if isIntegrityHash(input) {
			t.Fatal(input)
		}
	}
}
