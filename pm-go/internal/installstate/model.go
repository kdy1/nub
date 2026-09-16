// Package installstate records the completed install independently of current
// configuration. Callers own the project lease and pass resolved paths.
package installstate

type FileMeta struct {
	Size       uint64 `json:"size" required:"true"`
	MtimeSecs  int64  `json:"mtime_secs" required:"true"`
	MtimeNanos uint32 `json:"mtime_nanos"`
}

type LocalDirectoryFingerprint struct {
	ContentHash  string `json:"content_hash" required:"true"`
	MetadataHash string `json:"metadata_hash" required:"true"`
}

type State struct {
	LockfileHash            string                                `json:"lockfile_hash" required:"true"`
	LockfileSnapshotName    *string                               `json:"lockfile_snapshot_name,omitempty"`
	LockfileMeta            *FileMeta                             `json:"lockfile_meta,omitempty"`
	MemberLockfileHashes    map[string]string                     `json:"member_lockfile_hashes,omitempty"`
	MemberLockfileMeta      map[string]FileMeta                   `json:"member_lockfile_meta,omitempty"`
	PackageJSONHashes       map[string]string                     `json:"package_json_hashes" required:"true"`
	PackageJSONMeta         map[string]FileMeta                   `json:"package_json_meta,omitempty"`
	LocalDirectoryHashes    *map[string]LocalDirectoryFingerprint `json:"local_directory_hashes,omitempty"`
	EngineVersion           string                                `json:"aube_version" required:"true"`
	SectionFiltered         bool                                  `json:"prod"`
	SettingsHash            string                                `json:"settings_hash"`
	DepBuildPolicyHash      string                                `json:"dep_build_policy_hash,omitempty"`
	ReleasePolicyHash       string                                `json:"release_policy_hash,omitempty"`
	PackageContentHashes    map[string]string                     `json:"package_content_hashes,omitempty"`
	GraphLtHash             string                                `json:"graph_lthash,omitempty"`
	PackageSubtreeHashes    map[string]string                     `json:"package_subtree_hashes,omitempty"`
	PackageJSONShapeDigests map[string]string                     `json:"package_json_shape_digests,omitempty"`
	Layout                  *Layout                               `json:"layout"`
	UnreviewedBuilds        []string                              `json:"unreviewed_builds,omitempty"`
	DeferredDepBuilds       *[]string                             `json:"deferred_dep_builds"`
}

// Freshness omits graph-sized delta/license data. A missing deferred-build list
// means legacy unknown; a present empty list records successful completion.
type Freshness struct {
	LockfileHash            string                                `json:"lockfile_hash" required:"true"`
	LockfileSnapshotName    *string                               `json:"lockfile_snapshot_name,omitempty"`
	LockfileMeta            *FileMeta                             `json:"lockfile_meta,omitempty"`
	MemberLockfileHashes    map[string]string                     `json:"member_lockfile_hashes,omitempty"`
	MemberLockfileMeta      map[string]FileMeta                   `json:"member_lockfile_meta,omitempty"`
	PackageJSONHashes       map[string]string                     `json:"package_json_hashes" required:"true"`
	PackageJSONMeta         map[string]FileMeta                   `json:"package_json_meta,omitempty"`
	LocalDirectoryHashes    *map[string]LocalDirectoryFingerprint `json:"local_directory_hashes,omitempty"`
	SectionFiltered         bool                                  `json:"prod"`
	SettingsHash            string                                `json:"settings_hash"`
	DepBuildPolicyHash      string                                `json:"dep_build_policy_hash,omitempty"`
	PackageJSONShapeDigests map[string]string                     `json:"package_json_shape_digests,omitempty"`
	Layout                  *Layout                               `json:"layout"`
	UnreviewedBuilds        []string                              `json:"unreviewed_builds,omitempty"`
	DeferredDepBuilds       *[]string                             `json:"deferred_dep_builds"`
}

func (s *State) Freshness() Freshness {
	return Freshness{
		LockfileHash: s.LockfileHash, LockfileSnapshotName: s.LockfileSnapshotName, LockfileMeta: s.LockfileMeta,
		MemberLockfileHashes: s.MemberLockfileHashes, MemberLockfileMeta: s.MemberLockfileMeta,
		PackageJSONHashes: s.PackageJSONHashes, PackageJSONMeta: s.PackageJSONMeta, LocalDirectoryHashes: s.LocalDirectoryHashes,
		SectionFiltered: s.SectionFiltered, SettingsHash: s.SettingsHash, DepBuildPolicyHash: s.DepBuildPolicyHash,
		PackageJSONShapeDigests: s.PackageJSONShapeDigests, Layout: s.Layout, UnreviewedBuilds: s.UnreviewedBuilds, DeferredDepBuilds: s.DeferredDepBuilds,
	}
}

type Layout struct {
	Linker                   string                      `json:"linker" required:"true" enum:"isolated|hoisted"`
	ModulesDirName           string                      `json:"modules_dir_name,omitempty"`
	HoistingLimits           *string                     `json:"hoisting_limits,omitempty" enum:"none|workspaces|dependencies"`
	VirtualStoreDirMaxLength *uint64                     `json:"virtual_store_dir_max_length,omitempty"`
	DirectEntries            map[string][]string         `json:"direct_entries" required:"true"`
	Packages                 map[string]InstalledPackage `json:"packages" required:"true"`
	GVSNestedLinks           *map[string]string          `json:"gvs_nested_links,omitempty"`
}

type InstalledPackage struct {
	Name            string `json:"name" required:"true"`
	Version         string `json:"version" required:"true"`
	PackageJSONPath string `json:"package_json_path" required:"true"`
	PackageJSONHash string `json:"package_json_hash"`
	Link            bool   `json:"link,omitempty"`
}

type Licenses struct {
	Fingerprint       string            `json:"fingerprint" required:"true"`
	Licenses          map[string]string `json:"licenses" required:"true"`
	LinkedPackageDirs map[string]string `json:"linked_package_dirs,omitempty"`
}
