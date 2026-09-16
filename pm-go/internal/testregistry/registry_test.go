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
