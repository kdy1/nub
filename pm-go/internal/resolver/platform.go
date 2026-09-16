package resolver

import (
	"io/fs"
	"os"
	"runtime"
	"strings"
)

type Platform struct{ OS, CPU, Libc string }
type Architectures struct {
	OS, CPU, Libc []string
	AcceptAll     bool
}

func HostPlatform() Platform {
	p := Platform{OS: runtime.GOOS, CPU: runtime.GOARCH}
	if p.OS == "windows" {
		p.OS = "win32"
	}
	switch p.CPU {
	case "amd64":
		p.CPU = "x64"
	case "386":
		p.CPU = "ia32"
	case "ppc64le":
		p.CPU = "ppc64"
	case "loong64":
		p.CPU = "loongarch64"
	}
	if runtime.GOOS == "linux" {
		p.Libc = LinuxLibc(os.DirFS("/"))
	}
	return p
}

// LinuxLibc inspects the running loader before looking for installed loaders.
// Static executables have no libc mapping and use the filesystem fallback.
func LinuxLibc(root fs.FS) string {
	if maps, err := fs.ReadFile(root, "proc/self/maps"); err == nil {
		if strings.Contains(string(maps), "/ld-musl-") {
			return "musl"
		}
		if strings.Contains(string(maps), "/ld-linux") {
			return "glibc"
		}
	}
	for _, dir := range []string{"lib", "lib64", "lib/x86_64-linux-gnu", "lib/aarch64-linux-gnu"} {
		entries, _ := fs.ReadDir(root, dir)
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "ld-linux") {
				return "glibc"
			}
		}
	}
	entries, _ := fs.ReadDir(root, "lib")
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "ld-musl-") {
			return "musl"
		}
	}
	return "glibc"
}

func FieldMatches(constraints []string, target string) bool {
	if len(constraints) == 0 || target == "*" {
		return true
	}
	positive, matched := false, false
	for _, entry := range constraints {
		if excluded, neg := strings.CutPrefix(entry, "!"); neg {
			if excluded == target {
				return false
			}
		} else {
			positive = true
			matched = matched || entry == target
		}
	}
	return !positive || matched
}

func Supported(os, cpu, libc []string, host Platform, supported Architectures) bool {
	if supported.AcceptAll {
		return true
	}
	expand := func(values []string, current string) []string {
		if len(values) == 0 {
			return []string{current}
		}
		out := make([]string, len(values))
		for i, v := range values {
			if v == "current" {
				v = current
			}
			out[i] = v
		}
		return out
	}
	for _, o := range expand(supported.OS, host.OS) {
		if FieldMatches(os, o) {
			for _, c := range expand(supported.CPU, host.CPU) {
				if FieldMatches(cpu, c) {
					for _, l := range expand(supported.Libc, host.Libc) {
						if l == "" || FieldMatches(libc, l) {
							return true
						}
					}
				}
			}
		}
	}
	return false
}
