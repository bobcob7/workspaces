//go:build windows

package gate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
)

func scriptCommand(ctx context.Context, script string) (*exec.Cmd, error) {
	sh, err := findSh(exec.LookPath, os.Stat)
	if err != nil {
		return nil, err
	}
	return exec.CommandContext(ctx, sh, "-c", script), nil
}

// findSh returns the sh that runs loop scripts: sh on PATH, else the one
// beside git (Git for Windows), else ErrNoShell.
func findSh(lookPath func(string) (string, error), stat func(string) (os.FileInfo, error)) (string, error) {
	if sh, err := lookPath("sh"); err == nil {
		return sh, nil
	}
	git, err := lookPath("git")
	if err != nil {
		return "", ErrNoShell
	}
	for _, sh := range []string{
		filepath.Join(filepath.Dir(git), "..", "bin", "sh.exe"),
		filepath.Join(filepath.Dir(git), "..", "usr", "bin", "sh.exe"),
	} {
		if _, err := stat(sh); err == nil {
			return sh, nil
		}
	}
	return "", ErrNoShell
}
