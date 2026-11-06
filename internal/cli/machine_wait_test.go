package cli

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/machine"
)

// listingOf is a listing of the machineTree workspaces named in states, with
// each one's session in that state. The empty state means no session yet.
func listingOf(states map[string]lifetime.State) []lifetime.Entry {
	var entries []lifetime.Entry
	tree := machineTree()
	for i := range tree {
		state, ok := states[tree[i].ID]
		if !ok {
			continue
		}
		entry := lifetime.Entry{Workspace: tree[i]}
		if state != "" {
			entry.Session = &lifetime.Session{Kind: lifetime.KindAgent, ID: "s-" + tree[i].ID, State: state}
		}
		entries = append(entries, entry)
	}
	return entries
}

// scriptListing makes e's lifetime listing return each of polls in turn,
// then the last one again. The result counts the listings made.
func scriptListing(e *mockedApp, polls ...map[string]lifetime.State) func() int {
	calls := 0
	e.lifetime.ListFunc = func(context.Context) (lifetime.Listing, error) {
		i := min(calls, len(polls)-1)
		calls++
		return lifetime.Listing{Workspaces: listingOf(polls[i])}, nil
	}
	return func() int { return calls }
}

// waitOptions is ws machine wait's options with timeout.
func waitOptions(timeout time.Duration) *machineWaitOptions {
	return &machineWaitOptions{timeout: timeout}
}

// waitJSON is ws machine wait's output, as the tests read it.
type waitJSON struct {
	Children []machineWaitItemJSON `json:"children"`
	Warnings []string              `json:"warnings"`
}

func parseWait(t *testing.T, out string) waitJSON {
	t.Helper()
	var got waitJSON
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	return got
}

// newWaitApp is newMachineApp with JSON output and a 1 ms poll.
func newWaitApp(t *testing.T) *mockedApp {
	t.Helper()
	e := newMachineApp(t)
	e.app.global.json = true
	e.app.machinePoll = time.Millisecond
	return e
}

func writePassResult(t *testing.T, e *mockedApp, child string) {
	t.Helper()
	require.NoError(t, machine.NewStore(e.app.paths.State).WriteResult("boss", child, &machine.Result{Verdict: "PASS", RecordedAt: testNow()}, nil))
}

func writeFailResult(t *testing.T, e *mockedApp, child string) {
	t.Helper()
	require.NoError(t, machine.NewStore(e.app.paths.State).WriteResult("boss", child, &machine.Result{Verdict: "FAIL", RecordedAt: testNow()}, nil))
}

func TestApp_runMachineWait(t *testing.T) {
	t.Run("an idle child with a pass returns after one poll", func(t *testing.T) {
		e := newWaitApp(t)
		writePassResult(t, e, "a")
		polls := scriptListing(e, map[string]lifetime.State{"a": lifetime.StateIdle})
		require.NoError(t, e.app.runMachineWait(t.Context(), []string{"a"}, waitOptions(time.Minute)))
		assert.Equal(t, 1, polls())
		got := parseWait(t, e.stdout.String())
		require.Len(t, got.Children, 1)
		item := got.Children[0]
		assert.Equal(t, "a", item.ID)
		assert.Equal(t, "s-a", *item.Session)
		assert.Nil(t, item.Role)
		assert.Equal(t, "idle", item.Status)
		assert.True(t, item.Finished)
		assert.Equal(t, "pass", item.Outcome)
		assert.Equal(t, "PASS", item.Result.Verdict)
		assert.Nil(t, item.Error)
	})
	t.Run("an idle child with no result finishes on the second poll as missing", func(t *testing.T) {
		e := newWaitApp(t)
		polls := scriptListing(e, map[string]lifetime.State{"a": lifetime.StateIdle})
		err := e.app.runMachineWait(t.Context(), []string{"a"}, waitOptions(time.Minute))
		requireCode(t, codeChildFailed, err)
		assert.Equal(t, 2, polls())
		item := parseWait(t, e.stdout.String()).Children[0]
		assert.True(t, item.Finished)
		assert.Equal(t, "missing", item.Outcome)
		assert.Nil(t, item.Result)
		require.NotNil(t, item.Error)
		assert.Equal(t, codeChildFailed, item.Error.Code)
		assert.Equal(t, "a finished without recording a verdict", item.Error.Message)
	})
	t.Run("a child with a result waits for its own working child", func(t *testing.T) {
		e := newWaitApp(t)
		writePassResult(t, e, "a")
		polls := scriptListing(e,
			map[string]lifetime.State{"a": lifetime.StateIdle, "a1": lifetime.StateWorking},
			map[string]lifetime.State{"a": lifetime.StateIdle, "a1": lifetime.StateWorking},
			map[string]lifetime.State{"a": lifetime.StateIdle, "a1": lifetime.StateIdle},
		)
		require.NoError(t, e.app.runMachineWait(t.Context(), []string{"a"}, waitOptions(time.Minute)))
		assert.Equal(t, 3, polls())
	})
	t.Run("a working child returns on the third poll once idle with a result", func(t *testing.T) {
		e := newWaitApp(t)
		writePassResult(t, e, "a")
		polls := scriptListing(e,
			map[string]lifetime.State{"a": lifetime.StateWorking},
			map[string]lifetime.State{"a": lifetime.StateWorking},
			map[string]lifetime.State{"a": lifetime.StateIdle},
		)
		require.NoError(t, e.app.runMachineWait(t.Context(), []string{"a"}, waitOptions(time.Minute)))
		assert.Equal(t, 3, polls())
	})
	t.Run("a timeout with a child still working is wait_timeout", func(t *testing.T) {
		e := newWaitApp(t)
		scriptListing(e, map[string]lifetime.State{"a": lifetime.StateWorking})
		err := e.app.runMachineWait(t.Context(), []string{"a"}, waitOptions(20*time.Millisecond))
		requireCode(t, codeWaitTimeout, err)
		item := parseWait(t, e.stdout.String()).Children[0]
		assert.False(t, item.Finished)
		assert.Equal(t, "pending", item.Outcome)
		assert.Equal(t, "working", item.Status)
		require.NotNil(t, item.Error)
		assert.Equal(t, codeWaitTimeout, item.Error.Code)
		assert.Contains(t, item.Error.Message, "a is still working after 20ms; run ws machine wait again to keep waiting")
	})
	t.Run("--any returns when one child finishes, and the other gets no error", func(t *testing.T) {
		e := newWaitApp(t)
		writePassResult(t, e, "b")
		scriptListing(e, map[string]lifetime.State{"a": lifetime.StateWorking, "b": lifetime.StateIdle})
		opts := waitOptions(time.Minute)
		opts.any = true
		require.NoError(t, e.app.runMachineWait(t.Context(), []string{"a", "b"}, opts))
		got := parseWait(t, e.stdout.String())
		require.Len(t, got.Children, 2)
		assert.False(t, got.Children[0].Finished)
		assert.Nil(t, got.Children[0].Error)
		assert.Equal(t, "pass", got.Children[1].Outcome)
		assert.Nil(t, got.Children[1].Error)
	})
	t.Run("--any with neither finished at the timeout is wait_timeout for both", func(t *testing.T) {
		e := newWaitApp(t)
		scriptListing(e, map[string]lifetime.State{"a": lifetime.StateWorking, "b": lifetime.StateWorking})
		opts := waitOptions(20 * time.Millisecond)
		opts.any = true
		requireCode(t, codeWaitTimeout, e.app.runMachineWait(t.Context(), []string{"a", "b"}, opts))
		got := parseWait(t, e.stdout.String())
		for _, item := range got.Children {
			require.NotNil(t, item.Error)
			assert.Equal(t, codeWaitTimeout, item.Error.Code)
		}
	})
	t.Run("a failing child is child_failed, and only it has an error", func(t *testing.T) {
		e := newWaitApp(t)
		writeFailResult(t, e, "a")
		writePassResult(t, e, "b")
		scriptListing(e, map[string]lifetime.State{"a": lifetime.StateIdle, "b": lifetime.StateIdle})
		requireCode(t, codeChildFailed, e.app.runMachineWait(t.Context(), []string{"a", "b"}, waitOptions(time.Minute)))
		got := parseWait(t, e.stdout.String())
		require.Len(t, got.Children, 2)
		require.NotNil(t, got.Children[0].Error)
		assert.Equal(t, "a finished with verdict FAIL", got.Children[0].Error.Message)
		assert.Nil(t, got.Children[1].Error)
	})
	t.Run("a removed child with a kept pass is gone and finished at once", func(t *testing.T) {
		e := newWaitApp(t)
		require.NoError(t, machine.NewStore(e.app.paths.State).WriteResult("boss", "gone", &machine.Result{
			Verdict:    "PASS",
			Ancestors:  []string{"plan", "boss"},
			RecordedAt: testNow(),
		}, nil))
		polls := scriptListing(e, map[string]lifetime.State{})
		require.NoError(t, e.app.runMachineWait(t.Context(), []string{"gone"}, waitOptions(time.Minute)))
		assert.Equal(t, 1, polls())
		item := parseWait(t, e.stdout.String()).Children[0]
		assert.Equal(t, "gone", item.Status)
		assert.Nil(t, item.Session)
		assert.True(t, item.Finished)
		assert.Equal(t, "pass", item.Outcome)
	})
	t.Run("a removed child whose ancestors are another tree is workspace_not_found", func(t *testing.T) {
		e := newWaitApp(t)
		require.NoError(t, machine.NewStore(e.app.paths.State).WriteResult("boss", "gone", &machine.Result{
			Verdict:    "PASS",
			Ancestors:  []string{"other", "boss"},
			RecordedAt: testNow(),
		}, nil))
		scriptListing(e, map[string]lifetime.State{})
		err := e.app.runMachineWait(t.Context(), []string{"gone"}, waitOptions(time.Minute))
		requireCode(t, codeWorkspaceNotFound, err)
		assert.Equal(t, exitNotFound, asCLIError(err).exitCode())
	})
	t.Run("the same workspace named twice waits once", func(t *testing.T) {
		e := newWaitApp(t)
		writePassResult(t, e, "a")
		scriptListing(e, map[string]lifetime.State{"a": lifetime.StateIdle})
		require.NoError(t, e.app.runMachineWait(t.Context(), []string{"a", "a"}, waitOptions(time.Minute)))
		assert.Len(t, parseWait(t, e.stdout.String()).Children, 1)
	})
	t.Run("no workspace, or a timeout that isn't positive, is usage", func(t *testing.T) {
		e := newWaitApp(t)
		requireCode(t, codeUsage, e.app.runMachineWait(t.Context(), nil, waitOptions(time.Minute)))
		requireCode(t, codeUsage, e.app.runMachineWait(t.Context(), []string{"a"}, waitOptions(0)))
	})
	t.Run("outside a workspace", func(t *testing.T) {
		e := newWaitApp(t)
		e.outside()
		requireCode(t, codeOutsideWorkspace, e.app.runMachineWait(t.Context(), []string{"a"}, waitOptions(time.Minute)))
	})
	t.Run("a cancelled context returns an error without output", func(t *testing.T) {
		e := newWaitApp(t)
		scriptListing(e, map[string]lifetime.State{"a": lifetime.StateWorking})
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		err := e.app.runMachineWait(ctx, []string{"a"}, waitOptions(time.Minute))
		require.ErrorIs(t, err, context.Canceled)
		assert.Empty(t, e.stdout.String())
	})
}

func TestApp_waitTargets(t *testing.T) {
	resolve := func(t *testing.T, e *mockedApp, args ...string) ([]waitTarget, error) {
		t.Helper()
		store := machine.NewStore(e.app.paths.State)
		return e.app.waitTargets(args, machineTree(), "plan", "boss", store)
	}
	t.Run("a descendant resolves to its id", func(t *testing.T) {
		e := newWaitApp(t)
		targets, err := resolve(t, e, "a", "a1")
		require.NoError(t, err)
		assert.Equal(t, []waitTarget{{id: "a"}, {id: "a1"}}, targets)
	})
	t.Run("a duplicate is listed once", func(t *testing.T) {
		e := newWaitApp(t)
		targets, err := resolve(t, e, "a", "a")
		require.NoError(t, err)
		assert.Equal(t, []waitTarget{{id: "a"}}, targets)
	})
	t.Run("a workspace that isn't below the caller is not_descendant", func(t *testing.T) {
		e := newWaitApp(t)
		_, err := resolve(t, e, "other")
		requireCode(t, codeNotDescendant, err)
	})
	t.Run("the caller itself is not_descendant", func(t *testing.T) {
		e := newWaitApp(t)
		_, err := resolve(t, e, "plan")
		requireCode(t, codeNotDescendant, err)
	})
	t.Run("an unknown id is workspace_not_found", func(t *testing.T) {
		e := newWaitApp(t)
		_, err := resolve(t, e, "nope")
		requireCode(t, codeWorkspaceNotFound, err)
		assert.Equal(t, exitNotFound, asCLIError(err).exitCode())
	})
}

func TestPollChild(t *testing.T) {
	ws := machineTree()
	entry := func(id string, state lifetime.State) lifetime.Entry {
		for i := range ws {
			if ws[i].ID == id {
				return lifetime.Entry{Workspace: ws[i], Session: &lifetime.Session{Kind: lifetime.KindAgent, ID: "s-" + id, State: state}}
			}
		}
		panic("no workspace " + id)
	}
	passed := machine.Record{Result: &machine.Result{Verdict: "PASS"}}
	t.Run("a child with no session is starting", func(t *testing.T) {
		target := waitTarget{id: "a"}
		got := pollChild(&target, []lifetime.Entry{{Workspace: ws[2]}}, passed)
		assert.Equal(t, "starting", got.status)
		assert.False(t, got.finished)
	})
	t.Run("an idle child with a result is finished", func(t *testing.T) {
		target := waitTarget{id: "a"}
		got := pollChild(&target, []lifetime.Entry{entry("a", lifetime.StateIdle)}, passed)
		assert.Equal(t, "idle", got.status)
		assert.Equal(t, "s-a", got.session)
		assert.True(t, got.quiet)
		assert.True(t, got.finished)
	})
	t.Run("an idle child with no result waits for the next poll", func(t *testing.T) {
		target := waitTarget{id: "a"}
		entries := []lifetime.Entry{entry("a", lifetime.StateIdle)}
		first := pollChild(&target, entries, machine.Record{})
		assert.False(t, first.finished)
		assert.True(t, target.quiet)
		assert.True(t, pollChild(&target, entries, machine.Record{}).finished)
	})
	t.Run("a working descendant keeps its parent unfinished", func(t *testing.T) {
		target := waitTarget{id: "a"}
		entries := []lifetime.Entry{entry("a", lifetime.StateIdle), entry("a1", lifetime.StateWorking)}
		got := pollChild(&target, entries, passed)
		assert.False(t, got.quiet)
		assert.False(t, got.finished)
	})
	t.Run("a working child is not quiet", func(t *testing.T) {
		target := waitTarget{id: "a"}
		got := pollChild(&target, []lifetime.Entry{entry("a", lifetime.StateWorking)}, passed)
		assert.False(t, got.quiet)
		assert.False(t, target.quiet)
	})
	t.Run("a removed child is gone and finished", func(t *testing.T) {
		target := waitTarget{id: "a", gone: true}
		got := pollChild(&target, nil, machine.Record{})
		assert.Equal(t, "gone", got.status)
		assert.True(t, got.finished)
	})
	t.Run("a child missing from the listing is gone", func(t *testing.T) {
		target := waitTarget{id: "a"}
		got := pollChild(&target, nil, machine.Record{})
		assert.Equal(t, "gone", got.status)
		assert.True(t, got.finished)
	})
}
