//go:build windows

package cli

import (
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
)

// windowsTerminalEnv are the variables a Windows program needs to run at
// all (SystemRoot, PATHEXT, the profile folders and so on, ADR 0081's
// follow-up list). Windows spells their names in any case, so they match
// case-insensitively. Anything else is withheld, as on other systems.
var windowsTerminalEnv = []string{
	"PATH", "PATHEXT", "SYSTEMROOT", "SYSTEMDRIVE", "WINDIR", "COMSPEC",
	"USERPROFILE", "HOMEDRIVE", "HOMEPATH", "HOME", "APPDATA", "LOCALAPPDATA", "PROGRAMDATA",
	"PROGRAMFILES", "PROGRAMFILES(X86)", "PROGRAMW6432", "COMMONPROGRAMFILES",
	"TEMP", "TMP", "USERNAME", "USERDOMAIN", "COMPUTERNAME", "NUMBER_OF_PROCESSORS",
	"PROCESSOR_ARCHITECTURE", "OS", "LANG",
}

// defaultPathExt is the extensions Windows tries when PATHEXT is unset or empty.
var defaultPathExt = []string{".COM", ".EXE", ".BAT", ".CMD"}

// terminalEnvAllowed reports whether a terminal's program may see the
// variable key of the agent's environment. Windows programs fail without
// their system variables (SystemRoot, PATHEXT, the profile folders), so
// those pass, matched without regard to case; the agent's secrets do not.
func terminalEnvAllowed(key string) bool {
	return slices.ContainsFunc(windowsTerminalEnv, func(name string) bool {
		return strings.EqualFold(key, name)
	})
}

// isPathKey reports whether key is the search path variable, whatever its case:
// Windows keeps the environment as "Path".
func isPathKey(key string) bool { return strings.EqualFold(key, "PATH") }

// executableNames are the file names program may have. A program that already
// ends in one of the PATHEXT extensions keeps its name; any other gets each
// extension appended, in PATHEXT's order.
func executableNames(program string, env []string) []string {
	exts := pathExt(env)
	if slices.ContainsFunc(exts, func(ext string) bool { return strings.EqualFold(filepath.Ext(program), ext) }) {
		return []string{program}
	}
	names := make([]string, 0, len(exts))
	for _, ext := range exts {
		names = append(names, program+ext)
	}
	return names
}

// pathExt is the extensions in the last PATHEXT of env, empty entries
// dropped, or the defaults when there are none.
func pathExt(env []string) []string {
	var value string
	for _, kv := range env {
		if key, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(key, "PATHEXT") {
			value = v
		}
	}
	var exts []string
	for _, ext := range strings.Split(value, ";") {
		if ext != "" {
			exts = append(exts, ext)
		}
	}
	if len(exts) == 0 {
		return defaultPathExt
	}
	return exts
}

// isExecutable reports whether a file found by name can be run. Windows has
// no execute bits; the extension chosen by executableNames is the check.
func isExecutable(info fs.FileInfo) bool {
	return info.Mode().IsRegular()
}
