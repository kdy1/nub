package registry

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nubjs/nub/pm-go/internal/npmconfig"
)

func (c *Client) httpFor(registry, name string) *http.Client {
	settings := c.Config.ScopedTLSFor(registry, name)
	if settings == nil {
		if a := c.Config.AuthFor(registry, ""); a != nil {
			settings = &a.TLS
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if h := c.http[settings]; h != nil {
		return h
	}
	root, err := x509.SystemCertPool()
	if err != nil {
		root = x509.NewCertPool()
	}
	addFile := func(path string) {
		if !filepath.IsAbs(path) {
			path = filepath.Join(c.options.Dir, path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			c.warn("UNREADABLE_CAFILE", "ignoring unreadable CA bundle")
			return
		}
		if !root.AppendCertsFromPEM(data) {
			c.warn("INVALID_CAFILE", "ignoring invalid CA bundle")
		}
	}
	for _, item := range c.options.Env {
		if key, value, ok := strings.Cut(item, "="); ok && key == "NODE_EXTRA_CA_CERTS" && value != "" {
			addFile(value)
			break
		}
	}
	for _, t := range []*npmconfig.TLS{&c.Config.TLS, settings} {
		if t != nil {
			for _, pem := range t.CA {
				if !root.AppendCertsFromPEM([]byte(pem)) {
					c.warn("INVALID_CA", "ignoring invalid inline CA")
				}
			}
			if t.CAFile != nil {
				addFile(*t.CAFile)
			}
		}
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: root, InsecureSkipVerify: !c.Config.StrictSSL}
	if settings != nil && settings.Cert != nil && settings.Key != nil {
		if pair, err := tls.X509KeyPair([]byte(*settings.Cert), []byte(*settings.Key)); err == nil {
			tlsConfig.Certificates = []tls.Certificate{pair}
		} else {
			c.warn("INVALID_CLIENT_CERT", "ignoring invalid per-registry client cert/key")
		}
	}
	dialer := &net.Dialer{Timeout: c.options.Policy.Timeout, KeepAlive: 30 * time.Second}
	if c.Config.LocalAddress != nil {
		dialer.LocalAddr = &net.TCPAddr{IP: c.Config.LocalAddress}
	}
	transport := &http.Transport{
		Proxy: c.proxy, DialContext: dialer.DialContext, TLSClientConfig: tlsConfig, ForceAttemptHTTP2: true,
		MaxIdleConns: 256, MaxIdleConnsPerHost: 64, IdleConnTimeout: 90 * time.Second,
		TLSHandshakeTimeout: c.options.Policy.Timeout, ResponseHeaderTimeout: c.options.Policy.StallTimeout, ExpectContinueTimeout: time.Second,
	}
	if c.Config.MaxSockets > 0 {
		transport.MaxIdleConnsPerHost = c.Config.MaxSockets
	}
	h := &http.Client{Transport: transport, CheckRedirect: redirect}
	c.http[settings] = h
	return h
}

func (c *Client) proxy(req *http.Request) (*url.URL, error) {
	if c.Config.NoProxy != nil && bypassProxy(req.URL, *c.Config.NoProxy) {
		return nil, nil
	}
	var raw *string
	if req.URL.Scheme == "https" {
		raw = c.Config.HTTPSProxy
	} else if req.URL.Scheme == "http" {
		raw = c.Config.HTTPProxy
	}
	if raw == nil {
		return nil, nil
	}
	value := *raw
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" {
		c.warn("INVALID_PROXY", "ignoring invalid proxy URL")
		return nil, nil
	}
	return u, nil
}

func bypassProxy(u *url.URL, rules string) bool {
	host := strings.ToLower(u.Hostname())
	ip := net.ParseIP(host)
	for _, rule := range strings.Split(rules, ",") {
		rule = strings.ToLower(strings.TrimSpace(rule))
		if rule == "" {
			continue
		}
		if rule == "*" {
			return true
		}
		if _, network, err := net.ParseCIDR(rule); err == nil {
			if ip != nil && network.Contains(ip) {
				return true
			}
			continue
		}
		if h, p, err := net.SplitHostPort(rule); err == nil {
			port := u.Port()
			if port == "" {
				if u.Scheme == "https" {
					port = "443"
				} else {
					port = "80"
				}
			}
			if p != port {
				continue
			}
			rule = h
		}
		rule = strings.TrimPrefix(rule, ".")
		if host == rule || strings.HasSuffix(host, "."+rule) {
			return true
		}
	}
	return false
}
