//go:build windows

package shell

import (
	"os"
	"os/exec"
)

// program is %COMSPEC% from the environment, or cmd.exe when it's unset
// or empty. SHELL is ignored: Git Bash sets it to an MSYS path.
func (r *Runner) program() (string, []string) {
	if sh := lookup(r.environ, "COMSPEC"); sh != "" {
		return sh, nil
	}
	return "cmd.exe", nil
}

// inheritLock does nothing: a LockFileEx lock isn't shared through an
// inherited handle (ADR 0015, constraints.md).
func inheritLock(_ *exec.Cmd, _ *os.File) {}
