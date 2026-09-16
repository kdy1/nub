package registry

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
)

func TestMetadataNormalization(t *testing.T) {
	p, err := Parse([]byte(`{"name":"@example/pkg","modified":42,"dist-tags":{"latest":"1.0.0","broken":{}},"time":null,"versions":{"1.0.0":{"name":"@example/pkg","version":"1.0.0","dependencies":{"valid":"^1","ancient":{"version":"1"}},"devDependencies":null,"peerDependenciesMeta":{"required":42,"optional":{"optional":true},"malformed":{"optional":"true"}},"os":["linux",42,"!win32"],"cpu":"arm64","libc":false,"engines":["node >= 0.8.0"],"bin":"cli.js","license":{"type":"MIT"},"funding":[null,{"url":"https://fund.example"}],"deprecated":false,"hasInstallScript":"true","bundledDependencies":false,"bundleDependencies":["ignored"],"dist":{"tarball":"https://registry/pkg.tgz","integrity":"sha512-value","unpackedSize":{}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	v := p.Versions["1.0.0"]
	if p.Modified != nil || len(p.Tags) != 1 || len(p.Time) != 0 || !reflect.DeepEqual(v.Dependencies, map[string]string{"valid": "^1"}) {
		t.Fatalf("%+v %+v", p, v)
	}
	if !v.PeerOptional["optional"] || v.PeerOptional["malformed"] || v.PeerOptional["required"] {
		t.Fatal(v.PeerOptional)
	}
	if !reflect.DeepEqual(v.OS, []string{"linux", "!win32"}) || !reflect.DeepEqual(v.CPU, []string{"arm64"}) || len(v.Libc) != 0 {
		t.Fatal(v.OS, v.CPU, v.Libc)
	}
	if len(v.Engines) != 0 || v.Bin[""] != "cli.js" || *v.License != "MIT" || *v.Funding != "https://fund.example" || v.Deprecated != nil || v.HasInstallScript || len(v.Bundled) != 0 || v.BundleAll || v.Dist.UnpackedSize != nil {
		t.Fatalf("%+v", v)
	}
}

func TestMetadataIntegrityIsStrict(t *testing.T) {
	for _, field := range []string{"integrity", "shasum"} {
		for _, bad := range []string{`false`, `{}`, `[]`, `42`} {
			v, err := jsonvalue.Parse([]byte(`{"name":"pkg","version":"1.0.0","dist":{"tarball":"https://registry/pkg.tgz","` + field + `":` + bad + `}}`))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = ParseVersion(v); err == nil || !strings.Contains(err.Error(), field) {
				t.Fatalf("%s=%s: %v", field, bad, err)
			}
		}
	}
	for _, body := range []string{`{}`, `{"name":"pkg","versions":null}`, `{"name":"pkg","versions":{"1":{"name":"pkg","version":"1","dependencies":[]}}}`} {
		if _, err := Parse([]byte(body)); err == nil {
			t.Fatal("accepted malformed metadata", body)
		}
	}
}

func TestDistributionAndBundleForms(t *testing.T) {
	for _, tc := range []struct {
		body  string
		all   bool
		names []string
	}{
		{`"bundledDependencies":true`, true, nil}, {`"bundleDependencies":["a"]`, false, []string{"a"}}, {`"bundledDependencies":null,"bundleDependencies":["b"]`, false, []string{"b"}},
	} {
		root, err := jsonvalue.Parse([]byte(`{"name":"pkg","version":"1.0.0","dist":{"tarball":"x","shasum":null,"unpackedSize":123},` + tc.body + `}`))
		if err != nil {
			t.Fatal(err)
		}
		v, err := ParseVersion(root)
		if err != nil {
			t.Fatal(err)
		}
		if v.BundleAll != tc.all || !reflect.DeepEqual(v.Bundled, tc.names) || *v.Dist.UnpackedSize != 123 {
			t.Fatalf("%+v", v)
		}
	}
}
