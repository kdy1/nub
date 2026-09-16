package manifest

import (
	"encoding/hex"
	"hash"
	"slices"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
	"lukechampine.com/blake3"
)

var installShapeFields = []string{
	"allowScripts", "bundleDependencies", "bundledDependencies", "catalog", "catalogs",
	"dependencies", "dependenciesMeta", "devDependencies", "engines", "name",
	"optionalDependencies", "overrides", "packageExtensions", "peerDependencies",
	"peerDependenciesMeta", "pnpm", "publishConfig", "resolutions", "trustedDependencies",
	"version", "workspaces",
}

// InstallShapeDigest fingerprints the reference's install-affecting fields.
// Nub has no branded manifest namespace. This observes authored fields for
// invalidation only; it does not grant them configuration precedence.
func InstallShapeDigest(value *jsonvalue.Value) string {
	if value == nil || value.Kind != '{' {
		h := blake3.Sum256([]byte("aube-manifest-v1/not-an-object"))
		return hex.EncodeToString(h[:])
	}
	h := blake3.New(32, nil)
	h.Write([]byte("aube-manifest-v1\n"))
	for _, field := range installShapeFields {
		if v := value.Get(field); v != nil {
			h.Write([]byte(field + "="))
			shapeJSON(h, v)
			h.Write([]byte{'\n'})
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// This is the reference hash encoding, not JSON serialization: strings and
// object keys enter the digest unescaped, while object order is canonical.
func shapeJSON(h hash.Hash, v *jsonvalue.Value) {
	switch v.Kind {
	case 's':
		h.Write([]byte{'"'})
		h.Write([]byte(v.Text()))
		h.Write([]byte{'"'})
	case '{':
		h.Write([]byte{'{'})
		fields := slices.Clone(v.Object)
		slices.SortFunc(fields, func(a, b jsonvalue.Field) int { return strings.Compare(a.Key, b.Key) })
		for i, f := range fields {
			if i != 0 {
				h.Write([]byte{','})
			}
			h.Write([]byte{'"'})
			h.Write([]byte(f.Key))
			h.Write([]byte{'"', ':'})
			shapeJSON(h, f.Value)
		}
		h.Write([]byte{'}'})
	case '[':
		h.Write([]byte{'['})
		for i, item := range v.Array {
			if i != 0 {
				h.Write([]byte{','})
			}
			shapeJSON(h, item)
		}
		h.Write([]byte{']'})
	default:
		data, _ := v.MarshalJSON()
		h.Write(data)
	}
}
