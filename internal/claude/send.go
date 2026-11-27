package claude

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"
)

// ErrNotDelivered means a message typed into a session wasn't recorded in
// its transcript in time (ADR 0047).
var ErrNotDelivered = errors.New("Claude Code didn't record the message") //nolint:staticcheck // ST1005: "Claude Code" is a name.

// ptyRows and ptyCols are the terminal's size: room for Claude Code to
// draw its prompt.
const (
	ptyRows = 40
	ptyCols = 120
)

// attachTerm is claude attach running on a terminal: Read is what it draws,
// Write types into it.
type attachTerm interface {
	io.ReadWriter
	// hangUp closes the terminal, which detaches claude attach, and waits
	// for it to exit, killing it if it takes longer than grace.
	hangUp(ctx context.Context, log *slog.Logger, grace time.Duration)
}

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
	term, err := c.startAttach(ctx, s.ID)
	if err != nil {
		return err
	}
	c.log.DebugContext(ctx, "claude", slog.Any("args", []string{"attach", s.ID}), slog.String("via", "pty"))
	defer term.hangUp(ctx, c.log, c.sendTimings.hangUp)
	return c.typeMessage(ctx, s, term, message)
}

// typeMessage waits for the prompt on control, types message and Enter,
// and waits for s's transcript to record it.
func (c *Client) typeMessage(ctx context.Context, s *Session, control io.ReadWriter, message string) error {
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
		if _, err := io.WriteString(control, step); err != nil {
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
func watchPrompt(control io.Reader) <-chan struct{} {
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
