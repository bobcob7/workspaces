package cli

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/workspace"
)

var testResets = time.Date(2026, 10, 29, 4, 30, 0, 0, time.UTC)

// newContinueApp is a test App whose workspace store finds workspaces by
// id, and whose lifetime manager continues each with session "s-<id>",
// idle at a five-hour limit, unless a dry run.
func newContinueApp(ids ...string) *mockedApp {
	e := newRmApp(ids...)
	e.lifetime.ContinueFunc = func(_ context.Context, opts lifetime.ContinueOptions) ([]lifetime.Continued, error) {
		var out []lifetime.Continued
		for i := range opts.Workspaces {
			action := lifetime.ActionContinued
			if opts.DryRun {
				action = lifetime.ActionWouldContinue
			}
			out = append(out, continuedAs(opts.Workspaces[i].ID, lifetime.StateIdle, action))
		}
		return out, nil
	}
	return e
}

// continuedAs is a workspace Continue found, with session s-<id>, and a
// five-hour limit unless action is empty.
func continuedAs(id string, state lifetime.State, action lifetime.Action) lifetime.Continued {
	c := lifetime.Continued{Workspace: workspace.Workspace{ID: id}, SessionID: "s-" + id, State: state, Action: action}
	if action != "" {
		c.Limit = &claude.Limit{Type: "five_hour", ResetsAt: testResets}
	}
	return c
}

func TestApp_newContinueCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newContinueCmd()
	t.Run("has --dry-run and --watch", func(t *testing.T) {
		assertFlag(t, cmd, "dry-run", "false")
		assertFlag(t, cmd, "watch", "false")
	})
	t.Run("workspaces are optional", func(t *testing.T) {
		assert.Nil(t, cmd.Args)
	})
	t.Run("runs the removal pass, then continues", func(t *testing.T) {
		e := newContinueApp("fix-login")
		cmd := e.app.newContinueCmd()
		cmd.SetContext(t.Context())
		require.NoError(t, cmd.ParseFlags([]string{"--dry-run", "fix-login"}))
		require.NoError(t, cmd.RunE(cmd, cmd.Flags().Args()))
		assert.Len(t, e.lifetime.SweepCalls(), 1)
		require.Len(t, e.lifetime.ContinueCalls(), 1)
		assert.True(t, e.lifetime.ContinueCalls()[0].Opts.DryRun)
	})
	t.Run("--watch with --json is a usage error", func(t *testing.T) {
		e := newContinueApp()
		e.app.global.json = true
		cmd := e.app.newContinueCmd()
		cmd.SetContext(t.Context())
		require.NoError(t, cmd.ParseFlags([]string{"--watch"}))
		requireCode(t, codeUsage, cmd.RunE(cmd, nil))
		assert.Empty(t, e.lifetime.ContinueCalls())
	})
}

func TestApp_runContinue(t *testing.T) {
	t.Run("every agent workspace without arguments", func(t *testing.T) {
		e := newContinueApp()
		e.lifetime.ContinueFunc = func(context.Context, lifetime.ContinueOptions) ([]lifetime.Continued, error) {
			return []lifetime.Continued{
				continuedAs("fix-login", lifetime.StateIdle, lifetime.ActionContinued),
				continuedAs("docs-pass", lifetime.StateIdle, lifetime.ActionWaiting),
				continuedAs("night", lifetime.StateIdle, lifetime.ActionAsleep),
				continuedAs("stuck", lifetime.StateIdle, lifetime.ActionGaveUp),
				continuedAs("busy", lifetime.StateWorking, ""),
			}, nil
		}
		require.NoError(t, e.app.runContinue(t.Context(), &continueOptions{}))
		assert.Equal(t, "continued  fix-login  s-fix-login  five_hour limit reset 2026-10-29T04:30:00Z\n"+
			"waiting    docs-pass  s-docs-pass  five_hour limit resets 2026-10-29T04:30:00Z\n"+
			"asleep     night      s-night  five_hour limit reset 2026-10-29T04:30:00Z\n"+
			"gave up    stuck      s-stuck  five_hour limit reset 2026-10-29T04:30:00Z\n"+
			"working    busy       s-busy\n", e.stdout.String())
		require.Len(t, e.lifetime.ContinueCalls(), 1)
		assert.Equal(t, lifetime.ContinueOptions{All: true}, e.lifetime.ContinueCalls()[0].Opts)
	})
	t.Run("--json", func(t *testing.T) {
		e := newContinueApp()
		e.lifetime.ContinueFunc = func(context.Context, lifetime.ContinueOptions) ([]lifetime.Continued, error) {
			return []lifetime.Continued{
				continuedAs("a", lifetime.StateIdle, lifetime.ActionContinued),
				continuedAs("b", lifetime.StateIdle, ""),
				{Workspace: workspace.Workspace{ID: "c"}, SessionID: "s-c"},
			}, nil
		}
		e.app.global.json = true
		require.NoError(t, e.app.runContinue(t.Context(), &continueOptions{}))
		assert.JSONEq(t, `{"workspaces":[
			{"id":"a","session":"s-a","state":"idle","limit":{"type":"five_hour","resets_at":"2026-10-29T04:30:00Z"},"action":"continued","error":null},
			{"id":"b","session":"s-b","state":"idle","limit":null,"action":null,"error":null},
			{"id":"c","session":"s-c","state":null,"limit":null,"action":null,"error":null}
		],"warnings":[]}`, e.stdout.String())
	})
	t.Run("named workspaces, each once, in order", func(t *testing.T) {
		e := newContinueApp("a", "b")
		require.NoError(t, e.app.runContinue(t.Context(), &continueOptions{workspaces: []string{"b", "a", "b"}}))
		assert.Equal(t, "continued  b  s-b  five_hour limit reset 2026-10-29T04:30:00Z\n"+
			"continued  a  s-a  five_hour limit reset 2026-10-29T04:30:00Z\n", e.stdout.String())
		opts := e.lifetime.ContinueCalls()[0].Opts
		assert.False(t, opts.All)
		require.Len(t, opts.Workspaces, 2)
		assert.Equal(t, "b", opts.Workspaces[0].ID)
	})
	t.Run("--dry-run", func(t *testing.T) {
		e := newContinueApp("a")
		require.NoError(t, e.app.runContinue(t.Context(), &continueOptions{workspaces: []string{"a"}, dryRun: true}))
		assert.Equal(t, "would continue  a  s-a  five_hour limit reset 2026-10-29T04:30:00Z\n", e.stdout.String())
	})
	t.Run("a failure doesn't stop the others; the exit code is the first failure's", func(t *testing.T) {
		e := newContinueApp("elsewhere", "ok")
		e.lifetime.ContinueFunc = func(_ context.Context, opts lifetime.ContinueOptions) ([]lifetime.Continued, error) {
			return []lifetime.Continued{
				{Workspace: opts.Workspaces[0], SessionID: "s-elsewhere", Err: lifetime.ErrSessionElsewhere},
				continuedAs("ok", lifetime.StateIdle, lifetime.ActionContinued),
			}, nil
		}
		e.app.global.json = true
		err := e.app.runContinue(t.Context(), &continueOptions{workspaces: []string{"missing", "elsewhere", "ok"}})
		requireCode(t, codeWorkspaceNotFound, err)
		assert.JSONEq(t, `{"workspaces":[
			{"id":"missing","session":null,"state":null,"limit":null,"action":null,"error":{"code":"workspace_not_found","message":"workspace not found"}},
			{"id":"elsewhere","session":"s-elsewhere","state":null,"limit":null,"action":null,"error":{"code":"session_mismatch","message":"the workspace's Claude Code session is in another directory"}},
			{"id":"ok","session":"s-ok","state":"idle","limit":{"type":"five_hour","resets_at":"2026-10-29T04:30:00Z"},"action":"continued","error":null}
		],"warnings":[]}`, e.stdout.String())
	})
	t.Run("a message that isn't delivered is claude_failed", func(t *testing.T) {
		e := newContinueApp("a")
		e.lifetime.ContinueFunc = func(context.Context, lifetime.ContinueOptions) ([]lifetime.Continued, error) {
			failed := continuedAs("a", lifetime.StateIdle, "")
			failed.Err = claude.ErrNotDelivered
			return []lifetime.Continued{failed}, nil
		}
		requireCode(t, codeClaudeFailed, e.app.runContinue(t.Context(), &continueOptions{workspaces: []string{"a"}}))
		assert.Equal(t, "ws: Claude Code didn't record the message\n", e.stderr.String())
	})
	t.Run("claude agents failing fails the command with nothing on stdout", func(t *testing.T) {
		e := newContinueApp()
		e.lifetime.ContinueFunc = func(context.Context, lifetime.ContinueOptions) ([]lifetime.Continued, error) {
			return nil, &claude.CommandError{Args: []string{"agents"}, ExitCode: 1, Err: errTest}
		}
		requireCode(t, codeClaudeFailed, e.app.runContinue(t.Context(), &continueOptions{}))
		assert.Empty(t, e.stdout.String())
	})
}

func TestApp_watchContinue(t *testing.T) {
	t.Run("prints only what each pass did, until interrupted", func(t *testing.T) {
		e := newContinueApp()
		e.app.continuePoll = time.Millisecond
		e.app.now = func() time.Time { return testResets.Add(time.Minute) }
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		passes := 0
		e.lifetime.ContinueFunc = func(context.Context, lifetime.ContinueOptions) ([]lifetime.Continued, error) {
			passes++
			switch passes {
			case 1:
				return []lifetime.Continued{continuedAs("a", lifetime.StateIdle, lifetime.ActionWaiting)}, nil
			case 2:
				return nil, claude.ErrNotInstalled
			case 3:
				return []lifetime.Continued{continuedAs("a", lifetime.StateIdle, lifetime.ActionContinued), continuedAs("b", lifetime.StateIdle, lifetime.ActionGaveUp)}, nil
			}
			cancel()
			return nil, ctx.Err()
		}
		require.NoError(t, e.app.watchContinue(ctx, &continueOptions{}))
		assert.Equal(t, 4, passes)
		assert.Equal(t, "2026-10-29T04:31:00Z  continued  a  s-a  five_hour limit reset 2026-10-29T04:30:00Z\n"+
			"2026-10-29T04:31:00Z  gave up  b  s-b  five_hour limit reset 2026-10-29T04:30:00Z\n", e.stdout.String())
		assert.Contains(t, e.stderr.String(), "ws: claude not found")
	})
	t.Run("prints each warning once", func(t *testing.T) {
		e := newContinueApp()
		e.app.continuePoll = time.Millisecond
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var warnings []string
		e.lifetime.WarningsFunc = func() []string { return warnings }
		e.lifetime.ContinueFunc = func(context.Context, lifetime.ContinueOptions) ([]lifetime.Continued, error) {
			warnings = append(warnings, "w"+string(rune('0'+len(warnings))))
			if len(warnings) == 3 {
				cancel()
			}
			return nil, nil
		}
		require.NoError(t, e.app.watchContinue(ctx, &continueOptions{}))
		assert.Equal(t, "ws: warning: w0\nws: warning: w1\n", e.stderr.String())
	})
}
