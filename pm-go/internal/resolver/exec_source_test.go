package resolver

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/processenv"
)

func TestExecGeneratorUsesPathNodeAndContext(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("PATH Node required for generators")
	}
	root, temp := t.TempDir(), t.TempDir()
	var stdout, stderr bytes.Buffer
	env := processenv.Environment{Dir: root, Vars: os.Environ(), Out: &stdout, Err: &stderr}.With("PATH", filepath.Dir(node)).With("GENERATOR_MARKER", "from-context")
	script := `
if (process.env.GENERATOR_MARKER !== 'from-context') throw new Error('wrong environment');
if (!execEnv.locator.startsWith('alias@exec:')) throw new Error('wrong locator');
if (!fs.statSync(execEnv.tempDir).isDirectory()) throw new Error('missing temp');
console.log('generator stdout');
console.error('generator stderr');
fs.writeFileSync(path.join(execEnv.buildDir, 'package.json'), JSON.stringify({name:'generated-name',version:'4.2.0',dependencies:{dep:'^1'},devDependencies:{ignored:'*'}}));
`
	if err = os.WriteFile(filepath.Join(root, "generate.mjs"), []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := ResolveExecManifest(context.Background(), env, temp, "alias", lockfile.Source{Kind: lockfile.Exec, Path: "generate.mjs"})
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	if !reflect.DeepEqual(result, LocalManifest{"alias", "4.2.0", map[string]string{"dep": "^1"}}) || !strings.Contains(stdout.String(), "generator stdout") || !strings.Contains(stderr.String(), "generator stderr") {
		t.Fatal(result, stdout.String(), stderr.String())
	}
	entries, err := os.ReadDir(temp)
	if err != nil || len(entries) != 0 {
		t.Fatal(entries, err)
	}
	if env.Lookup("__NUB_PM_GO_EXEC_ENV") != "" {
		t.Fatal("mutated parent environment")
	}
	_, err = ResolveExecManifest(context.Background(), env.With("PATH", ""), temp, "alias", lockfile.Source{Kind: lockfile.Exec, Path: "generate.mjs"})
	if err == nil || !strings.Contains(err.Error(), "Node.js from PATH") {
		t.Fatal(err)
	}
}
func TestExecGeneratorFailuresAndCleanup(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("PATH Node required for generators")
	}
	root, temp := t.TempDir(), t.TempDir()
	var output bytes.Buffer
	env := processenv.Environment{Dir: root, Vars: os.Environ(), Out: &output, Err: &output}
	for _, tc := range []struct{ script, want string }{
		{"process.exit(7)", "failed with status"},
		{"throw new Error('generator failed')", "failed with status"},
		{"void 0", "read generated package.json"},
		{`fs.writeFileSync(path.join(execEnv.buildDir,'package.json'),'{')`, "registry error for alias"},
	} {
		if err := os.WriteFile(filepath.Join(root, "generate.cjs"), []byte(tc.script), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := ResolveExecManifest(context.Background(), env, temp, "alias", lockfile.Source{Kind: lockfile.Exec, Path: "generate.cjs"})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatal(tc, err)
		}
		entries, e := os.ReadDir(temp)
		if e != nil || len(entries) != 0 {
			t.Fatal(entries, e)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "generate.cjs"), []byte(`setInterval(()=>{},1000)`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := ResolveExecManifest(ctx, env, temp, "alias", lockfile.Source{Kind: lockfile.Exec, Path: "generate.cjs"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	entries, e := os.ReadDir(temp)
	if e != nil || len(entries) != 0 {
		t.Fatal(entries, e)
	}
}
