// Package shell runs a workspace's interactive shell in the foreground
// (ADR 0015).
package shell

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
)

// defaultShell runs when SHELL is unset or empty.
const defaultShell = "/bin/sh"

// Runner runs interactive shells with ws's environment and streams.
type Runner struct {
	log     *slog.Logger
	environ []string
	stdin   io.Reader
	stdout  io.Writer
	stderr  io.Writer
	notify  func(c chan<- os.Signal, sig ...os.Signal)
	stop    func(c chan<- os.Signal)
}

// New returns a Runner that starts shells with environ, normally
// os.Environ(), on the given streams.
func New(log *slog.Logger, environ []string, stdin io.Reader, stdout, stderr io.Writer) *Runner {
	return &Runner{
		log:     log,
		environ: environ,
		stdin:   stdin,
		stdout:  stdout,
		stderr:  stderr,
		notify:  signal.Notify,
		stop:    signal.Stop,
	}
}

// Session is one shell to run.
type Session struct {
	// Dir is the shell's working directory.
	Dir string
	// Env holds KEY=value pairs added to the environment, replacing any
	// of the same name.
	Env []string
	// Lock, if set, is passed to the shell as file descriptor 3, so the
	// shell and every command it starts hold it (ADR 0015).
	Lock *os.File
	// AfterExit, if set, runs once the shell has exited or failed to
	// start, while ws still catches the signals Run handles, so they can't
	// kill ws partway through it.
	AfterExit func()
}

// Run runs $SHELL -i, or /bin/sh -i, for sess and waits for it to exit.
// It returns the shell's exit status: its exit code, or 128 plus the
// signal's number when a signal killed it. err is set only when the shell
// couldn't be started.
//
// The shell outlives ctx: cancelling ctx doesn't stop it. Until it exits,
// ws catches and discards SIGINT and SIGQUIT, which the terminal sends the
// shell, and sends the shell SIGHUP when ws gets SIGHUP or SIGTERM, so ws
// stays to clean up after it. ws keeps catching all four until
// sess.AfterExit has returned.
func (r *Runner) Run(ctx context.Context, sess Session) (int, error) {
	program := r.program()
	cmd := exec.CommandContext(context.WithoutCancel(ctx), program, "-i")
	cmd.Dir = sess.Dir
	cmd.Env = withVars(r.environ, append([]string{"PWD=" + sess.Dir}, sess.Env...))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = r.stdin, r.stdout, r.stderr
	if sess.Lock != nil {
		cmd.ExtraFiles = []*os.File{sess.Lock}
	}
	signals := make(chan os.Signal, 4)
	r.notify(signals, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGHUP, syscall.SIGTERM)
	defer r.stop(signals)
	if sess.AfterExit != nil {
		defer sess.AfterExit()
	}
	r.log.DebugContext(ctx, "starting shell", slog.String("shell", program), slog.String("dir", sess.Dir))
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("starting shell %s: %w", program, err)
	}
	done := make(chan struct{})
	defer close(done)
	go r.forward(cmd.Process, signals, done)
	err := cmd.Wait()
	return exitStatus(cmd.ProcessState, err)
}

// program is $SHELL from the environment, or /bin/sh when it's unset or
// empty.
func (r *Runner) program() string {
	if sh := lookup(r.environ, "SHELL"); sh != "" {
		return sh
	}
	return defaultShell
}

// forward sends the shell the signal it should get for each one ws
// receives, until done.
func (r *Runner) forward(p *os.Process, signals <-chan os.Signal, done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case sig := <-signals:
			r.pass(p, sig)
		}
	}
}

// pass sends the shell the signal for sig, if there is one.
func (r *Runner) pass(p *os.Process, sig os.Signal) {
	out, ok := passedOn(sig)
	if !ok {
		r.log.Debug("ignoring signal while the shell runs", slog.String("signal", sig.String()))
		return
	}
	r.log.Debug("signalling the shell", slog.String("received", sig.String()), slog.String("sent", out.String()))
	if err := p.Signal(out); err != nil && !errors.Is(err, os.ErrProcessDone) {
		r.log.Warn("couldn't signal the shell", slog.String("signal", out.String()), slog.Any("error", err))
	}
}

// passedOn returns the signal the shell gets when ws gets sig while it
// runs. The terminal sends SIGINT and SIGQUIT to its whole foreground
// process group, so a shell that shares ws's group already has them, and
// one with its own group isn't meant to. SIGHUP and SIGTERM may have been
// sent to ws alone; both become SIGHUP, which ends an interactive shell
// as closing the terminal does, where SIGTERM is ignored (ADR 0023).
func passedOn(sig os.Signal) (os.Signal, bool) {
	if sig == syscall.SIGHUP || sig == syscall.SIGTERM {
		return syscall.SIGHUP, true
	}
	return nil, false
}

// exitStatus is the exit status of a shell that Wait returned err for.
// Without a process state, the shell's end is unknown.
func exitStatus(state *os.ProcessState, err error) (int, error) {
	if state == nil {
		return 0, fmt.Errorf("waiting for the shell: %w", err)
	}
	if ws, ok := state.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal()), nil
	}
	return state.ExitCode(), nil
}

// lookup returns the last value of key in environ.
func lookup(environ []string, key string) string {
	value := ""
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			value = v
		}
	}
	return value
}

// withVars is environ without the variables vars sets, followed by vars.
func withVars(environ, vars []string) []string {
	set := make(map[string]bool, len(vars))
	for _, kv := range vars {
		k, _, _ := strings.Cut(kv, "=")
		set[k] = true
	}
	out := make([]string, 0, len(environ)+len(vars))
	for _, kv := range environ {
		if k, _, _ := strings.Cut(kv, "="); !set[k] {
			out = append(out, kv)
		}
	}
	return append(out, vars...)
}
