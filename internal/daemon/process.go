package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// OSStarter starts and signals runner processes with the operating system.
type OSStarter struct{}

// Start runs exe with args in a session of its own, as processStarter
// documents. The process is reaped in the background once it exits.
func (OSStarter) Start(exe string, args []string, dir, logPath string, env []string) (int, error) {
	log, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // G703: logPath is under the state directory, and the workspace id is one path element.
	if err != nil {
		return 0, fmt.Errorf("opening %s: %w", logPath, err)
	}
	defer func() { _ = log.Close() }()
	cmd := exec.Command(exe, args...) //nolint:noctx // the runner outlives the request that starts it, so no context ends it.
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("starting %s: %w", exe, err)
	}
	go func() { _ = cmd.Wait() }()
	return cmd.Process.Pid, nil
}

// Signal sends sig to the process group pgid. A runner is its own group
// leader, so its pid is its group id.
func (OSStarter) Signal(pgid int, sig syscall.Signal) error {
	if err := syscall.Kill(-pgid, sig); err != nil {
		return fmt.Errorf("signalling process group %d: %w", pgid, err)
	}
	return nil
}
