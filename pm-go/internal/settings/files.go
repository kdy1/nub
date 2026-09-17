package settings

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// ParseTOMLEntries retains the source order of top-level values. Nested tables
// and inline tables are not scalar settings; array values flatten recursively,
// as in the reference's managed-config reader.
func ParseTOMLEntries(data []byte) ([]Entry, error) {
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("invalid UTF-8 in TOML document")
	}
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	// The streaming AST gives source order and datetime precision, while the
	// decoder additionally validates duplicate definitions and scalar bounds.
	var validated map[string]any
	if err := toml.Unmarshal(data, &validated); err != nil {
		return nil, err
	}
	out := []Entry{}
	var parser unstable.Parser
	parser.Reset(data)
	inTable := false
	for parser.NextExpression() {
		n := parser.Expression()
		if n.Kind == unstable.Table || n.Kind == unstable.ArrayTable {
			inTable = true
		}
		if inTable || n.Kind != unstable.KeyValue {
			continue
		}
		keys := n.Key()
		if !keys.Next() {
			continue
		}
		key := string(keys.Node().Data)
		if keys.Next() { // A dotted key creates a table, not a setting entry.
			continue
		}
		if raw, ok := tomlRaw(n.Value()); ok {
			out = append(out, Entry{key, raw})
		}
	}
	if err := parser.Error(); err != nil {
		return nil, err
	}
	return out, nil
}

func tomlRaw(n *unstable.Node) (string, bool) {
	raw := string(n.Data)
	switch n.Kind {
	case unstable.String, unstable.Bool:
		return raw, true
	case unstable.Integer:
		value := strings.ReplaceAll(raw, "_", "")
		base := 10
		if strings.HasPrefix(value, "0x") || strings.HasPrefix(value, "0o") || strings.HasPrefix(value, "0b") {
			base = 0
		}
		number, err := strconv.ParseInt(value, base, 64)
		return strconv.FormatInt(number, 10), err == nil
	case unstable.Float:
		if strings.TrimLeft(raw, "+-") == "nan" {
			return "NaN", true
		}
		number, err := strconv.ParseFloat(strings.ReplaceAll(raw, "_", ""), 64)
		if math.IsInf(number, 1) {
			return "inf", true
		}
		if math.IsInf(number, -1) {
			return "-inf", true
		}
		return strconv.FormatFloat(number, 'f', -1, 64), err == nil
	case unstable.LocalDate, unstable.LocalTime, unstable.LocalDateTime, unstable.DateTime:
		// TOML 1.1 retains omitted seconds and authored zero fractions. Rust
		// truncates fractions to nanoseconds, then trims redundant zeros.
		raw = strings.ToUpper(strings.ReplaceAll(raw, " ", "T"))
		if start := strings.IndexByte(raw, '.'); start >= 0 {
			end := start + 1
			for end < len(raw) && raw[end] >= '0' && raw[end] <= '9' {
				end++
			}
			fraction := strings.TrimRight(raw[start+1:min(end, start+10)], "0")
			if fraction == "" {
				fraction = "0"
			}
			raw = raw[:start+1] + fraction + raw[end:]
		}
		return raw, true
	case unstable.Array:
		values := []string{}
		children := n.Children()
		for children.Next() {
			if value, ok := tomlRaw(children.Node()); ok {
				values = append(values, value)
			}
		}
		return strings.Join(values, ","), true
	default:
		return "", false
	}
}

type ManagedFiles struct {
	Dir string
	Env map[string]string
	// Nil uses /etc/nub/managed.toml on the invocation's drive. An explicit
	// path permits an isolated host/test filesystem; an empty path omits it.
	SystemPath *string
	Warn       func(string)
}

// LoadManagedFiles appends the explicit file after the system policy. It never
// reads another tool's branded config and never replaces system restrictions.
func LoadManagedFiles(in ManagedFiles) ([]Entry, error) {
	if !filepath.IsAbs(in.Dir) {
		return nil, fmt.Errorf("managed settings require an absolute invocation directory")
	}
	path := filepath.Join(filepath.VolumeName(in.Dir)+string(filepath.Separator), "etc", "nub", "managed.toml")
	if in.SystemPath != nil {
		path = *in.SystemPath
	}
	out := []Entry{}
	warn := func(message string) {
		if in.Warn != nil {
			in.Warn(message)
		}
	}
	load := func(path string, explicit bool) {
		if path == "" {
			return
		}
		display := path
		if !filepath.IsAbs(path) {
			path = filepath.Join(in.Dir, path)
		}
		if explicit {
			if _, err := os.Stat(path); err != nil {
				if os.IsNotExist(err) {
					warn("managed config path from NUB_MANAGED_CONFIG_PATH does not exist: " + display)
				} else {
					warn(fmt.Sprintf("failed to check managed config path from NUB_MANAGED_CONFIG_PATH at %s: %v", display, err))
				}
				return
			}
		}
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return
		}
		var entries []Entry
		if err == nil {
			entries, err = ParseTOMLEntries(data)
		}
		if err != nil {
			warn(fmt.Sprintf("failed to load nub config at %s: %v", display, err))
			return
		}
		out = append(out, entries...)
	}
	load(path, false)
	load(in.Env["NUB_MANAGED_CONFIG_PATH"], true)
	return out, nil
}
