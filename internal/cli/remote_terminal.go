package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/bobcob7/workspaces/internal/daemon"
	"github.com/bobcob7/workspaces/internal/envproto"
)

// resizeCoalesce is the least time between two resize frames.
const resizeCoalesce = 100 * time.Millisecond

// Terminal size used when the local terminal can't tell its own.
const (
	defaultCols = 80
	defaultRows = 24
)

// daemonTerminals adapts the daemon's client to terminalOpener.
type daemonTerminals struct {
	client *daemon.Client
}

func (d daemonTerminals) OpenTerminal(ctx context.Context, name string, args envproto.TerminalOpenArgs) (remoteTerminal, envproto.TerminalOpenResult, error) { //nolint:gocritic // hugeParam: the args value is the fixed API.
	conn, result, err := d.client.OpenTerminal(ctx, name, args)
	if err != nil {
		return nil, result, err
	}
	return conn, result, nil
}

// dialTerminalOpener is the daemon's client, started when none runs.
func (a *App) dialTerminalOpener(ctx context.Context) (terminalOpener, error) {
	client, err := a.daemonClient(ctx)
	if err != nil {
		return nil, err
	}
	return daemonTerminals{client: client}, nil
}

// stdinIsTerminal is whether stdin is a terminal.
func (a *App) stdinIsTerminal() bool {
	return isTerminal(a.stdin)
}

// pauseFor waits d, or until ctx ends.
func pauseFor(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}

// localRawMode puts stdin in raw mode and returns what restores it.
func (a *App) localRawMode() (func(), error) {
	f, ok := a.stdin.(*os.File)
	if !ok {
		return nil, usageErrorf("stdin isn't a terminal")
	}
	return makeRaw(int(f.Fd()))
}

// localTermSize is stdout's size.
func (a *App) localTermSize() (cols, rows int, err error) {
	f, ok := a.stdout.(*os.File)
	if !ok {
		return 0, 0, usageErrorf("stdout isn't a terminal")
	}
	return terminalSize(int(f.Fd()))
}

// currentSize is the local terminal's size, or 80x24 when it can't say.
func (a *App) currentSize() (cols, rows int) {
	cols, rows, err := a.termSize()
	if err != nil || cols < 1 || rows < 1 || cols > envproto.MaxTermSize || rows > envproto.MaxTermSize {
		return defaultCols, defaultRows
	}
	return cols, rows
}

// terminalRun is one remote terminal's local side.
type terminalRun struct {
	app    *App
	term   remoteTerminal
	cancel context.CancelCauseFunc
}

// runRemoteTerminal opens the terminal on env, puts stdin in raw mode
// (restored on every return, including a panic and a signal), sends the
// size and every SIGWINCH (coalesced to one per 100 ms), copies stdin to
// Input in frames of at most 4 KiB paced at InputPaceBytes per second,
// writes the output to stdout, and returns the exit. ctx's end closes the
// terminal. An exit other than exited or closed is an error: environment_offline
// for a disconnect.
func (a *App) runRemoteTerminal(ctx context.Context, env string, args envproto.TerminalOpenArgs) (exit envproto.TerminalExit, err error) { //nolint:gocritic // hugeParam: the args value is the fixed API.
	args.Cols, args.Rows = a.currentSize()
	opener, err := a.dialTerminals(ctx)
	if err != nil {
		return exit, err
	}
	term, _, err := opener.OpenTerminal(ctx, env, args)
	if err != nil {
		return exit, callError(err)
	}
	var closeOnce sync.Once
	closeTerm := func() { closeOnce.Do(func() { _ = term.Close() }) }
	defer closeTerm()
	sigs := make(chan os.Signal, 4)
	a.notify(sigs, terminalSignals()...)
	defer a.stopNotify(sigs)
	restore, err := a.rawMode()
	if err != nil {
		return exit, err
	}
	defer restore()
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	run := &terminalRun{app: a, term: term, cancel: cancel}
	ended := make(chan termResult, 1)
	go run.readOutput(ended)
	go run.copyInput(ctx)
	go run.watchSignals(ctx, sigs)
	select {
	case result := <-ended:
		if result.err != nil {
			return exit, result.err
		}
		return *result.exit, exitError(env, result.exit)
	case <-ctx.Done():
	}
	closeTerm()
	<-ended
	exit.Reason = "closed"
	if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) && !errors.Is(cause, context.DeadlineExceeded) {
		return exit, cause
	}
	return exit, nil
}

// termResult is how a terminal's output ended.
type termResult struct {
	exit *envproto.TerminalExit
	err  error
}

// recoverTo turns a panic of a terminal goroutine into an error for the run,
// so the terminal is restored by the run's deferred calls.
func recoverTo(report func(error)) {
	if p := recover(); p != nil {
		report(fmt.Errorf("the terminal failed: %v", p))
	}
}

// readOutput copies output to stdout until the exit, and reports it.
func (r *terminalRun) readOutput(done chan<- termResult) {
	defer recoverTo(func(err error) { done <- termResult{err: newError(codeInternal, err)} })
	for {
		data, exit, err := r.term.Next()
		switch {
		case exit != nil:
			done <- termResult{exit: exit}
			return
		case err != nil:
			done <- termResult{err: newError(codeInternal, fmt.Errorf("the connection to the daemon ended: %w", err))}
			return
		}
		if _, err := r.app.stdout.Write(data); err != nil {
			done <- termResult{err: newError(codeInternal, fmt.Errorf("writing the terminal's output: %w", err))}
			return
		}
	}
}

// copyInput sends stdin as input frames of at most MaxInputBytes, pausing
// after each so the rate stays at InputPaceBytes per second. The end of
// stdin closes the terminal.
func (r *terminalRun) copyInput(ctx context.Context) {
	defer recoverTo(func(err error) { r.cancel(newError(codeInternal, err)) })
	buf := make([]byte, envproto.MaxInputBytes)
	for ctx.Err() == nil {
		n, err := r.app.stdin.Read(buf)
		if n > 0 {
			if sendErr := r.term.Input(buf[:n]); sendErr != nil {
				r.cancel(newError(codeInternal, sendErr))
				return
			}
			r.app.pause(ctx, time.Duration(n)*time.Second/envproto.InputPaceBytes)
		}
		if errors.Is(err, io.EOF) {
			r.cancel(context.Canceled)
			return
		}
		if err != nil {
			r.cancel(newError(codeInternal, fmt.Errorf("reading the terminal: %w", err)))
			return
		}
	}
}

// watchSignals sends the size on every SIGWINCH, at most once per 100 ms,
// and ends the terminal on a SIGTERM, SIGHUP or SIGINT, so the run restores
// the local terminal.
func (r *terminalRun) watchSignals(ctx context.Context, sigs <-chan os.Signal) {
	defer recoverTo(func(err error) { r.cancel(newError(codeInternal, err)) })
	for {
		var sig os.Signal
		select {
		case <-ctx.Done():
			return
		case sig = <-sigs:
		}
		if !isResize(sig) {
			r.cancel(context.Canceled)
			return
		}
		r.app.pause(ctx, resizeCoalesce)
		if drainsToStop(sigs) {
			r.cancel(context.Canceled)
			return
		}
		cols, rows := r.app.currentSize()
		if err := r.term.Resize(cols, rows); err != nil {
			r.cancel(newError(codeInternal, fmt.Errorf("sending the terminal's size: %w", err)))
			return
		}
	}
}

// drainsToStop takes every signal waiting on sigs, and is whether any of
// them is not a SIGWINCH.
func drainsToStop(sigs <-chan os.Signal) bool {
	stop := false
	for {
		select {
		case sig := <-sigs:
			if !isResize(sig) {
				stop = true
			}
		default:
			return stop
		}
	}
}

// exitError is nil for an exit that is the terminal's own end, and the error
// for one the agent or the connection caused.
func exitError(env string, exit *envproto.TerminalExit) error {
	switch exit.Reason {
	case "exited", "closed":
		return nil
	case "disconnected":
		return newError(codeEnvironmentOffline, fmt.Errorf("%s went offline; the agent keeps running", env))
	case "idle":
		return newError(codeInternal, fmt.Errorf("%s closed the terminal: it was idle for too long", env))
	case "input_gap":
		return newError(codeInternal, fmt.Errorf("%s closed the terminal: its input arrived out of order (input_gap)", env))
	case "input_limit":
		return newError(codeInternal, fmt.Errorf("%s closed the terminal: the input came faster than it accepts (input_limit)", env))
	}
	return newError(codeInternal, fmt.Errorf("%s closed the terminal: %s", env, exit.Reason))
}
