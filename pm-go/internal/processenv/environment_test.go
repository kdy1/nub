package processenv

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestPathAndEnvironmentIsolation(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	name := "fixture-tool"
	file := name
	if runtime.GOOS == "windows" {
		file += ".exe"
	}
	path := filepath.Join(bin, file)
	if err := os.WriteFile(path, nil, 0755); err != nil {
		t.Fatal(err)
	}
	e := Environment{Dir: root, Vars: []string{"PATH=bin", "MARKER=old", "MARKER=last"}}
	before := append([]string{}, e.Vars...)
	if got, err := e.LookPath(name); err != nil || got != path {
		t.Fatal(got, path, err)
	}
	if e.Lookup("MARKER") != "last" {
		t.Fatal(e)
	}
	updated := e.With("MARKER", "new")
	if !reflect.DeepEqual(e.Vars, before) || updated.Lookup("MARKER") != "new" || len(updated.Vars) != 2 {
		t.Fatal(e, updated)
	}
	if _, err := e.With("PATH", "").LookPath(name); err == nil {
		t.Fatal("used global PATH")
	}
	if _, err := (Environment{Dir: "relative", Vars: e.Vars}).LookPath(name); err == nil {
		t.Fatal("implicit cwd")
	}
	if runtime.GOOS == "windows" {
		if e.With("Path", bin).Lookup("PATH") != bin {
			t.Fatal("Windows environment case")
		}
	} else {
		if err := os.Chmod(path, 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := e.LookPath(name); err == nil {
			t.Fatal("non executable accepted")
		}
	}
}
func TestCommandUsesExplicitDirectoryAndVariables(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	env := Environment{Dir: t.TempDir(), Vars: []string{"GO_PM_PROCESS_CHILD=1", "ONLY_CHILD=present"}, Out: &out, Err: &out}
	cmd, err := env.Command(context.Background(), exe, "-test.run=^TestProcessEnvironmentChild$")
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Run(); err != nil {
		t.Fatal(err, out.String())
	}
	var got struct{ Dir, Value, Path string }
	if err = json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err, out.String())
	}
	want, err := filepath.EvalSymlinks(env.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Dir != want || got.Value != "present" || got.Path != "" {
		t.Fatal(got, want)
	}
}
func TestProcessEnvironmentChild(t *testing.T) {
	if os.Getenv("GO_PM_PROCESS_CHILD") != "1" {
		return
	}
	dir, _ := os.Getwd()
	json.NewEncoder(os.Stdout).Encode(struct{ Dir, Value, Path string }{dir, os.Getenv("ONLY_CHILD"), os.Getenv("PATH")})
	os.Exit(0)
}
