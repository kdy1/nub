package installstate

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"os"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/manifest"
	"github.com/nubjs/nub/pm-go/internal/store"
	"lukechampine.com/blake3"
)

type RecordInput struct {
	// State supplies version, section selection, settings/policy/graph hashes,
	// manifest hashes and unreviewed builds. Filesystem-derived fields below
	// are always recaptured from the completed install.
	State                   State
	Layout                  LayoutInput
	Workspace               FreshnessInput
	SharedWorkspaceLockfile bool
	DeferredDepBuilds       []string
}

// Record publishes state only after successful linking and lifecycle work.
// The caller clears the link sentinel after all remaining finalization succeeds.
func (p Paths) Record(ctx context.Context, input RecordInput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s := input.State
	name, path := p.ActiveLockfile()
	s.LockfileMeta = CaptureMeta(path)
	s.LockfileHash = ""
	s.LockfileSnapshotName = nil
	if data, err := os.ReadFile(path); err == nil {
		s.LockfileHash = HashBytes(data)
		s.LockfileSnapshotName = &name
	}
	var err error
	s.Layout, err = CaptureLayout(ctx, p.Project, input.Layout)
	if err != nil {
		return err
	}
	s.PackageJSONShapeDigests = map[string]string{}
	s.PackageJSONMeta = map[string]FileMeta{}
	for _, rel := range keys(s.PackageJSONHashes) {
		path := manifestPath(p.Project, rel)
		if data, err := os.ReadFile(path); err == nil {
			if v, err := jsonvalue.Parse(data); err == nil {
				s.PackageJSONShapeDigests[rel] = manifest.InstallShapeDigest(v)
			}
		}
		if meta := CaptureMeta(path); meta != nil {
			s.PackageJSONMeta[rel] = *meta
		}
	}
	s.MemberLockfileHashes = map[string]string{}
	s.MemberLockfileMeta = map[string]FileMeta{}
	if !input.SharedWorkspaceLockfile {
		for _, member := range input.Workspace.Members {
			key := relative(member, p.Project)
			_, path := ActiveLockfile(member, input.Workspace.branch(member, p.Branch))
			if path == "" {
				s.MemberLockfileHashes[key] = ""
			} else {
				s.MemberLockfileHashes[key] = HashFile(path)
				if meta := CaptureMeta(path); meta != nil {
					s.MemberLockfileMeta[key] = *meta
				}
			}
		}
	}
	locals := map[string]LocalDirectoryFingerprint{}
	for _, key := range keys(input.Layout.Graph.Packages) {
		pkg := input.Layout.Graph.Packages[key]
		if pkg.Source == nil || pkg.Source.Kind != lockfile.Directory && pkg.Source.Kind != lockfile.Portal {
			continue
		}
		rel := pkg.Source.PathPOSIX()
		if _, ok := locals[rel]; ok {
			continue
		}
		content, metadata, err := store.DirectoryFingerprints(ctx, join(p.Project, pkg.Source.Path))
		if err != nil {
			return err
		}
		locals[rel] = LocalDirectoryFingerprint{content, metadata}
	}
	s.LocalDirectoryHashes = &locals
	builds := append([]string{}, input.DeferredDepBuilds...)
	s.DeferredDepBuilds = &builds
	fingerprint := LicenseFingerprint(s.GraphLtHash, s.PackageContentHashes)
	var old Licenses
	if !readJSON(join(p.State, "licenses.json"), &old) || old.Fingerprint != fingerprint {
		licenses, err := p.collectLicenses(ctx, input.Layout, fingerprint)
		if err != nil {
			return err
		}
		if err := p.removeLegacy(); err != nil {
			return err
		}
		if err := p.WriteLicenses(licenses); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.Write(&s)
}

func CollectManifestHashes(project string, importers []string) map[string]string {
	out := map[string]string{}
	for _, rel := range importers {
		member := join(project, rel)
		key := manifestKey(project, member)
		path := manifestPath(project, key)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			out[key] = HashFile(path)
		}
	}
	return out
}

func LicenseFingerprint(graph string, hashes map[string]string) string {
	h := blake3.New(32, nil)
	h.Write([]byte(graph))
	for _, key := range keys(hashes) {
		var size [8]byte
		binary.LittleEndian.PutUint64(size[:], uint64(len(key)))
		h.Write(size[:])
		h.Write([]byte(key))
		h.Write([]byte(hashes[key]))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (p Paths) collectLicenses(ctx context.Context, input LayoutInput, fingerprint string) (*Licenses, error) {
	if input.MaxFilenameLength == 0 {
		input.MaxFilenameLength = lockfile.DefaultVirtualStoreMaxLength
	}
	out := &Licenses{Fingerprint: fingerprint, Licenses: map[string]string{}, LinkedPackageDirs: map[string]string{}}
	for _, key := range keys(input.Graph.Packages) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pkg := input.Graph.Packages[key]
		dir, err := input.packageDir(p.Project, key, pkg)
		if err != nil {
			return nil, err
		}
		if pkg.Source != nil && pkg.Source.Kind == lockfile.Link {
			out.LinkedPackageDirs[key] = relative(dir, p.Project)
		}
		if license, ok := readLicense(dir); ok {
			out.Licenses[key] = license
		} else if pkg.License != nil {
			out.Licenses[key] = *pkg.License
		}
	}
	return out, nil
}

func readLicense(dir string) (string, bool) {
	data, err := os.ReadFile(join(dir, "package.json"))
	if err != nil {
		return "", false
	}
	v, err := jsonvalue.ParseObjectFields(data)
	if err != nil || v.Kind != '{' {
		return "", false
	}
	seen := map[string]bool{}
	for _, f := range v.Object {
		if f.Key == "license" || f.Key == "licenses" {
			if seen[f.Key] {
				return "", false
			}
			seen[f.Key] = true
		}
	}
	// The outer struct rejects duplicates; generic JSON inside its two fields
	// uses last-value-wins object semantics.
	v, err = jsonvalue.Parse(data)
	if err != nil {
		return "", false
	}
	if s, ok := extractLicense(v.Get("license")); ok {
		return s, true
	}
	var licenses []string
	if values := v.Get("licenses"); values != nil && values.Kind == '[' {
		for _, v := range values.Array {
			if s, ok := extractLicense(v); ok {
				licenses = append(licenses, s)
			}
		}
	}
	return strings.Join(licenses, " OR "), len(licenses) > 0
}
func extractLicense(v *jsonvalue.Value) (string, bool) {
	if v != nil && v.Kind == '{' {
		v = v.Get("type")
	}
	if v != nil && v.Kind == 's' {
		return v.Text(), true
	}
	return "", false
}
