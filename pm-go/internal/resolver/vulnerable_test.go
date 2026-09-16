package resolver

import (
	"github.com/nubjs/nub/pm-go/internal/semver"
	"testing"
)

func TestVulnerabilityPreferenceAgeTiers(t *testing.T) {
	p := pack("1.0.0", "1.1.0", "1.2.0", "1.3.0")
	fallback := p.Versions["1.0.0"]
	ranges := map[string][]string{"pkg": {"invalid", "<1.1.0"}}
	opts := PickOptions{Cutoff: "2020-01-01"}
	p.Time["1.0.0"] = "2019-01-01"
	p.Time["1.3.0"] = "2026-01-01"
	check := func(want string) {
		t.Helper()
		if got := PreferNonVulnerable("pkg", p, "^1", fallback, opts, ranges); got.Version != want {
			t.Fatal(got.Version, want)
		}
	}
	check("1.2.0") // Undated safe versions outrank the vulnerable fallback.
	p.Time["1.1.0"] = "2019-01-01"
	p.Versions["1.1.0"].Deprecated = new("old")
	check("1.1.0") // A dated acceptable version outranks the undated tier.
	opts.AgeExempt = func(raw string, _ *semver.Version) bool { return raw == "1.3.0" }
	check("1.3.0")
	opts.ExemptCutoff = "2020-01-01"
	check("1.1.0")
	delete(p.Versions, "1.1.0")
	delete(p.Versions, "1.2.0")
	check("1.0.0") // Known-too-new candidates never enter the undated tier.
	if got := PreferNonVulnerable("pkg", p, "tag", fallback, opts, ranges); got != fallback {
		t.Fatal(got)
	}
}

func TestVulnerabilityPreferenceRankingAndNoop(t *testing.T) {
	p := pack("1.0.0", "1.1.0", "1.2.0", "1.3.0")
	p.Versions["1.1.0"].Deprecated = new("old")
	p.Versions["1.3.0"].Deprecated = new("old")
	ranges := map[string][]string{"pkg": {"1.0.0"}}
	for _, lowest := range []bool{false, true} {
		got := PreferNonVulnerable("pkg", p, "*", p.Versions["1.0.0"], PickOptions{Lowest: lowest}, ranges)
		if got.Version != "1.2.0" {
			t.Fatal(got)
		}
	}
	if got := PreferNonVulnerable("pkg", p, "*", p.Versions["1.1.0"], PickOptions{}, ranges); got != p.Versions["1.1.0"] {
		t.Fatal("non-vulnerable fallback was upgraded")
	}
	if IsVulnerable("pkg", "not-a-version", ranges) || IsVulnerable("other", "1.0.0", ranges) {
		t.Fatal("invalid advisory match")
	}
	if IsVulnerable("pkg", "1.0.0", map[string][]string{"pkg": {"", " "}}) {
		t.Fatal("empty advisory became wildcard")
	}
}
