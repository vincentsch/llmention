package config

import "strings"

// JoinAnd writes a list for a sentence: "a", "a and b" or "a, b and c".
func JoinAnd(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}
