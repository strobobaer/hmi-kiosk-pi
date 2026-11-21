package client

import "strings"

// mergeEnv hängt add an base; gleiche Namen aus base entfallen.
func mergeEnv(base, add []string) []string {
	skip := map[string]bool{}
	for _, kv := range add {
		k, _, _ := strings.Cut(kv, "=")
		skip[k] = true
	}
	out := make([]string, 0, len(base)+len(add))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if !skip[k] {
			out = append(out, kv)
		}
	}
	return append(out, add...)
}
