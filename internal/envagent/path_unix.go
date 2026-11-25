//go:build unix

package envagent

import "strings"

// hasPathPrefix reports whether s starts with prefix, byte for byte.
func hasPathPrefix(s, prefix string) bool {
	return strings.HasPrefix(s, prefix)
}
