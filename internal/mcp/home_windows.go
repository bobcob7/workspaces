package mcp

// userHome is $HOME when set (Git Bash sets it), else %USERPROFILE%.
func userHome(environ []string) string {
	if home := lookup(environ, "HOME"); home != "" {
		return home
	}
	return lookup(environ, "USERPROFILE")
}
