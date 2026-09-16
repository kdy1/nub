package spec

import "strings"

func ValidName(name string) bool {
	if len(name) == 0 || len(name) > 214 {
		return false
	}
	component := func(s string) bool {
		if s == "" || s == "." || s == ".." {
			return false
		}
		for _, ch := range s {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_' || ch == '.') {
				return false
			}
		}
		return true
	}
	if tail, ok := strings.CutPrefix(name, "@"); ok {
		s, p, ok := strings.Cut(tail, "/")
		return ok && component(s) && component(p)
	}
	return component(name)
}
