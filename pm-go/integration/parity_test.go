package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nubjs/nub/pm-go/internal/parity"
)

func TestRustManifestParity(t *testing.T) {
	rust, goBin := os.Getenv("PM_RUST_BIN"), os.Getenv("PM_GO_BIN")
	if rust == "" || goBin == "" {
		t.Skip("requires explicit Rust and Go reference executables")
	}
	for _, tc := range []struct {
		name, body string
		args       []string
	}{
		{"get", `{"name":"example"}`, []string{"pkg", "get", "name"}},
		{"get-json", `{"name":"example","private":true}`, []string{"pkg", "get", "name", "private", "missing"}},
		{"set", "{\r\n\t\"name\": \"example\"\r\n}", []string{"pkg", "set", "--json", "private=true"}},
		{"late-json", `{"name":"unchanged"}`, []string{"pkg", "set", "name=changed", "--json"}},
		{"get-late-json", `{"name":"example"}`, []string{"pkg", "get", "name", "--json"}},
		{"set-nested", `{"name":"example","a":5}`, []string{"pkg", "set", "a[1].name=value"}},
		{"delete", `{"name":"example","a":[1,2,3]}`, []string{"pkg", "delete", "a[1]"}},
		{"fix", `{"name":10,"version":{},"dependencies":false,"scripts":[],"bin":null}`, []string{"pkg", "fix"}},
		{"script", `{"name":"example","scripts":false}`, []string{"set-script", "build:all", "node", "--test"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			var results []parity.Result
			for i, binary := range []string{rust, goBin} {
				base := filepath.Join(root, []string{"rust", "go"}[i])
				project := filepath.Join(base, "project")
				state := filepath.Join(base, "state")
				if err := os.MkdirAll(project, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(project, "package.json"), []byte(tc.body), 0644); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				result, err := parity.Run(ctx, binary, project, state, tc.args, nil)
				cancel()
				if err != nil {
					t.Fatal(err)
				}
				wantCode := 0
				if tc.name == "late-json" {
					wantCode = 1
				}
				if result.Code != wantCode {
					t.Fatalf("%s failed: %s", binary, result.Err)
				}
				results = append(results, result)
			}
			if err := parity.Compare(results[0], results[1]); err != nil {
				t.Fatal(err)
			}
		})
	}
}
