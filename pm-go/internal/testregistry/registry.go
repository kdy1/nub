// Package testregistry provides immutable npm metadata and tarballs for tests.
package testregistry

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

type Package struct {
	Name     string
	Version  string
	Manifest map[string]any
	Files    map[string]string
	// Empty uses an old, fixed timestamp so native age gates can resolve fixtures.
	Published   string
	TarballPath string
}

type Registry struct {
	*httptest.Server
	mu       sync.Mutex
	requests []string
}

func Start(t testing.TB, packages ...Package) *Registry {
	t.Helper()
	r := &Registry{}
	metadata := map[string]map[string]any{}
	tarballs := map[string][]byte{}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.requests = append(r.requests, req.Method+" "+req.URL.Path)
		r.mu.Unlock()
		if req.Method != "GET" {
			http.Error(w, "read-only registry", 405)
			return
		}
		if data, ok := tarballs[req.URL.Path]; ok {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(data)
			return
		}
		name, err := url.PathUnescape(strings.TrimPrefix(req.URL.EscapedPath(), "/"))
		if err != nil {
			http.Error(w, "bad path", 400)
			return
		}
		if value, ok := metadata[name]; ok {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(value)
			return
		}
		http.NotFound(w, req)
	}))
	t.Cleanup(r.Close)
	for _, p := range packages {
		manifest := map[string]any{}
		for k, v := range p.Manifest {
			manifest[k] = v
		}
		manifest["name"], manifest["version"] = p.Name, p.Version
		var b bytes.Buffer
		gz := gzip.NewWriter(&b)
		tw := tar.NewWriter(gz)
		files := map[string]string{}
		for k, v := range p.Files {
			files[k] = v
		}
		data, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		files["package.json"] = string(data)
		for name, contents := range files {
			if err := tw.WriteHeader(&tar.Header{Name: "package/" + name, Mode: 0644, Size: int64(len(contents))}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write([]byte(contents)); err != nil {
				t.Fatal(err)
			}
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		path := fmt.Sprintf("/tarballs/%s-%s.tgz", p.Name, p.Version)
		if p.TarballPath != "" {
			path = p.TarballPath
		}
		tarballs[path] = b.Bytes()
		hash := sha512.Sum512(b.Bytes())
		manifest["dist"] = map[string]any{"tarball": r.URL + path, "integrity": "sha512-" + base64.StdEncoding.EncodeToString(hash[:])}
		if metadata[p.Name] == nil {
			metadata[p.Name] = map[string]any{"name": p.Name, "versions": map[string]any{}, "dist-tags": map[string]string{}, "time": map[string]string{}}
		}
		published := p.Published
		if published == "" {
			published = "2020-01-01T00:00:00.000Z"
		}
		metadata[p.Name]["time"].(map[string]string)[p.Version] = published
		metadata[p.Name]["versions"].(map[string]any)[p.Version] = manifest
		metadata[p.Name]["dist-tags"].(map[string]string)["latest"] = p.Version
	}
	return r
}

func (r *Registry) Requests() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.requests...)
}
