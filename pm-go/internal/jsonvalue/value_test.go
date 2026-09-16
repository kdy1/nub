package jsonvalue

import (
	"strings"
	"testing"
)

func TestPathsAndOrderedValues(t *testing.T) {
	v, err := Parse([]byte(`{"name":"example","version":"1.0.0","count":9007199254740993,"a":5}`))
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"a[1].name": "second", `scripts['build:all']`: "node --test"} {
		path, err := Path(key)
		if err != nil {
			t.Fatal(err)
		}
		if err := v.Set(path, String(value)); err != nil {
			t.Fatal(err)
		}
		if got := v.At(path).Text(); got != value {
			t.Fatalf("%s = %s", key, got)
		}
	}
	data, err := v.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), `{"name":"example","version":"1.0.0","count":9007199254740993,`) {
		t.Fatalf("lost order or numeric precision: %s", data)
	}
	path, _ := Path("a[0]")
	if err := v.Delete(path); err != nil {
		t.Fatal(err)
	}
	path, _ = Path("a[0].name")
	if v.At(path).Text() != "second" {
		t.Fatal("array delete did not splice")
	}
}

func TestRejectsUnsafeEditsAndInvalidPaths(t *testing.T) {
	for _, key := range []string{"__proto__.a", "x.constructor", "x[0].prototype"} {
		v := Object()
		path, err := Path(key)
		if err != nil {
			t.Fatal(err)
		}
		if v.Set(path, String("bad")) == nil || v.Delete(path) == nil {
			t.Fatal("unsafe edit accepted", key)
		}
		if len(v.Object) != 0 {
			t.Fatal("failed edit changed value")
		}
	}
	for _, key := range []string{"", "a..b", "a[", "a[-1]", "a[1", "a b", "a]"} {
		if _, err := Path(key); err == nil {
			t.Fatal("bad path accepted", key)
		}
	}
}

func TestMalformedJSON(t *testing.T) {
	for _, data := range []string{`{"x":}`, `[] true`, `{"x":1,}`, "{\"x\":\"\xff\"}", strings.Repeat("[", 130) + strings.Repeat("]", 130)} {
		if _, err := Parse([]byte(data)); err == nil {
			t.Fatal("invalid JSON accepted")
		}
	}
}

func TestJSONStringEscaping(t *testing.T) {
	input := "\"\\\t\n\r\b\f\x01<>&한글😀\u2028\u2029literal\\u2028"
	value := String(input)
	encoded, err := value.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Parse(encoded)
	if err != nil || decoded.Text() != input {
		t.Fatal(string(encoded), err)
	}
	if !strings.Contains(string(encoded), "한글😀\u2028\u2029") || !strings.Contains(string(encoded), `literal\\u2028`) {
		t.Fatal(string(encoded))
	}
}
