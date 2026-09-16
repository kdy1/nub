package registry

import "strings"

// RedactURL preserves the reference's diagnostic representation without parsing
// and re-encoding a URL. Both userinfo and named query credentials are masked.
func RedactURL(raw string) string {
	after := -1
	if i := strings.Index(raw, "://"); i >= 0 {
		after = i + 3
	} else if strings.HasPrefix(raw, "//") {
		after = 2
	}
	if after >= 0 {
		tail := raw[after:]
		at := strings.IndexByte(tail, '@')
		slash := strings.IndexByte(tail, '/')
		if slash < 0 {
			slash = len(tail)
		}
		if at >= 0 && at < slash {
			raw = raw[:after] + "***@" + tail[at+1:]
		}
	}
	q := strings.IndexByte(raw, '?')
	if q < 0 {
		return raw
	}
	query, fragment := raw[q+1:], ""
	if i := strings.IndexByte(query, '#'); i >= 0 {
		query, fragment = query[:i], query[i:]
	}
	pairs := strings.Split(query, "&")
	for i, pair := range pairs {
		key, _, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		switch strings.ToLower(key) {
		case "token", "auth", "api_key", "apikey", "access_token":
			pairs[i] = key + "=***"
		}
	}
	return raw[:q+1] + strings.Join(pairs, "&") + fragment
}
