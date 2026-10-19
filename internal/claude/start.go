package claude

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
)

// noWorktreeSettings makes a background session edit the workspace's repos
// in place instead of trying to create a worktree of its own (ADR 0015).
const noWorktreeSettings = `{"worktree":{"bgIsolation":"none"}}`

// backgroundedPrefix starts the line `claude --bg` prints first:
// "backgrounded · <id> · <name>", with U+00B7 middle dots (ADR 0024).
const backgroundedPrefix = "backgrounded · "

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

// Start runs `claude --bg -n <name> --settings <no worktree> <args>...` in
// opts.Dir and returns the new session's short id. It returns once Claude
// Code has accepted the session. A directory Claude Code doesn't trust
// fails with ErrUntrusted, and --settings in opts.Args fails with
// ErrSettingsArg without running claude.
func (c *Client) Start(ctx context.Context, opts StartOptions) (string, error) {
	if hasSettingsArg(opts.Args) {
		return "", ErrSettingsArg
	}
	args := []string{"--bg"}
	if opts.Name != "" {
		args = append(args, "-n", opts.Name)
	}
	args = append(args, "--settings", noWorktreeSettings)
	out, err := c.run(ctx, opts.Dir, append(args, opts.Args...)...)
	if err != nil {
		return "", err
	}
	if id, ok := parseBackgrounded(out); ok {
		return id, nil
	}
	c.log.DebugContext(ctx, "no session id in claude --bg output; looking it up", slog.String("output", string(out)))
	return c.findStarted(ctx, opts.Dir)
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

// parseBackgrounded returns the session id from the "backgrounded" line of
// `claude --bg`'s output. Lines before it, such as a warning, are skipped.
func parseBackgrounded(out []byte) (string, bool) {
	for line := range bytes.Lines(out) {
		rest, ok := strings.CutPrefix(strings.TrimSpace(string(line)), backgroundedPrefix)
		if !ok {
			continue
		}
		id := strings.Fields(rest)
		if len(id) == 0 || !validID(id[0]) || id[0] == "·" {
			return "", false
		}
		return id[0], true
	}
	return "", false
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
