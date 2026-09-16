package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
)

// ExactPackument retains the selected release and only trust evidence for
// other versions. Historical dependency trees are not retained in memory.
type ExactPackument struct {
	Metadata *Version
	Time     map[string]string
	History  map[string]*Version
}

func ParseExactPackument(data []byte, version string) (*ExactPackument, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("expected an npm packument containing the requested exact version")
	}
	out := &ExactPackument{Time: map[string]string{}, History: map[string]*Version{}}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return nil, err
		}
		if key == "versions" {
			if token, err := d.Token(); err != nil || token != json.Delim('{') {
				return nil, fmt.Errorf("expected an npm packument versions map")
			}
			out.Metadata = nil
			out.History = map[string]*Version{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return nil, err
				}
				name, ok := key.(string)
				if !ok {
					return nil, fmt.Errorf("expected a version key")
				}
				var raw json.RawMessage
				if err := d.Decode(&raw); err != nil {
					return nil, err
				}
				value, err := jsonvalue.Parse(raw)
				if err != nil {
					return nil, err
				}
				if name == version {
					out.Metadata, err = ParseVersion(value)
				} else {
					out.History[name], err = parseTrustMetadata(value)
				}
				if err != nil {
					return nil, err
				}
			}
			if _, err := d.Token(); err != nil {
				return nil, err
			}
		} else {
			var raw json.RawMessage
			if err := d.Decode(&raw); err != nil {
				return nil, err
			}
			if key == "time" {
				value, err := jsonvalue.Parse(raw)
				if err != nil {
					return nil, err
				}
				out.Time, err = stringMap(value)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	if _, err := d.Token(); err != nil {
		return nil, err
	}
	var extra json.RawMessage
	if err := d.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("trailing data after packument")
	}
	if out.Metadata == nil {
		return nil, fmt.Errorf("packument does not contain requested version %s", version)
	}
	return out, nil
}

func parseTrustMetadata(value *jsonvalue.Value) (*Version, error) {
	if value == nil || value.Kind != '{' {
		return nil, fmt.Errorf("expected version trust metadata object")
	}
	raw := jsonvalue.Object()
	if approver := value.Get("approver"); approver != nil {
		raw.Put("approver", approver)
	}
	// npm user fields are tolerant in the baseline, including malformed names.
	if user := value.Get("_npmUser"); user != nil && user.Kind == '{' {
		selected := jsonvalue.Object()
		if publisher := user.Get("trustedPublisher"); publisher != nil {
			selected.Put("trustedPublisher", publisher)
		}
		raw.Put("_npmUser", selected)
	}
	out := &Version{Raw: raw}
	if dist := value.Get("dist"); dist != nil && dist.Kind != 'n' {
		if dist.Kind != '{' {
			return nil, fmt.Errorf("expected version trust distribution object")
		}
		if attestations := dist.Get("attestations"); attestations != nil && attestations.Kind != 'n' {
			if attestations.Kind != '{' {
				return nil, fmt.Errorf("expected attestations object")
			}
			selected := jsonvalue.Object()
			if provenance := attestations.Get("provenance"); provenance != nil {
				selected.Put("provenance", provenance)
			}
			out.Dist = &Dist{Attestations: selected}
		}
	}
	return out, nil
}

// ExactMetadataAt requests a fresh full document, retaining compact evidence
// only. Like the reference this bypasses the full-document disk cache; callers
// may fall back to their normal cached metadata flow on an exact miss.
func (c *Client) ExactMetadataAt(ctx context.Context, name, version, registry string, mode NetworkMode) (*ExactPackument, error) {
	if !ValidName(name) {
		return nil, fmt.Errorf("invalid package name: %q", name)
	}
	if mode == Offline {
		return nil, fmt.Errorf("offline: trust history for %s", name)
	}
	header := http.Header{"Accept": []string{"application/json; q=1.0, */*"}, "Priority": []string{"u=0"}}
	r, err := c.Do(ctx, Request{URL: strings.TrimRight(registry, "/") + "/" + strings.ReplaceAll(name, "/", "%2F"), Registry: registry, Package: name, Header: header, MaxBytes: c.options.Policy.PackumentMaxBytes, Retry: true, Validate: func(data []byte) error { _, err := ParseExactPackument(data, version); return err }})
	if err != nil {
		return nil, err
	}
	if r.Status < 200 || r.Status >= 300 {
		return nil, &HTTPError{r.Status, name}
	}
	return ParseExactPackument(r.Body, version)
}
