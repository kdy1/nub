package bun

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
)

func quote(s string) string { data, _ := jsonvalue.String(s).MarshalJSON(); return string(data) }
func inline(v *jsonvalue.Value) string {
	if v == nil {
		return "null"
	}
	switch v.Kind {
	case '[':
		parts := make([]string, len(v.Array))
		for i, item := range v.Array {
			parts[i] = inline(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case '{':
		if len(v.Object) == 0 {
			return "{}"
		}
		parts := make([]string, len(v.Object))
		for i, f := range v.Object {
			parts[i] = quote(f.Key) + ": " + inline(f.Value)
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	default:
		data, _ := v.MarshalJSON()
		return string(data)
	}
}
func formatLockfile(workspaces, packages []jsonvalue.Field, config uint32, extras []jsonvalue.Field) []byte {
	var out strings.Builder
	fmt.Fprintf(&out, "{\n  \"lockfileVersion\": 1,\n  \"configVersion\": %d,\n  \"workspaces\": {\n", config)
	for _, ws := range workspaces {
		fmt.Fprintf(&out, "    %s: {\n", quote(ws.Key))
		for _, f := range ws.Value.Object {
			if f.Value.Kind == '{' && len(f.Value.Object) > 0 && slices.Contains([]string{"dependencies", "devDependencies", "optionalDependencies", "peerDependencies"}, f.Key) {
				fmt.Fprintf(&out, "      %s: {\n", quote(f.Key))
				for _, dep := range f.Value.Object {
					fmt.Fprintf(&out, "        %s: %s,\n", quote(dep.Key), inline(dep.Value))
				}
				out.WriteString("      },\n")
			} else {
				fmt.Fprintf(&out, "      %s: %s,\n", quote(f.Key), inline(f.Value))
			}
		}
		out.WriteString("    },\n")
	}
	out.WriteString("  },\n")
	for _, f := range extras {
		if f.Value.Kind == '{' && len(f.Value.Object) > 0 {
			fmt.Fprintf(&out, "  %s: {\n", quote(f.Key))
			for _, item := range f.Value.Object {
				if f.Key == "catalogs" && item.Value.Kind == '{' && len(item.Value.Object) > 0 {
					fmt.Fprintf(&out, "    %s: {\n", quote(item.Key))
					for _, dep := range item.Value.Object {
						fmt.Fprintf(&out, "      %s: %s,\n", quote(dep.Key), inline(dep.Value))
					}
					out.WriteString("    },\n")
				} else {
					fmt.Fprintf(&out, "    %s: %s,\n", quote(item.Key), inline(item.Value))
				}
			}
			out.WriteString("  },\n")
		} else {
			fmt.Fprintf(&out, "  %s: %s,\n", quote(f.Key), inline(f.Value))
		}
	}
	if len(packages) == 0 {
		out.WriteString("  \"packages\": {}\n")
	} else {
		out.WriteString("  \"packages\": {\n")
		for i, p := range packages {
			if i > 0 {
				out.WriteByte('\n')
			}
			fmt.Fprintf(&out, "    %s: %s,\n", quote(p.Key), inline(p.Value))
		}
		out.WriteString("  }\n")
	}
	out.WriteString("}\n")
	return []byte(out.String())
}
