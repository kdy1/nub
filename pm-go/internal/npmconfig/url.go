package npmconfig

import "strings"

func RegistryURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasSuffix(raw, "/") {
		return raw
	}
	return raw + "/"
}

func stripPort(key, port string) string {
	if !strings.HasPrefix(key, "//") {
		return key
	}
	authority, path, slash := strings.Cut(key[2:], "/")
	if !strings.HasSuffix(authority, port) {
		return key
	}
	key = "//" + strings.TrimSuffix(authority, port)
	if slash {
		key += "/" + path
	}
	return key
}

func URIKey(raw string) string {
	if strings.HasPrefix(raw, "https:") {
		return stripPort(strings.TrimPrefix(raw, "https:"), ":443")
	}
	if strings.HasPrefix(raw, "http:") {
		return stripPort(strings.TrimPrefix(raw, "http:"), ":80")
	}
	return raw
}

func normalizeKey(raw string) string { return stripPort(stripPort(raw, ":443"), ":80") }

func scope(name string) string {
	if strings.HasPrefix(name, "@") {
		if s, _, ok := strings.Cut(name, "/"); ok {
			return strings.ToLower(s)
		}
	}
	return ""
}

func lookup[T any](values map[string]T, key string) (T, bool) {
	if v, ok := values[key]; ok {
		return v, true
	}
	cursor := strings.TrimRight(key, "/")
	if v, ok := values[cursor]; ok {
		return v, true
	}
	for {
		i := strings.LastIndexByte(cursor, '/')
		if i <= 2 {
			break
		}
		cursor = cursor[:i]
		if v, ok := values[cursor+"/"]; ok {
			return v, true
		}
		if v, ok := values[cursor]; ok {
			return v, true
		}
	}
	var zero T
	return zero, false
}

func uriPrefix(key, prefix string) bool {
	return key == prefix || strings.HasPrefix(key, prefix) && (strings.HasSuffix(prefix, "/") || strings.HasPrefix(strings.TrimPrefix(key, prefix), "/"))
}
