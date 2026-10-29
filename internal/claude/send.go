package claude

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// ErrNotDelivered means a message typed into a session wasn't recorded in
// its transcript in time (ADR 0047).
var ErrNotDelivered = errors.New("Claude Code didn't record the message") //nolint:staticcheck // ST1005: "Claude Code" is a name.

// promptGlyph is what Claude Code draws at its prompt; once it's on the
// terminal, typed text reaches the prompt (ADR 0047).
const promptGlyph = "❯"

// sendTimings are how long Send waits at each step.
type sendTimings struct {
	// prompt is the longest wait for the prompt to be drawn, after which
	// the message is typed anyway.
	prompt time.Duration
	// settle is the pause after the prompt is drawn, and between typing
	// the message and pressing Enter.
	settle time.Duration
	// record is the longest wait for the transcript to record the message.
	record time.Duration
	// poll is how often the transcript is read while waiting.
	poll time.Duration
	// hangUp is how long claude attach has to exit once its terminal is
	// closed, before it is killed.
	hangUp time.Duration
}

func defaultSendTimings() sendTimings {
	return sendTimings{
		prompt: 10 * time.Second,
		settle: 500 * time.Millisecond,
		record: 15 * time.Second,
		poll:   250 * time.Millisecond,
		hangUp: 2 * time.Second,
	}
}

// Send types message into s at its prompt, as a person replying would: it
// runs `claude attach <id>` in a pseudo-terminal, waits for the prompt,
// types message and Enter, and waits until s's transcript records message
// as a user entry. Then it closes the terminal, which detaches and leaves
// the session running (ADR 0047). A message that isn't recorded in time
// is ErrNotDelivered.
func (c *Client) Send(ctx context.Context, s *Session, message string) error {
	if !validID(s.ID) {
		return fmt.Errorf("%w: %q", errBadID, s.ID)
	}
	if err := c.ensureVersion(ctx); err != nil {
		return err
	}
	control, tty, err := openPTY()
	if err != nil {
		return fmt.Errorf("opening a terminal for claude attach %s: %w", s.ID, err)
	}
	//nolint:gosec // G204: running claude with arguments is this package's purpose.
	cmd := exec.CommandContext(ctx, c.path, "attach", s.ID)
	cmd.Env = append(append([]string{}, c.environ...), "TERM=xterm-256color")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	err = cmd.Start()
	tty.Close()
	if err != nil {
		control.Close()
		if notInstalled(err) {
			return fmt.Errorf("%w: %w", ErrNotInstalled, err)
		}
		return fmt.Errorf("running claude attach %s: %w", s.ID, err)
	}
	c.log.DebugContext(ctx, "claude", slog.Any("args", []string{"attach", s.ID}), slog.String("via", "pty"))
	defer c.hangUp(ctx, cmd, control)
	return c.typeMessage(ctx, s, control, message)
}

// typeMessage waits for the prompt on control, types message and Enter,
// and waits for s's transcript to record it.
func (c *Client) typeMessage(ctx context.Context, s *Session, control *os.File, message string) error {
	t := c.sendTimings
	select {
	case <-watchPrompt(control):
	case <-time.After(t.prompt):
		c.log.DebugContext(ctx, "no prompt from claude attach; typing anyway", slog.String("session", s.ID))
	case <-ctx.Done():
		return ctx.Err()
	}
	since := time.Now().Truncate(time.Second).Add(-time.Second)
	for _, step := range []string{message, "\r"} {
		if err := pause(ctx, t.settle); err != nil {
			return err
		}
		if _, err := control.WriteString(step); err != nil {
			return fmt.Errorf("typing into claude attach %s: %w", s.ID, err)
		}
	}
	deadline := time.Now().Add(t.record)
	for {
		ok, err := c.Recorded(s, message, since)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("%w: session %s, within %s", ErrNotDelivered, s.ID, t.record)
		}
		if err := pause(ctx, t.poll); err != nil {
			return err
		}
	}
}

// watchPrompt reads everything claude attach draws on control, and closes
// the channel it returns once the prompt glyph has been drawn. It keeps
// reading, so the terminal never fills, until control is closed.
func watchPrompt(control *os.File) <-chan struct{} {
	drawn := make(chan struct{})
	go func() {
		var seen []byte
		waiting := true
		buf := make([]byte, 32*1024)
		for {
			n, err := control.Read(buf)
			if waiting && n > 0 {
				seen = append(seen[max(len(seen)-len(promptGlyph), 0):], buf[:n]...)
				if bytes.Contains(seen, []byte(promptGlyph)) {
					close(drawn)
					waiting, seen = false, nil
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return drawn
}

// hangUp closes the terminal, which detaches claude attach, and waits for
// it to exit, killing it if it takes longer than the hang-up timing.
func (c *Client) hangUp(ctx context.Context, cmd *exec.Cmd, control *os.File) {
	control.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
		return
	case <-time.After(c.sendTimings.hangUp):
	}
	c.log.DebugContext(ctx, "claude attach didn't exit on hang-up; killing it")
	_ = cmd.Process.Kill()
	<-done
}

// pause waits for d, or until ctx is done.
func pause(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
