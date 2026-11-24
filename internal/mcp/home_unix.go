//go:build unix

package mcp

// userHome is $HOME, the only home variable Claude Code reads on unix.
func userHome(environ []string) string {
	return lookup(environ, "HOME")
}
