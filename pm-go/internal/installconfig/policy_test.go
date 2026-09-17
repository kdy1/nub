package installconfig

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/registry"
	"github.com/nubjs/nub/pm-go/internal/settings"
)

func TestInstallPolicyBoundaries(t *testing.T) {
	for _, ci := range []string{"", "false", "0", "true"} {
		env := map[string]string{"CI": ci}
		p := (InstallRequest{}).Resolve(nil, env)
		if p.Mode != Frozen || p.StrictNoLockfile || p.SkipRootLifecycle || !p.RunDevPreinstall {
			t.Fatal(ci, p)
		}
		if p := (InstallRequest{LockfileOnly: true}).Resolve(nil, env); p.Mode != Prefer {
			t.Fatal("lockfile-only auto-CI", p)
		}
		if p := (InstallRequest{Force: true}).Resolve(new(true), env); p.Mode != No {
			t.Fatal("force must re-resolve without an override", p)
		}
		p = (InstallRequest{Force: true, Lockfile: LockfileFlags{Frozen: true}}).Resolve(new(false), env)
		if p.Mode != Frozen || !p.StrictNoLockfile {
			t.Fatal("force must preserve explicit frozen", p)
		}
	}
	if p := (InstallRequest{}).Resolve(nil, nil); p.Mode != Prefer {
		t.Fatal(p)
	}
	if p := (InstallRequest{FixLockfile: true, Force: true}).Resolve(nil, nil); p.Mode != Fix {
		t.Fatal(p)
	}
	p := ChainedPolicy(Fix, LockfileFlags{})
	if p.Mode != Fix || !p.SkipRootLifecycle || p.RunDevPreinstall || p.StrictNoLockfile {
		t.Fatal(p)
	}
	p = ChainedPolicy(No, LockfileFlags{Frozen: true})
	if p.Mode != Frozen || p.StrictNoLockfile {
		t.Fatal("chained constructor does not enable strict missing-lockfile errors", p)
	}
}

func TestInstallFlagBagFeedsSettings(t *testing.T) {
	r := InstallRequest{NoVerifyStoreIntegrity: true, EnableGlobalStore: true, DisableGlobalStore: true, PublicHoistPattern: []string{"first", "last"}, NetworkConcurrency: new(uint64(0)), FixLockfile: true}
	c := settings.Context{CLI: r.CLIEntries(), ProjectNpmrc: []settings.Entry{{"verify-store-integrity", "true"}, {"network-concurrency", "15"}}}
	if *c.Bool("verifyStoreIntegrity") || *c.Bool("enableGlobalVirtualStore") || *c.Uint64("networkConcurrency") != 0 {
		t.Fatal("explicit inverse flags must override file values", c.CLI)
	}
	if got := c.Strings("publicHoistPattern"); !reflect.DeepEqual(got, []string{"last"}) {
		t.Fatal(got)
	}
	for _, e := range c.CLI {
		if strings.Contains(e[0], "frozen") {
			t.Fatal("fix mode leaked a different setting", e)
		}
	}
}

type policyCase struct {
	Request InstallRequest
	Prefer  *bool
}

func policyResult(c policyCase, env map[string]string) any {
	p := c.Request.Resolve(c.Prefer, env)
	network := map[registry.NetworkMode]string{registry.Normal: "Online", registry.Offline: "Offline", registry.PreferOffline: "PreferOffline"}[p.Network]
	return map[string]any{"mode": p.Mode, "strict": p.StrictNoLockfile, "network": network, "dev": p.Deps.DevOnly, "prod": p.Deps.ProdOnly, "optional": p.Deps.SkipOptional, "filtered": p.Deps.IsFiltered(), "axis": p.Deps.ProdOrDevAxis(), "label": p.Deps.Label(), "skipRoot": p.SkipRootLifecycle, "devPreinstall": p.RunDevPreinstall, "flags": c.Request.CLIEntries(), "overrideFlag": c.Request.Lockfile.Override().Flag(), "gvsSet": c.Request.EnableGlobalStore || c.Request.DisableGlobalStore}
}

func TestRustInstallPolicyOracle(t *testing.T) {
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		t.Skip("set PM_RUST_LOCKFILE_ORACLE for install policy parity")
	}
	var cases []policyCase
	for flags := range 64 {
		for _, prefer := range []*bool{nil, new(false), new(true)} {
			cases = append(cases, policyCase{Request: InstallRequest{Lockfile: LockfileFlags{Frozen: flags&1 != 0, NoFrozen: flags&2 != 0, PreferFrozen: flags&4 != 0}, Force: flags&8 != 0, FixLockfile: flags&16 != 0, LockfileOnly: flags&32 != 0}, Prefer: prefer})
		}
	}
	for flags := range 32 {
		cases = append(cases, policyCase{Request: InstallRequest{Prod: flags&1 != 0, Dev: flags&2 != 0, NoOptional: flags&4 != 0, Offline: flags&8 != 0, PreferOffline: flags&16 != 0}})
	}
	for flags := range 16 {
		cases = append(cases, policyCase{Request: InstallRequest{ResolutionMode: new("time-based"), NodeLinker: new("hoisted"), LockfileDir: new("../locks"), PackageImportMethod: new("copy"), PublicHoistPattern: []string{"*", "!bad", "@scope/*"}, ShamefullyHoist: true, EnableGlobalStore: flags&1 != 0, DisableGlobalStore: flags&2 != 0, VerifyStoreIntegrity: flags&4 != 0, NoVerifyStoreIntegrity: flags&8 != 0, SideEffectsCache: flags&1 != 0, NoSideEffectsCache: flags&2 != 0, NetworkConcurrency: new(uint64(flags)), AllowAllBuilds: true}})
	}
	data, _ := json.Marshal(cases)
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	for _, ci := range []*string{nil, new(""), new("false"), new("1")} {
		env := map[string]string{}
		cmd := exec.CommandContext(t.Context(), oracle, "install-policy", path)
		cmd.Env = []string{}
		for _, pair := range os.Environ() {
			key, _, _ := strings.Cut(pair, "=")
			if !strings.EqualFold(key, "CI") {
				cmd.Env = append(cmd.Env, pair)
			}
		}
		if ci != nil {
			env["CI"] = *ci
			cmd.Env = append(cmd.Env, "CI="+*ci)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		var got, want []any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		for _, c := range cases {
			want = append(want, policyResult(c, env))
		}
		data, _ := json.Marshal(want)
		if err := json.Unmarshal(data, &want); err != nil {
			t.Fatal(err)
		}
		if len(got) != len(want) {
			t.Fatal(len(got), len(want))
		}
		for i := range got {
			if !reflect.DeepEqual(got[i], want[i]) {
				t.Errorf("env=%v case=%+v: Rust %v; Go %v", env, cases[i], got[i], want[i])
			}
		}
	}
	t.Logf("compared %d install mode, flag and dependency-selection cases", len(cases)*4)
}
