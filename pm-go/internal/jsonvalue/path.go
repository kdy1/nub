package jsonvalue

import (
	"fmt"
	"strconv"
	"unicode"
)

type Segment struct {
	Key     string
	Index   int
	IsIndex bool
}

func Path(path string) ([]Segment, error) {
	r := []rune(path)
	var out []Segment
	bad := func(why string) ([]Segment, error) { return nil, fmt.Errorf("invalid property path %q: %s", path, why) }
	for i := 0; i < len(r); {
		if unicode.IsSpace(r[i]) {
			i++
			continue
		}
		switch r[i] {
		case '.':
			i++
			start := i
			for i < len(r) && r[i] != '.' && r[i] != '[' && r[i] != ']' && !unicode.IsSpace(r[i]) {
				i++
			}
			if start == i {
				return bad("empty segment after `.`")
			}
			out = append(out, Segment{Key: string(r[start:i])})
		case '[':
			i++
			for i < len(r) && unicode.IsSpace(r[i]) {
				i++
			}
			if i == len(r) {
				return bad("unterminated `[`")
			}
			var seg Segment
			if r[i] == '"' || r[i] == '\'' {
				quote := r[i]
				i++
				start := i
				for i < len(r) && r[i] != quote {
					i++
				}
				if i == len(r) {
					return bad("unterminated string literal")
				}
				seg.Key = string(r[start:i])
				i++
			} else {
				start := i
				for i < len(r) && r[i] >= '0' && r[i] <= '9' {
					i++
				}
				if i == start {
					return bad("expected string or integer inside `[]`")
				}
				index, err := strconv.Atoi(string(r[start:i]))
				if err != nil {
					return bad("bad array index")
				}
				seg = Segment{Index: index, IsIndex: true}
			}
			for i < len(r) && unicode.IsSpace(r[i]) {
				i++
			}
			if i == len(r) || r[i] != ']' {
				return bad("expected `]`")
			}
			i++
			out = append(out, seg)
		default:
			if len(out) > 0 || r[i] == ']' {
				return bad(fmt.Sprintf("unexpected %q", r[i]))
			}
			start := i
			for i < len(r) && r[i] != '.' && r[i] != '[' && r[i] != ']' && !unicode.IsSpace(r[i]) {
				i++
			}
			out = append(out, Segment{Key: string(r[start:i])})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty property path")
	}
	return out, nil
}

func safe(path []Segment) error {
	for _, s := range path {
		if !s.IsIndex && (s.Key == "__proto__" || s.Key == "constructor" || s.Key == "prototype") {
			return fmt.Errorf("refusing to use unsafe property-path key %q", s.Key)
		}
	}
	return nil
}

func (v *Value) At(path []Segment) *Value {
	for _, s := range path {
		if v == nil {
			return nil
		}
		if s.IsIndex {
			if v.Kind != '[' || s.Index >= len(v.Array) {
				return nil
			}
			v = v.Array[s.Index]
		} else {
			v = v.Get(s.Key)
		}
	}
	return v
}

func (v *Value) Set(path []Segment, value *Value) error {
	if err := safe(path); err != nil {
		return err
	}
	if len(path) == 0 {
		return fmt.Errorf("cannot set a value with an empty property path")
	}
	// Parsed paths are nonnegative; guard programmatically built segments too.
	for _, s := range path {
		if s.IsIndex && s.Index < 0 {
			return fmt.Errorf("negative array index")
		}
	}
	for i, s := range path {
		last := i == len(path)-1
		if s.IsIndex {
			if v.Kind != '[' {
				*v = Value{Kind: '['}
			}
			for len(v.Array) <= s.Index {
				v.Array = append(v.Array, Null())
			}
			if last {
				v.Array[s.Index] = value
				return nil
			}
			v = v.Array[s.Index]
		} else {
			if last {
				v.Put(s.Key, value)
				return nil
			}
			if v.Kind != '{' {
				*v = Value{Kind: '{'}
			}
			child := v.Get(s.Key)
			if child == nil {
				child = Null()
				v.Put(s.Key, child)
			}
			v = child
		}
	}
	return nil
}

func (v *Value) Delete(path []Segment) error {
	if err := safe(path); err != nil {
		return err
	}
	if len(path) == 0 {
		return nil
	}
	parent := v.At(path[:len(path)-1])
	if parent == nil {
		return nil
	}
	s := path[len(path)-1]
	if s.IsIndex {
		if parent.Kind == '[' && s.Index < len(parent.Array) {
			parent.Array = append(parent.Array[:s.Index], parent.Array[s.Index+1:]...)
		}
	} else {
		parent.Remove(s.Key)
	}
	return nil
}
