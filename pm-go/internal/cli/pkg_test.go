package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureCommand(t *testing.T, dir string, args ...string) (string, string, int) {
	t.Helper()
	var out, stderr bytes.Buffer
	code := Run(context.Background(), args, Environment{Dir: dir, Out: &out, Err: &stderr})
	return out.String(), stderr.String(), code
}

func TestPkgEditAndRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.json")
	if err := os.WriteFile(path, []byte("{\n  \"name\": \"example\"\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := fixtureCommand(t, dir, "pkg", "set", "private=true", "contributors[0].name=\"A\"", "--json"); code != 0 {
		t.Fatal(stderr)
	}
	if out, stderr, code := fixtureCommand(t, dir, "pkg", "get", "private"); code != 0 || out != "true\n" {
		t.Fatal(out, stderr)
	}
	if out, _, _ := fixtureCommand(t, dir, "pkg", "get", "contributors[0].name"); out != "A\n" {
		t.Fatal(out)
	}
	if out, _, _ := fixtureCommand(t, dir, "pkg", "get", "absent"); out != "\n" {
		t.Fatal(out)
	}
	if _, stderr, code := fixtureCommand(t, dir, "ss", "build:all", "node", "--test"); code != 0 {
		t.Fatal(stderr)
	}
	if out, _, _ := fixtureCommand(t, dir, "pkg", "get", `scripts['build:all']`); out != "node --test\n" {
		t.Fatal(out)
	}
	if _, stderr, code := fixtureCommand(t, dir, "pkg", "delete", "contributors[0]"); code != 0 {
		t.Fatal(stderr)
	}
	if out, _, _ := fixtureCommand(t, dir, "pkg", "get", "contributors"); out != "[]\n" {
		t.Fatal(out)
	}
}

func TestPkgFailureDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.json")
	original := `{"name":"original"}`
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"pkg", "set", "name=changed", "invalid"},
		{"pkg", "set", "name=changed", "__proto__.x=bad"},
		{"pkg", "set", "name=\"changed\"", "private=not-json", "--json"},
		{"pkg", "delete", "name", "x["},
	} {
		if _, _, code := fixtureCommand(t, dir, args...); code == 0 {
			t.Fatal("expected failure", args)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != original {
			t.Fatalf("failed edit wrote %s", data)
		}
	}
}

func TestPkgDirAndOptionBoundary(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "package.json"), []byte(`{"name":"target"}`), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"-C", "target", "pkg", "get", "name"}, {"pkg", "get", "name", "--dir=target"}, {"pkg", "-Ctarget", "get", "name"}} {
		if out, stderr, code := fixtureCommand(t, dir, args...); code != 0 || out != "target\n" {
			t.Fatal(args, out, stderr)
		}
	}
	if _, err, code := fixtureCommand(t, target, "pkg", "get", "name", "--not-an-option"); code == 0 || !strings.Contains(err, "unknown option") {
		t.Fatal(code, err)
	}
}
