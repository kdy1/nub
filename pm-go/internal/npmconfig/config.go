package npmconfig

import (
	"encoding/base64"
	"fmt"
	"net"
	"strconv"
	"strings"
)

const DefaultRegistry = "https://registry.npmjs.org/"

type TLS struct {
	CA                []string
	CAFile, Cert, Key *string
}

func (t TLS) Present() bool {
	return len(t.CA) != 0 || t.CAFile != nil || t.Cert != nil || t.Key != nil
}

type Auth struct {
	Token, Basic, Username, Password, TokenHelper *string
	AlwaysAuth                                    bool
	TLS                                           TLS
}

func (a *Auth) HasCredentials() bool {
	return a != nil && (a.Token != nil || a.Basic != nil || a.TokenHelper != nil || a.Username != nil && a.Password != nil)
}

func (a *Auth) BasicValue() (string, bool) {
	if a == nil {
		return "", false
	}
	if a.Basic != nil {
		return *a.Basic, true
	}
	if a.Username == nil || a.Password == nil {
		return "", false
	}
	password, err := base64.StdEncoding.DecodeString(*a.Password)
	if err != nil {
		return "", false
	}
	return base64.StdEncoding.EncodeToString(append([]byte(*a.Username+":"), password...)), true
}

type Warning struct{ Code, Message string }

type Config struct {
	Registry                                    string
	Scopes                                      map[string]string
	Auth                                        map[string]*Auth
	ScopedAuth                                  map[string]map[string]*Auth
	HTTPSProxy, HTTPProxy, NoProxy, LegacyProxy *string
	StrictSSL, AlwaysAuth                       bool
	LocalAddress                                net.IP
	MaxSockets                                  int
	TLS                                         TLS
	Warnings                                    []Warning
}

func Resolve(entries []Entry, env map[string]string) Config {
	c := Config{Registry: DefaultRegistry, StrictSSL: true, Scopes: map[string]string{}, Auth: map[string]*Auth{}, ScopedAuth: map[string]map[string]*Auth{}}
	c.apply(entries)
	c.applyProxyEnv(env)
	if _, ok := c.Scopes["@jsr"]; !ok {
		c.Scopes["@jsr"] = "https://npm.jsr.io/"
	}
	return c
}

func (c *Config) RegistryFor(name string) string {
	if value, ok := c.Scopes[scope(name)]; ok {
		return value
	}
	return c.Registry
}

func (c *Config) AuthFor(url, name string) *Auth {
	if a := c.scopedFor(url, name, func(a *Auth) bool { return a.HasCredentials() }); a != nil {
		return a
	}
	a, _ := lookup(c.Auth, URIKey(url))
	return a
}

func (c *Config) ScopedTLSFor(url, name string) *TLS {
	if a := c.scopedFor(url, name, func(a *Auth) bool { return a.TLS.Present() }); a != nil {
		return &a.TLS
	}
	return nil
}

func (c *Config) scopedFor(url, name string, match func(*Auth) bool) *Auth {
	var found *Auth
	longest := -1
	for prefix, scopes := range c.ScopedAuth {
		if a := scopes[scope(name)]; a != nil && len(prefix) > longest && uriPrefix(URIKey(url), prefix) && match(a) {
			found, longest = a, len(prefix)
		}
	}
	return found
}

func (c *Config) AlwaysAuthFor(url string) bool {
	a, _ := lookup(c.Auth, URIKey(url))
	return c.AlwaysAuth || a != nil && a.AlwaysAuth
}

func (c *Config) warning(code, format string, args ...any) {
	c.Warnings = append(c.Warnings, Warning{"WARN_AUBE_" + code, fmt.Sprintf(format, args...)})
}

func (c *Config) authEntry(uri, scope string) *Auth {
	entries := c.Auth
	if scope != "" {
		if c.ScopedAuth[uri] == nil {
			c.ScopedAuth[uri] = map[string]*Auth{}
		}
		entries = c.ScopedAuth[uri]
		uri = strings.ToLower(scope)
	}
	if entries[uri] == nil {
		entries[uri] = &Auth{}
	}
	return entries[uri]
}

func registrySlot(source Source) Source {
	if source == ProjectAuthFile {
		return UserAuthFile
	}
	return source
}

func (c *Config) apply(entries []Entry) {
	registries := map[Source]string{}
	for _, e := range entries {
		if e.Key == "registry" {
			registries[registrySlot(e.Source)] = RegistryURL(e.Value)
		}
	}
	explicit := map[[2]string]bool{}
	for _, e := range entries {
		key, value, source := e.Key, e.Value, e.Source
		suffix := key
		if i := strings.LastIndexByte(key, ':'); i >= 0 {
			suffix = key[i+1:]
		}
		if !source.Trusted() && isEnvAuth(suffix) && (containsEnvRef(key) || containsEnvRef(value)) {
			c.warning("UNTRUSTED_AUTH_ENV", "ignoring auth setting %q from untrusted source %s: project-controlled `.npmrc` cannot expand environment variables in auth config", key, source)
			continue
		}
		if strings.HasPrefix(key, "//") {
			i := strings.LastIndexByte(key, ':')
			if i < 0 {
				continue
			}
			uri, scoped := key[:i], ""
			if j := strings.LastIndexByte(uri, ':'); j >= 0 && strings.HasPrefix(uri[j+1:], "@") && len(uri[j+1:]) > 1 && !strings.Contains(uri[j+1:], "/") {
				uri, scoped = uri[:j], uri[j+1:]
			}
			uri = normalizeKey(uri)
			if !c.acceptHelper(suffix, value, source) {
				continue
			}
			if !authField(suffix) {
				continue
			}
			setAuth(c.authEntry(uri, scoped), suffix, value)
			if scoped == "" && rescopable(suffix) {
				explicit[[2]string{uri, canonicalSuffix(suffix)}] = true
			}
			continue
		}
		if rescopable(key) {
			if !c.acceptHelper(key, value, source) {
				continue
			}
			registry := registries[registrySlot(source)]
			if registry == "" {
				registry = DefaultRegistry
			}
			uri := URIKey(registry)
			if explicit[[2]string{uri, canonicalSuffix(key)}] {
				c.warning("UNSCOPED_AUTH_RESCOPED", "ignoring unscoped %s from %s: URI-scoped `%s:%s` is already configured", key, source, uri, key)
				continue
			}
			message := fmt.Sprintf("unscoped %s from %s was pinned to %s", canonicalSuffix(key), source, registry)
			if source != Env && source != PnpmAuth {
				message += fmt.Sprintf("; write `%s:%s=...` instead", uri, canonicalSuffix(key))
			}
			c.warning("UNSCOPED_AUTH_RESCOPED", "%s", message)
			setAuth(c.authEntry(uri, ""), key, value)
			continue
		}
		switch key {
		case "registry":
			c.Registry = RegistryURL(value)
		case "always-auth", "always_auth":
			c.AlwaysAuth = truthy(value)
		case "https-proxy", "httpsProxy", "http-proxy", "httpProxy", "proxy", "noproxy", "noProxy", "no-proxy":
			if !source.Trusted() {
				c.warning("UNTRUSTED_PROXY", "ignoring %s from untrusted source %s: committed `.npmrc` cannot set registry proxies", key, source)
				continue
			}
			switch key {
			case "https-proxy", "httpsProxy":
				c.HTTPSProxy = proxy(value)
			case "http-proxy", "httpProxy":
				c.HTTPProxy = proxy(value)
			case "proxy":
				c.LegacyProxy = nonempty(value)
			default:
				c.NoProxy = proxy(value)
			}
		case "strict-ssl", "strictSsl":
			if b, ok := parseBool(value); ok {
				if !b && !source.Trusted() {
					c.warning("UNTRUSTED_STRICT_SSL_DISABLE", "ignoring strict-ssl=false: %s source is not trusted (committed `.npmrc` cannot disable TLS validation)", source)
				} else {
					c.StrictSSL = b
				}
			}
		case "local-address", "localAddress":
			if ip := net.ParseIP(strings.TrimSpace(value)); ip != nil {
				c.LocalAddress = ip
			} else {
				c.warning("INVALID_LOCAL_ADDRESS", "ignoring invalid local-address %q", value)
			}
		case "maxsockets":
			if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && n > 0 {
				c.MaxSockets = n
			} else {
				c.warning("INVALID_MAXSOCKETS", "ignoring invalid maxsockets %q", value)
			}
		case "cafile", "caFile":
			c.TLS.CAFile = &value
		case "ca", "ca[]":
			c.TLS.CA = append(c.TLS.CA, pem(value))
		default:
			if s, ok := strings.CutSuffix(key, ":registry"); ok && strings.HasPrefix(s, "@") {
				c.Scopes[strings.ToLower(s)] = RegistryURL(value)
			}
		}
	}
}

func setAuth(a *Auth, key, value string) {
	switch key {
	case "_authToken":
		a.Token = &value
	case "_auth":
		a.Basic = &value
	case "username":
		a.Username = &value
	case "_password":
		a.Password = &value
	case "tokenHelper", "token-helper":
		value = strings.TrimSpace(value)
		a.TokenHelper = &value
	case "always-auth", "always_auth":
		a.AlwaysAuth = truthy(value)
	case "ca", "ca[]":
		a.TLS.CA = append(a.TLS.CA, pem(value))
	case "cafile", "caFile":
		a.TLS.CAFile = &value
	case "cert":
		value = pem(value)
		a.TLS.Cert = &value
	case "key":
		value = pem(value)
		a.TLS.Key = &value
	}
}

func isEnvAuth(key string) bool {
	switch key {
	case "_authToken", "_auth", "username", "_password", "cert", "key":
		return true
	}
	return false
}
func rescopable(key string) bool {
	return isEnvAuth(key) || key == "tokenHelper" || key == "token-helper"
}
func canonicalSuffix(key string) string {
	if key == "token-helper" {
		return "tokenHelper"
	}
	return key
}
func authField(key string) bool {
	if rescopable(key) {
		return true
	}
	switch key {
	case "always-auth", "always_auth", "ca", "ca[]", "cafile", "caFile":
		return true
	}
	return false
}
func containsEnvRef(value string) bool {
	_, rest, ok := strings.Cut(value, "${")
	return ok && strings.Contains(rest, "}")
}
func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}
func parseBool(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1":
		return true, true
	case "false", "0":
		return false, true
	}
	return false, false
}
func nonempty(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
func proxy(value string) *string {
	v := nonempty(value)
	if v != nil && (*v == "false" || *v == "null") {
		return nil
	}
	return v
}
func pem(value string) string { return strings.ReplaceAll(value, `\n`, "\n") }

func (c *Config) acceptHelper(key, value string, source Source) bool {
	if key != "tokenHelper" && key != "token-helper" {
		return true
	}
	if !source.Trusted() {
		c.warning("UNTRUSTED_TOKEN_HELPER", "ignoring tokenHelper from untrusted source %s: committed `.npmrc` cannot set this", source)
		return false
	}
	if !ValidTokenHelper(value) {
		c.warning("INVALID_TOKEN_HELPER", "ignoring tokenHelper: value is not a bare absolute path: %q", value)
		return false
	}
	return true
}

func ValidTokenHelper(value string) bool {
	s := strings.TrimSpace(value)
	abs := strings.HasPrefix(s, "/") || strings.HasPrefix(s, `\\`) || len(s) >= 3 && (s[0] >= 'a' && s[0] <= 'z' || s[0] >= 'A' && s[0] <= 'Z') && s[1] == ':' && (s[2] == '/' || s[2] == '\\')
	return abs && !strings.ContainsAny(s, " \t\n\r\v\f\"'`$&|;<>()*?\x00")
}

func (c *Config) applyProxyEnv(env map[string]string) {
	first := func(names ...string) *string {
		for _, name := range names {
			if v := nonempty(env[name]); v != nil {
				return v
			}
		}
		return nil
	}
	disabled := c.LegacyProxy != nil && *c.LegacyProxy == "false"
	if c.HTTPSProxy == nil && !disabled {
		c.HTTPSProxy = c.LegacyProxy
		if c.HTTPSProxy == nil {
			c.HTTPSProxy = first("HTTPS_PROXY", "https_proxy")
		}
	}
	if c.HTTPProxy == nil {
		c.HTTPProxy = c.HTTPSProxy
		if c.HTTPProxy == nil && !disabled {
			c.HTTPProxy = first("HTTP_PROXY", "http_proxy", "PROXY", "proxy")
		}
	}
	if c.NoProxy == nil {
		c.NoProxy = first("NO_PROXY", "no_proxy")
	}
}
