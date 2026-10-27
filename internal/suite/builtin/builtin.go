// Package builtin embeds the suites that ship with llmention.
package builtin

import (
	"embed"
	"fmt"
	"maps"
	"slices"
)

//go:embed *.yaml
var files embed.FS

var names = map[string]string{
	"recognition.basic": "recognition.basic.yaml",
}

// IDs lists the built-in suite ids in order.
func IDs() []string { return slices.Sorted(maps.Keys(names)) }

// Exists reports whether id names a built-in suite.
func Exists(id string) bool {
	_, ok := names[id]
	return ok
}

// Read returns the YAML of a built-in suite.
func Read(id string) ([]byte, error) {
	name, ok := names[id]
	if !ok {
		return nil, fmt.Errorf("unknown built-in suite %q", id)
	}
	return files.ReadFile(name)
}
