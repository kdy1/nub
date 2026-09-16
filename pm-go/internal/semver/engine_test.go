package semver

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestEngineGrammarDifferencesAreExplicit(t *testing.T) {
	for _, tc := range []struct {
		version, requested string
		npm, engine        bool
	}{
		{"1.2.9", "<=1.2", true, false},
		{"1.2.3", "1.2.3 garbage", false, true},
		{"0.5.0", ">2 <1", false, true},
		{"01.02.03", "^1", false, true},
		{"V1.2.3", "^1", false, true},
		{"1.2.3beta", "^1", false, false},
		{"1.2.3-01", "1.2.3-1", false, true},
		{"2.0.0", "1 - *", true, false},
	} {
		if got := Satisfies(tc.version, tc.requested); got != tc.npm {
			t.Fatal(tc, "npm", got)
		}
		if got := EngineSatisfies(tc.version, tc.requested); got != tc.engine {
			t.Fatal(tc, "engine", got)
		}
	}
	if v, err := ParseEngineVersion("1.2.3beta"); err != nil || v.Prerelease() != "beta" {
		t.Fatal(v, err)
	}
	for _, tc := range []struct {
		left, right string
		want        bool
	}{
		{"^7.0.0", ">=7.5.0", true}, {"^7.0.0", ">=8", false},
		{">1.0.0", "<1.0.0", true}, {">=1.0.0", "<1.0.0", false},
		{"*", "1.0.0-beta", true},
	} {
		a, e1 := ParseEngineRange(tc.left)
		b, e2 := ParseEngineRange(tc.right)
		if e1 != nil || e2 != nil || a.AllowsAny(b) != tc.want {
			t.Fatal(tc, e1, e2)
		}
	}
}

func TestRustEngineSemverOracle(t *testing.T) {
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		t.Skip("Rust crate oracle runs in reference CI")
	}
	versions := []string{"0.0.0-0", "0.0.0", "0.0.1", "0.1.0", "0.2.3", "0.3.0", "1.0.0-alpha", "1.0.0", "1.2.0-0", "1.2.0", "1.2.3-0", "1.2.3-alpha.1", "1.2.3-beta.1", "1.2.3-beta.2", "1.2.3", "1.2.3+build", "1.2.4-alpha", "1.2.4", "1.3.0-beta", "1.9.9", "2.0.0-alpha", "2.0.0", "2.3.4", "2.4.0", "3.0.0", "v1.2.3", "V1.2.3", "01.02.03", "1.2.3beta", "1.2.3-01", "1.2.3+001", " 1.2.3", "1.2.3 ", "v 1.2.3", "1.2", "9007199254740992.0.0"}
	ranges := []string{"", " ", "*", "x", "X", "1", "1.2", "1.2.3", "=1.2.3", "v1.2.3", "v 1.2.3", "1.x", "1.2.x", "1.x.3", "*.*", "^0", "^0.0", "^0.0.1", "^0.2.3", "^1", "^1.2", "^1.2.3", "^1.2.3-beta.1", "~1", "~1.2", "~1.2.3", "~>1.2.3", "~ > 1.2.3", "~1.2.3-beta.1", "1.2 - 2.3", "1 - 2", "1.2.3 - 2.3.4", "* - 2", "1 - *", ">1", ">1.2", ">=1.2", "<1.2", "<=1.2", "<*", ">*", ">=*", "<=*", ">= 1.2.3 < 2.0.0", ">=1.2.3-beta.1 <2.0.0", ">=1.2.3-beta.1 <2.0.0-alpha", "1.x || ^2.3", "1.2.3 ||", "||1.2.3", "1.2.3+build", "latest", "workspace:*", "!=1.2.3", "1,2", "01.2.3", ">=1.2.3-beta.01", "^", "~", ">=>1.2.3", "1.2.3 garbage", ">2 <1", ">1.0.0", "<1.0.0", ">=1.0.0", "1.0.0-beta", "^7.0.0", ">=7.5.0", ">=8", "\n1.2.3", "1.2.3\n", "^*", "1.0.0 2.0.0"}
	input, err := json.Marshal(struct{ Versions, Ranges []string }{versions, ranges})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(path, input, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, oracle, "semver", path).CombinedOutput()
	if err != nil {
		t.Fatal(string(out), err)
	}
	var ref struct {
		Versions, Ranges     []bool
		Contains, Intersects [][]bool
	}
	if err := json.Unmarshal(out, &ref); err != nil {
		t.Fatal(string(out), err)
	}
	if len(ref.Versions) != len(versions) || len(ref.Ranges) != len(ranges) {
		t.Fatal("oracle sizes")
	}
	var parsedVersions []*Version
	var parsedRanges []*EngineRange
	for i, raw := range versions {
		v, err := ParseEngineVersion(raw)
		parsedVersions = append(parsedVersions, v)
		if (err == nil) != ref.Versions[i] {
			t.Errorf("version %q: Go=%v Rust=%v", raw, err, ref.Versions[i])
		}
	}
	for i, raw := range ranges {
		r, err := ParseEngineRange(raw)
		parsedRanges = append(parsedRanges, r)
		if (err == nil) != ref.Ranges[i] {
			t.Errorf("range %q: Go=%v Rust=%v", raw, err, ref.Ranges[i])
		}
	}
	for i, r := range parsedRanges {
		for j, v := range parsedVersions {
			if got := r.Contains(v); got != ref.Contains[i][j] {
				t.Errorf("contains %q %q: Go=%v Rust=%v", ranges[i], versions[j], got, ref.Contains[i][j])
			}
		}
		for j, other := range parsedRanges {
			if got := r.AllowsAny(other); got != ref.Intersects[i][j] {
				t.Errorf("overlap %q %q: Go=%v Rust=%v", ranges[i], ranges[j], got, ref.Intersects[i][j])
			}
		}
	}
	t.Logf("compared %d versions, %d ranges, %d memberships and %d range pairs", len(versions), len(ranges), len(versions)*len(ranges), len(ranges)*len(ranges))
}
