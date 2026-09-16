package linker

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
)

func binJSON(t *testing.T, raw string) *jsonvalue.Value {
	t.Helper()
	value, err := jsonvalue.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func resolvedTestBin(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if runtime.GOOS == "windows" {
		path += ".cmd"
	}
	resolved, err := ResolveBinShim(path)
	if err != nil || resolved == nil {
		t.Fatal("unrecognized test shim", path, resolved, err)
	}
	return resolved.Target
}

func TestBinManifestEntriesAndDirectoryPrecedence(t *testing.T) {
	root := t.TempDir()
	prefer := false
	opts := BinOptions{PreferSymlinked: &prefer}
	pkgDir := filepath.Join(root, "pkg")
	one := binFixture(t, pkgDir, "commands/a/tool", "#!/usr/bin/env node\n")
	two := binFixture(t, pkgDir, "commands/z/tool", "#!/usr/bin/env node\n")
	binFixture(t, pkgDir, "commands/other", "#!/usr/bin/env node\n")
	outside := filepath.Join(root, "outside")
	binFixture(t, outside, "external", "#!/usr/bin/env node\n")
	if err := CreateDirLink(t.Context(), outside, filepath.Join(pkgDir, "commands", "escape")); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, json string
		bins       map[string]string
	}{
		{"string", `{"bin":"commands/a/tool"}`, map[string]string{"pkg": one}},
		{"object", `{"bin":{"a":"commands/a/tool","@s/b":"commands/z/tool","../escape":"commands/a/tool","outside":"../outside/external","nonstring":true,"unsafe":"$(bad)"}}`, map[string]string{"a": one, "@s/b": two}},
		{"directory", `{"directories":{"bin":"commands"}}`, map[string]string{"tool": two, "other": filepath.Join(pkgDir, "commands", "other")}},
		{"null-bin", `{"bin":null,"directories":{"bin":"commands"}}`, nil},
		{"explicit-wins", `{"bin":"commands/a/tool","directories":{"bin":"commands"}}`, map[string]string{"pkg": one}},
		{"root", `{"directories":{"bin":"."}}`, nil},
		{"outside", `{"directories":{"bin":"../outside"}}`, nil},
		{"symlink-outside", `{"directories":{"bin":"commands/escape"}}`, nil},
		{"missing", `{"directories":{"bin":"absent"}}`, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			binDir := filepath.Join(root, test.name, ".bin")
			links := NewBinLinks(true)
			if err := links.LinkManifest(binDir, pkgDir, "@scope/pkg", binJSON(t, test.json), opts, nil); err != nil {
				t.Fatal(err)
			}
			if len(links.entries[binDir]) != len(test.bins) {
				t.Fatal(links.entries[binDir], test.bins)
			}
			for name, target := range test.bins {
				if got := resolvedTestBin(t, binDir, name); got != target {
					t.Fatal(name, got, target)
				}
			}
			if len(test.bins) == 0 {
				if _, err := os.Stat(binDir); !os.IsNotExist(err) {
					t.Fatal("created an empty bin directory", err)
				}
			}
		})
	}
}

func TestManagedBinFamiliesSurviveLifecycleReplacement(t *testing.T) {
	for _, replacement := range []string{"file", "deleted", "directory"} {
		t.Run(replacement, func(t *testing.T) {
			root := t.TempDir()
			pkgDir := filepath.Join(root, "pkg")
			binFixture(t, pkgDir, "cli.js", "#!/usr/bin/env node\n")
			binDir := filepath.Join(root, ".bin")
			prefer := false
			opts := BinOptions{PreferSymlinked: &prefer}
			before := NewBinLinks(true)
			declarations := binJSON(t, `{"stable":"cli.js","changed":"cli.js"}`)
			if err := before.LinkEntries(binDir, pkgDir, "pkg", declarations, opts, nil); err != nil {
				t.Fatal(err)
			}
			paths := binPaths(binDir, "changed")
			modified := paths[len(paths)-1]
			if err := os.Remove(modified); err != nil {
				t.Fatal(err)
			}
			switch replacement {
			case "file":
				binFixture(t, filepath.Dir(modified), filepath.Base(modified), "lifecycle output")
			case "directory":
				binFixture(t, modified, "created", "lifecycle output")
			}
			preserved, err := before.RemoveUnchanged()
			if err != nil || !preserved[binDir]["changed"] || preserved[binDir]["stable"] {
				t.Fatal(preserved, err)
			}
			for _, path := range binPaths(binDir, "stable") {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatal("unchanged shim not removed", path, err)
				}
			}
			// One changed launcher preserves every unchanged sibling too.
			for _, path := range paths[:len(paths)-1] {
				if _, err := os.Lstat(path); err != nil {
					t.Fatal("command sibling removed", path, err)
				}
			}
			after := NewBinLinks(false)
			if err := after.LinkEntries(binDir, pkgDir, "pkg", declarations, opts, preserved); err != nil {
				t.Fatal(err)
			}
			if err := before.RemoveUnclaimed(preserved, after); err != nil {
				t.Fatal(err)
			}
			switch replacement {
			case "deleted":
				if _, err := os.Lstat(modified); !os.IsNotExist(err) {
					t.Fatal("deleted launcher restored", err)
				}
			case "file":
				if data, err := os.ReadFile(modified); err != nil || string(data) != "lifecycle output" {
					t.Fatal(string(data), err)
				}
			case "directory":
				if _, err := os.Stat(filepath.Join(modified, "created")); err != nil {
					t.Fatal(err)
				}
			}
			// A removed declaration permits cleanup of all previously tracked
			// paths, including a lifecycle-created directory at the shim slot.
			if err := before.RemoveUnclaimed(preserved, NewBinLinks(false)); err != nil {
				t.Fatal(err)
			}
			for _, path := range paths {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatal("unclaimed launcher remains", path, err)
				}
			}
		})
	}
}

func TestManagedBinRetargetAndLastWriter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlink ownership; Windows command families are tested separately")
	}
	root := t.TempDir()
	one := binFixture(t, root, "one/cli", "#!/usr/bin/env node\n")
	two := binFixture(t, root, "two/cli", "#!/usr/bin/env node\n")
	binDir := filepath.Join(root, ".bin")
	before := NewBinLinks(true)
	for _, target := range []string{one, two} {
		if err := before.create(binDir, "shared", target, BinOptions{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if preserved, err := before.RemoveUnchanged(); err != nil || len(preserved) != 0 {
		t.Fatal("last writer was not captured", preserved, err)
	}
	if err := before.create(binDir, "shared", one, BinOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(binDir, "shared")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(two, path); err != nil {
		t.Fatal(err)
	}
	preserved, err := before.RemoveUnchanged()
	if err != nil || !preserved[binDir]["shared"] {
		t.Fatal(preserved, err)
	}
	if target, err := os.Readlink(path); err != nil || target != two {
		t.Fatal(target, err)
	}
}
