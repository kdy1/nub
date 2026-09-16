package resolver

import (
	"testing"

	"github.com/nubjs/nub/pm-go/internal/registry"
)

func TestMergeCompactMetadataRetainsFresherVersionsAndHistory(t *testing.T) {
	pack := func(version string) *registry.Packument {
		return &registry.Packument{Name: "p", Versions: map[string]*registry.Version{version: {Name: "p", Version: version}}, Time: map[string]string{version: version}, Tags: map[string]string{"latest": version}}
	}
	history := func() *TrustHistory {
		return &TrustHistory{Evidence: map[string]TrustEvidence{"0.1.0": TrustedPublisher}}
	}
	for _, fullFirst := range []bool{false, true} {
		d := &driver{packuments: map[string]*registry.Packument{}, histories: map[string]TrustHistory{}}
		if fullFirst {
			d.mergeMetadata("p", pack("1.0.0"), nil)
		}
		d.mergeMetadata("p", pack("2.0.0"), history())
		if !fullFirst {
			d.mergeMetadata("p", pack("1.0.0"), nil)
		}
		if len(d.packuments["p"].Versions) != 2 || d.histories["p"].Evidence["0.1.0"] != TrustedPublisher || len(d.packuments["p"].Time) != 2 {
			t.Fatal(fullFirst, d.packuments, d.histories)
		}
		full := pack("2.0.0")
		full.Versions["1.0.0"] = &registry.Version{Name: "p", Version: "1.0.0"}
		d.mergeMetadata("p", full, nil)
		if _, ok := d.histories["p"]; ok {
			t.Fatal("complete response did not supersede compact history")
		}
		d.mergeMetadata("p", pack("2.0.0"), history())
		if d.packuments["p"] != full || len(full.Versions) != 2 {
			t.Fatal("compact response displaced authoritative full metadata")
		}
	}
}
