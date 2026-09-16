package testregistry

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestMetadataPublicationTimes(t *testing.T) {
	r := Start(t, Package{Name: "pkg", Version: "1.0.0"}, Package{Name: "pkg", Version: "2.0.0", Published: "2026-09-01T01:02:03.000Z"})
	resp, err := http.Get(r.URL + "/pkg")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var doc struct {
		Time map[string]string `json:"time"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	if doc.Time["1.0.0"] != "2020-01-01T00:00:00.000Z" || doc.Time["2.0.0"] != "2026-09-01T01:02:03.000Z" {
		t.Fatal(doc)
	}
}

func TestExplicitTarballLocation(t *testing.T) {
	r := Start(t, Package{Name: "pkg", Version: "1.0.0", TarballPath: "/pkg/-/pkg-1.0.0.tgz"})
	resp, err := http.Get(r.URL + "/pkg/-/pkg-1.0.0.tgz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.Status)
	}
	metadata, err := http.Get(r.URL + "/pkg")
	if err != nil {
		t.Fatal(err)
	}
	defer metadata.Body.Close()
	var doc struct {
		Versions map[string]struct{ Dist struct{ Tarball string } }
	}
	if err := json.NewDecoder(metadata.Body).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	if got := doc.Versions["1.0.0"].Dist.Tarball; got != r.URL+"/pkg/-/pkg-1.0.0.tgz" {
		t.Fatal(got)
	}
}
