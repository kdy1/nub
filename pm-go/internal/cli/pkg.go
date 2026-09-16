package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
	"github.com/nubjs/nub/pm-go/internal/manifest"
)

func manifestPath(env Environment, args arguments) (string, error) {
	dir := env.Dir
	if requested := args.value("dir"); requested != "" {
		if filepath.IsAbs(requested) {
			dir = requested
		} else {
			dir = filepath.Join(dir, requested)
		}
		return filepath.Join(dir, "package.json"), nil
	}
	path, err := manifest.Find(dir)
	if os.IsNotExist(err) {
		return filepath.Join(dir, "package.json"), nil
	}
	return path, err
}

func runPkg(name string, args []string, env Environment) error {
	defs := []option{}
	stopAfter := 0
	if name == "pkg" {
		defs = append(defs, option{"json", nil, false})
	} else {
		stopAfter = 2
	}
	a, err := parseArgs(args, defs, stopAfter)
	if err != nil {
		return err
	}
	if a.flag("help") {
		if name == "pkg" {
			fmt.Fprintln(env.Out, "Usage: nub-pm-go pkg <get|set|delete|fix> [arguments] [--json] [-C DIR]")
		} else {
			fmt.Fprintln(env.Out, "Usage: nub-pm-go set-script <name> <command...> [-C DIR]")
		}
		return nil
	}
	path, err := manifestPath(env, a)
	if err != nil {
		return err
	}
	doc, err := manifest.Read(path)
	if err != nil {
		return err
	}
	if name == "set-script" {
		if len(a.positional) < 2 {
			return fmt.Errorf("`set-script` requires a script name and a command")
		}
		if err := doc.Root.Set([]jsonvalue.Segment{{Key: "scripts"}, {Key: a.positional[0]}}, jsonvalue.String(strings.Join(a.positional[1:], " "))); err != nil {
			return err
		}
		return doc.Save()
	}
	if len(a.positional) == 0 {
		return fmt.Errorf("pkg requires a subcommand (get, set, delete, or fix)")
	}
	keys := a.positional[1:]
	switch sub := a.positional[0]; sub {
	case "get":
		selected := doc.Root
		if len(keys) == 1 {
			p, err := jsonvalue.Path(keys[0])
			if err != nil {
				return err
			}
			selected = doc.Root.At(p)
			if selected == nil {
				fmt.Fprintln(env.Out)
				return nil
			}
			if selected.Kind == 's' && !a.flag("json") {
				fmt.Fprintln(env.Out, selected.Text())
				return nil
			}
		} else if len(keys) > 1 {
			selected = jsonvalue.Object()
			for _, key := range keys {
				p, err := jsonvalue.Path(key)
				if err != nil {
					return err
				}
				value := doc.Root.At(p)
				if value == nil {
					value = jsonvalue.Null()
				}
				selected.Put(key, value)
			}
		}
		data, err := selected.Pretty()
		if err != nil {
			return err
		}
		_, err = env.Out.Write(data)
		return err
	case "set":
		if len(keys) == 0 {
			return fmt.Errorf("`pkg set` requires at least one key=value pair")
		}
		for _, arg := range keys {
			key, raw, ok := strings.Cut(arg, "=")
			if !ok {
				return fmt.Errorf("invalid argument %q: expected key=value format", arg)
			}
			value := jsonvalue.String(raw)
			if a.flag("json") {
				value, err = jsonvalue.Parse([]byte(raw))
				if err != nil {
					return fmt.Errorf("failed to parse value as JSON: %q", raw)
				}
			}
			p, err := jsonvalue.Path(key)
			if err != nil {
				return err
			}
			if err := doc.Root.Set(p, value); err != nil {
				return err
			}
		}
	case "delete":
		if len(keys) == 0 {
			return fmt.Errorf("`pkg delete` requires at least one key")
		}
		for _, key := range keys {
			p, err := jsonvalue.Path(key)
			if err != nil {
				return err
			}
			if err := doc.Root.Delete(p); err != nil {
				return err
			}
		}
	case "fix":
		for _, key := range []string{"name", "version"} {
			if value := doc.Root.Get(key); value != nil && value.Kind != 's' {
				doc.Root.Remove(key)
			}
		}
		for _, key := range []string{"dependencies", "devDependencies", "optionalDependencies", "peerDependencies", "scripts"} {
			if value := doc.Root.Get(key); value != nil && value.Kind != '{' {
				doc.Root.Remove(key)
			}
		}
		if value := doc.Root.Get("bin"); value != nil && value.Kind != 's' && value.Kind != '{' {
			doc.Root.Remove("bin")
		}
	default:
		return fmt.Errorf("unknown `pkg` subcommand %q (expected get, set, delete, or fix)", sub)
	}
	return doc.Save()
}
