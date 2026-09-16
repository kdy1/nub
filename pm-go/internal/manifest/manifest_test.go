package manifest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
)

func TestSavePreservesSurfaceStyle(t *testing.T) {
	for _, tc := range []struct{ name, before, after string }{
		{"tabs-crlf-no-newline", "{\r\n\t\"name\": \"a\",\r\n\t\"version\": \"1.0.0\"\r\n}", "{\r\n\t\"name\": \"b\",\r\n\t\"version\": \"1.0.0\"\r\n}"},
		{"spaces-lf", "{\n    \"name\": \"a\"\n}\n", "{\n    \"name\": \"b\"\n}\n"},
		{"compact-default", "{\"name\":\"a\"}", "{\n  \"name\": \"b\"\n}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "package.json")
			if err := os.WriteFile(path, []byte(tc.before), 0644); err != nil {
				t.Fatal(err)
			}
			doc, err := Read(path)
			if err != nil {
				t.Fatal(err)
			}
			doc.Root.Put("name", jsonvalue.String("b"))
			if err := doc.Save(); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tc.after {
				t.Fatalf("got %q want %q", data, tc.after)
			}
		})
	}
}
