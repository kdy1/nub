package cli

import (
	"fmt"
	"strconv"
	"strings"
)

type option struct {
	name    string
	aliases []string
	value   bool
}
type arguments struct {
	values     map[string][]string
	positional []string
}

var commonOptions = []option{
	{"dir", []string{"C"}, true},
	{"help", []string{"h"}, false},
}

func (a arguments) value(key string) string {
	v := a.values[key]
	if len(v) == 0 {
		return ""
	}
	return v[len(v)-1]
}
func (a arguments) flag(key string) bool { return a.value(key) == "true" }

func parseArgs(args []string, defs []option, stopAfter int) (arguments, error) {
	out := arguments{values: map[string][]string{}}
	lookup := map[string]option{}
	for _, def := range append(append([]option{}, commonOptions...), defs...) {
		lookup[def.name] = def
		for _, alias := range def.aliases {
			lookup[alias] = def
		}
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			out.positional = append(out.positional, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			out.positional = append(out.positional, arg)
			if stopAfter > 0 && len(out.positional) >= stopAfter {
				out.positional = append(out.positional, args[i+1:]...)
				break
			}
			continue
		}
		long := strings.HasPrefix(arg, "--")
		body := strings.TrimPrefix(arg, "-")
		if long {
			body = strings.TrimPrefix(body, "-")
		}
		for len(body) > 0 {
			key, val, explicit := strings.Cut(body, "=")
			if !long {
				key = body[:1]
				val = ""
				explicit = false
			}
			def, ok := lookup[key]
			if !ok {
				return out, fmt.Errorf("unknown option: %s", arg)
			}
			if !long && len(body) > 1 && def.value {
				val = strings.TrimPrefix(body[1:], "=")
				explicit = true
			}
			if def.value {
				if !explicit {
					i++
					if i >= len(args) || args[i] == "--" {
						return out, fmt.Errorf("option --%s requires a value", def.name)
					}
					val = args[i]
				}
			} else {
				if !explicit {
					val = "true"
				}
				if _, err := strconv.ParseBool(val); err != nil {
					return out, fmt.Errorf("invalid boolean for --%s: %s", def.name, val)
				}
				b, _ := strconv.ParseBool(val)
				val = strconv.FormatBool(b)
			}
			out.values[def.name] = append(out.values[def.name], val)
			if long || def.value {
				break
			}
			body = body[1:]
		}
	}
	return out, nil
}

func commandArgs(args []string) (string, []string, error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			return arg, append(append([]string{}, args[:i]...), args[i+1:]...), nil
		}
		if arg == "--dir" || arg == "-C" {
			i++
			if i >= len(args) {
				return "", nil, fmt.Errorf("option --dir requires a value")
			}
		}
	}
	return "", nil, fmt.Errorf("a package manager command is required")
}
