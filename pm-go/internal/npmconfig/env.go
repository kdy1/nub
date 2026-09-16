package npmconfig

import "strings"

// EnvEntries uses four precedence buckets, independent of the process's
// environment iteration order. The pnpm URI-auth compatibility spelling is
// distinct from bare pnpm_config settings, which require pnpm 11+.
func EnvEntries(vars []string, pnpm11 bool, bun bool) []Entry {
	var buckets [4][]Entry
	for _, item := range vars {
		name, value, ok := strings.Cut(item, "=")
		if !ok || value == "" {
			continue
		}
		key, ok := envKey(name, pnpm11)
		if !ok {
			continue
		}
		bucket := 0
		if strings.HasPrefix(strings.ToLower(name), "pnpm_config_") {
			bucket++
		}
		if strings.HasPrefix(key, "//") {
			bucket += 2
		}
		buckets[bucket] = append(buckets[bucket], Entry{Env, key, value})
	}
	var out []Entry
	for _, b := range buckets {
		out = append(out, b...)
	}
	if bun {
		for _, key := range []string{"BUN_CONFIG_REGISTRY", "BUN_CONFIG_TOKEN"} {
			for _, item := range vars {
				name, value, ok := strings.Cut(item, "=")
				if !ok || name != key {
					continue
				}
				if value != "" {
					if key == "BUN_CONFIG_TOKEN" {
						out = append(out, Entry{Env, "_authToken", value})
					} else if strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://") {
						out = append(out, Entry{Env, "registry", value})
					}
				}
				break
			}
		}
	}
	return out
}

func envKey(name string, pnpm11 bool) (string, bool) {
	suffix, ok := strings.CutPrefix(name, "npm_config_")
	if !ok {
		suffix, ok = strings.CutPrefix(name, "NPM_CONFIG_")
	}
	if !ok {
		for _, prefix := range []string{"npm_config_", "pnpm_config_"} {
			if len(name) >= len(prefix) && strings.EqualFold(name[:len(prefix)], prefix) && strings.HasPrefix(name[len(prefix):], "//") {
				suffix, ok = name[len(prefix):], true
				break
			}
		}
	}
	if !ok && pnpm11 {
		for _, prefix := range []string{"pnpm_config_", "PNPM_CONFIG_"} {
			if s, matches := strings.CutPrefix(name, prefix); matches && snake(s, prefix[0] == 'P') {
				suffix, ok = strings.ToLower(s), true
				break
			}
		}
	}
	if !ok {
		return "", false
	}
	if strings.HasPrefix(suffix, "//") {
		i := strings.LastIndexByte(suffix, ':')
		if i >= 0 {
			switch suffix[i+1:] {
			case "_authToken", "_auth", "username", "_password":
				return suffix, true
			}
		}
	}
	if strings.HasPrefix(suffix, "@") {
		scope, tail, ok := strings.Cut(suffix, ":")
		if ok && strings.EqualFold(tail, "registry") {
			return strings.ToLower(scope) + ":registry", true
		}
	}
	key, ok := map[string]string{
		"registry": "registry", "https_proxy": "https-proxy", "http_proxy": "http-proxy", "proxy": "proxy", "noproxy": "noproxy", "no_proxy": "noproxy", "strict_ssl": "strict-ssl", "local_address": "local-address", "maxsockets": "maxsockets",
	}[strings.ToLower(suffix)]
	return key, ok
}

func snake(s string, upper bool) bool {
	if s == "" {
		return false
	}
	for _, segment := range strings.Split(s, "_") {
		if segment == "" {
			return false
		}
		for _, ch := range segment {
			if ch >= '0' && ch <= '9' {
				continue
			}
			if upper && ch >= 'A' && ch <= 'Z' || !upper && ch >= 'a' && ch <= 'z' {
				continue
			}
			return false
		}
	}
	return true
}
