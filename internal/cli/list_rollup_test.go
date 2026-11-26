package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/environment"
	"github.com/bobcob7/workspaces/internal/envscope"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/machine"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// remoteRollupWorkspace is a snapshot object for the top-level workspace id
// whose rollup is total steps, done of them done.
func remoteRollupWorkspace(id string, total, done int) string {
	return fmt.Sprintf(`{"id":%q,"name":null,"path":"/srv/workspaces/%s","created_at":"2026-10-19T10:00:00Z","session":null,"hidden":false,"parent":null,"repos":[{"id":"github.com/bobcob7/buff-bear","path":"repo","ref":"main","ref_kind":"branch"}],"review":null,"loop_run":null,"progress":null,"rollup":{"total":%d,"done":%d}}`,
		id, id, total, done)
}

// localPlanApp is a list App whose local plan has 4 steps, 1 done. With
// withBuild, plan has a child build of 3 steps, 2 done, hidden when hidden.
func localPlanApp(t *testing.T, withBuild, hidden bool) *mockedApp {
	t.Helper()
	plan := listedWorkspace("plan")
	build := listedWorkspace("build")
	build.Parent = "plan"
	build.Hidden = hidden
	entries := []lifetime.Entry{{Workspace: plan}}
	stored := []workspace.Workspace{plan}
	if withBuild {
		entries = append(entries, lifetime.Entry{Workspace: build})
		stored = append(stored, build)
	}
	e := newListApp(entries, nil)
	e.store.ListFunc = func() ([]workspace.Workspace, error) { return stored, nil }
	e.app.paths = config.NewPaths(t.TempDir())
	store := machine.NewStore(e.app.paths.State)
	require.NoError(t, store.WriteProgress("plan", "plan", &machine.Progress{Name: "ship", Total: 4, Done: 1, UpdatedAt: testNow()}))
	if withBuild {
		require.NoError(t, store.WriteProgress("plan", "build", &machine.Progress{Total: 3, Done: 2}))
	}
	return e
}

// linuxWithSnapshot enrolls linux-server with a snapshot of the items.
func linuxWithSnapshot(t *testing.T, e *mockedApp, items ...string) *environment.Record {
	t.Helper()
	rec := addLinuxServer(t, e.app)
	enrollEnvironment(t, e.app, rec)
	writeRemoteSnapshot(t, e.app, rec, testNow(), remoteWorkspaces(items...))
	return rec
}

// assertRollup checks r holds total steps with done of them done.
func assertRollup(t *testing.T, r *rollupJSON, total, done int) {
	t.Helper()
	require.NotNil(t, r)
	assert.Equal(t, total, r.Total)
	assert.Equal(t, done, r.Done)
}

// runRollupList is ws list --json with opts, as the entries it prints.
func runRollupList(t *testing.T, e *mockedApp, opts listOptions) listJSON {
	t.Helper()
	e.stdout.Reset()
	e.app.global.json = true
	require.NoError(t, e.app.runList(t.Context(), &opts))
	var got listJSON
	require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &got))
	return got
}

func TestApp_mergeRollups(t *testing.T) {
	t.Run("without a ledger nothing is read", func(t *testing.T) {
		e := localPlanApp(t, false, false)
		linuxWithSnapshot(t, e, remoteRollupWorkspace("ip-1", 2000000000, 0))
		got := runScopeList(t, e)
		assertRollup(t, entryOf(t, got, "local", "plan").Rollup, 4, 1)
		assert.Empty(t, got.Warnings)
	})
	t.Run("a local parent's rollup includes its remote child", func(t *testing.T) {
		e := localPlanApp(t, false, false)
		rec := linuxWithSnapshot(t, e, remoteRollupWorkspace("ip-1", 2, 2))
		putLedgerEntry(t, e.app, &envscope.Entry{Environment: rec.ID, Workspace: "ip-1", Scope: []string{envscope.Local, rec.ID}, Parent: &envscope.Parent{Environment: envscope.Local, Workspace: "plan"}})
		got := runScopeList(t, e)
		assertRollup(t, entryOf(t, got, "local", "plan").Rollup, 6, 3)
	})
	t.Run("a local grandparent's rollup includes a remote child of its local child", func(t *testing.T) {
		e := localPlanApp(t, true, false)
		rec := linuxWithSnapshot(t, e, remoteRollupWorkspace("ip-1", 2, 2))
		putLedgerEntry(t, e.app, &envscope.Entry{Environment: rec.ID, Workspace: "ip-1", Scope: []string{envscope.Local, rec.ID}, Parent: &envscope.Parent{Environment: envscope.Local, Workspace: "build"}})
		got := runScopeList(t, e)
		assertRollup(t, entryOf(t, got, "local", "plan").Rollup, 9, 5)
	})
	t.Run("a remote child's rollup includes its own remote child", func(t *testing.T) {
		e := newRemoteListApp(t, nil)
		linux := linuxWithSnapshot(t, e, remoteRollupWorkspace("ip-1", 2, 1))
		win := addEnvironment(t, e.app, "win-rig")
		enrollEnvironment(t, e.app, win)
		writeRemoteSnapshot(t, e.app, win, testNow(), remoteWorkspaces(remoteRollupWorkspace("ip-2", 1, 0)))
		putLedgerEntry(t, e.app, &envscope.Entry{Environment: win.ID, Workspace: "ip-2", Scope: []string{envscope.Local, win.ID}, Parent: &envscope.Parent{Environment: linux.ID, Workspace: "ip-1"}})
		got := runScopeList(t, e)
		assertRollup(t, entryOf(t, got, "linux-server", "ip-1").Rollup, 3, 1)
	})
	t.Run("a remote child counts once", func(t *testing.T) {
		e := localPlanApp(t, false, false)
		rec := linuxWithSnapshot(t, e, remoteRollupWorkspace("ip-1", 2, 2))
		putLedgerEntry(t, e.app, &envscope.Entry{Environment: rec.ID, Workspace: "ip-1", Scope: []string{envscope.Local, rec.ID}, Parent: &envscope.Parent{Environment: envscope.Local, Workspace: "plan"}})
		assertRollup(t, entryOf(t, runScopeList(t, e), "local", "plan").Rollup, 6, 3)
		got := runRollupList(t, e, listOptions{envs: []string{"linux-server"}})
		assertRollup(t, entryOf(t, got, "linux-server", "ip-1").Rollup, 2, 2)
	})
	t.Run("an offline environment's last snapshot still counts", func(t *testing.T) {
		e := localPlanApp(t, false, false)
		rec := linuxWithSnapshot(t, e, remoteRollupWorkspace("ip-1", 2, 2))
		putLedgerEntry(t, e.app, &envscope.Entry{Environment: rec.ID, Workspace: "ip-1", Scope: []string{envscope.Local, rec.ID}, Parent: &envscope.Parent{Environment: envscope.Local, Workspace: "plan"}})
		got := runScopeList(t, e)
		assert.Equal(t, "offline", entryOf(t, got, "linux-server", "ip-1").EnvironmentStatus)
		assertRollup(t, entryOf(t, got, "local", "plan").Rollup, 6, 3)
	})
	t.Run("a child missing from its snapshot counts nothing", func(t *testing.T) {
		e := localPlanApp(t, false, false)
		rec := linuxWithSnapshot(t, e, remoteRollupWorkspace("ip-1", 2, 2))
		putLedgerEntry(t, e.app, &envscope.Entry{Environment: rec.ID, Workspace: "ip-9", Scope: []string{envscope.Local, rec.ID}, Parent: &envscope.Parent{Environment: envscope.Local, Workspace: "plan"}})
		got := runScopeList(t, e)
		assertRollup(t, entryOf(t, got, "local", "plan").Rollup, 4, 1)
	})
	t.Run("a removed environment counts nothing", func(t *testing.T) {
		e := localPlanApp(t, false, false)
		win := addEnvironment(t, e.app, "win-rig")
		putLedgerEntry(t, e.app, &envscope.Entry{Environment: win.ID, Workspace: "ip-3", Scope: []string{envscope.Local, win.ID}, Parent: &envscope.Parent{Environment: envscope.Local, Workspace: "plan"}})
		got := runScopeList(t, e)
		assertRollup(t, entryOf(t, got, "local", "plan").Rollup, 4, 1)
	})
	t.Run("an out-of-range snapshot rollup is a warning and counts nothing", func(t *testing.T) {
		e := localPlanApp(t, false, false)
		rec := linuxWithSnapshot(t, e, remoteRollupWorkspace("ip-1", 5, 6))
		putLedgerEntry(t, e.app, &envscope.Entry{Environment: rec.ID, Workspace: "ip-1", Scope: []string{envscope.Local, rec.ID}, Parent: &envscope.Parent{Environment: envscope.Local, Workspace: "plan"}})
		got := runScopeList(t, e)
		assertRollup(t, entryOf(t, got, "local", "plan").Rollup, 4, 1)
		assert.Nil(t, entryOf(t, got, "linux-server", "ip-1").Rollup)
		assert.Contains(t, got.Warnings, "ignoring the rollup of ip-1 on linux-server: out of range")
	})
	t.Run("an unreadable ledger leaves rollups local with one warning", func(t *testing.T) {
		e := localPlanApp(t, false, false)
		linuxWithSnapshot(t, e, remoteRollupWorkspace("ip-1", 2, 2))
		require.NoError(t, os.WriteFile(filepath.Join(e.app.paths.Environments, "scopes.json"), []byte("{"), 0o600))
		got := runScopeList(t, e)
		assertRollup(t, entryOf(t, got, "local", "plan").Rollup, 4, 1)
		assert.Equal(t, 1, strings.Count(strings.Join(got.Warnings, "\n"), "couldn't read the scope ledger"))
	})
	t.Run("hidden and --env-filtered entries still count", func(t *testing.T) {
		e := localPlanApp(t, true, true)
		rec := linuxWithSnapshot(t, e, remoteRollupWorkspace("ip-1", 2, 2))
		putLedgerEntry(t, e.app, &envscope.Entry{Environment: rec.ID, Workspace: "ip-1", Scope: []string{envscope.Local, rec.ID}, Parent: &envscope.Parent{Environment: envscope.Local, Workspace: "build"}})
		assertRollup(t, entryOf(t, runScopeList(t, e), "local", "plan").Rollup, 9, 5)
		got := runRollupList(t, e, listOptions{envs: []string{"linux-server"}})
		assertRollup(t, entryOf(t, got, "linux-server", "ip-1").Rollup, 2, 2)
	})
}
