// Package installdelta fingerprints installed content and schedules graph work.
package installdelta

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/resolver"
	"lukechampine.com/blake3"
)

type LtHash [128]uint16

func (h *LtHash) Add(fingerprint string)    { h.mix(fingerprint, false) }
func (h *LtHash) Remove(fingerprint string) { h.mix(fingerprint, true) }
func (h *LtHash) Combine(other LtHash) {
	for i, v := range other {
		h[i] += v
	}
}
func (h LtHash) Digest() string {
	var data [256]byte
	for i, v := range h {
		binary.LittleEndian.PutUint16(data[i*2:], v)
	}
	digest := blake3.Sum256(data[:])
	return hex.EncodeToString(digest[:])
}
func (h *LtHash) mix(fingerprint string, remove bool) {
	xof := blake3.New(256, nil)
	xof.Write([]byte(fingerprint))
	bytes := xof.Sum(nil)
	for i := range h {
		lane := binary.LittleEndian.Uint16(bytes[i*2:])
		if remove {
			h[i] -= lane
		} else {
			h[i] += lane
		}
	}
}
func GraphHash(fingerprints map[string]string) LtHash {
	var h LtHash
	for _, v := range fingerprints {
		h.Add(v)
	}
	return h
}

func PackageHashes(ctx context.Context, g *lockfile.Graph, patches map[string]string, project string) (map[string]string, error) {
	if g == nil || !filepath.IsAbs(project) {
		return nil, fmt.Errorf("content fingerprints require a graph and absolute project directory")
	}
	out := map[string]string{}
	for _, key := range keys(g.Packages) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pkg := g.Packages[key]
		var patch *string
		if _, value, ok := lockfile.LookupPatch(pkg, patches); ok {
			patch = &value
		}
		out[key] = fingerprint(pkg, patch, project)
	}
	return out, nil
}

func Compute(ctx context.Context, g *lockfile.Graph, patches map[string]string, project string) (leaf, subtree map[string]string, err error) {
	leaf, err = PackageHashes(ctx, g, patches, project)
	if err != nil {
		return nil, nil, err
	}
	subtree = SubtreeHashes(g, leaf)
	return leaf, subtree, ctx.Err()
}

func fingerprint(p *lockfile.Package, patch *string, project string) string {
	h := blake3.New(32, nil)
	field(h, "name", []byte(p.Name))
	field(h, "version", []byte(p.Version))
	field(h, "dep_path", []byte(p.DepPath))
	optional(h, "integrity", p.Integrity)
	optional(h, "alias_of", p.AliasOf)
	optional(h, "patch", patch)
	if p.TarballURL != nil && tarballAffectsContent(p, *p.TarballURL) {
		field(h, "tarball_url", []byte(*p.TarballURL))
	}
	h.Write([]byte("deps"))
	length(h, len(p.Dependencies))
	for _, key := range keys(p.Dependencies) {
		field(h, "k", []byte(key))
		field(h, "v", []byte(p.Dependencies[key]))
	}
	list(h, "os", p.OS)
	list(h, "cpu", p.CPU)
	list(h, "libc", p.Libc)
	if s := p.Source; s != nil {
		h.Write([]byte("local_source"))
		switch s.Kind {
		case lockfile.Directory, lockfile.Tarball, lockfile.Link, lockfile.Portal, lockfile.Exec:
			tag := map[lockfile.SourceKind]string{lockfile.Directory: "dir", lockfile.Tarball: "tar", lockfile.Link: "link", lockfile.Portal: "portal", lockfile.Exec: "exec"}[s.Kind]
			h.Write([]byte(tag))
			field(h, "path", []byte(strings.ToValidUTF8(s.Path, "\ufffd")))
			if s.Kind == lockfile.Exec {
				if path, err := resolver.ResolveExecScriptPath(*s, project); err == nil {
					if data, err := os.ReadFile(path); err == nil {
						field(h, "content", data)
					}
				}
			}
		case lockfile.Git:
			h.Write([]byte("git"))
			field(h, "url", []byte(s.URL))
			optional(h, "committish", s.Committish)
			field(h, "resolved", []byte(s.Resolved))
		case lockfile.RemoteTarball:
			h.Write([]byte("remote_tarball"))
			field(h, "url", []byte(s.URL))
			integrity := ""
			if s.Integrity != nil {
				integrity = *s.Integrity
			}
			field(h, "integrity", []byte(integrity))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func tarballAffectsContent(p *lockfile.Package, url string) bool {
	if p.Integrity == nil || p.RegistryGitHosted {
		return true
	}
	name := p.RegistryName()
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	path, _, _ := strings.Cut(url, "?")
	path, _, _ = strings.Cut(path, "#")
	return !strings.HasSuffix(path, "/-/"+name+"-"+p.Version+".tgz")
}
func field(h hash.Hash, tag string, data []byte) {
	h.Write([]byte(tag))
	length(h, len(data))
	h.Write(data)
}
func optional(h hash.Hash, tag string, value *string) {
	if value != nil {
		field(h, tag, []byte(*value))
	} else {
		h.Write([]byte(tag))
		h.Write([]byte{255, 255, 255, 255, 255, 255, 255, 255})
	}
}
func length(h hash.Hash, n int) {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], uint64(n))
	h.Write(b[:])
}
func list(h hash.Hash, tag string, values []string) {
	h.Write([]byte(tag))
	length(h, len(values))
	for _, v := range values {
		field(h, "i", []byte(v))
	}
}
func keys[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }
