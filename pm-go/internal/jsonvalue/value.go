// Package jsonvalue preserves object insertion order and JSON number spelling.
package jsonvalue

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

type Field struct {
	Key   string
	Value *Value
}
type Value struct {
	Kind   byte
	Object []Field
	Array  []*Value
	Scalar any
}

func Object() *Value         { return &Value{Kind: '{'} }
func String(s string) *Value { return &Value{Kind: 's', Scalar: s} }
func Null() *Value           { return &Value{Kind: 'n'} }

func Parse(data []byte) (*Value, error) {
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("invalid UTF-8 in JSON document")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	v, err := read(d, 0)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("unexpected value after JSON document")
		}
		return nil, err
	}
	return v, nil
}

func read(d *json.Decoder, depth int) (*Value, error) {
	if depth > 128 {
		return nil, fmt.Errorf("JSON nesting exceeds 128 levels")
	}
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch token := token.(type) {
	case json.Delim:
		v := &Value{Kind: byte(token)}
		switch token {
		case '{':
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return nil, err
				}
				value, err := read(d, depth+1)
				if err != nil {
					return nil, err
				}
				v.Put(key.(string), value)
			}
		case '[':
			for d.More() {
				value, err := read(d, depth+1)
				if err != nil {
					return nil, err
				}
				v.Array = append(v.Array, value)
			}
		default:
			return nil, fmt.Errorf("unexpected JSON delimiter %q", token)
		}
		if _, err := d.Token(); err != nil {
			return nil, err
		}
		return v, nil
	case string:
		return String(token), nil
	case json.Number:
		return &Value{Kind: 'd', Scalar: token}, nil
	case bool:
		return &Value{Kind: 'b', Scalar: token}, nil
	default:
		return Null(), nil
	}
}

func (v *Value) Get(key string) *Value {
	if v == nil || v.Kind != '{' {
		return nil
	}
	for _, f := range v.Object {
		if f.Key == key {
			return f.Value
		}
	}
	return nil
}

func (v *Value) Text() string {
	if v != nil && v.Kind == 's' {
		return v.Scalar.(string)
	}
	return ""
}

func (v *Value) Put(key string, value *Value) {
	if v.Kind != '{' {
		*v = Value{Kind: '{'}
	}
	for i := range v.Object {
		if v.Object[i].Key == key {
			v.Object[i].Value = value
			return
		}
	}
	v.Object = append(v.Object, Field{key, value})
}

func (v *Value) Remove(key string) {
	if v == nil || v.Kind != '{' {
		return
	}
	for i, f := range v.Object {
		if f.Key == key {
			v.Object = append(v.Object[:i], v.Object[i+1:]...)
			return
		}
	}
}

func scalarJSON(value any) []byte {
	var b bytes.Buffer
	if s, ok := value.(string); ok {
		b.WriteByte('"')
		for _, r := range s {
			switch r {
			case '"', '\\':
				b.WriteByte('\\')
				b.WriteRune(r)
			case '\b':
				b.WriteString(`\b`)
			case '\f':
				b.WriteString(`\f`)
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			default:
				if r < 0x20 {
					fmt.Fprintf(&b, `\u%04x`, r)
				} else {
					b.WriteRune(r)
				}
			}
		}
		b.WriteByte('"')
		return b.Bytes()
	}
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(value); err != nil {
		panic(err)
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}

func (v *Value) MarshalJSON() ([]byte, error) {
	if v == nil {
		return []byte("null"), nil
	}
	var b bytes.Buffer
	switch v.Kind {
	case '{':
		b.WriteByte('{')
		for i, f := range v.Object {
			if i > 0 {
				b.WriteByte(',')
			}
			b.Write(scalarJSON(f.Key))
			b.WriteByte(':')
			data, err := f.Value.MarshalJSON()
			if err != nil {
				return nil, err
			}
			b.Write(data)
		}
		b.WriteByte('}')
	case '[':
		b.WriteByte('[')
		for i, value := range v.Array {
			if i > 0 {
				b.WriteByte(',')
			}
			data, err := value.MarshalJSON()
			if err != nil {
				return nil, err
			}
			b.Write(data)
		}
		b.WriteByte(']')
	case 's', 'b', 'd':
		b.Write(scalarJSON(v.Scalar))
	default:
		b.WriteString("null")
	}
	return b.Bytes(), nil
}

func (v *Value) Pretty() ([]byte, error) {
	return v.Format("  ", false, true)
}

func (v *Value) Format(indent string, crlf, trailingNewline bool) ([]byte, error) {
	raw, err := v.MarshalJSON()
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if err := json.Indent(&b, raw, "", indent); err != nil {
		return nil, err
	}
	if trailingNewline {
		b.WriteByte('\n')
	}
	data := b.Bytes()
	if crlf {
		data = bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n"))
	}
	return data, nil
}
