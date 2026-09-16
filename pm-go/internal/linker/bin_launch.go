package linker

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
)

func ValidateBinName(name string) error {
	valid := name != "" && len(name) <= 255
	parts := strings.Split(name, "/")
	if len(parts) == 2 {
		valid = valid && strings.HasPrefix(parts[0], "@") && len(parts[0]) > 1
	} else if len(parts) != 1 {
		valid = false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsFunc(part, func(c rune) bool { return c == '\\' || c < 32 || c == 127 }) {
			valid = false
		}
		if runtime.GOOS == "windows" && (strings.Contains(part, ":") || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || reservedBinName(part)) {
			valid = false
		}
	}
	if !valid {
		return fmt.Errorf("invalid bin name: %q", name)
	}
	return nil
}

func reservedBinName(name string) bool {
	stem, _, _ := strings.Cut(strings.ToUpper(name), ".")
	switch stem {
	case "CON", "PRN", "NUL", "AUX":
		return true
	}
	return len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9'
}

func ValidateBinTarget(relative string) error {
	if relative == "" || strings.ContainsAny(relative, "\x00\\") {
		return fmt.Errorf("invalid bin target: %q", relative)
	}
	if strings.ContainsFunc(relative, func(c rune) bool { return strings.ContainsRune("$`%\"'&|^;<>()!*?", c) || unicode.IsControl(c) }) {
		return fmt.Errorf("bin target contains shell metacharacter: %q", relative)
	}
	if strings.HasPrefix(relative, "/") || len(relative) >= 2 && relative[1] == ':' {
		return fmt.Errorf("absolute bin target: %q", relative)
	}
	for _, part := range strings.Split(relative, "/") {
		if part == ".." {
			return fmt.Errorf("bin target escapes package: %q", relative)
		}
	}
	return nil
}

type binLaunch struct {
	direct  bool
	program string
}

func safeProgram(prog string) bool {
	if prog == "" || len(prog) > 64 {
		return false
	}
	for i, c := range []byte(prog) {
		alnum := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if !alnum && (i == 0 || !strings.ContainsRune("._+-", rune(c))) {
			return false
		}
	}
	return true
}

func IsNativeExecutable(data []byte) bool {
	for _, magic := range [][]byte{[]byte("\x7fELF"), []byte("MZ"), {0xfe, 0xed, 0xfa, 0xce}, {0xfe, 0xed, 0xfa, 0xcf}, {0xce, 0xfa, 0xed, 0xfe}, {0xcf, 0xfa, 0xed, 0xfe}, {0xca, 0xfe, 0xba, 0xbe}, {0xbe, 0xba, 0xfe, 0xca}, {0xca, 0xfe, 0xba, 0xbf}, {0xbf, 0xba, 0xfe, 0xca}} {
		if bytes.HasPrefix(data, magic) {
			return true
		}
	}
	return false
}

func detectBinLaunch(target string, warn func(string, string)) binLaunch {
	buf := make([]byte, 256)
	n, exists := 0, false
	if file, err := os.Open(target); err == nil {
		n, _ = file.Read(buf)
		exists = true
		file.Close()
	}
	data := buf[:n]
	if bytes.HasPrefix(data, []byte("#!")) && len(data) > 2 {
		if end := bytes.IndexByte(data, '\n'); end >= 0 {
			line := strings.TrimSpace(strings.ToValidUTF8(string(data[2:end]), "\ufffd"))
			program := "node"
			if rest, env := strings.CutPrefix(line, "/usr/bin/env"); env {
				rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
				if tail, split := strings.CutPrefix(rest, "-S"); split {
					rest = strings.TrimLeftFunc(tail, unicode.IsSpace)
				}
				for _, token := range strings.Fields(rest) {
					if !strings.Contains(token, "=") {
						program = token
						break
					}
				}
			} else if tokens := strings.Fields(line); len(tokens) > 0 {
				parts := strings.Split(tokens[0], "/")
				program = parts[len(parts)-1]
			}
			if safeProgram(program) {
				return binLaunch{program: program}
			}
			if warn != nil {
				warn("", fmt.Sprintf("ignoring unsafe shebang interpreter in %q: %q", target, program))
			}
		}
	}
	ext := strings.TrimPrefix(filepath.Ext(target), ".")
	switch ext {
	case "js", "cjs", "mjs":
		return binLaunch{program: "node"}
	case "cmd", "bat":
		return binLaunch{program: "cmd"}
	case "ps1":
		return binLaunch{program: "pwsh"}
	case "sh":
		return binLaunch{program: "sh"}
	}
	if exists && !bytes.HasPrefix(data, []byte("#!")) && (strings.EqualFold(ext, "exe") || IsNativeExecutable(data)) {
		return binLaunch{direct: true}
	}
	return binLaunch{program: "node"}
}
