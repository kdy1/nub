package pnpm

import "strings"

func yamlLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines
}
func indentOf(s string) int { return len(s) - len(strings.TrimLeft(s, " \t")) }

// Reformat applies the reference's post-serialization layout rules. Scalar
// quoting belongs to the YAML emitter; this pass preserves its scalar bytes.
func reformat(s string) string {
	lines := foldKeys(yamlLines(s))
	compact := []string{}
	for i := 0; i < len(lines); {
		line := lines[i]
		indent := indentOf(line)
		stripped := line[indent:]
		key, isKey := strings.CutSuffix(stripped, ":")
		if (key == "resolution" || key == "engines") && isKey && i+1 < len(lines) {
			entries := []string{}
			scalar := true
			j := i + 1
			for ; j < len(lines); j++ {
				n := lines[j]
				ni := indentOf(n)
				ns := n[ni:]
				if ns == "" || ni < indent+2 {
					break
				}
				if ni > indent+2 {
					scalar = false
					break
				}
				k, v, ok := strings.Cut(ns, ": ")
				if !ok {
					scalar = false
					break
				}
				entries = append(entries, k+": "+v)
			}
			block := false
			for _, e := range entries {
				block = block || e == "type: binary" || e == "type: variations"
			}
			if scalar && !block && len(entries) > 0 {
				compact = append(compact, strings.Repeat(" ", indent)+key+": {"+strings.Join(entries, ", ")+"}")
				i = j
				continue
			}
		}
		if isKey {
			if items, next, ok := gatherSeq(lines, i, indent); ok {
				if key == "cpu" || key == "os" || key == "libc" {
					compact = append(compact, strings.Repeat(" ", indent)+key+": ["+strings.Join(items, ", ")+"]")
				} else {
					compact = append(compact, line)
					for _, item := range items {
						compact = append(compact, strings.Repeat(" ", indent+2)+"- "+item)
					}
				}
				i = next
				continue
			}
		}
		compact = append(compact, line)
		i++
	}
	var out strings.Builder
	inEntries := false
	for i, line := range compact {
		indent := indentOf(line)
		stripped := line[indent:]
		top := indent == 0 && stripped != ""
		entry := inEntries && indent == 2 && !strings.HasPrefix(stripped, "-") && strings.Contains(stripped, ":")
		if top && i > 0 || entry {
			out.WriteByte('\n')
		}
		out.WriteString(line)
		out.WriteByte('\n')
		if top {
			inEntries = stripped == "importers:" || stripped == "packages:" || stripped == "snapshots:"
		}
	}
	return out.String()
}
func foldKeys(lines []string) []string {
	out := []string{}
	for i := 0; i < len(lines); {
		line := lines[i]
		indent := indentOf(line)
		stripped := line[indent:]
		if key, ok := strings.CutPrefix(stripped, "? "); ok {
			j := i + 1
			for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
				j++
			}
			if j < len(lines) {
				v := lines[j]
				vi := indentOf(v)
				rest, ok := strings.CutPrefix(v[vi:], ": ")
				if v[vi:] == ":" {
					rest, ok = "", true
				}
				if vi == indent && ok {
					pad := strings.Repeat(" ", indent)
					if rest == "" || strings.HasSuffix(rest, ":") {
						out = append(out, pad+key+":")
						if rest != "" {
							out = append(out, pad+"  "+rest)
						}
					} else {
						out = append(out, pad+key+": "+rest)
					}
					i = j + 1
					continue
				}
			}
		}
		out = append(out, line)
		i++
	}
	return out
}
func gatherSeq(lines []string, i, indent int) ([]string, int, bool) {
	var items []string
	j := i + 1
	for ; j < len(lines); j++ {
		n := lines[j]
		ni := indentOf(n)
		if ni != indent || !strings.HasPrefix(n[ni:], "- ") {
			break
		}
		items = append(items, n[ni+2:])
	}
	if len(items) == 0 {
		return nil, j, false
	}
	if j < len(lines) {
		s := lines[j]
		if strings.TrimSpace(s) != "" && indentOf(s) > indent {
			return nil, j, false
		}
	}
	return items, j, true
}
