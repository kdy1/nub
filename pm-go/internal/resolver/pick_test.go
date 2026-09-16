package resolver

import (
	"testing"

	"github.com/nubjs/nub/pm-go/internal/registry"
	"github.com/nubjs/nub/pm-go/internal/semver"
)

func pack(versions ...string) *registry.Packument {
	p := &registry.Packument{Name: "pkg", Versions: map[string]*registry.Version{}, Tags: map[string]string{}, Time: map[string]string{}}
	for _, v := range versions {
		p.Versions[v] = &registry.Version{Name: "pkg", Version: v}
	}
	return p
}
func picked(t *testing.T, p *registry.Packument, spec string, opts PickOptions, want string) {
	t.Helper()
	result := Pick(p, spec, opts)
	if result.Version == nil || result.Version.Version != want {
		t.Fatalf("%s: got %+v, want %s", spec, result, want)
	}
}

func TestPickPreferenceAndDeprecation(t *testing.T) {
	p := pack("1.0.0", "1.2.0", "1.3.0", "2.0.0-beta.1", "invalid")
	p.Tags["latest"] = "1.2.0"
	picked(t, p, "^1", PickOptions{}, "1.2.0")
	picked(t, p, "^1", PickOptions{Locked: "1.0.0"}, "1.0.0")
	picked(t, p, "^1", PickOptions{Lowest: true}, "1.0.0")
	picked(t, p, "npm:actual@^1", PickOptions{}, "1.2.0")
	picked(t, p, "npm:@scope/actual", PickOptions{}, "1.2.0")
	delete(p.Tags, "latest")
	picked(t, p, "latest", PickOptions{}, "1.3.0")
	p.Versions["1.3.0"].Deprecated = new("withdrawn")
	picked(t, p, "^1", PickOptions{}, "1.2.0")
	picked(t, p, "1.3.0", PickOptions{}, "1.3.0")
	p.Versions["1.0.0"].Deprecated = new("withdrawn")
	picked(t, p, "^1", PickOptions{Lowest: true}, "1.2.0")
	if r := Pick(p, "latest", PickOptions{}); r.Version == nil || r.Version.Version != "1.3.0" {
		t.Fatal("missing-tag fallback must pin highest stable even if deprecated", r)
	}
}

func TestPickTagsNeverResolveProtocols(t *testing.T) {
	p := pack("1.0.0", "2.0.0-beta.1")
	p.Tags["next"] = "2.0.0-beta.1"
	picked(t, p, "next", PickOptions{}, "2.0.0-beta.1")
	for _, spec := range []string{"workspace:*", "catalog:", "file:../local", "git+https:repo", "https:tarball", "custom+v1:foo"} {
		p.Tags[spec] = "1.0.0"
		if r := Pick(p, spec, PickOptions{}); r.Version != nil {
			t.Fatal("protocol resolved through registry tag", spec)
		}
	}
	if r := Pick(pack("1.0.0-alpha"), "latest", PickOptions{}); r.Version != nil {
		t.Fatal("latest admitted prerelease without tag")
	}
}

func TestPublishAgeClassification(t *testing.T) {
	p := pack("1.0.0", "1.1.0")
	cutoff := "2026-01-10T00:00:00Z"
	if got := ClassifyAge(p, "1.0.0", cutoff, true); got != Undeterminable {
		t.Fatal(got)
	}
	p.Modified = new("2026-01-01T00:00:00Z")
	if got := ClassifyAge(p, "1.0.0", cutoff, true); got != Clears {
		t.Fatal(got)
	}
	p.Time["1.1.0"] = "2026-01-02T00:00:00Z"
	if got := ClassifyAge(p, "1.0.0", cutoff, true); got != Undeterminable {
		t.Fatal("hole in time map was vouched for", got)
	}
	if got := ClassifyAge(p, "1.0.0", cutoff, false); got != Clears {
		t.Fatal(got)
	}
	p.Time["1.0.0"] = "2026-01-11T00:00:00Z"
	if got := ClassifyAge(p, "1.0.0", cutoff, false); got != TooNew {
		t.Fatal("lenient still compares known time", got)
	}
}

func TestAgeGateSelectionAndBoundedLatest(t *testing.T) {
	p := pack("1.0.0", "1.1.0", "2.0.0")
	p.Tags["latest"] = "1.1.0"
	p.Time = map[string]string{"1.0.0": "2026-01-01", "1.1.0": "2026-01-11", "2.0.0": "2026-01-01"}
	o := PickOptions{Strict: true, Cutoff: "2026-01-10"}
	picked(t, p, "latest", o, "1.0.0")
	picked(t, p, "*", o, "2.0.0")
	o.Locked = "1.1.0"
	picked(t, p, "^1", o, "1.0.0")
	if r := Pick(p, "1.1.0", o); r.Version != nil || r.AgeGate != TooNew {
		t.Fatal(r)
	}
	if r := Pick(p, "^3", o); r.Version != nil || r.AgeGate != Clears {
		t.Fatal("unsatisfiable range mislabeled age-gated", r)
	}
	o.Lowest = true
	if r := Pick(p, "latest", o); r.Version != nil || r.AgeGate != TooNew {
		t.Fatal("time-based latest widened", r)
	}
	p.Tags["next"] = "1.1.0"
	if r := Pick(p, "next", o); r.Version != nil {
		t.Fatal("nonlatest tag widened", r)
	}
}

func TestAgeGateFallbackExemptionsAndUnknownCause(t *testing.T) {
	p := pack("1.0.0", "1.1.0")
	o := PickOptions{Strict: true, Cutoff: "2026-01-10"}
	if r := Pick(p, "*", o); r.AgeGate != Undeterminable {
		t.Fatal(r)
	}
	p.Time["1.0.0"] = "2026-01-11"
	if r := Pick(p, "*", o); r.AgeGate != TooNew {
		t.Fatal("known rejection must outrank unknown age", r)
	}
	p.Time["1.1.0"] = "2026-01-12"
	o.Strict = false
	picked(t, p, "*", o, "1.0.0")
	o.Strict = true
	o.AgeExempt = func(raw string, v *semver.Version) bool { return raw == "1.1.0" && v != nil }
	picked(t, p, "*", o, "1.1.0")
	o.ExemptCutoff = "2026-01-10"
	if r := Pick(p, "*", o); r.Version != nil {
		t.Fatal("exemption bypassed time-based wall", r)
	}
}

func TestPickUsesReferenceEngineGrammar(t *testing.T) {
	p := pack("1.0.0", "1.2.0", "1.2.9")
	picked(t, p, "<=1.2", PickOptions{}, "1.0.0")
	picked(t, p, "^1 trailing-garbage", PickOptions{}, "1.2.9")
	picked(t, p, "", PickOptions{}, "1.2.9")
	p = pack("01.02.03")
	picked(t, p, "^1", PickOptions{}, "01.02.03")
}
