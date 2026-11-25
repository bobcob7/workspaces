package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/environment"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/envscope"
	"github.com/bobcob7/workspaces/internal/lifetime"
)

// removedEnvID is an environment id the store doesn't hold.
const removedEnvID = "env-deadbeef"

// runScopeList is ws list --json over every environment of app, its entries
// and warnings.
func runScopeList(t *testing.T, e *mockedApp) listJSON {
	t.Helper()
	e.app.global.json = true
	require.NoError(t, e.app.runList(t.Context(), &listOptions{}))
	var got listJSON
	require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &got))
	return got
}

// putLedgerEntry writes entry into app's scope ledger.
func putLedgerEntry(t *testing.T, app *App, entry *envscope.Entry) {
	t.Helper()
	entry.CreatedAt = testNow()
	require.NoError(t, envscope.NewLedger(app.paths.Environments).Put(t.Context(), entry))
}

// writeLocalScope writes the scope file of workspace id on app's machine.
func writeLocalScope(t *testing.T, app *App, id string, refs ...envscope.Ref) {
	t.Helper()
	scope, err := envscope.New(localRef(), refs)
	require.NoError(t, err)
	require.NoError(t, envscope.Write(app.paths.State, id, envscope.FileFor(scope, "granted", nil, testNow())))
}

// remoteChildSetup is an enrolled environment linux-server with one remote
// workspace ip-1 in its snapshot, which the ledger can link.
func remoteChildSetup(t *testing.T) (*mockedApp, environment.Record) {
	t.Helper()
	e := newRemoteListApp(t, nil)
	rec := addLinuxServer(t, e.app)
	enrollEnvironment(t, e.app, rec)
	writeRemoteSnapshot(t, e.app, rec, testNow(), remoteWorkspaces(remoteWorkspace("ip-1", "github.com/bobcob7/buff-bear", "", false)))
	return e, *rec
}

// entryOf is the entry of ws list --json for workspace id on environment env.
func entryOf(t *testing.T, got listJSON, env, id string) listEntryJSON {
	t.Helper()
	for i := range got.Workspaces {
		if got.Workspaces[i].Environment == env && deref(got.Workspaces[i].ID) == id {
			return got.Workspaces[i]
		}
	}
	require.Failf(t, "no entry", "no %s entry for %s", env, id)
	return listEntryJSON{}
}

func TestApp_annotateScopes(t *testing.T) {
	t.Run("a remote child gets scope_parent local from the ledger", func(t *testing.T) {
		e, rec := remoteChildSetup(t)
		putLedgerEntry(t, e.app, &envscope.Entry{Environment: rec.ID, Workspace: "ip-1", Scope: []string{envscope.Local, rec.ID}, Parent: &envscope.Parent{Environment: envscope.Local, Workspace: "plan-1"}})
		got := runScopeList(t, e)
		assert.Equal(t, &scopeParentJSON{Environment: "local", Workspace: "plan-1"}, entryOf(t, got, "linux-server", "ip-1").ScopeParent)
	})
	t.Run("a remote-to-remote parent is named by its environment's name", func(t *testing.T) {
		e, rec := remoteChildSetup(t)
		parent := addEnvironment(t, e.app, "win-rig")
		enrollEnvironment(t, e.app, parent)
		putLedgerEntry(t, e.app, &envscope.Entry{Environment: rec.ID, Workspace: "ip-1", Scope: []string{envscope.Local, rec.ID}, Parent: &envscope.Parent{Environment: parent.ID, Workspace: "plan-2"}})
		got := runScopeList(t, e)
		assert.Equal(t, &scopeParentJSON{Environment: "win-rig", Workspace: "plan-2"}, entryOf(t, got, "linux-server", "ip-1").ScopeParent)
	})
	t.Run("a person grant has scope but no scope_parent", func(t *testing.T) {
		e, rec := remoteChildSetup(t)
		putLedgerEntry(t, e.app, &envscope.Entry{Environment: rec.ID, Workspace: "ip-1", Scope: []string{envscope.Local, rec.ID}})
		got := entryOf(t, runScopeList(t, e), "linux-server", "ip-1")
		assert.Nil(t, got.ScopeParent)
		assert.Equal(t, []envproto.ScopeEnv{
			{Name: "linux-server", ID: rec.ID, State: "offline", Current: true},
			{Name: "local", ID: envscope.Local, State: "online", Current: false},
		}, got.Scope)
	})
	t.Run("snapshot scope_parent and scope are ignored", func(t *testing.T) {
		e := newRemoteListApp(t, nil)
		rec := addLinuxServer(t, e.app)
		enrollEnvironment(t, e.app, rec)
		hostile := strings.TrimSuffix(remoteWorkspace("ip-1", "github.com/bobcob7/buff-bear", "", false), "}") +
			`,"scope_parent":{"environment":"local","workspace":"plan-1"},"scope":[{"name":"local","id":"local","state":"online","current":false},{"name":"x","id":"env-1a2b3c4d","state":"online","current":false}]}`
		writeRemoteSnapshot(t, e.app, rec, testNow(), remoteWorkspaces(hostile))
		got := entryOf(t, runScopeList(t, e), "linux-server", "ip-1")
		assert.Nil(t, got.ScopeParent)
		assert.Nil(t, got.Scope)
	})
	t.Run("a local scope file gives scope with states, current first", func(t *testing.T) {
		e := newRemoteListApp(t, []lifetime.Entry{{Workspace: listedWorkspace("ws-a")}})
		rec := addLinuxServer(t, e.app)
		enrollEnvironment(t, e.app, rec)
		writeLocalScope(t, e.app, "ws-a", envscope.Ref{ID: rec.ID, Name: rec.Name}, envscope.Ref{ID: removedEnvID, Name: "old-rig"})
		got := entryOf(t, runScopeList(t, e), "local", "ws-a")
		assert.Equal(t, []envproto.ScopeEnv{
			{Name: "local", ID: envscope.Local, State: "online", Current: true},
			{Name: "linux-server", ID: rec.ID, State: "offline", Current: false},
			{Name: "old-rig", ID: removedEnvID, State: "removed", Current: false},
		}, got.Scope)
	})
	t.Run("a scope of only its own environment is null", func(t *testing.T) {
		e, rec := remoteChildSetup(t)
		putLedgerEntry(t, e.app, &envscope.Entry{Environment: rec.ID, Workspace: "ip-1", Scope: []string{rec.ID}})
		assert.Nil(t, entryOf(t, runScopeList(t, e), "linux-server", "ip-1").Scope)
	})
	t.Run("an invalid scope file is a warning and null", func(t *testing.T) {
		e := newRemoteListApp(t, []lifetime.Entry{{Workspace: listedWorkspace("ws-a")}})
		dir := filepath.Join(e.app.paths.State, "ws-a")
		require.NoError(t, os.MkdirAll(dir, 0o700))
		require.NoError(t, os.WriteFile(envscope.Path(e.app.paths.State, "ws-a"), []byte("{"), 0o600))
		got := runScopeList(t, e)
		assert.Nil(t, entryOf(t, got, "local", "ws-a").Scope)
		require.Len(t, got.Warnings, 1)
		assert.True(t, strings.HasPrefix(got.Warnings[0], "ignoring the scope of ws-a: "), got.Warnings[0])
	})
	t.Run("an unreadable ledger is a warning and null", func(t *testing.T) {
		e, _ := remoteChildSetup(t)
		require.NoError(t, os.WriteFile(filepath.Join(e.app.paths.Environments, "scopes.json"), []byte("{"), 0o600))
		got := runScopeList(t, e)
		entry := entryOf(t, got, "linux-server", "ip-1")
		assert.Nil(t, entry.ScopeParent)
		assert.Nil(t, entry.Scope)
		require.Len(t, got.Warnings, 1)
		assert.True(t, strings.HasPrefix(got.Warnings[0], "couldn't read the scope ledger: "), got.Warnings[0])
	})
	t.Run("without a ledger or scope files nothing dials the daemon", func(t *testing.T) {
		e, _ := remoteChildSetup(t)
		ln := listenDaemon(t, e.app)
		local, remote := listEntryJSON{Environment: localEnvironment}, listEntryJSON{Environment: "linux-server"}
		local.ID, remote.ID = new("ws-a"), new("ip-1")
		entries := []listEntryJSON{local, remote}
		e.app.annotateScopes(t.Context(), entries)
		assert.Nil(t, entries[0].Scope)
		assert.Nil(t, entries[1].ScopeParent)
		assert.Error(t, acceptDialled(t, ln))
	})
	t.Run("a removed parent environment gives a null scope_parent", func(t *testing.T) {
		e, rec := remoteChildSetup(t)
		putLedgerEntry(t, e.app, &envscope.Entry{Environment: rec.ID, Workspace: "ip-1", Scope: []string{envscope.Local, rec.ID}, Parent: &envscope.Parent{Environment: removedEnvID, Workspace: "plan-3"}})
		assert.Nil(t, entryOf(t, runScopeList(t, e), "linux-server", "ip-1").ScopeParent)
	})
}
