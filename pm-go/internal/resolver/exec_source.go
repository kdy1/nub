package resolver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/processenv"
)

const yarnExecWrapper = `
const env = JSON.parse(process.env.__NUB_PM_GO_EXEC_ENV);
globalThis.execEnv = env;
for (const name of ['fs', 'path', 'child_process', 'os', 'crypto', 'url', 'util', 'stream', 'buffer']) {
  globalThis[name] = require(name);
}
(async () => {
  await import(url.pathToFileURL(process.argv[1]).href);
})().catch((err) => {
  console.error(err);
  process.exit(1);
});
`

// ResolveExecManifest runs a project-confined Yarn exec: generator using PATH
// Node. The generated tree is temporary metadata input; later materialization
// owns the package output, matching the reference resolver's two-stage flow.
func ResolveExecManifest(ctx context.Context, env processenv.Environment, tempRoot, name string, source lockfile.Source) (LocalManifest, error) {
	if source.Kind != lockfile.Exec {
		return LocalManifest{}, &RegistryFailure{name, "resolve_exec_manifest called on non-exec source"}
	}
	var out LocalManifest
	err := WithExecBuild(ctx, env, tempRoot, name, source, func(build string) error {
		content, err := os.ReadFile(filepath.Join(build, "package.json"))
		if err != nil {
			return fmt.Errorf("read generated package.json for %s: %s", source.Specifier(), err)
		}
		out, err = parseLocalManifest(content)
		return err
	})
	if err != nil {
		if ctx.Err() != nil {
			return LocalManifest{}, ctx.Err()
		}
		return LocalManifest{}, &RegistryFailure{name, err.Error()}
	}
	out.Name = name
	return out, nil
}

// WithExecBuild runs a confined generator and hands its private output tree to
// consume. The tree is removed after consume returns, including error paths.
// Resolution reads its manifest; installation imports all generated files.
func WithExecBuild(ctx context.Context, env processenv.Environment, tempRoot, name string, source lockfile.Source, consume func(string) error) error {
	if source.Kind != lockfile.Exec {
		return fmt.Errorf("exec generator requires an exec source")
	}
	script, err := ResolveExecScriptPath(source, env.Dir)
	if err != nil {
		return fmt.Errorf("exec dependency %s: %s", source.Specifier(), err)
	}
	if !filepath.IsAbs(tempRoot) {
		return fmt.Errorf("exec dependency requires an absolute temporary directory")
	}
	dir, err := os.MkdirTemp(tempRoot, "nub-pm-go-exec-resolve-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	build, temp := filepath.Join(dir, "build"), filepath.Join(dir, "temp")
	for _, path := range []string{build, temp} {
		if err = os.MkdirAll(path, 0755); err != nil {
			return err
		}
	}
	value, err := json.Marshal(map[string]string{"tempDir": temp, "buildDir": build, "locator": name + "@" + source.Specifier()})
	if err != nil {
		return err
	}
	child := env.With("__NUB_PM_GO_EXEC_ENV", string(value))
	cmd, err := child.Command(ctx, "node", "-e", yarnExecWrapper, script)
	if err != nil {
		return fmt.Errorf("execute %s with Node.js from PATH: %s", source.Specifier(), err)
	}
	if err = cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			status := exit.Error()
			if code := exit.ExitCode(); code >= 0 {
				if runtime.GOOS == "windows" {
					status = fmt.Sprintf("exit code: %d", code)
				} else {
					status = fmt.Sprintf("exit status: %d", code)
				}
			}
			return fmt.Errorf("exec dependency %s failed with status %s", source.Specifier(), status)
		}
		return fmt.Errorf("execute %s with Node.js from PATH: %s", source.Specifier(), err)
	}
	return consume(build)
}
