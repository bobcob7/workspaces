package claude

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"syscall"
)

// noWorktreeSettings makes a background session edit the workspace's repos
// in place instead of trying to create a worktree of its own (ADR 0015).
const noWorktreeSettings = `{"worktree":{"bgIsolation":"none"}}`

// backgroundedPrefix starts the line `claude --bg` prints first:
// "backgrounded · <id> · <name>", with U+00B7 middle dots (ADR 0024).
const backgroundedPrefix = "backgrounded · "

// idleMarker follows the name on the backgrounded line of a session started
// without a prompt: "(idle — send a prompt to start)" (ADR 0025).
const idleMarker = "(idle"

// ErrSettingsArg means the caller's arguments include --settings, which
// would replace the settings Start passes instead of adding to them
// (ADR 0024).
var ErrSettingsArg = errors.New("--settings can't be passed to claude through ws; put the settings in a settings file such as .claude/settings.json instead")

// StartOptions is a background session to start.
type StartOptions struct {
	// Dir is the session's working directory.
	Dir string
	// Name, if set, is the session's name in `claude agents`.
	Name string
	// Args go to claude unchanged after ws's own options: the prompt, and
	// options such as --model.
	Args []string
}

// Started is a background session Start started.
type Started struct {
	// ID is the session's short id.
	ID string
	// Idle means Claude Code reported the session as waiting for a prompt:
	// it was started without one (ADR 0025). False when that isn't known.
	Idle bool
}

// Start runs `claude --bg -n <name> --settings <no worktree> <args>...` in
// opts.Dir and returns the new session. It returns once Claude Code has
// accepted the session. A directory Claude Code doesn't trust fails with
// ErrUntrusted, and --settings in opts.Args fails with ErrSettingsArg
// without running claude.
//
// claude runs in a process group of its own and isn't stopped when ctx
// ends: interrupting `claude --bg` can leave a session behind that its
// caller never hears of, so Start always waits for its answer (ADR 0025).
func (c *Client) Start(ctx context.Context, opts StartOptions) (Started, error) {
	if err := CheckArgs(opts.Args); err != nil {
		return Started{}, err
	}
	ctx = context.WithoutCancel(ctx)
	if err := c.ensureVersion(ctx); err != nil {
		return Started{}, err
	}
	args := []string{"--bg"}
	if opts.Name != "" {
		args = append(args, "-n", opts.Name)
	}
	args = append(args, "--settings", noWorktreeSettings)
	out, err := c.execWith(ctx, opts.Dir, append(args, opts.Args...), &syscall.SysProcAttr{Setpgid: true})
	if err != nil {
		return Started{}, err
	}
	if started, ok := parseBackgrounded(out, opts.Name); ok {
		return started, nil
	}
	c.log.DebugContext(ctx, "no session id in claude --bg output; looking it up", slog.String("output", string(out)))
	id, err := c.findStarted(ctx, opts.Dir)
	return Started{ID: id}, err
}

// CheckArgs fails with ErrSettingsArg when args, the options passed on to
// `claude --bg`, include --settings (ADR 0024).
func CheckArgs(args []string) error {
	if hasSettingsArg(args) {
		return ErrSettingsArg
	}
	return nil
}

// hasSettingsArg reports whether args pass --settings, as `--settings x`
// or `--settings=x`, before any `--` that ends claude's options.
func hasSettingsArg(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "--settings" || strings.HasPrefix(arg, "--settings=") {
			return true
		}
	}
	return false
}

// parseBackgrounded reads the "backgrounded · <id> · <name>" line of
// `claude --bg`'s output, skipping lines before it, such as a warning.
// The session is idle when "(idle" follows the name, as it does for a
// session started without a prompt (ADR 0025).
func parseBackgrounded(out []byte, name string) (Started, bool) {
	for line := range bytes.Lines(out) {
		rest, ok := strings.CutPrefix(strings.TrimSpace(string(line)), backgroundedPrefix)
		if !ok {
			continue
		}
		id := strings.Fields(rest)
		if len(id) == 0 || !validID(id[0]) || id[0] == "·" {
			return Started{}, false
		}
		return Started{ID: id[0], Idle: idleAfter(rest, id[0], name)}, true
	}
	return Started{}, false
}

// idleAfter reports whether the backgrounded line's text after the id and
// the session's name starts with "(idle". Without a name, where the name
// Claude Code chose ends isn't known, so the session isn't known to be
// idle.
func idleAfter(rest, id, name string) bool {
	if name == "" {
		return false
	}
	tail, ok := strings.CutPrefix(rest, id+" · "+name)
	if !ok {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(tail), idleMarker)
}

// findStarted returns the id of the one background session started in
// dir itself, as ADR 0015 first described finding it. Sessions in
// directories below dir don't count.
func (c *Client) findStarted(ctx context.Context, dir string) (string, error) {
	sessions, err := c.Agents(ctx, ListOptions{All: true, Cwd: dir})
	if err != nil {
		return "", fmt.Errorf("finding the session started in %s: %w", dir, err)
	}
	want := resolved(dir)
	var ids []string
	for i := range sessions {
		if resolved(sessions[i].Cwd) == want {
			ids = append(ids, sessions[i].ID)
		}
	}
	if len(ids) != 1 {
		return "", fmt.Errorf("%w: %d background sessions in %s, want 1", ErrUnexpectedOutput, len(ids), dir)
	}
	return ids[0], nil
}

// resolved is dir with symlinks resolved, as Claude Code reports a
// session's directory, or dir cleaned when it can't be resolved.
func resolved(dir string) string {
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		return r
	}
	return filepath.Clean(dir)
}
