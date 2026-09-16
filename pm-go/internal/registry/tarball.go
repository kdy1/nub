package registry

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

func (c *Client) Tarball(ctx context.Context, rawURL string, mode NetworkMode) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return nil, fmt.Errorf("invalid tarball URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("tarball: refusing scheme %q", parsed.Scheme)
	}
	if mode == Offline {
		return nil, fmt.Errorf("offline: tarball is not available in the store")
	}
	// Match URI credentials against the full URL, including its path. A
	// registry credential must never follow a tarball hosted elsewhere.
	response, err := c.Do(ctx, Request{URL: rawURL, Registry: rawURL, Header: http.Header{"Accept-Encoding": []string{"identity"}}, MaxBytes: c.options.Policy.TarballMaxBytes, Retry: true})
	if err != nil {
		return nil, err
	}
	if response.Status < 200 || response.Status >= 300 {
		return nil, &HTTPError{Status: response.Status, Package: "tarball"}
	}
	return response.Body, nil
}
