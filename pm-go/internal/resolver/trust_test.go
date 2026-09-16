package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
	"github.com/nubjs/nub/pm-go/internal/registry"
)

func trustVersion(t *testing.T, version, fields string) *registry.Version {
	t.Helper()
	raw := fmt.Sprintf(`{"name":"pkg","version":%q%s}`, version, fields)
	v, err := jsonvalue.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	meta, err := registry.ParseVersion(v)
	if err != nil {
		t.Fatal(err)
	}
	return meta
}

const provenanceFields = `,"dist":{"tarball":"https://example.invalid/pkg.tgz","attestations":{"provenance":{"predicateType":"https://slsa.dev/provenance/v1"}}}`
const publisherFields = `,"_npmUser":{"trustedPublisher":{"id":"github"}}`
const stagedFields = `,"approver":{"name":"publisher"}`

func TestTrustEvidenceShapes(t *testing.T) {
	for _, tc := range []struct {
		fields string
		want   TrustEvidence
	}{
		{"", NoTrustEvidence}, {provenanceFields, Provenance}, {publisherFields, TrustedPublisher}, {stagedFields, StagedPublish},
		{provenanceFields + publisherFields + stagedFields, StagedPublish},
		{`,"approver":{"nested":[null,{},false,"",0]}`, NoTrustEvidence},
		{`,"approver":{"nested":[false,0.5]}`, StagedPublish},
		{`,"approver":-1`, StagedPublish}, {`,"approver":-0.0`, NoTrustEvidence},
		{`,"approver":true`, StagedPublish}, {`,"approver":" "`, StagedPublish},
		{`,"_npmUser":{"trustedPublisher":true}`, NoTrustEvidence},
		{`,"_npmUser":{"trustedPublisher":{"id":3}}`, NoTrustEvidence},
		{`,"_npmUser":{"trustedPublisher":{"id":""}}`, NoTrustEvidence},
		{`,"dist":{"tarball":"x","attestations":{"provenance":{"predicateType":"https://slsa.dev/provenance/vx"}}}`, NoTrustEvidence},
		{`,"dist":{"tarball":"x","attestations":{"provenance":{"predicateType":"https://slsa.dev/provenance/v2extra"}}}`, Provenance},
	} {
		if got := EvidenceFor(trustVersion(t, "1.0.0", tc.fields)); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}
func TestTrustChronologyExclusionAndClock(t *testing.T) {
	p := pack("1.0.0", "2.0.0", "3.0.0-beta.1")
	p.Versions["1.0.0"] = trustVersion(t, "1.0.0", provenanceFields)
	p.Versions["3.0.0-beta.1"] = trustVersion(t, "3.0.0-beta.1", stagedFields)
	p.Time = map[string]string{"1.0.0": "2020-01-01T00:00:00.000Z", "2.0.0": "2020-01-03T00:00:00.000Z", "3.0.0-beta.1": "2020-01-02T00:00:00.000Z"}
	err := CheckNoTrustDowngrade(p, "2.0.0", p.Versions["2.0.0"], TrustOptions{})
	if err == nil || err.PriorVersion != "1.0.0" || err.PriorEvidence != Provenance || err.MissingTime {
		t.Fatal(err)
	}
	delete(p.Time, "2.0.0")
	if err = CheckNoTrustDowngrade(p, "2.0.0", p.Versions["2.0.0"], TrustOptions{}); err == nil || !err.MissingTime {
		t.Fatal(err)
	}
	policy, e := ParseVersionPolicy([]string{"pkg@^2"})
	if e != nil {
		t.Fatal(e)
	}
	if err = CheckNoTrustDowngrade(p, "2.0.0", p.Versions["2.0.0"], TrustOptions{Exclude: policy}); err != nil {
		t.Fatal(err)
	}
	p.Time["2.0.0"] = "2020-01-03T00:00:00.000Z"
	opts := TrustOptions{IgnoreAfterMinutes: 60, Now: time.Date(2020, 1, 3, 1, 0, 0, 0, time.UTC)}
	if err = CheckNoTrustDowngrade(p, "2.0.0", p.Versions["2.0.0"], opts); err == nil {
		t.Fatal("boundary should not be exempt")
	}
	opts.Now = opts.Now.Add(time.Second)
	if err = CheckNoTrustDowngrade(p, "2.0.0", p.Versions["2.0.0"], opts); err != nil {
		t.Fatal(err)
	}
	p.Time = nil
	if err = CheckNoTrustDowngrade(p, "2.0.0", p.Versions["2.0.0"], TrustOptions{}); err != nil {
		t.Fatal(err)
	}
}
func TestTrustRepickDirectionTagAndAge(t *testing.T) {
	p := pack("1.0.0", "1.1.0", "1.2.0", "1.3.0", "1.4.0")
	for i, raw := range peerKeys(p.Versions) {
		fields := provenanceFields
		if raw == "1.3.0" {
			fields = ""
		}
		p.Versions[raw] = trustVersion(t, raw, fields)
		p.Time[raw] = fmt.Sprintf("2020-01-%02dT00:00:00.000Z", i+1)
	}
	p.Tags["latest"] = "1.3.0"
	p.Tags["next"] = "1.3.0"
	check := func(requested string, pick PickOptions, vuln map[string][]string, want string) {
		t.Helper()
		got := RepickPastTrustDowngrade(p, "pkg", requested, "1.3.0", pick, TrustOptions{}, vuln)
		if got == nil && want != "" || got != nil && got.Version != want {
			t.Fatal(requested, got, want)
		}
	}
	check("^1", PickOptions{}, nil, "1.2.0")
	check("latest", PickOptions{}, nil, "1.2.0")
	check("next", PickOptions{}, nil, "")
	check("^1", PickOptions{Lowest: true}, nil, "1.4.0")
	check("latest", PickOptions{Lowest: true}, nil, "")
	check("^1", PickOptions{Cutoff: "2020-01-02T00:00:00.000Z", Strict: true}, nil, "1.1.0")
	check("^1", PickOptions{}, map[string][]string{"pkg": {"1.2.0"}}, "1.1.0")
	check("^1", PickOptions{}, map[string][]string{"pkg": {"*"}}, "1.2.0")
}

func runPolicyOracle(t *testing.T, operation string, input any, output any) {
	t.Helper()
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		t.Skip("reference library CI")
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "input.json")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, oracle, operation, path).CombinedOutput()
	if err != nil {
		t.Fatal(string(out), err)
	}
	if err = json.Unmarshal(out, output); err != nil {
		t.Fatal(string(out), err)
	}
}
func TestRustTrustOracle(t *testing.T) {
	type input struct {
		Packument          json.RawMessage
		Picked             string
		Excludes           []string
		IgnoreAfterMinutes uint64
	}
	var inputs []input
	var packs []*registry.Packument
	for _, prior := range []string{"1.0.0", "3.0.0-beta.1", "9.0.0", "invalid"} {
		for _, before := range []string{"", provenanceFields, publisherFields, stagedFields} {
			for _, after := range []string{"", provenanceFields, publisherFields, stagedFields, `,"approver":{"name":""}`, `,"approver":[0,1]`, `,"_npmUser":{"trustedPublisher":"github"}`} {
				for _, mode := range []int{0, 1, 2, 3, 4} {
					p := pack(prior, "2.0.0")
					p.Versions[prior] = trustVersion(t, prior, before)
					p.Versions["2.0.0"] = trustVersion(t, "2.0.0", after)
					p.Time = map[string]string{prior: "2020-01-01T00:00:00.000Z", "2.0.0": "2020-02-01T00:00:00.000Z"}
					excludes := []string{}
					ignore := uint64(0)
					switch mode {
					case 1:
						p.Time = map[string]string{}
					case 2:
						delete(p.Time, "2.0.0")
					case 3:
						delete(p.Time, "2.0.0")
						excludes = []string{"pkg@^2"}
					case 4:
						ignore = 60
					}
					versions := map[string]*jsonvalue.Value{}
					for v, meta := range p.Versions {
						versions[v] = meta.Raw
					}
					raw, err := json.Marshal(map[string]any{"name": p.Name, "time": p.Time, "versions": versions})
					if err != nil {
						t.Fatal(err)
					}
					inputs = append(inputs, input{raw, "2.0.0", excludes, ignore})
					packs = append(packs, p)
				}
			}
		}
	}
	var refs []struct {
		Evidence TrustEvidence
		Prior    *PriorTrustEvidence
		Error    *string
	}
	runPolicyOracle(t, "trust", inputs, &refs)
	if len(refs) != len(inputs) {
		t.Fatal("result count")
	}
	for i, in := range inputs {
		p := packs[i]
		ref := refs[i]
		if EvidenceFor(p.Versions[in.Picked]) != ref.Evidence || !reflect.DeepEqual(StrongestPriorEvidence(p, in.Picked), ref.Prior) {
			t.Fatalf("case %d evidence or prior: %+v", i, ref)
		}
		excludes, err := ParseVersionPolicy(in.Excludes)
		if err != nil {
			t.Fatal(err)
		}
		got := CheckNoTrustDowngrade(p, in.Picked, p.Versions[in.Picked], TrustOptions{Exclude: excludes, IgnoreAfterMinutes: in.IgnoreAfterMinutes, Now: time.Now()})
		if ref.Error == nil && got != nil || ref.Error != nil && (got == nil || got.Error() != *ref.Error) {
			t.Fatalf("case %d: Go=%v Rust=%v", i, got, ref.Error)
		}
	}
	t.Logf("compared %d trust evidence, chronology and diagnostic cases", len(inputs))
}
