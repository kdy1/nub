package linker

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"
)

// ParseWinShimTarget recovers a target, not ownership. Other managers can
// produce the same syntax; callers must also check the resolved destination.
func ParseWinShimTarget(content string) (string, bool) {
	for _, line := range shimLines(content) {
		line = strings.TrimSpace(line)
		after, direct := strings.CutPrefix(line, `@"%~dp0\`)
		if !direct {
			start := strings.Index(line, `%~dp0\`)
			if start < 0 || strings.Contains(line, `.exe"`) {
				continue
			}
			after = line[start+6:]
		}
		if target, _, ok := strings.Cut(after, `"`); ok {
			return target, true
		}
	}
	return "", false
}

func ParsePosixShimTarget(content string) (string, bool) {
	for _, line := range shimLines(content) {
		if target, ok := strings.CutPrefix(line, posixShimMarker); ok {
			return target, true
		}
	}
	return "", false
}

type ResolvedBinShim struct {
	Target   string
	NodePath *string
}

// ResolveBinShim reads only bounded regular files and never executes a wrapper.
// A nil result identifies an unrecognized or invalid wrapper (including links).
func ResolveBinShim(path string) (*ResolvedBinShim, error) {
	const limit = 64 * 1024
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit || !utf8.Valid(data) {
		return nil, nil
	}
	content := string(data)
	target, ok := ParsePosixShimTarget(content)
	cmd := !ok
	if cmd {
		target, ok = parseCmdShimTarget(content)
	}
	if !ok {
		return nil, nil
	}
	parent := filepath.Dir(path)
	target, ok = resolveShimRelative(parent, target, cmd)
	if !ok {
		return nil, nil
	}
	result := &ResolvedBinShim{Target: target}
	for _, line := range shimLines(content) {
		value, found := strings.CutPrefix(line, `export NODE_PATH="`)
		if cmd {
			value, found = strings.CutPrefix(line, "@SET NODE_PATH=")
			value = strings.TrimRight(value, "\r")
		} else if found {
			value, found = strings.CutSuffix(value, `"`)
		}
		if found {
			nodePath, ok := resolveShimNodePath(parent, value, cmd)
			if !ok {
				return nil, nil
			}
			result.NodePath = &nodePath
			break
		}
	}
	return result, nil
}

func shimLines(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines
}

func parseCmdShimTarget(content string) (string, bool) {
	lines := shimLines(content)
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], "\r")
	}
	if len(lines) < 2 || lines[0] != "@SETLOCAL" {
		return "", false
	}
	lines = lines[1:]
	if strings.HasPrefix(lines[0], "@SET NODE_PATH=") {
		lines = lines[1:]
	}
	if len(lines) != 6 {
		return "", false
	}
	program, ok := strings.CutPrefix(lines[0], `@IF EXIST "%~dp0\`)
	if !ok {
		return "", false
	}
	program, ok = strings.CutSuffix(program, `.exe" (`)
	if !ok || !safeProgram(program) {
		return "", false
	}
	target, ok := strings.CutPrefix(lines[1], `  "%~dp0\`+program+`.exe" "%~dp0\`)
	if !ok {
		return "", false
	}
	target, ok = strings.CutSuffix(target, `" %*`)
	if !ok || lines[2] != ") ELSE (" || lines[3] != "  @SET PATHEXT=%PATHEXT:;.JS;=;%" || lines[5] != ")" || lines[4] != `  `+program+` "%~dp0\`+target+`" %*` {
		return "", false
	}
	return target, true
}

func resolveShimRelative(parent, relative string, cmd bool) (string, bool) {
	if relative == "" || strings.ContainsRune(relative, 0) || strings.HasPrefix(relative, "/") || strings.HasPrefix(relative, "\\") || len(relative) >= 2 && relative[1] == ':' {
		return "", false
	}
	if cmd {
		relative = strings.ReplaceAll(relative, "\\", string(filepath.Separator))
	}
	return normalizeShimPath(parent + string(filepath.Separator) + relative), true
}

// Match the reference's lexical component stack, including parent components
// above the root. Canonicalization would change paths reached through symlinks.
func normalizeShimPath(path string) string {
	volume := filepath.VolumeName(path)
	rest := path[len(volume):]
	if runtime.GOOS == "windows" {
		rest = strings.ReplaceAll(rest, "/", "\\")
	}
	sep := string(filepath.Separator)
	root := strings.HasPrefix(rest, sep)
	var parts []string
	for _, part := range strings.Split(rest, sep) {
		switch part {
		case "", ".":
		case "..":
			if len(parts) > 0 {
				parts = parts[:len(parts)-1]
			} else {
				parts = append(parts, part)
			}
		default:
			parts = append(parts, part)
		}
	}
	if root {
		volume += sep
	}
	return volume + strings.Join(parts, sep)
}

func resolveShimNodePath(parent, value string, cmd bool) (string, bool) {
	separator, prefix := ":", "$basedir/"
	if cmd {
		separator, prefix = ";", "%~dp0"
	} else if strings.Contains(value, ";") {
		return "", false
	}
	var paths []string
	for _, entry := range strings.Split(value, separator) {
		relative, ok := strings.CutPrefix(entry, prefix)
		if !ok {
			return "", false
		}
		path, ok := resolveShimRelative(parent, relative, cmd)
		if !ok {
			return "", false
		}
		if runtime.GOOS == "windows" {
			if strings.ContainsRune(path, '"') {
				return "", false
			}
			if strings.ContainsRune(path, ';') {
				path = `"` + path + `"`
			}
		} else if strings.ContainsRune(path, ':') {
			return "", false
		}
		paths = append(paths, path)
	}
	return strings.Join(paths, string(filepath.ListSeparator)), true
}
