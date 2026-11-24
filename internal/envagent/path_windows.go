package envagent

import "strings"

// hasPathPrefix ignores case: NTFS names are case-insensitive, so c:\ and
// C:\ are the same root.
func hasPathPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}
