package registry

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/npmconfig"
)

func TestRegistryHostAndLockedURLPolicy(t *testing.T) {
	for _, pair := range [][2]string{
		{"https://user:pass@NPM.Work.net:443/path", "http://npm.work.net:80/other"},
		{"https://[2001:0db8::1]:443/a", "https://[2001:db8::1]/b"},
		{"https://bücher.example/a", "https://xn--bcher-kva.example/b"},
		{"http://127.1/", "http://127.0.0.1/"},
	} {
		if !SameRegistryHost(pair[0], pair[1]) {
			t.Fatal(pair)
		}
	}
	if SameRegistryHost("https://host:444/a", "https://host/a") || SameRegistryHost("broken", "https://host/a") {
		t.Fatal("host mismatch accepted")
	}
	for _, tc := range []struct {
		locked, expected string
		matches          bool
	}{
		{"https://host/p/-/p.tgz?a=1", "https://host/p/-/p.tgz?a=1", true},
		{"https://host/p/-/p.tgz?a=1", "https://host/p/-/p.tgz?a=2", false},
		{"https://registry.npmjs.org/@s/p/-/p.tgz", "https://mirror.example/@s/p/-/p.tgz", true},
		{"https://registry.npmjs.org/p/-/p.tgz?one", "https://mirror.example/p/-/p.tgz?two", true},
		{"https://other.example/p/-/p.tgz", "https://mirror.example/p/-/p.tgz", false},
		{"https://registry.npmjs.org/p/-/p.tgz", "https://mirror.example/q/-/p.tgz", false},
		{"https://registry.npmjs.org/p.tgz", "https://mirror.example/p.tgz", false},
	} {
		if got := LockfileURLMatches(tc.locked, tc.expected); got != tc.matches {
			t.Fatal(tc, got)
		}
	}
}

func TestRustRegistryURLOracle(t *testing.T) {
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		t.Skip("set PM_RUST_LOCKFILE_ORACLE to compare registry URLs")
	}
	urls := []string{
		"https://registry.npmjs.org/", "https://NPM.Work.net:443/path", "http://user:pass@LOCALHOST:80/x", "https://HOST:80/x", "http://localhost:4873/",
		"https://[2001:0db8::1]:443/", "http://[::1]:8080/", "https://bücher.example/", "https://%65xample.org/", "https://EXAMPLE.org./",
		"http://127.1/", "http://0x7f000001/", "http://0177.0.0.1/", "http://2130706433/", "https://host:00443/",
		"ftp://host:21/", "ws://host:80/", "wss://host:443/", "file://localhost/path", "file://server/path", "custom://UPPER:123/path", "custom:///path", "mailto:a@example.org",
		"", "relative/path", "//host/path", "https://", "https://host:65536/", "https://host:no/", "https://[broken]/", "https://a b/", "https://host:/", " https://HOST/path\n", "https:\\host\\path",
	}
	var hosts []*string
	for _, raw := range urls {
		if key, ok := HostKey(raw); ok {
			hosts = append(hosts, &key)
		} else {
			hosts = append(hosts, nil)
		}
	}
	var coordinates []map[string]string
	var tarballs []string
	for _, endpoint := range []string{"https://registry.npmjs.org/", "http://localhost:4873/prefix///"} {
		client := NewClient(npmconfig.Resolve([]npmconfig.Entry{{Source: npmconfig.User, Key: "registry", Value: endpoint}}, nil), ClientOptions{})
		for _, name := range []string{"package", "@scope/package", "@scope"} {
			coordinates = append(coordinates, map[string]string{"registry": endpoint, "name": name, "version": "1.2.3-beta+build"})
			tarballs = append(tarballs, client.TarballURL(name, "1.2.3-beta+build"))
		}
		client.Close()
	}
	data, err := json.Marshal(map[string]any{"urls": urls, "coordinates": coordinates})
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(t.TempDir(), "urls.json")
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	output, err := exec.CommandContext(t.Context(), oracle, "registry-urls", input).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	var got struct {
		Hosts    []*string `json:"hosts"`
		Tarballs []string  `json:"tarballs"`
	}
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	if !reflect.DeepEqual(hosts, got.Hosts) {
		t.Fatalf("host keys differ\nRust %s\nGo %v", output, hosts)
	}
	if !reflect.DeepEqual(tarballs, got.Tarballs) {
		t.Fatal(got.Tarballs, tarballs)
	}
	t.Logf("compared %d registry host keys and %d archive URLs", len(urls), len(tarballs))
}
