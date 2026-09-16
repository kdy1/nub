// Package installconfig resolves invocation-scoped install policy from the
// selected project identity. It does not read process-global environment state.
package installconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nubjs/nub/pm-go/internal/identity"
)

type NodeLinker string

const (
	Isolated NodeLinker = "isolated"
	Hoisted  NodeLinker = "hoisted"
)

// ResolveNodeLinker gives a CLI spelling precedence over the already-resolved
// settings value. Unknown settings fall back to isolated; unknown CLI values
// fail. PnP is explicitly unsupported by the baseline engine in both layers.
func ResolveNodeLinker(cli *string, configured string) (NodeLinker, error) {
	value := configured
	if cli != nil {
		value = *cli
	}
	switch asciiLower(strings.TrimSpace(value)) {
	case "pnp":
		return "", fmt.Errorf("node-linker=pnp is not supported by aube; use `isolated` (default) or `hoisted`")
	case "hoisted":
		return Hoisted, nil
	case "isolated":
		return Isolated, nil
	default:
		if cli != nil {
			return "", fmt.Errorf("unknown --node-linker value `%s`; expected `isolated` or `hoisted`", *cli)
		}
		return Isolated, nil
	}
}

type YarnLinkerInput struct {
	Kind            identity.Kind
	Root, CWD, Home string
	Env             map[string]string
	MutatingInstall bool
}

type PnPUnsupported struct{}

func (*PnPUnsupported) Code() string { return "ERR_NUB_PNP_UNSUPPORTED" }
func (*PnPUnsupported) Error() string {
	return "nub: this project is configured for Yarn Plug'n'Play (nodeLinker: pnp, or Yarn Berry's default) — nub installs a node_modules tree and doesn't support PnP yet, so the result would diverge from yarn's. Install with yarn, or set `nodeLinker: node-modules` in .yarnrc.yml. [ERR_NUB_PNP_UNSUPPORTED]"
}

// CheckYarnPnP is a plan-time guard, before any installation mutation. Foreign
// .yarnrc.yml files and YARN_NODE_LINKER are irrelevant to other identities.
func CheckYarnPnP(input YarnLinkerInput) error {
	if !input.MutatingInstall || input.Kind != identity.Yarn && input.Kind != identity.YarnBerry {
		return nil
	}
	value, err := EffectiveYarnNodeLinker(input)
	if err != nil {
		return err
	}
	if value == "pnp" {
		return &PnPUnsupported{}
	}
	return nil
}

func EffectiveYarnNodeLinker(input YarnLinkerInput) (string, error) {
	if !filepath.IsAbs(input.Root) || !filepath.IsAbs(input.CWD) || input.Home != "" && !filepath.IsAbs(input.Home) {
		return "", fmt.Errorf("Yarn linker configuration requires absolute invocation paths")
	}
	if value := strings.TrimSpace(input.Env["YARN_NODE_LINKER"]); value != "" {
		return asciiLower(value), nil
	}
	value := ""
	if input.Home != "" {
		value = YarnRCNodeLinker(input.Home)
	}
	current := input.Root
	if relative, err := filepath.Rel(input.Root, input.CWD); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative) {
		current = input.CWD
	}
	var ancestors []string
	for {
		ancestors = append(ancestors, current)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	for i := len(ancestors) - 1; i >= 0; i-- {
		if next := YarnRCNodeLinker(ancestors[i]); next != "" {
			value = next
		}
	}
	if value == "" && input.Kind == identity.YarnBerry {
		value = "pnp"
	}
	return value, nil
}

// YarnRCNodeLinker reproduces the adapter's top-level scalar scan, including
// first-value precedence. This deliberately is not a general YAML decoder.
func YarnRCNodeLinker(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, ".yarnrc.yml"))
	if err != nil || !utf8.Valid(data) {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		first, _ := utf8.DecodeRuneInString(line)
		if unicode.IsSpace(first) {
			continue
		}
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "nodeLinker:")
		if !ok {
			continue
		}
		value := strings.TrimSpace(rest)
		quoted := false
		if len(value) > 0 && (value[0] == '\'' || value[0] == '"') {
			if end := strings.IndexByte(value[1:], value[0]); end >= 0 {
				value, quoted = value[1:1+end], true
			}
		}
		if !quoted {
			value, _, _ = strings.Cut(value, "#")
			value = strings.TrimSpace(value)
		}
		if value != "" {
			return asciiLower(value)
		}
	}
	return ""
}

func asciiLower(value string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, value)
}
