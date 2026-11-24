//go:build unix

package claude

// userHome is $HOME, the only home variable Claude Code reads on unix.
func userHome(environ []string) string {
	return lookup(environ, "HOME")
}

// rootDir is where a detached service runs when there is no home.
func rootDir() string {
	return "/"
}
