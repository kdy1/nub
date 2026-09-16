package resolver

import (
	"testing"
	"testing/fstest"
)

func TestPlatformConstraints(t *testing.T) {
	host := Platform{"linux", "x64", "glibc"}
	for _, tc := range []struct {
		os, cpu, libc []string
		want          bool
	}{
		{nil, nil, nil, true}, {[]string{"!win32"}, nil, nil, true}, {[]string{"linux", "!linux"}, nil, nil, false},
		{[]string{"darwin"}, nil, nil, false}, {nil, []string{"arm64"}, nil, false}, {nil, nil, []string{"musl"}, false},
		{[]string{"linux"}, []string{"x64"}, []string{"glibc"}, true}, {[]string{"any"}, nil, nil, false},
	} {
		if got := Supported(tc.os, tc.cpu, tc.libc, host, Architectures{}); got != tc.want {
			t.Fatal(tc, got)
		}
	}
	if !Supported([]string{"darwin"}, []string{"arm64"}, []string{"musl"}, Platform{"darwin", "arm64", ""}, Architectures{}) {
		t.Fatal("libc enforced off Linux")
	}
	if !Supported([]string{"linux"}, []string{"arm64"}, []string{"musl"}, host, Architectures{CPU: []string{"current", "arm64"}, Libc: []string{"current", "musl"}}) {
		t.Fatal("supported architecture set ignored")
	}
	if !Supported([]string{"!linux"}, nil, nil, host, Architectures{OS: []string{"*"}}) {
		t.Fatal("target wildcard ignored")
	}
	if !Supported([]string{"!linux"}, nil, nil, host, Architectures{AcceptAll: true}) {
		t.Fatal("cross-platform lockfile omitted variant")
	}
}

func TestLinuxLibcPrefersActiveLoader(t *testing.T) {
	for _, tc := range []struct {
		files fstest.MapFS
		want  string
	}{
		{fstest.MapFS{}, "glibc"},
		{fstest.MapFS{"lib/ld-musl-x86_64.so.1": {}}, "musl"},
		{fstest.MapFS{"lib/ld-musl-x86_64.so.1": {}, "lib64/ld-linux-x86-64.so.2": {}}, "glibc"},
		{fstest.MapFS{"proc/self/maps": {Data: []byte("/lib/ld-musl-x86_64.so.1")}, "lib64/ld-linux-x86-64.so.2": {}}, "musl"},
		{fstest.MapFS{"proc/self/maps": {Data: []byte("/lib/ld-linux-x86-64.so.2")}, "lib/ld-musl-x86_64.so.1": {}}, "glibc"},
	} {
		if got := LinuxLibc(tc.files); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}
