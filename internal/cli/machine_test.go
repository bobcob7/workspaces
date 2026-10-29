package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/lockfile"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// machineTree is the workspaces newMachineApp knows, oldest first: the
// caller plan, started by boss, with children a and hidden b, a's child
// a1, and plan's sibling other.
func machineTree() []workspace.Workspace {
	return []workspace.Workspace{
		{ID: "boss", Path: "/cfg/workspaces/boss"},
		{ID: "plan", Path: "/cfg/workspaces/plan", Parent: "boss"},
		{ID: "a", Path: "/cfg/workspaces/a", Parent: "plan"},
		{ID: "b", Path: "/cfg/workspaces/b", Parent: "plan", Hidden: true},
		{ID: "a1", Path: "/cfg/workspaces/a1", Parent: "a"},
		{ID: "other", Path: "/cfg/workspaces/other", Parent: "boss"},
	}
}

// newMachineApp is a test App run inside workspace plan, whose stores know
// machineTree, and whose lifetime manager removes each workspace alone.
func newMachineApp() *mockedApp {
	e := newMockedApp()
	tree := machineTree()
	e.store.ListFunc = func() ([]workspace.Workspace, error) { return tree, nil }
	e.store.FindFunc = func(arg string) (workspace.Workspace, error) {
		for i := range tree {
			if tree[i].ID == arg || tree[i].Path == arg {
				return tree[i], nil
			}
		}
		return workspace.Workspace{}, workspace.ErrNotFound
	}
	e.store.ContainingFunc = func(string) (workspace.Workspace, error) { return tree[1], nil }
	e.app.getwd = func() (string, error) { return "/cfg/workspaces/plan/buff-bear", nil }
	e.lifetime.ListFunc = func(context.Context) (lifetime.Listing, error) {
		entries := make([]lifetime.Entry, 0, len(tree))
		for i := range tree {
			entries = append(entries, lifetime.Entry{Workspace: tree[i]})
		}
		return lifetime.Listing{Workspaces: entries, Unknown: []string{"/cfg/workspaces/junk"}}, nil
	}
	removeEach(e, func(ws *workspace.Workspace) (lifetime.Removed, error) {
		return lifetime.Removed{Workspace: *ws}, nil
	})
	return e
}

// outside makes e run outside every workspace.
func (e *mockedApp) outside() {
	e.store.ContainingFunc = func(string) (workspace.Workspace, error) {
		return workspace.Workspace{}, workspace.ErrNotFound
	}
}

// runMachine runs `ws args...` through the root command, as ws does, and
// returns its exit code.
func runMachine(t *testing.T, e *mockedApp, args ...string) int {
	t.Helper()
	return e.app.execute(t.Context(), e.app.newRootCmd(), args)
}

// rmResultIDs is the ids in a ws rm --json result.
func rmResultIDs(t *testing.T, out string) []string {
	t.Helper()
	var result struct {
		Workspaces []rmJSON `json:"workspaces"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	ids := []string{}
	for _, w := range result.Workspaces {
		ids = append(ids, w.ID)
	}
	return ids
}

func TestApp_newMachineCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newMachineCmd()
	t.Run("is in the group for agents", func(t *testing.T) {
		assert.Equal(t, agentsGroup, cmd.GroupID)
	})
	t.Run("has spawn, children, and rm", func(t *testing.T) {
		var names []string
		for _, sub := range cmd.Commands() {
			names = append(names, sub.Name())
		}
		assert.ElementsMatch(t, []string{"spawn", "children", "rm"}, names)
	})
	t.Run("refuses an unknown operation", func(t *testing.T) {
		requireUsageError(t, cmd.Args(cmd, []string{"nope"}))
	})
	t.Run("without an operation prints its help", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		cmd := app.newMachineCmd()
		cmd.SetOut(stdout)
		require.NoError(t, cmd.RunE(cmd, nil))
		assert.Contains(t, stdout.String(), "spawn")
	})
}

func TestApp_newMachineSpawnCmd(t *testing.T) {
	app, _, _ := newTestApp()
	newCmd := func(t *testing.T, args ...string) (*cobra.Command, []string) {
		t.Helper()
		cmd := app.newMachineSpawnCmd()
		require.NoError(t, cmd.ParseFlags(args))
		return cmd, cmd.Flags().Args()
	}
	t.Run("has ws claude's flags but --parent", func(t *testing.T) {
		cmd := app.newMachineSpawnCmd()
		assertFlag(t, cmd, "name", "")
		assertFlag(t, cmd, "offline", "false")
		assertFlag(t, cmd, "hidden", "false")
		assert.Nil(t, cmd.Flags().Lookup("parent"))
	})
	t.Run("refuses a missing prompt", func(t *testing.T) {
		cmd, args := newCmd(t, "buff-bear")
		requireUsageError(t, cmd.Args(cmd, args))
		cmd, args = newCmd(t, "buff-bear", "--")
		requireUsageError(t, cmd.Args(cmd, args))
	})
	t.Run("refuses --settings after --", func(t *testing.T) {
		cmd, args := newCmd(t, "buff-bear", "--", "fix it", "--settings", "{}")
		requireUsageError(t, cmd.Args(cmd, args))
	})
	t.Run("accepts specs and a prompt", func(t *testing.T) {
		cmd, args := newCmd(t, "buff-bear", "--", "fix it", "--model", "opus")
		assert.NoError(t, cmd.Args(cmd, args))
	})
	t.Run("prints JSON without --json, with the caller as parent", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.ws.Parent = "fix-login-d8efb24f"
		e.store.ContainingFunc = func(string) (workspace.Workspace, error) {
			return workspace.Workspace{ID: "fix-login-d8efb24f"}, nil
		}
		e.app.newStores = func(config.Paths, config.Config) stores {
			return stores{cache: stubCache(), workspaces: e.store, lifetime: stubLifetime()}
		}
		code := e.app.execute(t.Context(), e.app.newRootCmd(), []string{"machine", "spawn", "--name", "Fix Login", "buff-bear", "--", "fix it"})
		require.Equal(t, exitOK, code, e.stderr.String())
		require.Len(t, e.store.CreateCalls(), 1)
		assert.Equal(t, "fix-login-d8efb24f", e.store.CreateCalls()[0].Opts.Parent)
		assert.Equal(t, "Fix Login", e.store.CreateCalls()[0].Opts.Name)
		var got claudeResult
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &got))
		assert.Equal(t, "x", got.ID)
		require.NotNil(t, got.Parent)
		assert.Equal(t, "fix-login-d8efb24f", *got.Parent)
	})
}

func TestApp_newMachineChildrenCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newMachineChildrenCmd()
	t.Run("has --all", func(t *testing.T) {
		assertFlag(t, cmd, "all", "false")
	})
	t.Run("takes no arguments", func(t *testing.T) {
		requireUsageError(t, cmd.Args(cmd, []string{"x"}))
	})
	t.Run("prints JSON without --json, without a removal pass first", func(t *testing.T) {
		e := newMachineApp()
		require.Equal(t, exitOK, runMachine(t, e, "machine", "children"), e.stderr.String())
		assert.True(t, json.Valid(e.stdout.Bytes()), e.stdout.String())
		assert.Empty(t, e.lifetime.SweepCalls())
	})
}

func TestApp_newMachineRmCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newMachineRmCmd()
	t.Run("has --all, --dry-run, and --keep-children", func(t *testing.T) {
		assertFlag(t, cmd, "all", "false")
		assertFlag(t, cmd, "dry-run", "false")
		assertFlag(t, cmd, "keep-children", "false")
	})
	t.Run("needs workspaces or --all", func(t *testing.T) {
		cmd := app.newMachineRmCmd()
		requireUsageError(t, cmd.Args(cmd, nil))
		require.NoError(t, cmd.ParseFlags([]string{"--all"}))
		assert.NoError(t, cmd.Args(cmd, nil))
		requireUsageError(t, cmd.Args(cmd, []string{"a"}))
	})
	t.Run("prints JSON without --json, after the removal pass", func(t *testing.T) {
		e := newMachineApp()
		require.Equal(t, exitOK, runMachine(t, e, "machine", "rm", "--dry-run", "a"), e.stderr.String())
		assert.Len(t, e.lifetime.SweepCalls(), 1)
		assert.Equal(t, []string{"a"}, rmResultIDs(t, e.stdout.String()))
		assert.True(t, e.lifetime.RemoveTreeCalls()[0].Opts.DryRun)
	})
}

func TestIsAgentCommand(t *testing.T) {
	app, _, _ := newTestApp()
	root := app.newRootCmd()
	find := func(t *testing.T, args ...string) *cobra.Command {
		t.Helper()
		cmd, _, err := root.Find(args)
		require.NoError(t, err)
		return cmd
	}
	t.Run("machine and its commands are", func(t *testing.T) {
		assert.True(t, isAgentCommand(find(t, "machine")))
		assert.True(t, isAgentCommand(find(t, "machine", "rm")))
	})
	t.Run("the others aren't", func(t *testing.T) {
		assert.False(t, isAgentCommand(root))
		assert.False(t, isAgentCommand(find(t, "rm")))
		assert.False(t, isAgentCommand(find(t, "cache", "list")))
	})
}

func TestPromptAfterDash(t *testing.T) {
	newCmd := func(t *testing.T, args ...string) (*cobra.Command, []string) {
		t.Helper()
		cmd := &cobra.Command{Use: "spawn"}
		require.NoError(t, cmd.ParseFlags(args))
		return cmd, cmd.Flags().Args()
	}
	t.Run("accepts a prompt", func(t *testing.T) {
		cmd, args := newCmd(t, "buff-bear", "--", "fix it")
		assert.NoError(t, promptAfterDash(cmd, args))
	})
	t.Run("refuses no --", func(t *testing.T) {
		cmd, args := newCmd(t, "buff-bear")
		requireUsageError(t, promptAfterDash(cmd, args))
	})
	t.Run("refuses nothing after --", func(t *testing.T) {
		cmd, args := newCmd(t, "buff-bear", "--")
		requireUsageError(t, promptAfterDash(cmd, args))
	})
	t.Run("refuses a blank prompt", func(t *testing.T) {
		cmd, args := newCmd(t, "--", "  ")
		requireUsageError(t, promptAfterDash(cmd, args))
	})
}

func TestCheckMachineRmArgs(t *testing.T) {
	cmd := &cobra.Command{Use: "rm"}
	t.Run("workspaces", func(t *testing.T) {
		assert.NoError(t, checkMachineRmArgs(cmd, false, []string{"a"}))
	})
	t.Run("--all", func(t *testing.T) {
		assert.NoError(t, checkMachineRmArgs(cmd, true, nil))
	})
	t.Run("neither", func(t *testing.T) {
		requireUsageError(t, checkMachineRmArgs(cmd, false, nil))
	})
	t.Run("both", func(t *testing.T) {
		requireUsageError(t, checkMachineRmArgs(cmd, true, []string{"a"}))
	})
}

func TestApp_caller(t *testing.T) {
	t.Run("the workspace containing the working directory", func(t *testing.T) {
		e := newMachineApp()
		id, err := e.app.caller()
		require.NoError(t, err)
		assert.Equal(t, "plan", id)
		assert.Equal(t, "/cfg/workspaces/plan/buff-bear", e.store.ContainingCalls()[0].Dir)
	})
	t.Run("outside_workspace outside one", func(t *testing.T) {
		e := newMachineApp()
		e.outside()
		_, err := e.app.caller()
		requireCode(t, codeOutsideWorkspace, err)
		assert.ErrorIs(t, err, errOutsideWorkspace)
	})
	t.Run("a working directory that can't be read", func(t *testing.T) {
		e := newMachineApp()
		e.app.getwd = func() (string, error) { return "", errTest }
		_, err := e.app.caller()
		assert.ErrorIs(t, err, errTest)
	})
}

func TestApp_runMachineSpawn(t *testing.T) {
	t.Run("is ws claude with the caller as parent", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.store.ContainingFunc = func(string) (workspace.Workspace, error) {
			return workspace.Workspace{ID: "fix-login-d8efb24f"}, nil
		}
		e.app.global.json = true
		require.NoError(t, e.app.runMachineSpawn(t.Context(), &claudeOptions{specs: []string{"buff-bear"}, claudeArgs: []string{"fix it"}}))
		require.Len(t, e.store.CreateCalls(), 1)
		assert.Equal(t, "fix-login-d8efb24f", e.store.CreateCalls()[0].Opts.Parent)
		assert.Equal(t, []string{"fix it"}, e.claude.StartCalls()[0].Opts.Args)
	})
	t.Run("never attaches, even to an idle session on a terminal", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.idle()
		e.app.interactive = true
		e.store.ContainingFunc = func(string) (workspace.Workspace, error) {
			return workspace.Workspace{ID: "fix-login-d8efb24f"}, nil
		}
		e.app.newStores = func(config.Paths, config.Config) stores {
			return stores{cache: stubCache(), workspaces: e.store, lifetime: stubLifetime()}
		}
		code := e.app.execute(t.Context(), e.app.newRootCmd(), []string{"machine", "spawn", "--", "fix it"})
		require.Equal(t, exitOK, code, e.stderr.String())
		assert.Empty(t, e.claude.AttachCalls())
	})
	t.Run("outside a workspace, creates nothing", func(t *testing.T) {
		e := newClaudeEnv(t)
		err := e.app.runMachineSpawn(t.Context(), &claudeOptions{claudeArgs: []string{"fix it"}})
		requireCode(t, codeOutsideWorkspace, err)
		assert.Empty(t, e.store.CreateCalls())
		assert.Empty(t, e.claude.CheckCalls())
	})
}

func TestApp_runMachineChildren(t *testing.T) {
	ids := func(t *testing.T, out string) []string {
		t.Helper()
		var got listJSON
		require.NoError(t, json.Unmarshal([]byte(out), &got))
		var ids []string
		for _, w := range got.Workspaces {
			require.NotNil(t, w.ID)
			ids = append(ids, *w.ID)
		}
		return ids
	}
	t.Run("lists the direct children, hidden ones included", func(t *testing.T) {
		e := newMachineApp()
		e.app.global.json = true
		require.NoError(t, e.app.runMachineChildren(t.Context(), false))
		assert.Equal(t, []string{"a", "b"}, ids(t, e.stdout.String()))
		assert.Contains(t, e.stdout.String(), `"hidden_count":0`)
		assert.Contains(t, e.stdout.String(), `"parent":"plan"`)
	})
	t.Run("--all lists every descendant, oldest first", func(t *testing.T) {
		e := newMachineApp()
		e.app.global.json = true
		require.NoError(t, e.app.runMachineChildren(t.Context(), true))
		assert.Equal(t, []string{"a", "b", "a1"}, ids(t, e.stdout.String()))
	})
	t.Run("nothing but descendants, not unknown directories", func(t *testing.T) {
		e := newMachineApp()
		e.app.global.json = true
		require.NoError(t, e.app.runMachineChildren(t.Context(), true))
		assert.NotContains(t, e.stdout.String(), "junk")
		assert.NotContains(t, e.stdout.String(), `"other"`)
		assert.NotContains(t, e.stdout.String(), `"boss"`)
	})
	t.Run("none is an empty list", func(t *testing.T) {
		e := newMachineApp()
		e.store.ContainingFunc = func(string) (workspace.Workspace, error) { return machineTree()[5], nil }
		e.app.global.json = true
		require.NoError(t, e.app.runMachineChildren(t.Context(), false))
		assert.JSONEq(t, `{"workspaces":[],"hidden_count":0,"warnings":[]}`, e.stdout.String())
	})
	t.Run("outside a workspace", func(t *testing.T) {
		e := newMachineApp()
		e.outside()
		requireCode(t, codeOutsideWorkspace, e.app.runMachineChildren(t.Context(), false))
		assert.Empty(t, e.lifetime.ListCalls())
	})
	t.Run("a listing that fails", func(t *testing.T) {
		e := newMachineApp()
		e.lifetime.ListFunc = func(context.Context) (lifetime.Listing, error) { return lifetime.Listing{}, errTest }
		assert.ErrorIs(t, e.app.runMachineChildren(t.Context(), false), errTest)
	})
}

func TestDescendantEntries(t *testing.T) {
	entryIDs := func(entries []lifetime.Entry) []string {
		var ids []string
		for i := range entries {
			ids = append(ids, entries[i].Workspace.ID)
		}
		return ids
	}
	t.Run("a workspace's descendants", func(t *testing.T) {
		var entries []lifetime.Entry
		for _, ws := range machineTree() {
			entries = append(entries, lifetime.Entry{Workspace: ws})
		}
		assert.Equal(t, []string{"a1"}, entryIDs(descendantEntries(entries, "a")))
	})
	t.Run("in the listing's order, oldest first, not the tree's", func(t *testing.T) {
		entries := []lifetime.Entry{
			{Workspace: workspace.Workspace{ID: "a", Parent: "plan"}},
			{Workspace: workspace.Workspace{ID: "a1", Parent: "a"}},
			{Workspace: workspace.Workspace{ID: "b", Parent: "plan"}},
		}
		assert.Equal(t, []string{"a", "a1", "b"}, entryIDs(descendantEntries(entries, "plan")))
	})
}

func TestDescendants(t *testing.T) {
	t.Run("children and theirs", func(t *testing.T) {
		assert.Equal(t, map[string]bool{"a": true, "b": true, "a1": true}, descendants(machineTree(), "plan"))
	})
	t.Run("never the workspace itself or its ancestors", func(t *testing.T) {
		got := descendants(machineTree(), "plan")
		assert.False(t, got["plan"])
		assert.False(t, got["boss"])
		assert.False(t, got["other"])
	})
	t.Run("a cycle ends", func(t *testing.T) {
		list := []workspace.Workspace{{ID: "x", Parent: "y"}, {ID: "y", Parent: "x"}}
		assert.Equal(t, map[string]bool{"y": true}, descendants(list, "x"))
	})
}

func TestDirectChildren(t *testing.T) {
	assert.Equal(t, []string{"a", "b"}, directChildren(machineTree(), "plan"))
	assert.Equal(t, []string{}, directChildren(machineTree(), "a1"))
}

func TestRefuseOutside(t *testing.T) {
	targets := []rmTarget{
		{arg: "a", ws: workspace.Workspace{ID: "a"}},
		{arg: "boss", ws: workspace.Workspace{ID: "boss"}},
		{arg: "nope", err: workspace.ErrNotFound},
		{arg: "s-gone", swept: &lifetime.Removed{Workspace: workspace.Workspace{ID: "gone"}}},
	}
	refuseOutside(targets, map[string]bool{"a": true})
	require.NoError(t, targets[0].err)
	requireCode(t, codeNotDescendant, targets[1].err)
	require.ErrorIs(t, targets[2].err, workspace.ErrNotFound, "keeps its own error")
	requireCode(t, codeNotDescendant, targets[3].err)
}

func TestApp_runMachineRm(t *testing.T) {
	run := func(t *testing.T, e *mockedApp, opts *machineRmOptions) error {
		t.Helper()
		e.app.global.json = true
		return e.app.runMachineRm(t.Context(), opts)
	}
	t.Run("removes a descendant", func(t *testing.T) {
		e := newMachineApp()
		require.NoError(t, run(t, e, &machineRmOptions{workspaces: []string{"a1"}}))
		assert.JSONEq(t, `{"workspaces":[{"id":"a1","parent":null,"removed":true,"error":null}],"warnings":[]}`, e.stdout.String())
	})
	t.Run("refuses the caller, its ancestors, and unrelated workspaces, and removes the rest", func(t *testing.T) {
		e := newMachineApp()
		err := run(t, e, &machineRmOptions{workspaces: []string{"plan", "a", "boss", "other", "b"}})
		requireCode(t, codeNotDescendant, err)
		assert.Equal(t, exitRefused, asCLIError(err).exitCode())
		assert.JSONEq(t, `{"workspaces":[
			{"id":"plan","parent":null,"removed":false,"error":{"code":"not_descendant","message":"plan is not a workspace this one started"}},
			{"id":"a","parent":null,"removed":true,"error":null},
			{"id":"boss","parent":null,"removed":false,"error":{"code":"not_descendant","message":"boss is not a workspace this one started"}},
			{"id":"other","parent":null,"removed":false,"error":{"code":"not_descendant","message":"other is not a workspace this one started"}},
			{"id":"b","parent":null,"removed":true,"error":null}
		],"warnings":[]}`, e.stdout.String())
		var removed []string
		for _, c := range e.lifetime.RemoveTreeCalls() {
			removed = append(removed, c.Ws.ID)
		}
		assert.Equal(t, []string{"a", "b"}, removed)
	})
	t.Run("a refused workspace is reported by its id, not the argument", func(t *testing.T) {
		e := newMachineApp()
		requireCode(t, codeNotDescendant, run(t, e, &machineRmOptions{workspaces: []string{"/cfg/workspaces/plan"}}))
		assert.Equal(t, []string{"plan"}, rmResultIDs(t, e.stdout.String()))
	})
	t.Run("a workspace not found is its item's error", func(t *testing.T) {
		e := newMachineApp()
		requireCode(t, codeWorkspaceNotFound, run(t, e, &machineRmOptions{workspaces: []string{"nope", "a"}}))
		assert.Equal(t, []string{"nope", "a"}, rmResultIDs(t, e.stdout.String()))
	})
	t.Run("--all removes every direct child", func(t *testing.T) {
		e := newMachineApp()
		require.NoError(t, run(t, e, &machineRmOptions{all: true}))
		assert.Equal(t, []string{"a", "b"}, rmResultIDs(t, e.stdout.String()))
	})
	t.Run("--all without children removes nothing", func(t *testing.T) {
		e := newMachineApp()
		e.store.ContainingFunc = func(string) (workspace.Workspace, error) { return machineTree()[5], nil }
		require.NoError(t, run(t, e, &machineRmOptions{all: true}))
		assert.JSONEq(t, `{"workspaces":[],"warnings":[]}`, e.stdout.String())
	})
	t.Run("--keep-children gives the children to the caller", func(t *testing.T) {
		e := newMachineApp()
		require.NoError(t, run(t, e, &machineRmOptions{workspaces: []string{"a"}, keepChildren: true}))
		opts := e.lifetime.RemoveTreeCalls()[0].Opts
		assert.True(t, opts.KeepChildren)
		assert.Equal(t, "plan", opts.Caller)
	})
	t.Run("--dry-run is passed on", func(t *testing.T) {
		e := newMachineApp()
		require.NoError(t, run(t, e, &machineRmOptions{workspaces: []string{"a"}, dryRun: true}))
		assert.True(t, e.lifetime.RemoveTreeCalls()[0].Opts.DryRun)
	})
	t.Run("a descendant the removal pass just removed counts as removed", func(t *testing.T) {
		e := newMachineApp()
		e.app.swept = []lifetime.Removed{{Workspace: workspace.Workspace{ID: "c", Path: "/cfg/workspaces/c", Parent: "a"}, SessionID: "s-c"}}
		require.NoError(t, run(t, e, &machineRmOptions{workspaces: []string{"s-c"}}))
		assert.JSONEq(t, `{"workspaces":[{"id":"c","parent":null,"removed":true,"error":null}],"warnings":[]}`, e.stdout.String())
	})
	t.Run("one the removal pass removed that wasn't a descendant is refused", func(t *testing.T) {
		e := newMachineApp()
		e.app.swept = []lifetime.Removed{{Workspace: workspace.Workspace{ID: "c", Parent: "other"}, SessionID: "s-c"}}
		requireCode(t, codeNotDescendant, run(t, e, &machineRmOptions{workspaces: []string{"s-c"}}))
	})
	t.Run("outside a workspace, removes nothing", func(t *testing.T) {
		e := newMachineApp()
		e.outside()
		requireCode(t, codeOutsideWorkspace, run(t, e, &machineRmOptions{workspaces: []string{"a"}}))
		assert.Empty(t, e.lifetime.RemoveTreeCalls())
	})
	t.Run("workspaces that can't be listed", func(t *testing.T) {
		e := newMachineApp()
		e.store.ListFunc = func() ([]workspace.Workspace, error) { return nil, errTest }
		require.ErrorIs(t, run(t, e, &machineRmOptions{workspaces: []string{"a"}}), errTest)
		assert.Empty(t, e.lifetime.RemoveTreeCalls())
	})
}

// TestMachine_endToEnd runs each ws machine command as ws does, through
// the root command and execute, without --json.
func TestMachine_endToEnd(t *testing.T) {
	t.Run("every command fails with outside_workspace outside a workspace, as JSON", func(t *testing.T) {
		for _, args := range [][]string{
			{"machine", "spawn", "buff-bear", "--", "fix it"},
			{"machine", "children"},
			{"machine", "children", "--all"},
			{"machine", "rm", "a"},
			{"machine", "rm", "--all"},
		} {
			e := newMachineApp()
			e.outside()
			assert.Equal(t, exitUsage, runMachine(t, e, args...), args)
			assert.Empty(t, e.stdout.String(), args)
			assert.JSONEq(t, `{"error":{"code":"outside_workspace","message":"`+errOutsideWorkspace.Error()+`","details":null}}`, e.stderr.String(), args)
		}
	})
	t.Run("a usage error is JSON", func(t *testing.T) {
		e := newMachineApp()
		assert.Equal(t, exitUsage, runMachine(t, e, "machine", "spawn", "buff-bear"))
		assert.Contains(t, e.stderr.String(), `{"error":{"code":"usage"`)
	})
	t.Run("a flag error is JSON", func(t *testing.T) {
		e := newMachineApp()
		assert.Equal(t, exitUsage, runMachine(t, e, "machine", "rm", "--nope"))
		assert.Contains(t, e.stderr.String(), `{"error":{"code":"usage"`)
	})
	t.Run("children prints JSON", func(t *testing.T) {
		e := newMachineApp()
		require.Equal(t, exitOK, runMachine(t, e, "machine", "children"), e.stderr.String())
		assert.True(t, json.Valid(e.stdout.Bytes()), e.stdout.String())
	})
	t.Run("rm refuses a tree that a cycle of parents leads back to the caller", func(t *testing.T) {
		e := newMachineApp()
		cycle := []workspace.Workspace{{ID: "plan", Parent: "b"}, {ID: "b", Parent: "plan"}}
		e.store.ListFunc = func() ([]workspace.Workspace, error) { return cycle, nil }
		e.store.FindFunc = func(arg string) (workspace.Workspace, error) {
			for i := range cycle {
				if cycle[i].ID == arg {
					return cycle[i], nil
				}
			}
			return workspace.Workspace{}, workspace.ErrNotFound
		}
		e.store.ContainingFunc = func(string) (workspace.Workspace, error) { return cycle[0], nil }
		e.lifetime.RemoveTreeFunc = func(_ context.Context, ws *workspace.Workspace, opts lifetime.TreeOptions) ([]lifetime.TreeItem, error) {
			assert.Equal(t, "plan", opts.Caller)
			return []lifetime.TreeItem{{Workspace: *ws, Err: fmt.Errorf("%w: %s", lifetime.ErrCallerInTree, opts.Caller)}}, nil
		}
		assert.Equal(t, exitRefused, runMachine(t, e, "machine", "rm", "b"))
		assert.Contains(t, e.stdout.String(), `"code":"not_descendant"`)
	})
	t.Run("rm refuses with exit 4 and prints JSON", func(t *testing.T) {
		e := newMachineApp()
		assert.Equal(t, exitRefused, runMachine(t, e, "machine", "rm", "plan"))
		assert.Contains(t, e.stdout.String(), `"code":"not_descendant"`)
		assert.Empty(t, e.stderr.String())
	})
	t.Run("spawn prints JSON", func(t *testing.T) {
		e := newMachineApp()
		e.app.claude = &claudeClientMock{
			CheckFunc: func(context.Context) error { return nil },
			StartFunc: func(context.Context, claude.StartOptions) (claude.Started, error) {
				return claude.Started{ID: "e8cdf5d8", Idle: true}, nil
			},
		}
		lockPath := filepath.Join(t.TempDir(), "lock")
		e.store.CreateFunc = func(_ context.Context, opts workspace.CreateOptions) (workspace.Workspace, *lockfile.Lock, error) {
			lock, ok, err := lockfile.TryExclusiveLock(lockPath)
			require.NoError(t, err)
			require.True(t, ok)
			return workspace.Workspace{ID: "x", Path: "/cfg/workspaces/x", Parent: opts.Parent}, lock, nil
		}
		e.store.RecordSessionFunc = func(string, workspace.SessionKind, string) error { return nil }
		e.app.interactive = true
		require.Equal(t, exitOK, runMachine(t, e, "machine", "spawn", "--", "fix it"), e.stderr.String())
		assert.Contains(t, e.stdout.String(), `"parent":"plan"`)
	})
	t.Run("end to end: rm never removes the caller through a cycle of parents", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		plan := e.start(t, "plan", "11111111")
		e.setAgents(t, "["+agentJSON("11111111", plan)+"]")
		helper := e.start(t, "helper", "22222222")
		setParent(t, &e.paths, filepath.Base(plan), filepath.Base(helper))
		setParent(t, &e.paths, filepath.Base(helper), filepath.Base(plan))
		e.setAgents(t, "["+agentJSON("11111111", plan)+","+agentJSON("22222222", helper)+"]")
		e.stdout, e.stderr = &bytes.Buffer{}, &bytes.Buffer{}
		e.app = New("test", nil, e.stdout, e.stderr, e.configs, e.environ)
		e.app.claude = claude.New(slog.New(slog.NewJSONHandler(io.Discard, nil)), filepath.Join(e.scratch, "claude"), e.environ)
		e.app.getwd = func() (string, error) { return plan, nil }
		assert.Equal(t, exitRefused, e.app.Run(t.Context(), []string{"machine", "rm", filepath.Base(helper)}), "stderr: %s", e.stderr.String())
		assert.Contains(t, e.stdout.String(), `"code":"not_descendant"`)
		assert.DirExists(t, plan)
		assert.DirExists(t, helper)
		assert.Empty(t, e.calls(t))
	})
}

// setParent rewrites workspace id's record to give it parent, as a
// hand-edited record could.
func setParent(t *testing.T, paths *config.Paths, id, parent string) {
	t.Helper()
	path := filepath.Join(paths.State, id, "workspace.toml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	text, ok := strings.CutPrefix(string(data), "version = 1\n")
	require.True(t, ok, "record starts with its version: %s", data)
	text = fmt.Sprintf("version = 3\nparent = %q\n", parent) + text
	require.NoError(t, os.WriteFile(path, []byte(text), 0o600))
}
