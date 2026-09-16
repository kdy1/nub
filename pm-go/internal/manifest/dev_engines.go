package manifest

import "github.com/nubjs/nub/pm-go/internal/jsonvalue"

type EngineDependency struct {
	Name            string
	Version, OnFail *string
}

// EngineDependencies is a tolerant read-only view of a devEngines slot. Raw
// fields stay in Package.Raw; this does not provision or switch any runtime.
func (p *Package) EngineDependencies(slot string) []EngineDependency {
	value := p.Raw.Get("devEngines").Get(slot)
	values := []*jsonvalue.Value{value}
	if value != nil && value.Kind == '[' {
		values = value.Array
	}
	var out []EngineDependency
	for _, v := range values {
		if v == nil || v.Kind != '{' {
			continue
		}
		name := v.Get("name")
		if name == nil || name.Kind != 's' {
			continue
		}
		version, err := OptionalString(v.Get("version"))
		if err != nil {
			continue
		}
		policy, err := OptionalString(v.Get("onFail"))
		if err != nil {
			continue
		}
		if policy != nil && *policy != "ignore" && *policy != "warn" && *policy != "error" && *policy != "download" {
			continue
		}
		out = append(out, EngineDependency{Name: name.Text(), Version: version, OnFail: policy})
	}
	return out
}
