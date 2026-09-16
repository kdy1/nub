package manifest

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/fsutil"
	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
)

type Document struct {
	Path     string
	Root     *jsonvalue.Value
	original []byte
	mode     os.FileMode
}

func Read(path string) (*Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	value, err := jsonvalue.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}
	if value.Kind != '{' {
		return nil, fmt.Errorf("%s must contain a JSON object", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	return &Document{path, value, data, info.Mode().Perm()}, nil
}

func (d *Document) Save() error {
	indent := "  "
	width := int(^uint(0) >> 1)
	rootIndent := ""
	seenRoot := false
	for _, line := range strings.Split(string(d.original), "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		n := len(line) - len(trimmed)
		if len(strings.TrimSpace(trimmed)) == 0 {
			continue
		}
		if !seenRoot {
			rootIndent = line[:n]
			seenRoot = true
			continue
		}
		if n > len(rootIndent) && strings.HasPrefix(line, rootIndent) && n-len(rootIndent) < width {
			width = n - len(rootIndent)
			indent = line[len(rootIndent):n]
		}
	}
	data, err := d.Root.Format(indent, bytes.Contains(d.original, []byte("\r\n")), bytes.HasSuffix(d.original, []byte("\n")))
	if err != nil {
		return err
	}
	if bytes.Equal(data, d.original) {
		return nil
	}
	if err := fsutil.Write(d.Path, data, d.mode); err != nil {
		return err
	}
	d.original = data
	return nil
}

func Find(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		path := filepath.Join(dir, "package.json")
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() {
			return path, nil
		}
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

func Strings(value *jsonvalue.Value) map[string]string {
	out := map[string]string{}
	if value != nil && value.Kind == '{' {
		for _, field := range value.Object {
			if field.Value.Kind == 's' {
				out[field.Key] = field.Value.Text()
			}
		}
	}
	return out
}
