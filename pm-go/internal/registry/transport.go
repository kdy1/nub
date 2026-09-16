package registry

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nubjs/nub/pm-go/internal/npmconfig"
)

type FetchPolicy struct {
	Timeout, StallTimeout              time.Duration
	Retries, RetryFactor               uint32
	RetryMin, RetryMax                 time.Duration
	PackumentMaxBytes, TarballMaxBytes int64
}

func DefaultFetchPolicy() FetchPolicy {
	return FetchPolicy{
		Timeout: 5 * time.Minute, StallTimeout: time.Minute, Retries: 2, RetryFactor: 10, RetryMin: 10 * time.Second, RetryMax: time.Minute, PackumentMaxBytes: 200 << 20, TarballMaxBytes: 1 << 30,
	}
}

func (p FetchPolicy) Backoff(attempt uint32) time.Duration {
	wait := p.RetryMin
	factor := max(p.RetryFactor, 1)
	for i := uint32(1); i < attempt; i++ {
		if wait >= p.RetryMax || factor > 1 && wait > p.RetryMax/time.Duration(factor) {
			return p.RetryMax
		}
		wait *= time.Duration(factor)
	}
	return min(max(wait, p.RetryMin), p.RetryMax)
}

type ClientOptions struct {
	Dir       string
	Env       []string
	Policy    FetchPolicy
	UserAgent string
	Warn      func(npmconfig.Warning)
}

type helperResult struct {
	done  chan struct{}
	token string
}
type Client struct {
	Config  npmconfig.Config
	options ClientOptions
	mu      sync.Mutex
	http    map[*npmconfig.TLS]*http.Client
	helpers map[string]*helperResult
}

func NewClient(config npmconfig.Config, options ClientOptions) *Client {
	return &Client{Config: config, options: options, http: map[*npmconfig.TLS]*http.Client{}, helpers: map[string]*helperResult{}}
}

func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, h := range c.http {
		h.CloseIdleConnections()
	}
}

type Response struct {
	Status int
	Header http.Header
	Body   []byte
}
type Request struct {
	Method, URL, Registry, Package string
	Header                         http.Header
	Body                           []byte
	MaxBytes                       int64
	Retry                          bool
}

var errStalled = errors.New("registry response stalled")

type BodyTooLarge struct{ Limit int64 }

func (e *BodyTooLarge) Error() string { return fmt.Sprintf("response body exceeds cap %d", e.Limit) }

// Do applies the same retry budget to response headers and bodies. Mutation
// callers opt in explicitly; a publish or account update is never retried by
// merely constructing a client with fetch retries enabled.
func (c *Client) Do(ctx context.Context, input Request) (Response, error) {
	var last Response
	var err error
	timeouts := 0
	policy := c.options.Policy
	for attempt := uint32(0); ; attempt++ {
		if err := ctx.Err(); err != nil {
			return last, err
		}
		last, err = c.attempt(ctx, input)
		if ctx.Err() != nil {
			return last, ctx.Err()
		}
		retry := err != nil || last.Status == 429 || last.Status >= 500 && last.Status <= 599
		var large *BodyTooLarge
		if errors.As(err, &large) {
			retry = false
		}
		if !input.Retry || !retry || attempt >= policy.Retries {
			return last, err
		}
		var netErr net.Error
		if errors.Is(err, errStalled) || errors.Is(err, context.DeadlineExceeded) || errors.As(err, &netErr) && netErr.Timeout() {
			if timeouts >= 1 {
				return last, err
			}
			timeouts++
		}
		delay := policy.Backoff(attempt + 1)
		if err == nil {
			if d, ok := RetryAfter(last.Header.Get("Retry-After")); ok {
				delay = d
			}
		}
		c.warn("HTTP_RETRY", fmt.Sprintf("registry request will retry (attempt %d of %d)", attempt+2, uint64(policy.Retries)+1))
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return last, ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *Client) attempt(ctx context.Context, input Request) (Response, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	if c.options.Policy.Timeout > 0 {
		var stop context.CancelFunc
		ctx, stop = context.WithTimeout(ctx, c.options.Policy.Timeout)
		defer stop()
	}
	method := input.Method
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, input.URL, bytes.NewReader(input.Body))
	if err != nil {
		return Response{}, err
	}
	req.Header = input.Header.Clone()
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	if req.Header.Get("User-Agent") == "" && c.options.UserAgent != "" {
		req.Header.Set("User-Agent", c.options.UserAgent)
	}
	if req.Header.Get("Authorization") == "" {
		if auth := c.Config.AuthFor(input.Registry, input.Package); auth != nil {
			if auth.Token != nil {
				req.Header.Set("Authorization", "Bearer "+*auth.Token)
			} else if token := c.helper(ctx, auth.TokenHelper); token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			} else if basic, ok := auth.BasicValue(); ok {
				req.Header.Set("Authorization", "Basic "+basic)
			}
		}
	}
	h := c.httpFor(input.Registry, input.Package)
	resp, err := h.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	out := Response{Status: resp.StatusCode, Header: resp.Header.Clone()}
	if input.MaxBytes > 0 && resp.ContentLength > input.MaxBytes {
		return out, &BodyTooLarge{input.MaxBytes}
	}
	var stalled atomic.Bool
	var timer *time.Timer
	if c.options.Policy.StallTimeout > 0 {
		timer = time.AfterFunc(c.options.Policy.StallTimeout, func() { stalled.Store(true); cancel(errStalled) })
		defer timer.Stop()
	}
	var body bytes.Buffer
	buffer := make([]byte, 32<<10)
	for {
		n, e := resp.Body.Read(buffer)
		if n > 0 {
			if input.MaxBytes > 0 && int64(body.Len())+int64(n) > input.MaxBytes {
				return out, &BodyTooLarge{input.MaxBytes}
			}
			body.Write(buffer[:n])
			if timer != nil {
				timer.Reset(c.options.Policy.StallTimeout)
			}
		}
		if stalled.Load() {
			return out, errStalled
		}
		if e == io.EOF {
			out.Body = body.Bytes()
			return out, nil
		}
		if e != nil {
			return out, e
		}
	}
}

func (c *Client) helper(ctx context.Context, path *string) string {
	if path == nil || !npmconfig.ValidTokenHelper(*path) {
		return ""
	}
	c.mu.Lock()
	if found := c.helpers[*path]; found != nil {
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return ""
		case <-found.done:
			return found.token
		}
	}
	r := &helperResult{done: make(chan struct{})}
	c.helpers[*path] = r
	c.mu.Unlock()
	defer close(r.done)
	cmd := exec.CommandContext(ctx, *path)
	cmd.Dir = c.options.Dir
	cmd.Env = append([]string{}, c.options.Env...)
	output, err := cmd.Output()
	if err != nil {
		c.warn("TOKEN_HELPER_FAILED", "tokenHelper could not return a token")
		return ""
	}
	line, _, _ := strings.Cut(string(output), "\n")
	r.token = strings.TrimSpace(line)
	return r.token
}

func (c *Client) warn(code, message string) {
	if c.options.Warn != nil {
		c.options.Warn(npmconfig.Warning{Code: "WARN_AUBE_" + code, Message: message})
	}
}

func RetryAfter(raw string) (time.Duration, bool) {
	seconds, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, false
	}
	return time.Duration(min(seconds, 60)) * time.Second, true
}

func redirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("too many redirects")
	}
	if len(via) > 0 {
		last := via[len(via)-1].URL
		if last.Scheme == "https" && req.URL.Scheme != "https" {
			return http.ErrUseLastResponse
		}
		// net/http copies the original headers again for every redirect. Once
		// this chain crosses an authority boundary they must stay stripped,
		// even when a later hop is on the same hostname as an earlier hop.
		crossed := !sameOrigin(via[0].URL, req.URL)
		for _, prior := range via {
			crossed = crossed || !sameOrigin(via[0].URL, prior.URL)
		}
		if crossed {
			req.Header.Del("Authorization")
			req.Header.Del("Cookie")
			req.Header.Del("Cookie2")
			req.Header.Del("Proxy-Authorization")
			req.Header.Del("Www-Authenticate")
		}
	}
	return nil
}
func sameOrigin(a, b *url.URL) bool {
	port := func(u *url.URL) string {
		if p := u.Port(); p != "" {
			return p
		}
		if u.Scheme == "https" {
			return "443"
		}
		return "80"
	}
	return strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}
