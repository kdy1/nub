package surface

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestRegistryMatchesRust(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "crates", "nub-cli", "src", "pm_engine", "mod.rs"))
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?s)VerbSpec\s*\{\s*canonical:\s*"([^"]+)",\s*aliases:\s*&\[([^\]]*)\],\s*family:\s*Family::(\w+),\s*aube_args:\s*"([^"]+)"`)
	matches := re.FindAllSubmatch(data, -1)
	if len(matches) < 50 {
		t.Fatal("Rust command extraction lost entries", len(matches))
	}
	if len(Commands()) != len(matches)+3 {
		t.Fatalf("Go command count %d != Rust registry %d + install/ci/pm", len(Commands()), len(matches))
	}
	seen := map[string]bool{}
	for _, c := range Commands() {
		for _, name := range append([]string{c.Name}, c.Aliases...) {
			if seen[name] {
				t.Fatal("duplicate command", name)
			}
			seen[name] = true
		}
	}
	quoted := regexp.MustCompile(`"([^"]+)"`)
	for _, match := range matches {
		c, ok := Lookup(string(match[1]))
		if !ok {
			t.Fatal("missing command", string(match[1]))
		}
		if c.Family != string(match[3]) || c.RustArgs != string(match[4]) {
			t.Fatal("source mapping drift", c.Name)
		}
		aliases := quoted.FindAllSubmatch(match[2], -1)
		if len(c.Aliases) != len(aliases) {
			t.Fatal("alias count drift", c.Name)
		}
		for i, alias := range aliases {
			if c.Aliases[i] != string(alias[1]) {
				t.Fatal("alias drift", c.Name)
			}
		}
	}
}
