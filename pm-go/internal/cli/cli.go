package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/surface"
)

var Version = "0.9.2-dev"

type Environment struct {
	Dir  string
	Vars []string
	In   io.Reader
	Out  io.Writer
	Err  io.Writer
}

func System() Environment {
	dir, _ := os.Getwd()
	return Environment{dir, os.Environ(), os.Stdin, os.Stdout, os.Stderr}
}

func Run(ctx context.Context, args []string, env Environment) int {
	if err := ctx.Err(); err != nil {
		fmt.Fprintln(env.Err, err)
		return 130
	}
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(env.Out, "Usage: nub-pm-go <command> [options]\n\nPackage manager commands:")
		for _, c := range surface.Commands() {
			fmt.Fprintf(env.Out, "  %s\n", c.Name)
		}
		return 0
	}
	if args[0] == "--version" || args[0] == "-v" {
		fmt.Fprintln(env.Out, Version)
		return 0
	}
	name, tail, err := commandArgs(args)
	if err != nil {
		fmt.Fprintln(env.Err, err)
		return 1
	}
	c, ok := surface.Lookup(name)
	if !ok {
		fmt.Fprintf(env.Err, "Unknown command: %s\n", name)
		return 1
	}
	if message, ok := refused(c.Name); ok {
		fmt.Fprintf(env.Err, "nub-pm-go %s: %s\n", name, message)
		return 1
	}
	if c.Name == "pkg" || c.Name == "set-script" {
		if err := runPkg(c.Name, tail, env); err != nil {
			fmt.Fprintln(env.Err, err)
			return 1
		}
		return 0
	}
	// A recorded command is not evidence of an implemented command.
	fmt.Fprintf(env.Err, "ERR_NUB_GO_NOT_PORTED: %s is not implemented in the Go executable\n", c.Name)
	return 1
}

func refused(name string) (string, bool) {
	switch name {
	case "clean", "purge":
		return "not supported — nub does not delete node_modules for you.\n  Remove it directly (`rm -rf node_modules`) and reinstall with\n  `nub install`; `nub ci` does the clean + frozen install in one step.", true
	case "recursive":
		return "not supported — nub has no recursive meta-verb.\n  Use the verb's own workspace flags instead: `nub -r <verb>` /\n  `nub <verb> -r` or `--filter <pattern>` (e.g. `nub run -r build`,\n  `nub update -r`).", true
	case "deploy":
		return "not yet supported — the engine's deploy (copy a workspace\n  package + its production deps into a self-contained directory) hasn't\n  been wired. For now: pnpm deploy", true
	case "sbom":
		return "not yet supported — the engine stamps its own identity into\n  the SBOM document body, which nub won't emit until the identity\n  derives from the embedder. For now: npm sbom", true
	}
	return "", false
}

func environ(vars []string) map[string]string {
	out := make(map[string]string, len(vars))
	for _, v := range vars {
		if k, value, ok := strings.Cut(v, "="); ok {
			out[k] = value
		}
	}
	return out
}
