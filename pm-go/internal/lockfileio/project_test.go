package lockfileio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nubjs/nub/pm-go/internal/identity"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/manifest"
	"github.com/nubjs/nub/pm-go/internal/testutil"
)

func TestProjectReadAndWriteSelection(t *testing.T) {
	for _, tc := range []struct {
		name, declaration string
		files             []string
		want              string
		writeFails        bool
	}{
		{"declared-npm", `"packageManager":"npm@11"`, []string{"bun.lock", "package-lock.json"}, "package-lock.json", false},
		{"declared-shrinkwrap", `"packageManager":"npm@11"`, []string{"bun.lock", "package-lock.json", "npm-shrinkwrap.json"}, "npm-shrinkwrap.json", false},
		{"declared-berry", `"packageManager":"yarn@4"`, []string{"bun.lock", "yarn.lock"}, "yarn.lock", false},
		{"declared-self", `"packageManager":"nub@0.9.2"`, []string{"pnpm-lock.yaml", "nub.lock", "lock.yaml"}, "nub.lock", false},
		{"declared-self-foreign", `"packageManager":"nub@0.9.2"`, []string{"bun.lock", "package-lock.json"}, "bun.lock", false},
		{"dev-engines", `"devEngines":{"packageManager":{"name":"npm"}}`, []string{"bun.lock", "package-lock.json"}, "package-lock.json", false},
		{"alternatives", `"devEngines":{"packageManager":[{"name":"npm"},{"name":"bun"}]}`, []string{"bun.lock", "package-lock.json"}, "bun.lock", true},
		{"contradiction-read-fallback", `"packageManager":"npm@11"`, []string{"nub.lock", "pnpm-lock.yaml"}, "pnpm-lock.yaml", true},
		{"ambiguous-read-fallback", "", []string{"nub.lock", "pnpm-lock.yaml"}, "pnpm-lock.yaml", true},
		{"legacy", "", []string{"lock.yaml"}, "lock.yaml", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			pj := `{"name":"fixture"`
			if tc.declaration != "" {
				pj += "," + tc.declaration
			}
			pj += "}"
			writeFile(t, filepath.Join(dir, "package.json"), []byte(pj))
			project, err := manifest.ParsePackage([]byte(pj))
			if err != nil {
				t.Fatal(err)
			}
			for _, file := range tc.files {
				writeFile(t, filepath.Join(dir, file), []byte(emptyDocument(file)))
			}
			result, err := ReadProject(dir, project, ProjectReadOptions{})
			if err != nil || filepath.Base(result.Path) != tc.want {
				t.Fatal(result, err)
			}
			compareProjectRead(t, dir, result, err)
			written, err := WriteProject(dir, result.Graph, project, "", WriteOptions{})
			if tc.writeFails {
				if err == nil {
					t.Fatal("ambiguous write succeeded")
				}
			} else if err != nil || written.Written {
				t.Fatal(written, err)
			}
			assertFile(t, result.Path, []byte(emptyDocument(tc.want)))
		})
	}
}

func TestProjectImportAndBinaryRefusal(t *testing.T) {
	project, _ := manifest.ParsePackage([]byte(`{"name":"fixture"}`))
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), []byte(`{"name":"fixture"}`))
	for _, file := range []string{"nub.lock", "lock.yaml"} {
		writeFile(t, filepath.Join(dir, file), []byte(emptyDocument(file)))
	}
	_, err := ReadProject(dir, project, ProjectReadOptions{ForImport: true})
	var missing *NotFoundError
	if !errors.As(err, &missing) {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "package-lock.json"), []byte(emptyDocument("package-lock.json")))
	result, err := ReadProject(dir, project, ProjectReadOptions{ForImport: true})
	if err != nil || result.Kind != identity.Npm {
		t.Fatal(result, err)
	}
	writeFile(t, filepath.Join(dir, "bun.lockb"), []byte("binary"))
	result, err = ReadProject(dir, project, ProjectReadOptions{})
	if err == nil || !strings.Contains(err.Error(), "bun.lockb (binary format) is not supported") {
		t.Fatal(err)
	}
	compareProjectRead(t, dir, result, err)
	writeFile(t, filepath.Join(dir, "bun.lock"), []byte(emptyDocument("bun.lock")))
	result, err = ReadProject(dir, project, ProjectReadOptions{})
	if err != nil || result.Kind != identity.Bun {
		t.Fatal(result, err)
	}
	compareProjectRead(t, dir, result, err)
}

func TestProjectReadDoesNotSkipMalformedSelectedFile(t *testing.T) {
	dir := t.TempDir()
	project, _ := manifest.ParsePackage([]byte(`{}`))
	writeFile(t, filepath.Join(dir, "pnpm-lock.yaml"), []byte("[broken"))
	writeFile(t, filepath.Join(dir, "package-lock.json"), []byte(emptyDocument("package-lock.json")))
	if _, err := ReadProject(dir, project, ProjectReadOptions{}); err == nil {
		t.Fatal("corrupt selected file skipped")
	}
}

func TestProjectBranchReadFallbackAndWriteTarget(t *testing.T) {
	for _, kind := range []identity.Kind{identity.Nub, identity.Pnpm} {
		t.Run(string(kind), func(t *testing.T) {
			dir := t.TempDir()
			project, _ := manifest.ParsePackage([]byte(`{}`))
			branch := "feature/port"
			base := filepath.Join(dir, kind.Filename())
			branchPath := filepath.Join(dir, kind.BranchFilename(branch))
			body := []byte(emptyDocument(kind.Filename()))
			writeFile(t, base, body)
			result, err := ReadProject(dir, project, ProjectReadOptions{Branch: branch})
			if err != nil || result.Path != base {
				t.Fatal(result, err)
			}
			written, err := WriteProject(dir, result.Graph, project, branch, WriteOptions{})
			if err != nil || !written.Written || written.Path != branchPath {
				t.Fatal(written, err)
			}
			assertFile(t, base, body)
			result, err = ReadProject(dir, project, ProjectReadOptions{Branch: branch})
			if err != nil || result.Path != branchPath {
				t.Fatal(result, err)
			}
			if result, err = ReadProject(dir, project, ProjectReadOptions{}); err != nil || result.Path != base {
				t.Fatal(result, err)
			}
		})
	}
}

func TestProjectFreshWriteUsesDeclaration(t *testing.T) {
	for _, pm := range []string{"nub", "npm", "pnpm", "bun", "yarn"} {
		t.Run(pm, func(t *testing.T) {
			dir := t.TempDir()
			project, _ := manifest.ParsePackage([]byte(`{"name":"fixture","packageManager":"` + pm + `@1"}`))
			g := lockfile.NewGraph()
			g.Importers["."] = nil
			result, err := WriteProject(dir, g, project, "", WriteOptions{})
			if err != nil || !result.Written || filepath.Base(result.Path) != identity.Kind(pm).Filename() {
				t.Fatal(result, err)
			}
		})
	}
}

func emptyDocument(filename string) string {
	switch filename {
	case "bun.lock":
		return `{ "lockfileVersion":1, "workspaces":{"":{"name":"fixture"}}, "packages":{} }`
	case "package-lock.json", "npm-shrinkwrap.json":
		return `{ "lockfileVersion":3, "packages":{"":{"name":"fixture"}} }`
	case "yarn.lock":
		return "# native\n__metadata:\n  version: 8\n"
	default:
		return "# retained\nlockfileVersion: '9.0'\nimporters:\n  .: {}\n"
	}
}

func compareProjectRead(t *testing.T, dir string, got ProjectReadResult, readErr error) {
	t.Helper()
	oracle := os.Getenv("PM_RUST_LOCKFILE_ORACLE")
	if oracle == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, oracle, "read-project", dir).CombinedOutput()
	if err != nil {
		t.Fatal(string(out), err)
	}
	var ref struct {
		OK    bool
		Error string
		Graph json.RawMessage
	}
	if err := json.Unmarshal(out, &ref); err != nil {
		t.Fatal(string(out), err)
	}
	if ref.OK != (readErr == nil) {
		t.Fatalf("Go: %v; Rust: %s", readErr, out)
	}
	if readErr != nil {
		if ref.Error != readErr.Error() {
			t.Fatalf("Go: %v; Rust: %s", readErr, out)
		}
		return
	}
	b, err := testutil.GraphJSON(got.Graph)
	if err != nil {
		t.Fatal(err)
	}
	var left, right any
	if err := json.Unmarshal(b, &left); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(ref.Graph, &right); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(left, right) {
		var formatted bytes.Buffer
		_ = json.Indent(&formatted, b, "", "  ")
		t.Fatalf("Go: %s\nRust: %s", formatted.String(), ref.Graph)
	}
}
