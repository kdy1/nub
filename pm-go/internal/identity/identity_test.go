package identity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
)

func value(t *testing.T, body string) *jsonvalue.Value {
	t.Helper()
	v, err := jsonvalue.Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestDeclarationChannels(t *testing.T) {
	root := value(t, `{"packageManager":"pnpm@11.3.0+sha512.abc","devEngines":{"packageManager":{"name":"npm"}}}`)
	if got := Declared(root); got.Name != "pnpm" || got.Version != "11.3.0" {
		t.Fatal(got)
	}
	root = value(t, `{"devEngines":{"packageManager":[{"name":"npm","version":"10"},{"name":"pnpm","version":"^11"}]}}`)
	if got := Declared(root); got.Name != "pnpm" {
		t.Fatal(got)
	}
	if got := UnanimousDeclaration(root); got.Name != "" {
		t.Fatal("alternative tools are not a lockfile pin", got)
	}
	root = value(t, `{"devEngines":{"packageManager":[{"name":"npm","version":"10"},{"name":"npm","version":"11"}]}}`)
	if got := UnanimousDeclaration(root); got.Name != "npm" {
		t.Fatal(got)
	}
}

func TestLockfileSelection(t *testing.T) {
	for _, tc := range []struct {
		name, manifest string
		files          []string
		want           Kind
		file, errCode  string
	}{
		{"fresh", `{}`, nil, Nub, "nub.lock", ""},
		{"declared-fresh", `{"packageManager":"pnpm@10.0.0"}`, nil, Pnpm, "pnpm-lock.yaml", ""},
		{"declared-ignores-stray", `{"packageManager":"npm@11.0.0"}`, []string{"package-lock.json", "pnpm-lock.yaml"}, Npm, "package-lock.json", ""},
		{"shrinkwrap", `{}`, []string{"package-lock.json", "npm-shrinkwrap.json"}, Shrinkwrap, "npm-shrinkwrap.json", ""},
		{"ambiguous", `{}`, []string{"nub.lock", "pnpm-lock.yaml"}, "", "", "ERR_NUB_LOCKFILE_AMBIGUOUS"},
		{"mismatch", `{"packageManager":"pnpm@10"}`, []string{"package-lock.json"}, "", "", "ERR_NUB_LOCKFILE_DECLARATION_MISMATCH"},
		{"declared-nub", `{"packageManager":"nub@0.9.2"}`, []string{"nub.lock", "package-lock.json"}, Nub, "nub.lock", ""},
		{"nub-foreign", `{"packageManager":"nub@0.9.2"}`, []string{"bun.lock", "pnpm-lock.yaml"}, Pnpm, "pnpm-lock.yaml", ""},
		{"legacy", `{}`, []string{"lock.yaml"}, Nub, "lock.yaml", ""},
		{"canonical-over-legacy", `{}`, []string{"nub.lock", "lock.yaml"}, Nub, "nub.lock", ""},
		{"berry", `{"packageManager":"yarn@4"}`, []string{"yarn.lock"}, YarnBerry, "yarn.lock", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, name := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("__metadata:\n  version: 8\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			lock, err := Detect(dir, value(t, tc.manifest))
			if tc.errCode != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errCode) {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if lock.Kind != tc.want || filepath.Base(lock.Path) != tc.file {
				t.Fatal(lock)
			}
		})
	}
}

func TestOverrideIdentityAndVersionMatrix(t *testing.T) {
	root := value(t, `{"resolutions":{"a":"1","onlyResolution":"1"},"pnpm":{"overrides":{"a":"2"}},"overrides":{"a":"3","onlyOverride":"3"}}`)
	for _, tc := range []struct {
		role, version, want string
		warnings            int
	}{
		{"nub", "", "3", 0}, {"bun", "1", "3", 0}, {"pnpm", "10", "2", 1}, {"yarn", "4", "1", 1}, {"npm", "11", "3", 1}, {"npm", "8.2", "", 2}, {"npm", "8.3", "3", 1},
	} {
		out, warnings := Overrides(tc.role, ParseVersion(tc.version), root)
		if out["a"] != tc.want || len(warnings) != tc.warnings {
			t.Fatalf("%v: %v %v", tc, out, warnings)
		}
	}
	duplicate := value(t, `{"overrides":{"a":"1"},"resolutions":{"a":"1"}}`)
	if _, warnings := Overrides("npm", ParseVersion("11"), duplicate); len(warnings) != 0 {
		t.Fatal(warnings)
	}
	if PnpmYAMLSettings("nub", ParseVersion("11")) || PnpmYAMLSettings("pnpm", ParseVersion("latest")) || !PnpmYAMLSettings("pnpm", ParseVersion("^11.1.0")) {
		t.Fatal("pnpm settings model drift")
	}
}

func TestBranchCandidateOrder(t *testing.T) {
	want := []string{"pnpm-lock.feature!port.yaml", "pnpm-lock.yaml", "bun.lock", "yarn.lock", "npm-shrinkwrap.json", "package-lock.json", "nub.feature!port.lock", "nub.lock", "lock.yaml"}
	candidates := Candidates("project", true, "feature/port")
	if len(candidates) != len(want) {
		t.Fatal(candidates)
	}
	for i, candidate := range candidates {
		if filepath.Base(candidate.Path) != want[i] {
			t.Fatal(candidates)
		}
	}
	if got := Candidates("project", false, "feature/port"); len(got) != 6 {
		t.Fatal(got)
	}
}

func TestDetectionUsesBoundedBerryProbe(t *testing.T) {
	for _, body := range []string{"__metadata:\n", strings.Repeat("#", 4096) + "\n__metadata:\n", "  __metadata:\n"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "yarn.lock"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := Detect(dir, nil)
		want := Yarn
		if body == "__metadata:\n" {
			want = YarnBerry
		}
		if err != nil || got.Kind != want {
			t.Fatal(got, err)
		}
	}
}
