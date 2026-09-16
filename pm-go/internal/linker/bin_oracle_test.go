package linker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRustBinCreationOracle(t *testing.T) {
	if os.Getenv("PM_RUST_LOCKFILE_ORACLE") == "" {
		t.Skip("set PM_RUST_LOCKFILE_ORACLE to compare executable shims")
	}
	var cases, expected []map[string]any
	for _, source := range launchCases() {
		for _, name := range []string{"tool", "@scope/tool", "node"} {
			for _, symlink := range []bool{false, true} {
				root := t.TempDir()
				target := filepath.Join(root, "pkg", source.name)
				if !source.missing {
					binFixture(t, root, "pkg/"+source.name, source.body)
				}
				binDir := filepath.Join(root, "node_modules", ".bin")
				hidden := filepath.Join(root, "node_modules", ".store", "node_modules")
				options := BinOptions{PreferSymlinked: &symlink, ExtendNodePath: true, HiddenModulesDir: hidden}
				if err := CreateBinShim(binDir, name, target, options); err != nil {
					t.Fatal(err)
				}
				expected = append(expected, binSnapshot(binDir, name, target))
				RemoveBinShim(binDir, name)
				if !source.missing {
					if err := os.Chmod(target, 0644); err != nil {
						t.Fatal(err)
					}
				}
				cases = append(cases, map[string]any{"op": "create", "bin_dir": binDir, "name": name, "target": target, "hidden": hidden, "symlink": symlink, "extend": true})
			}
		}
	}
	compareBinOracle(t, cases, expected)
}

func TestRustBinReaderAndValidationOracle(t *testing.T) {
	if os.Getenv("PM_RUST_LOCKFILE_ORACLE") == "" {
		t.Skip("set PM_RUST_LOCKFILE_ORACLE to compare executable shims")
	}
	var cases, expected []map[string]any
	for _, name := range []string{"tool", "@scope/tool", "", ".", "..", "a/b", "@/tool", "@scope/..", "@scope/a/b", "foo\x7f", "π", "a:b", "CON", "@scope/", "a\\b", "a\x00", strings.Repeat("é", 128)} {
		cases = append(cases, map[string]any{"op": "name", "value": name})
		expected = append(expected, map[string]any{"valid": ValidateBinName(name) == nil})
	}
	for _, target := range []string{"bin/cli.js", "./cli.js", "a//b", "bin with spaces/π.js", ".", "", "../a", "/a", "C:/a", "a\\b", "a\x00", "a\u0085", "$(id)", "a%PATH%", "a`id`", "foo;bar", "a'b", "a\"b", "a!b", "a?b", "a*b", "a(b"} {
		cases = append(cases, map[string]any{"op": "target", "value": target})
		expected = append(expected, map[string]any{"valid": ValidateBinTarget(target) == nil})
	}
	root := t.TempDir()
	contents := []string{"foreign", "@SETLOCAL\r\n", posixShimMarker + "", posixShimMarker + "../x\n", posixShimMarker + "../x\r\n", posixShimMarker + "../../../../../../../../../../../../x\n", " " + posixShimMarker + "../x\n", posixShimMarker + "/absolute\n", posixShimMarker + "C:/x\n", posixShimMarker + "\x00\n", posixShimMarker + "../x\nexport NODE_PATH=\"/absolute\"\n", posixShimMarker + "../x\nexport NODE_PATH=\"$basedir/..;$basedir/hidden\"\n", posixShimMarker + strings.Repeat("x", 65536)}
	for _, style := range []string{"posix", "cmd", "powershell", "gitbash"} {
		for _, launch := range []binLaunch{{program: "node"}, {program: "python3.11"}, {direct: true}} {
			for _, nodePath := range []bool{false, true} {
				target, node := "../pkg/cli.js", ""
				if nodePath {
					node = "$basedir/..:$basedir/../.store/node_modules"
				}
				if style == "cmd" {
					target = `..\pkg\cli.js`
					if nodePath {
						node = `%~dp0..;%~dp0..\.store\node_modules`
					}
				}
				contents = append(contents, renderBinShim(style, launch, target, node))
			}
		}
	}
	baseCmd := renderBinShim("cmd", binLaunch{program: "node"}, `..\pkg\cli.js`, "")
	contents = append(contents, baseCmd+"echo foreign\n", strings.Replace(baseCmd, "  node ", "  python ", 1), strings.Replace(baseCmd, "@SETLOCAL", "@echo off", 1))
	for i, content := range contents {
		path := binFixture(t, root, fmt.Sprint("read-", i), content)
		var posix, win, resolved any
		if target, ok := ParsePosixShimTarget(content); ok {
			posix = target
		}
		if target, ok := ParseWinShimTarget(content); ok {
			win = target
		}
		if shim, err := ResolveBinShim(path); err != nil {
			t.Fatal(err)
		} else if shim != nil {
			resolved = map[string]any{"target": shim.Target, "node_path": shim.NodePath}
		}
		cases = append(cases, map[string]any{"op": "read", "path": path})
		expected = append(expected, map[string]any{"posix": posix, "win": win, "resolved": resolved})
	}
	compareBinOracle(t, cases, expected)
}
