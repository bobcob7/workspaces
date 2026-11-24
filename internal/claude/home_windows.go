package claude

import "os"

// userHome is $HOME when set (Git Bash sets it), else %USERPROFILE%.
func userHome(environ []string) string {
	if home := lookup(environ, "HOME"); home != "" {
		return home
	}
	return lookup(environ, "USERPROFILE")
}

// rootDir is the root of the system drive, Windows' nearest thing to "/".
func rootDir() string {
	return os.Getenv("SystemDrive") + "\\"
}
