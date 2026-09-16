package installstate

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/nubjs/nub/pm-go/internal/linker"
)

func TestRustInstallStateOracle(t *testing.T) {
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		t.Skip("set PM_RUST_LOCKFILE_ORACLE for state parity")
	}
	base := `"lockfile_hash":"lock","package_json_hashes":{},"aube_version":"version"`
	corpus := []string{`null`, `[]`, `{}`, `{` + base + `}`, `{` + base + `,"extra":[],"extra":null}`}
	for _, field := range []string{"lockfile_hash", "aube_version", "prod", "settings_hash", "lockfile_meta", "local_directory_hashes", "deferred_dep_builds", "unreviewed_builds", "package_json_hashes", "member_lockfile_meta", "package_content_hashes", "layout"} {
		for _, value := range []string{`null`, `"text"`, `{}`, `[]`, `false`, `1.0`} {
			corpus = append(corpus, `{`+base+`,"`+field+`":`+value+`}`)
		}
	}
	corpus = append(corpus, `{`+base+`,"member_lockfile_hashes":{"x":"old","x":"new"}}`, `{`+base+`,"package_json_meta":{".":{"size":2,"mtime_secs":123,"mtime_nanos":3}},"deferred_dep_builds":[],"local_directory_hashes":{},"unreviewed_builds":["a@1"]}`)
	var cases []map[string]any
	var want []any
	for _, raw := range corpus {
		cases = append(cases, map[string]any{"op": "state", "raw": raw})
		var s State
		if decode([]byte(raw), &s) != nil {
			want = append(want, nil)
			continue
		}
		full, _ := encode(&s)
		fresh := s.Freshness()
		small, _ := encode(&fresh)
		want = append(want, map[string]any{"state": string(full), "fresh": string(small)})
	}
	for i := range 11 {
		p := paths(t)
		meta := "node_modules/pkg/package.json"
		direct := "node_modules/pkg"
		put(t, filepath.Join(p.Project, meta), `{"name":"pkg","version":"1.0.0"}`)
		l := &Layout{Linker: "isolated", DirectEntries: map[string][]string{".": {direct}}, Packages: map[string]InstalledPackage{"pkg@1": {Name: "pkg", Version: "1.0.0", PackageJSONPath: meta, PackageJSONHash: HashFile(join(p.Project, meta))}}}
		switch i {
		case 1:
			put(t, join(p.Project, meta), `{"name":"pkg","version":"1.0.0","extra":true}`)
		case 2:
			put(t, join(p.Project, meta), `{"name":"pkg","version":"2.0.0"}`)
		case 3:
			put(t, join(p.Project, meta), `{"name":null}`)
		case 4:
			if err := os.Remove(join(p.Project, meta)); err != nil {
				t.Fatal(err)
			}
		case 5:
			if err := os.RemoveAll(join(p.Project, direct)); err != nil {
				t.Fatal(err)
			}
		case 6:
			pkg := l.Packages["pkg@1"]
			pkg.Link = true
			l.Packages["pkg@1"] = pkg
			put(t, join(p.Project, meta), `invalid`)
		case 7:
			links := map[string]string{}
			l.GVSNestedLinks = &links
		case 8, 9, 10:
			link := join(p.Project, "node_modules/child")
			if err := linker.CreateDirLink(t.Context(), "pkg", link); err != nil {
				t.Fatal(err)
			}
			target, _ := os.Readlink(link)
			if i == 9 {
				target = "elsewhere"
			}
			if i == 10 {
				os.Remove(link)
			}
			links := map[string]string{"node_modules/child": target}
			l.GVSNestedLinks = &links
		}
		raw, _ := encode(l)
		cases = append(cases, map[string]any{"op": "layout", "project": p.Project, "raw": string(raw)})
		var reason any
		if r := VerifyLayout(p.Project, l); r != "" {
			reason = r
		}
		want = append(want, map[string]any{"reason": reason, "gvs_current": GVSNestedLinksCurrent(p.Project, l)})
	}
	data, _ := json.Marshal(cases)
	path := filepath.Join(t.TempDir(), "cases.json")
	put(t, path, string(data))
	output, err := exec.CommandContext(t.Context(), oracle, "state", path).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	var got []any
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	if len(got) != len(want) {
		t.Fatal(len(got), len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("%v: Rust %v; Go %v", cases[i], got[i], want[i])
		}
	}
	t.Logf("compared %d state schema and installed-layout cases", len(cases))
}

func TestRustFreshnessInputsOracle(t *testing.T) {
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		t.Skip("set PM_RUST_LOCKFILE_ORACLE for freshness input parity")
	}
	var cases []map[string]any
	var want []map[string]any
	for i := range 15 {
		p, _ := recordedFixture(t)
		f := p.ReadFreshness()
		path := join(p.Project, "package.json")
		switch i {
		case 1:
			stamp := time.Unix(1500000000, 0)
			if err := os.Chtimes(path, stamp, stamp); err != nil {
				t.Fatal(err)
			}
		case 2:
			put(t, path, "{\n  \"name\": \"root\"\n}\n")
		case 3:
			put(t, path, `{"name":"root","dependencies":{"x":"1"}}`)
		case 4:
			put(t, path, `{"name":"root","scripts":{"build":"exit 1"}}`)
		case 5:
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		case 6:
			put(t, path, `{`)
		case 7:
			f.PackageJSONShapeDigests = nil
			put(t, path, `{"name":"root","description":"irrelevant"}`)
		case 8:
			meta := f.PackageJSONMeta["."]
			put(t, path, `{"name":"evil"}`)
			stamp := time.Unix(meta.MtimeSecs, int64(meta.MtimeNanos))
			if err := os.Chtimes(path, stamp, stamp); err != nil {
				t.Fatal(err)
			}
		case 9:
			delete(f.PackageJSONHashes, ".")
			put(t, path, `{"name":"untracked"}`)
		case 10:
			put(t, path, `{"name":"root","workspaces":["packages/*"]}`)
		case 11:
			put(t, path, `{"name":"root","license":"ISC"}`)
		case 12:
			put(t, path, `{"name":null}`)
		case 13:
			put(t, path, `[]`)
		case 14:
			put(t, path, "{\"name\":\"\xff\"}")
		}
		for j := range 4 {
			switch j {
			case 0:
				f.DeferredDepBuilds = nil
			case 1:
				b := []string{}
				f.DeferredDepBuilds = &b
			case 2:
				b := []string{"a@1"}
				f.DeferredDepBuilds = &b
			case 3:
				b := []string{"a@1", "b@1", "c@1", "d@1", "e@1"}
				f.DeferredDepBuilds = &b
			}
			raw, _ := encode(f)
			cases = append(cases, map[string]any{"op": "freshness", "raw": string(raw), "project": p.Project})
			var manifest, builds any
			if r := packageJSONsStale(p.Project, f); r != "" {
				manifest = r
			}
			if r := DeferredBuildsStale(f.DeferredDepBuilds); r != "" {
				builds = r
			}
			want = append(want, map[string]any{"manifest": manifest, "builds": builds})
		}
	}
	data, _ := json.Marshal(cases)
	path := filepath.Join(t.TempDir(), "cases.json")
	put(t, path, string(data))
	output, err := exec.CommandContext(t.Context(), oracle, "state", path).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	var got []map[string]any
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	if len(got) != len(want) {
		t.Fatal(len(got), len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("%v: Rust %v; Go %v", cases[i], got[i], want[i])
		}
	}
	t.Logf("compared %d manifest/build freshness cases", len(cases))
}
