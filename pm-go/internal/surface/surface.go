// Package surface records the Nub PM command boundary independently of handlers.
package surface

import (
	_ "embed"
	"encoding/json"
)

const Baseline = "2a4573ef059798b75f789aa8c22da51e470e3138"

type Command struct {
	Name     string   `json:"canonical"`
	Aliases  []string `json:"aliases"`
	Family   string   `json:"family"`
	RustArgs string   `json:"rust_args"`
}

//go:embed commands.json
var data []byte

func Commands() []Command {
	var commands []Command
	if err := json.Unmarshal(data, &commands); err != nil {
		panic(err)
	}
	return commands
}

func Lookup(name string) (Command, bool) {
	for _, c := range Commands() {
		if c.Name == name {
			return c, true
		}
		for _, alias := range c.Aliases {
			if alias == name {
				return c, true
			}
		}
	}
	return Command{}, false
}
