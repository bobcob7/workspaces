//go:build unix

package shell

import (
	"os"
	"os/exec"
)

// defaultShell runs when SHELL is unset or empty.
const defaultShell = "/bin/sh"

// program is $SHELL from the environment, or /bin/sh when it's unset or
// empty, and its arguments.
func (r *Runner) program() (string, []string) {
	if sh := lookup(r.environ, "SHELL"); sh != "" {
		return sh, []string{"-i"}
	}
	return defaultShell, []string{"-i"}
}

// inheritLock passes lock to cmd as file descriptor 3.
func inheritLock(cmd *exec.Cmd, lock *os.File) {
	cmd.ExtraFiles = []*os.File{lock}
}
