package installstate

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nubjs/nub/pm-go/internal/linker"
)

func put(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}
func paths(t *testing.T) Paths {
	t.Helper()
	p, err := NewPaths(t.TempDir(), "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestStateCodecAndFreshnessMigration(t *testing.T) {
	p := paths(t)
	old := `{"lockfile_hash":"lock","package_json_hashes":{".":"manifest"},"aube_version":"0.24.0"}`
	put(t, filepath.Join(p.State, "state.json"), old)
	f := p.ReadFreshness()
	if f == nil || f.DeferredDepBuilds != nil || f.Layout != nil {
		t.Fatalf("legacy state %#v", f)
	}
	got, err := os.ReadFile(filepath.Join(p.State, "fresh.json"))
	want := `{"lockfile_hash":"lock","package_json_hashes":{".":"manifest"},"prod":false,"settings_hash":"","layout":null,"deferred_dep_builds":null}`
	if err != nil || string(got) != want {
		t.Fatal(string(got), err)
	}
	s := p.Read()
	empty := []string{}
	local := map[string]LocalDirectoryFingerprint{}
	s.DeferredDepBuilds = &empty
	s.LocalDirectoryHashes = &local
	s.PackageContentHashes = map[string]string{"pkg@1": "content"}
	s.UnreviewedBuilds = []string{"pkg@1"}
	if err := p.Write(s); err != nil {
		t.Fatal(err)
	}
	f = p.ReadFreshness()
	if f == nil || f.DeferredDepBuilds == nil || len(*f.DeferredDepBuilds) != 0 || f.LocalDirectoryHashes == nil {
		t.Fatalf("clean completion lost %#v", f)
	}
	got, err = os.ReadFile(filepath.Join(p.State, "fresh.json"))
	if err != nil || strings.Contains(string(got), "package_content_hashes") || !strings.Contains(string(got), `"deferred_dep_builds":[]`) {
		t.Fatal(string(got), err)
	}
	// Malformed fresh state falls back to a valid full state and rewrites it.
	put(t, filepath.Join(p.State, "fresh.json"), `{"lockfile_hash":null}`)
	if f = p.ReadFreshness(); f == nil || !reflect.DeepEqual(f.UnreviewedBuilds, []string{"pkg@1"}) {
		t.Fatal(f)
	}
	put(t, filepath.Join(p.State, "state.json"), `{}`)
	if p.Read() != nil {
		t.Fatal("missing required fields accepted")
	}
	// The valid fresh projection survives an unrelated damaged large sidecar.
	if p.ReadFreshness() == nil {
		t.Fatal("fresh state unnecessarily requires full state")
	}
}

func TestTypedStateRejectsCorruption(t *testing.T) {
	base := `"lockfile_hash":"","package_json_hashes":{},"aube_version":"x"`
	for _, raw := range []string{
		`null`, `[]`, `{}`, `{"LOCKFILE_HASH":"","package_json_hashes":{},"aube_version":"x"}`,
		`{` + base + `,"lockfile_hash":"duplicate"}`, `{` + base + `,"prod":null}`, `{` + base + `,"member_lockfile_hashes":null}`,
		`{` + base + `,"layout":{"linker":"pnp","direct_entries":{},"packages":{}}}`,
		`{` + base + `,"layout":{"linker":"isolated","direct_entries":{".":null},"packages":{}}}`,
		`{` + base + `,"lockfile_meta":{"size":1,"mtime_secs":2,"mtime_nanos":4294967296}}`,
		`{` + base + `,"lockfile_meta":{"size":1.0,"mtime_secs":2}}`,
		`{` + base + `,"deferred_dep_builds":[null]}`, `{` + base + `,"settings_hash":"\ud800"}`,
		"{" + base + ",\"settings_hash\":\"\xff\"}",
	} {
		var s State
		if err := decode([]byte(raw), &s); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	var s State
	if err := decode([]byte(`{`+base+`,"unknown":null,"unknown":{},"member_lockfile_hashes":{"a":"old","a":"new"}}`), &s); err != nil || s.MemberLockfileHashes["a"] != "new" {
		t.Fatal(s, err)
	}
	encoded, err := encode(&State{LockfileHash: "<>&\u2028", PackageJSONHashes: map[string]string{"z": "last", "a": "first"}})
	if err != nil || !strings.Contains(string(encoded), `"lockfile_hash":"<>&`+"\u2028"+`"`) || strings.Index(string(encoded), `"a":"first"`) > strings.Index(string(encoded), `"z":"last"`) {
		t.Fatal(string(encoded), err)
	}
}

func TestLegacyFilesPlacementsAndInterruptedLink(t *testing.T) {
	p := paths(t)
	put(t, p.State, "legacy")
	if p.Read() != nil {
		t.Fatal("legacy accepted")
	}
	if _, err := os.Lstat(p.State); !os.IsNotExist(err) {
		t.Fatal("legacy file remains", err)
	}
	if !p.LinkCompletedCleanly() {
		t.Fatal("unexpected link sentinel")
	}
	if err := p.MarkLinkInProgress(); err != nil {
		t.Fatal(err)
	}
	if p.LinkCompletedCleanly() {
		t.Fatal("interrupted link marked complete")
	}
	if err := p.Write(&State{}); err != nil {
		t.Fatal(err)
	}
	if p.LinkCompletedCleanly() {
		t.Fatal("writing state cleared link sentinel prematurely")
	}
	if err := p.ClearLinkInProgress(); err != nil || !p.LinkCompletedCleanly() {
		t.Fatal(err)
	}
	if err := p.ClearLinkInProgress(); err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(p.Project, "node_modules", "a")
	b := filepath.Join(p.Project, "packages", "b", "node_modules", "a")
	put(t, filepath.Join(a, "package.json"), `{}`)
	put(t, filepath.Join(b, "package.json"), `{}`)
	placements := linker.HoistedPlacements{"a@1": {a, b}, "absent@1": {filepath.Join(p.Project, "missing")}, "empty": {}}
	if err := p.WritePlacements(placements); err != nil {
		t.Fatal(err)
	}
	got := p.ReadPlacements()
	if !reflect.DeepEqual(got["a@1"], []string{a, b}) || len(got["absent@1"]) != 0 {
		t.Fatal(got)
	}
	if _, ok := got["empty"]; ok {
		t.Fatal("reference iteration omits empty placements")
	}
	if err := os.RemoveAll(a); err != nil {
		t.Fatal(err)
	}
	if got := p.ReadPlacements(); !reflect.DeepEqual(got["a@1"], []string{b}) {
		t.Fatal(got)
	}
	if err := p.WritePlacements(nil); err != nil || p.ReadPlacements() != nil {
		t.Fatal(err)
	}
	if err := p.Remove(); err != nil {
		t.Fatal(err)
	}
	if err := p.Remove(); err != nil {
		t.Fatal(err)
	}
}

func TestStatePathsLockfilePrecedenceAndMetadata(t *testing.T) {
	root := t.TempDir()
	p, err := NewPaths(root, "custom", "node_modules", "feat/name")
	if err != nil {
		t.Fatal(err)
	}
	if p.State != filepath.Join(root, "custom", ".nub-state") {
		t.Fatal(p)
	}
	nub, err := NewPaths(root, "custom", "", "")
	if err != nil || nub.State != filepath.Join(root, "node_modules", ".store", ".nub-state") {
		t.Fatal(nub, err)
	}
	for _, name := range []string{"package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "bun.lock", "pnpm-lock.yaml", "pnpm-lock.feat!name.yaml", "lock.yaml", "nub.lock", "nub.feat!name.lock"} {
		put(t, filepath.Join(root, name), name)
	}
	name, path := p.ActiveLockfile()
	// Ask the identity helper for the exact branch spelling; candidate order
	// is independent of manifest declarations, as in the state reader.
	if name != "nub.feat!name.lock" || path != filepath.Join(root, name) {
		t.Fatal(name, path)
	}
	stamp := time.Unix(1600000000, 456789123)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	m := CaptureMeta(path)
	info, err := os.Stat(path)
	if err != nil || m == nil || m.Size != uint64(info.Size()) || m.MtimeSecs != info.ModTime().Unix() || m.MtimeNanos != uint32(info.ModTime().Nanosecond()) {
		t.Fatal(m, err)
	}
	if HashFile(filepath.Join(root, "absent")) != HashBytes(nil) {
		t.Fatal("missing file hash")
	}
	if err := p.WriteLicenses(&Licenses{Fingerprint: "graph", Licenses: map[string]string{"a@1": "MIT"}}); err != nil {
		t.Fatal(err)
	}
	if p.ReadLicenses().Licenses["a@1"] != "MIT" {
		t.Fatal("license sidecar")
	}
}

func TestSidecarDeletionNeverRemovesDirectories(t *testing.T) {
	p := paths(t)
	for _, name := range []string{"hoisted-placements.json", "link-in-progress"} {
		path := filepath.Join(p.State, name)
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.WritePlacements(nil); err == nil {
		t.Fatal("placement directory deleted")
	}
	if err := p.ClearLinkInProgress(); err == nil {
		t.Fatal("sentinel directory deleted")
	}
	if p.LinkCompletedCleanly() {
		t.Fatal("unclearable sentinel marked complete")
	}
}
