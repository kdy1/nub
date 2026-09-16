package resolver

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
	"github.com/nubjs/nub/pm-go/internal/registry"
	"github.com/nubjs/nub/pm-go/internal/semver"
)

type TrustEvidence uint8

const (
	NoTrustEvidence TrustEvidence = iota
	Provenance
	TrustedPublisher
	StagedPublish
)

func (e TrustEvidence) Label() string {
	switch e {
	case Provenance:
		return "provenance attestation"
	case TrustedPublisher:
		return "trusted publisher"
	case StagedPublish:
		return "staged publish approval"
	default:
		return "no trust evidence"
	}
}

// EvidenceFor validates registry metadata shapes. It does not verify signatures
// or download attestation bundles, matching the reference trust policy.
func EvidenceFor(meta *registry.Version) TrustEvidence {
	if meta == nil {
		return NoTrustEvidence
	}
	if hasApprover(meta.Raw.Get("approver")) {
		return StagedPublish
	}
	if meta.Raw.Get("_npmUser").Get("trustedPublisher").Get("id").Text() != "" {
		return TrustedPublisher
	}
	if meta.Dist != nil {
		predicate := meta.Dist.Attestations.Get("provenance").Get("predicateType").Text()
		if rest, ok := strings.CutPrefix(predicate, "https://slsa.dev/provenance/v"); ok && rest != "" && rest[0] >= '0' && rest[0] <= '9' {
			return Provenance
		}
	}
	return NoTrustEvidence
}
func hasApprover(v *jsonvalue.Value) bool {
	if v == nil {
		return false
	}
	switch v.Kind {
	case 's':
		return v.Text() != ""
	case 'b':
		b, _ := v.Scalar.(bool)
		return b
	case 'd':
		n, ok := v.Scalar.(json.Number)
		if !ok {
			return false
		}
		f, err := n.Float64()
		return err == nil && f != 0
	case '[':
		for _, item := range v.Array {
			if hasApprover(item) {
				return true
			}
		}
	case '{':
		for _, field := range v.Object {
			if hasApprover(field.Value) {
				return true
			}
		}
	}
	return false
}

type PriorTrustEvidence struct {
	Version  string
	Evidence TrustEvidence
}
type TrustCheckError struct {
	Name, Version                  string
	MissingTime                    bool
	CurrentEvidence, PriorEvidence TrustEvidence
	PriorVersion                   string
}

func (e *TrustCheckError) Error() string {
	if e.MissingTime {
		return fmt.Sprintf("trust check failed for %s@%s (trustPolicy=no-downgrade): registry packument has no `time` entry for the picked version", e.Name, e.Version)
	}
	return fmt.Sprintf("trust downgrade for %s@%s (trustPolicy=no-downgrade): earlier published version %s had %s but this version has %s", e.Name, e.Version, e.PriorVersion, e.PriorEvidence.Label(), e.CurrentEvidence.Label())
}
func (e *TrustCheckError) Code() string {
	if e.MissingTime {
		return "ERR_AUBE_TRUST_MISSING_TIME"
	}
	return "ERR_AUBE_TRUST_DOWNGRADE"
}

type TrustOptions struct {
	Exclude            PackageVersionPolicy
	IgnoreAfterMinutes uint64
	// Now is supplied by the execution context. Zero/unix-before-epoch means
	// the wall clock is unavailable and disables only the age exemption.
	Now time.Time
}
type TrustHistory struct {
	Time     map[string]string
	Evidence map[string]TrustEvidence
}

func HistoryFor(p *registry.Packument) TrustHistory {
	h := TrustHistory{Time: p.Time, Evidence: map[string]TrustEvidence{}}
	for raw, meta := range p.Versions {
		h.Evidence[raw] = EvidenceFor(meta)
	}
	return h
}
func StrongestPriorEvidence(p *registry.Packument, picked string) *PriorTrustEvidence {
	return HistoryFor(p).StrongestPrior(picked)
}
func (h TrustHistory) StrongestPrior(picked string) *PriorTrustEvidence {
	pickedTime, ok := h.Time[picked]
	if !ok {
		return nil
	}
	pv, err := semver.ParseEngineVersion(picked)
	skipPre := err == nil && pv.Prerelease() == ""
	var best *PriorTrustEvidence
	for _, raw := range peerKeys(h.Evidence) {
		if raw == picked {
			continue
		}
		date, ok := h.Time[raw]
		if !ok || date >= pickedTime {
			continue
		}
		if skipPre {
			if v, err := semver.ParseEngineVersion(raw); err == nil && v.Prerelease() != "" {
				continue
			}
		}
		evidence := h.Evidence[raw]
		if evidence != NoTrustEvidence && (best == nil || evidence > best.Evidence) {
			best = &PriorTrustEvidence{raw, evidence}
		}
		if best != nil && best.Evidence == StagedPublish {
			break
		}
	}
	return best
}
func CheckNoTrustDowngrade(p *registry.Packument, picked string, meta *registry.Version, opts TrustOptions) *TrustCheckError {
	return HistoryFor(p).Check(p.Name, picked, EvidenceFor(meta), opts)
}
func (h TrustHistory) Check(name, picked string, evidence TrustEvidence, opts TrustOptions) *TrustCheckError {
	if opts.Exclude.Excludes(name, picked) || len(h.Time) == 0 {
		return nil
	}
	published, ok := h.Time[picked]
	if !ok {
		return &TrustCheckError{Name: name, Version: picked, MissingTime: true}
	}
	if opts.IgnoreAfterMinutes > 0 && !opts.Now.IsZero() && opts.Now.Unix() >= 0 {
		now := uint64(opts.Now.Unix())
		cutoff := uint64(0)
		if opts.IgnoreAfterMinutes <= now/60 {
			cutoff = now - opts.IgnoreAfterMinutes*60
		}
		if published < time.Unix(int64(cutoff), 0).UTC().Format("2006-01-02T15:04:05.000Z") {
			return nil
		}
	}
	prior := h.StrongestPrior(picked)
	if prior != nil && evidence < prior.Evidence {
		return &TrustCheckError{Name: name, Version: picked, CurrentEvidence: evidence, PriorEvidence: prior.Evidence, PriorVersion: prior.Version}
	}
	return nil
}

// RepickPastTrustDowngrade stays strictly below a rejected highest-wins pick
// (above a lowest-wins pick). Trust and age remain mandatory in both tiers;
// known-vulnerable alternatives are considered only after clean alternatives.
func RepickPastTrustDowngrade(p *registry.Packument, name, requested, rejected string, pick PickOptions, trust TrustOptions, vulnerable map[string][]string) *registry.Version {
	r, err := semver.ParseDependencyRange(requested)
	if err != nil {
		if requested != "latest" {
			return nil
		}
		latest, err := semver.ParseEngineVersion(p.Tags["latest"])
		if err != nil || latest.Prerelease() != "" {
			return nil
		}
		r, err = semver.ParseEngineRange("<=" + p.Tags["latest"])
		if err != nil {
			return nil
		}
	}
	rejectedVersion, err := semver.ParseEngineVersion(rejected)
	if err != nil {
		return nil
	}
	var best, unsafe *registry.Version
	var bestVersion, unsafeVersion *semver.Version
	history := HistoryFor(p)
	for _, raw := range peerKeys(p.Versions) {
		v, err := semver.ParseEngineVersion(raw)
		if err != nil {
			continue
		}
		if pick.Lowest && v.Compare(rejectedVersion) <= 0 || !pick.Lowest && v.Compare(rejectedVersion) >= 0 || !r.Contains(v) {
			continue
		}
		cutoff := pick.Cutoff
		if pick.AgeExempt != nil && pick.AgeExempt(raw, v) {
			cutoff = pick.ExemptCutoff
		}
		if ClassifyAge(p, raw, cutoff, pick.Strict) != Clears {
			continue
		}
		meta := p.Versions[raw]
		if history.Check(p.Name, raw, EvidenceFor(meta), trust) != nil {
			continue
		}
		if IsVulnerable(name, raw, vulnerable) {
			if outranks(v, meta, unsafeVersion, unsafe, pick.Lowest) {
				unsafe, unsafeVersion = meta, v
			}
		} else if outranks(v, meta, bestVersion, best, pick.Lowest) {
			best, bestVersion = meta, v
		}
	}
	if best != nil {
		return best
	}
	return unsafe
}
