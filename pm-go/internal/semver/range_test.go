package semver

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestRanges(t *testing.T) {
	for _, tc := range []struct {
		version, rangeText string
		want               bool
	}{
		{"1.2.3", "", true}, {"1.2.3-beta.1", "*", false}, {"1.2.3-beta.2", ">=1.2.3-beta.1 <2", true}, {"1.2.4-beta.1", ">=1.2.3-beta.1 <2", false},
		{"0.2.9", "^0.2.3", true}, {"0.3.0", "^0.2.3", false}, {"0.0.2", "^0.0.1", false}, {"1.9.0", "1.x", true}, {"2.0.0", "1.x", false},
		{"2.3.9", "1.2 - 2.3", true}, {"2.4.0", "1.2 - 2.3", false}, {"1.2.3+meta", "1.2.3+different", true}, {"1.2.3", "^2 || 1.x", true},
		{"1.2", "*", false}, {"01.2.3", "*", false}, {"1.2.3", "!=2.0.0", false}, {"1.2.3", "latest", false}, {"1.2.3", "1,2", false},
	} {
		if got := Satisfies(tc.version, tc.rangeText); got != tc.want {
			t.Errorf("Satisfies(%q,%q)=%v", tc.version, tc.rangeText, got)
		}
	}
}

// The repository already carries the actual semver 7.7.4 package as a registry
// fixture. Execute it only as a test oracle; the production module never calls
// Node or Rust for version parsing or matching.
func TestNodeSemverOracle(t *testing.T) {
	if os.Getenv("PM_SEMVER_ORACLE") != "1" {
		t.Skip("set PM_SEMVER_ORACLE=1 to compare with node-semver")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	f, err := os.Open("../../../vendor/aube/test/registry/storage/semver/semver-7.7.4.tgz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		if !filepath.IsLocal(h.Name) {
			t.Fatal("invalid fixture path", h.Name)
		}
		path := filepath.Join(dir, filepath.FromSlash(h.Name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	versions := []string{"0.0.0-0", "0.0.0", "0.0.1", "0.0.2", "0.1.0", "0.2.0", "0.2.3", "0.3.0", "1.0.0-alpha", "1.0.0", "1.2.0", "1.2.3-0", "1.2.3-alpha.1", "1.2.3-beta.1", "1.2.3-beta.2", "1.2.3", "1.2.3+build.1", "1.2.4-alpha", "1.2.4", "1.3.0-beta", "1.3.0", "1.9.9", "2.0.0-alpha", "2.0.0", "2.3.4", "2.4.0", "3.0.0", "v1.2.3", "01.2.3", "1.2", "1.2.3.4", "1.2.3-01", "9007199254740992.0.0"}
	ranges := []string{"", " ", "*", "x", "X", "1", "1.2", "1.2.3", "=1.2.3", "v1.2.3", "1.x", "1.2.x", "1.x.3", "*.*", "^0", "^0.0", "^0.0.1", "^0.2.3", "^1", "^1.2", "^1.2.3", "^1.2.3-beta.1", "~1", "~1.2", "~1.2.3", "~>1.2.3", "~1.2.3-beta.1", "1.2 - 2.3", "1 - 2", "1.2.3 - 2.3.4", "* - 2", "1 - *", ">1", ">1.2", ">=1.2", "<1.2", "<=1.2", "<*", ">*", ">=*", "<=*", ">= 1.2.3 < 2.0.0", ">=1.2.3-beta.1 <2.0.0", ">=1.2.3-beta.1 <2.0.0-alpha", "1.x || ^2.3", "1.2.3 ||", "||1.2.3", "1.2.3+build", "latest", "workspace:*", "!=1.2.3", "1,2", "01.2.3", ">=1.2.3-beta.01", "^", "~", ">=>1.2.3"}
	type pair struct{ Version, Range string }
	var pairs []pair
	for _, r := range ranges {
		for _, v := range versions {
			pairs = append(pairs, pair{v, r})
		}
	}
	input, err := json.Marshal(pairs)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "-e", `const s=require(process.argv[1]);let b='';process.stdin.on('data',x=>b+=x);process.stdin.on('end',()=>console.log(JSON.stringify(JSON.parse(b).map(x=>s.satisfies(x.Version,x.Range)))));`, filepath.Join(dir, "package"))
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var want []bool
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(pairs) {
		t.Fatal("oracle result count", len(want))
	}
	for i, p := range pairs {
		if got := Satisfies(p.Version, p.Range); got != want[i] {
			t.Errorf("Satisfies(%q,%q): Go=%v node-semver=%v", p.Version, p.Range, got, want[i])
		}
	}
}
