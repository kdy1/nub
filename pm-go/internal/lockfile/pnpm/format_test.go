package pnpm

import (
	"strings"
	"testing"
)

func TestPnpmYAMLLayout(t *testing.T) {
	input := "lockfileVersion: '9.0'\nsettings:\n  autoInstallPeers: true\npackages:\n  '@scope/pkg@1.0.0':\n    resolution:\n      integrity: sha512-test\n      tarball: https://example.test/pkg.tgz\n    engines:\n      node: '>=18'\n    cpu:\n    - arm64\n    - x64\n    os:\n    - darwin\nsnapshots:\n  '@scope/pkg@1.0.0':\n    transitivePeerDependencies:\n    - peer\n  empty@1.0.0: {}\n"
	want := "lockfileVersion: '9.0'\n\nsettings:\n  autoInstallPeers: true\n\npackages:\n\n  '@scope/pkg@1.0.0':\n    resolution: {integrity: sha512-test, tarball: https://example.test/pkg.tgz}\n    engines: {node: '>=18'}\n    cpu: [arm64, x64]\n    os: [darwin]\n\nsnapshots:\n\n  '@scope/pkg@1.0.0':\n    transitivePeerDependencies:\n      - peer\n\n  empty@1.0.0: {}\n"
	if got := reformat(input); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
func TestPnpmYAMLRuntimeAndCatalogLayout(t *testing.T) {
	input := "catalogs:\n  default:\n    a:\n      specifier: '*'\n      version: 1.0.0\n  other: {}\npackages:\n  node@runtime:24.4.1:\n    resolution:\n      type: variations\n      variants:\n      - resolution:\n          archive: tarball\n          type: binary\n        targets:\n        - cpu: arm64\n          os: darwin\n"
	got := reformat(input)
	for _, unchanged := range []string{"catalogs:\n  default:", "      version: 1.0.0\n  other: {}", "      variants:\n      - resolution:", "        targets:\n        - cpu:"} {
		if !strings.Contains(got, unchanged) {
			t.Fatal(got)
		}
	}
	if strings.Contains(got, "resolution: {") {
		t.Fatal("runtime resolution collapsed", got)
	}
}
func TestPnpmYAMLExplicitKeyFolding(t *testing.T) {
	key := "'" + strings.Repeat("long", 50) + "'"
	input := "snapshots:\n  ? " + key + "\n  : dependencies:\n      a: 1.0.0\n    optionalDependencies:\n      b: 2.0.0\n    transitivePeerDependencies:\n    - peer\n  ? other\n  : {}\n"
	got := reformat(input)
	want := "snapshots:\n\n  " + key + ":\n    dependencies:\n      a: 1.0.0\n    optionalDependencies:\n      b: 2.0.0\n    transitivePeerDependencies:\n      - peer\n\n  other: {}\n"
	if got != want {
		t.Fatalf("%s", got)
	}
}
