package linker

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func binFixture(t *testing.T, root, relative, body string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBinValidationAndLaunch(t *testing.T) {
	for _, name := range []string{"pkg", "@scope/pkg", "π", "foo-bar.js"} {
		if err := ValidateBinName(name); err != nil {
			t.Fatal(name, err)
		}
	}
	for _, name := range []string{"", ".", "..", "a/b", "a/b/c", "/absolute", "@/name", "@scope/..", "a\\b", "a\x00", "a\x7f", strings.Repeat("é", 128)} {
		if ValidateBinName(name) == nil {
			t.Fatal("accepted name", name)
		}
	}
	for _, name := range []string{"CON", "com1.txt", "NUL", "prn", "aux.js", "LPT9", "pkg:", "trail.", "trail "} {
		if (ValidateBinName(name) != nil) != (runtime.GOOS == "windows") {
			t.Fatal("platform name policy", name)
		}
	}
	for _, target := range []string{"bin/cli.js", "./cli.js", "bin with spaces/π.js", "a//b", "."} {
		if err := ValidateBinTarget(target); err != nil {
			t.Fatal(target, err)
		}
	}
	for _, target := range []string{"", "../cli", "a/../cli", "C:/cli", "/cli", "a\\cli", "cli\x00", "cli\u0085", "$(id)", "a%PATH%", "a`id`", "foo;bar", "foo!bar", "foo*bar", "foo?bar", "a'b", "a\"b"} {
		if ValidateBinTarget(target) == nil {
			t.Fatal("accepted target", target)
		}
	}
	root := t.TempDir()
	for _, tc := range launchCases() {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(root, tc.name)
			if !tc.missing {
				binFixture(t, root, tc.name, tc.body)
			}
			if got := detectBinLaunch(path, nil); got != tc.launch {
				t.Fatal(got, tc.launch)
			}
		})
	}
}

type launchCase struct {
	name, body string
	missing    bool
	launch     binLaunch
}

func launchCases() []launchCase {
	return []launchCase{
		{"node.js", "#!/usr/bin/env node\n", false, binLaunch{program: "node"}},
		{"python", "#!/usr/bin/env -S KEY=value python3.11 -u\n", false, binLaunch{program: "python3.11"}},
		{"absolute", "#! /bin/bash -e\r\n", false, binLaunch{program: "bash"}},
		{"unsafe.sh", "#!/usr/bin/env bash&payload\n", false, binLaunch{program: "sh"}},
		{"empty-env", "#!/usr/bin/env KEY=value\n", false, binLaunch{program: "node"}},
		{"no-newline.sh", "#!/usr/bin/env python", false, binLaunch{program: "sh"}},
		{"long.sh", "#!/usr/bin/env " + strings.Repeat("x", 260) + "\n", false, binLaunch{program: "sh"}},
		{"extensionless", "console.log(1)\n", false, binLaunch{program: "node"}},
		{"script.cmd", "echo hello", false, binLaunch{program: "cmd"}},
		{"script.bat", "echo hello", false, binLaunch{program: "cmd"}},
		{"script.ps1", "Write-Output hello", false, binLaunch{program: "pwsh"}},
		{"future.EXE", "placeholder", false, binLaunch{direct: true}},
		{"missing.exe", "", true, binLaunch{program: "node"}},
		{"elf", "\x7fELF\x00", false, binLaunch{direct: true}},
		{"pe", "MZ", false, binLaunch{direct: true}},
		{"macho", "\xcf\xfa\xed\xfe", false, binLaunch{direct: true}},
		{"fat", "\xca\xfe\xba\xbf", false, binLaunch{direct: true}},
		{"native.js", "\x7fELF", false, binLaunch{program: "node"}},
		{"script.exe", "#!/usr/bin/env node\n", false, binLaunch{program: "node"}},
	}
}

func TestBinRelocationAndPathNode(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("PATH Node is required for this execution fixture", err)
	}
	for _, symlink := range []bool{false, true} {
		t.Run(fmt.Sprint("symlink=", symlink), func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, "before move")
			// NODE_PATH must supply both the top level and the hidden transitives,
			// independently of the invocation's unrelated working directory.
			body := "#!/usr/bin/env node\nconsole.log(JSON.stringify({args:process.argv.slice(2),top:require('top'),hidden:require('hidden')}));process.exitCode=23;\n"
			target := binFixture(t, root, "packages/cli/main.js", body)
			binFixture(t, root, "node_modules/top/index.js", "module.exports='root';")
			hidden := filepath.Join(root, "node_modules", ".store", "node_modules")
			binFixture(t, hidden, "hidden/index.js", "module.exports='transitive';")
			binDir := filepath.Join(root, "node_modules", ".bin")
			options := BinOptions{ExtendNodePath: true, PreferSymlinked: &symlink, HiddenModulesDir: hidden}
			if symlink && runtime.GOOS != "windows" {
				// The default symlink intentionally cannot inject NODE_PATH.
				binFixture(t, root, "packages/cli/main.js", "#!/usr/bin/env node\nconsole.log(JSON.stringify(process.argv.slice(2)));process.exitCode=23;\n")
			}
			if err := CreateBinShim(binDir, "@scope/tool", target, options); err != nil {
				t.Fatal(err)
			}
			moved := filepath.Join(parent, "after move")
			if err := os.Rename(root, moved); err != nil {
				t.Fatal(err)
			}
			shim := filepath.Join(moved, "node_modules", ".bin", "@scope", "tool")
			var commands []*exec.Cmd
			if runtime.GOOS == "windows" {
				commands = append(commands, exec.CommandContext(t.Context(), "cmd.exe", "/d", "/s", "/c", `""`+shim+`.cmd" "hello world""`))
				commands = append(commands, exec.CommandContext(t.Context(), "pwsh", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", shim+".ps1", "hello world"))
			} else {
				commands = append(commands, exec.CommandContext(t.Context(), shim, "hello world"))
				if !symlink {
					alias := filepath.Join(parent, "alias")
					if err := os.Symlink(shim, alias); err != nil {
						t.Fatal(err)
					}
					commands = append(commands, exec.CommandContext(t.Context(), alias, "hello world"))
				}
			}
			for _, command := range commands {
				command.Dir = t.TempDir()
				out, err := command.CombinedOutput()
				exit, ok := err.(*exec.ExitError)
				if !ok || exit.ExitCode() != 23 {
					t.Fatalf("%v: %v: %s", command.Args, err, out)
				}
				want := `{"args":["hello world"],"top":"root","hidden":"transitive"}`
				if symlink && runtime.GOOS != "windows" {
					want = `["hello world"]`
				}
				if strings.TrimSpace(string(out)) != want {
					t.Fatalf("%s: %s", command.Args, out)
				}
			}
			RemoveBinShim(filepath.Dir(filepath.Dir(shim)), "@scope/tool")
			if _, err := os.Lstat(shim); !os.IsNotExist(err) {
				t.Fatal("shim not removed", err)
			}
			if _, err := os.Stat(filepath.Dir(shim)); !os.IsNotExist(err) {
				t.Fatal("empty scope not removed", err)
			}
		})
	}
}

func TestBinInterpreterCollisionAndForeignDirectory(t *testing.T) {
	root := t.TempDir()
	target := binFixture(t, root, "pkg/main.js", "#!/usr/bin/env node\n")
	binDir := filepath.Join(root, ".bin")
	prefer := false
	var warnings []string
	opts := BinOptions{PreferSymlinked: &prefer, Warn: func(code, _ string) { warnings = append(warnings, code) }}
	binFixture(t, binDir, "node", "stale")
	if err := CreateBinShim(binDir, "node", target, opts); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(warnings, []string{"WARN_AUBE_BIN_SHIM_NAME_IS_INTERPRETER"}) {
		t.Fatal(warnings)
	}
	for _, path := range binPaths(binDir, "node") {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("self-recursive shim emitted", path, err)
		}
	}
	if err := CreateBinShim(binDir, "@scope/node", target, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(binDir, "@scope", "node")); err != nil {
		t.Fatal(err)
	}
	foreign := binFixture(t, binDir, "foreign/keep", "untouched")
	if err := CreateBinShim(binDir, "foreign", target, opts); err == nil {
		t.Fatal("populated directory overwritten")
	}
	RemoveBinShim(binDir, "foreign")
	if data, err := os.ReadFile(foreign); err != nil || string(data) != "untouched" {
		t.Fatal(string(data), err)
	}
}

func TestNativeBinWithoutNode(t *testing.T) {
	if os.Getenv("PM_GO_BIN_CHILD") == "1" {
		fmt.Println("native-child")
		os.Exit(29)
	}
	target, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	prefer := false
	if err := CreateBinShim(root, "node", target, BinOptions{PreferSymlinked: &prefer}); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(root, "node")
	command := exec.CommandContext(t.Context(), shim, "-test.run=^TestNativeBinWithoutNode$")
	if runtime.GOOS == "windows" {
		command = exec.CommandContext(t.Context(), "cmd.exe", "/d", "/s", "/c", `""`+shim+`.cmd" -test.run=^TestNativeBinWithoutNode$"`)
	}
	command.Env = append(os.Environ(), "PM_GO_BIN_CHILD=1")
	out, err := command.CombinedOutput()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 29 || strings.TrimSpace(string(out)) != "native-child" {
		t.Fatalf("%v: %s", err, out)
	}
}

func TestResolveBinShim(t *testing.T) {
	root := t.TempDir()
	for _, style := range []string{"posix", "cmd"} {
		target, node := "../pkg/cli.js", "$basedir/..:$basedir/../.store/node_modules"
		if style == "cmd" {
			target, node = `..\pkg\cli.js`, `%~dp0..;%~dp0..\.store\node_modules`
		}
		body := renderBinShim(style, binLaunch{program: "node"}, target, node)
		path := binFixture(t, root, "node_modules/.bin/tool-"+style, body)
		resolved, err := ResolveBinShim(path)
		want := &ResolvedBinShim{Target: filepath.Join(root, "node_modules", "pkg", "cli.js")}
		nodePath := strings.Join([]string{filepath.Join(root, "node_modules"), filepath.Join(root, "node_modules", ".store", "node_modules")}, string(filepath.ListSeparator))
		want.NodePath = &nodePath
		if err != nil || !reflect.DeepEqual(resolved, want) {
			t.Fatal(style, resolved, want, err)
		}
	}
	for i, body := range []string{
		"foreign wrapper", "#!/bin/sh\n# aube-bin-shim v1 target=../x\n", posixShimMarker + "/absolute\n",
		posixShimMarker + "C:/x\n", posixShimMarker + "\x00\n", posixShimMarker + "../cli\nexport NODE_PATH=\"/foreign\"\n",
		posixShimMarker + "../cli\nexport NODE_PATH=\"$basedir/..;$basedir/hidden\"\n",
		posixShimMarker + "../cli\n\xff", posixShimMarker + strings.Repeat("x", 65536),
		renderBinShim("cmd", binLaunch{direct: true}, `..\pkg\native.exe`, ""),
		renderBinShim("cmd", binLaunch{program: "node"}, `..\pkg\cli.js`, "") + "echo foreign\n",
	} {
		path := binFixture(t, root, fmt.Sprint("invalid-", i), body)
		if got, err := ResolveBinShim(path); err != nil || got != nil {
			t.Fatal(i, got, err)
		}
	}
	if got, err := ResolveBinShim(root); err != nil || got != nil {
		t.Fatal(got, err)
	}
	if _, err := ResolveBinShim(filepath.Join(root, "missing")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

// Snapshot observable wrapper bytes/link destinations and permission bits.
func binSnapshot(binDir, name, target string) map[string]any {
	files := map[string]any{}
	for _, path := range binPaths(binDir, name) {
		info, err := os.Lstat(path)
		if err != nil {
			continue
		}
		entry := map[string]any{}
		if info.Mode()&os.ModeSymlink != 0 {
			entry["link"], _ = os.Readlink(path)
		} else {
			data, _ := os.ReadFile(path)
			entry["body"] = string(data)
		}
		if runtime.GOOS != "windows" {
			entry["mode"] = int(info.Mode().Perm())
		}
		files[strings.TrimPrefix(path, filepath.Join(binDir, filepath.FromSlash(name)))] = entry
	}
	result := map[string]any{"files": files}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(target); err == nil {
			result["target_mode"] = int(info.Mode().Perm())
		}
	}
	return result
}

func compareBinOracle(t *testing.T, cases, expected []map[string]any) {
	t.Helper()
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		t.Skip("set PM_RUST_LOCKFILE_ORACLE to compare executable shims")
	}
	input, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	path := binFixture(t, t.TempDir(), "bins.json", string(input))
	out, err := exec.CommandContext(t.Context(), oracle, "bin-shims", path).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	var got, want any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	wantJSON, _ := json.Marshal(expected)
	if err := json.Unmarshal(wantJSON, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		for i, entry := range got.([]any) {
			if !reflect.DeepEqual(entry, want.([]any)[i]) {
				t.Errorf("case %d %v: Rust %+v; Go %+v", i, cases[i], entry, want.([]any)[i])
			}
		}
	}
	t.Logf("compared %d executable shim cases", len(cases))
}
