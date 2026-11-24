//go:build unix

package procgroup

import "syscall"

// OwnGroup starts a process as the leader of a new process group.
func OwnGroup() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }

// Detached starts a process in a new session, with no controlling terminal.
func Detached() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }

// KillTree kills the process group led by pid.
func KillTree(pid int) error { return syscall.Kill(-pid, syscall.SIGKILL) }

// Signal sends sig to the process group pgid.
func Signal(pgid int, sig syscall.Signal) error { return syscall.Kill(-pgid, sig) }
