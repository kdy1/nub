package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestUnimplementedIsFailure(t *testing.T) {
	for _, name := range []string{"install", "i", "a", "publish", "pm"} {
		var out, stderr bytes.Buffer
		code := Run(context.Background(), []string{name}, Environment{Dir: t.TempDir(), Out: &out, Err: &stderr})
		if code == 0 || !strings.Contains(stderr.String(), "ERR_NUB_GO_NOT_PORTED") {
			t.Fatalf("%s: %d %s", name, code, &stderr)
		}
	}
}

func TestNoRuntimeOrImplicitScriptCommands(t *testing.T) {
	for _, name := range []string{"run", "test", "start", "upgrade", "node", "file.ts"} {
		var out, stderr bytes.Buffer
		if Run(context.Background(), []string{name}, Environment{Out: &out, Err: &stderr}) == 0 || !strings.Contains(stderr.String(), "Unknown command") {
			t.Fatal(name)
		}
	}
}

func TestPreservedRefusals(t *testing.T) {
	for _, name := range []string{"clean", "purge", "recursive", "multi", "m", "deploy", "sbom"} {
		var out, stderr bytes.Buffer
		if Run(context.Background(), []string{name}, Environment{Out: &out, Err: &stderr}) == 0 || !strings.Contains(stderr.String(), "supported") {
			t.Fatal(name)
		}
	}
}
