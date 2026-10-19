// Package backup keeps re-creatable directories out of backups (ADR 0011).
package backup

import (
	"log/slog"
	"os/exec"
	"runtime"
	"syscall"
)

// starter starts cmd and returns a function that waits for it to finish.
type starter func(cmd *exec.Cmd) (wait func() error, err error)

// Exclude asks Time Machine to skip dir, without waiting: tmutil takes
// seconds, so it runs detached in its own process group, where a Ctrl-C to
// ws doesn't reach it. If it finishes while ws is still running, a failure
// is logged; otherwise its outcome isn't reported. It does nothing on other
// platforms.
func Exclude(log *slog.Logger, dir string) {
	startExclusion(log, runtime.GOOS, startCmd, dir)
}

// startExclusion starts tmutil for dir on darwin and returns a channel closed once
// the result is logged, or at once when nothing was started.
func startExclusion(log *slog.Logger, goos string, start starter, dir string) <-chan struct{} {
	done := make(chan struct{})
	if goos != "darwin" {
		close(done)
		return done
	}
	//nolint:noctx // tmutil must outlive ws, so no context may cancel it.
	cmd := exec.Command("tmutil", "addexclusion", dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	wait, err := start(cmd)
	if err != nil {
		log.Debug("couldn't start tmutil", slog.String("dir", dir), slog.Any("error", err))
		close(done)
		return done
	}
	go func() {
		defer close(done)
		if err := wait(); err != nil {
			log.Warn("couldn't exclude a directory from Time Machine backups", slog.String("dir", dir), slog.Any("error", err))
			return
		}
		log.Debug("excluded from Time Machine backups", slog.String("dir", dir))
	}()
	return done
}

// startCmd starts cmd with stdin, stdout, and stderr on the null device.
func startCmd(cmd *exec.Cmd) (func() error, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd.Wait, nil
}
