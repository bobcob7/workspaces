//go:build !windows

package cli

import (
	"io/fs"
	"strings"
)

// terminalEnvAllowed reports whether a terminal's program may see the
// variable key of the agent's environment. Nothing else is passed on: the
// agent may hold CLAUDE_CODE_OAUTH_TOKEN, forge tokens and other secrets.
func terminalEnvAllowed(key string) bool {
	switch key {
	case "PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "LANGUAGE", "TMPDIR",
		"XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME",
		"LC_ALL", "LC_CTYPE", "LC_MESSAGES", "LC_COLLATE", "LC_TIME", "LC_NUMERIC", "LC_MONETARY",
		"LC_PAPER", "LC_NAME", "LC_ADDRESS", "LC_TELEPHONE", "LC_MEASUREMENT", "LC_IDENTIFICATION":
		return true
	}
	return false
}

// isPathKey reports whether key is the environment variable that holds the search path.
func isPathKey(key string) bool { return key == "PATH" }

// executableNames are the file names program may have: on these systems, just program.
func executableNames(program string, _ []string) []string {
	return []string{program}
}

// isExecutable reports whether a file found by name can be run.
func isExecutable(info fs.FileInfo) bool {
	return info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

// homeOf is HOME in env, or "" when it is unset or empty.
func homeOf(env []string) string {
	for _, kv := range env {
		if home, ok := strings.CutPrefix(kv, "HOME="); ok {
			return home
		}
	}
	return ""
}
