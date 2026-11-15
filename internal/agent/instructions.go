package agent

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxInstructionBytes = 8 << 10
	instructionsCut     = "[instructions cut at 8 KiB by ws]"
)

// LoadInstructions reads CLAUDE.md then AGENTS.md at repoDir and, when
// different, workspaceDir; then, when userFile != "", that file. Joined
// with "\n\n", cut at 8 KiB with "[instructions cut at 8 KiB by ws]".
// @path imports are left as text.
func LoadInstructions(repoDir, workspaceDir, userFile string) (string, error) {
	dirs := []string{repoDir}
	if filepath.Clean(workspaceDir) != filepath.Clean(repoDir) {
		dirs = append(dirs, workspaceDir)
	}
	var parts []string
	for _, dir := range dirs {
		for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
			text, err := readOptional(filepath.Join(dir, name))
			if err != nil {
				return "", err
			}
			parts = appendNonEmpty(parts, text)
		}
	}
	if userFile != "" {
		text, err := readOptional(userFile)
		if err != nil {
			return "", err
		}
		parts = appendNonEmpty(parts, text)
	}
	return cutInstructions(strings.Join(parts, "\n\n")), nil
}

func appendNonEmpty(parts []string, text string) []string {
	if text == "" {
		return parts
	}
	return append(parts, text)
}

// readOptional returns the file's text, or "" when it doesn't exist.
func readOptional(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading instructions: %w", err)
	}
	return string(raw), nil
}

func cutInstructions(s string) string {
	if len(s) <= maxInstructionBytes {
		return s
	}
	return strings.ToValidUTF8(s[:maxInstructionBytes], "") + "\n" + instructionsCut
}
