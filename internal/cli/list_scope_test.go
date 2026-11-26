package cli

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/envagent"
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

// enrollThisMachine makes app's machine the enrolled environment rec, by
// saving the agent identity it would have after an enroll.
func enrollThisMachine(t *testing.T, app *App, rec *environment.Record) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: rec.Name},
		NotBefore:             testNow().Add(-time.Hour),
		NotAfter:              testNow().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	id := &envagent.Identity{
		ID: rec.ID, Name: rec.Name, Broker: "ws.example.com:8883", ServerName: "ws.example.com",
		EnrolledAt: testNow(), Key: key, Cert: cert, Roots: []*x509.Certificate{cert},
	}
	require.NoError(t, envagent.NewFiles(app.paths.Environment).Save(id))
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
	t.Run("annotateScopes clears scope_parent and scope it is handed", func(t *testing.T) {
		e, _ := remoteChildSetup(t)
		preset := func(id, env string) listEntryJSON {
			return listEntryJSON{
				ID:          new(id),
				Environment: env,
				ScopeParent: &scopeParentJSON{Environment: "local", Workspace: "plan-1"},
				Scope:       []envproto.ScopeEnv{{Name: "x", ID: "env-1a2b3c4d", State: "online"}},
			}
		}
		noID := preset("ws-b", localEnvironment)
		noID.ID = nil
		entries := []listEntryJSON{preset("ws-a", localEnvironment), preset("ip-1", "linux-server"), noID}
		e.app.annotateScopes(t.Context(), entries, nil)
		for i := range entries {
			assert.Nil(t, entries[i].ScopeParent, "entry %d", i)
			assert.Nil(t, entries[i].Scope, "entry %d", i)
		}
	})
	t.Run("a local scope file gives scope with states, current first", func(t *testing.T) {
		e := newRemoteListApp(t, []lifetime.Entry{{Workspace: listedWorkspace("ws-a")}})
		rec := addLinuxServer(t, e.app)
		enrollEnvironment(t, e.app, rec)
		enrollThisMachine(t, e.app, rec)
		self := envscope.Ref{ID: rec.ID, Name: rec.Name}
		scope, err := envscope.New(self, []envscope.Ref{{ID: envscope.Local, Name: envscope.Local}, {ID: removedEnvID, Name: "aaa-rig"}})
		require.NoError(t, err)
		require.NoError(t, envscope.Write(e.app.paths.State, "ws-a", envscope.FileFor(scope, "granted", nil, testNow())))
		got := entryOf(t, runScopeList(t, e), "local", "ws-a")
		assert.Equal(t, []envproto.ScopeEnv{
			{Name: "linux-server", ID: rec.ID, State: "offline", Current: true},
			{Name: "aaa-rig", ID: removedEnvID, State: "removed", Current: false},
			{Name: "local", ID: envscope.Local, State: "online", Current: false},
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
		e.app.annotateScopes(t.Context(), entries, nil)
		assert.Nil(t, entries[0].Scope)
		assert.Nil(t, entries[1].ScopeParent)
		assert.Error(t, acceptDialled(t, ln))
	})
	t.Run("a removed parent environment gives a null scope_parent", func(t *testing.T) {
		e, rec := remoteChildSetup(t)
		putLedgerEntry(t, e.app, &envscope.Entry{Environment: rec.ID, Workspace: "ip-1", Scope: []string{envscope.Local, rec.ID}, Parent: &envscope.Parent{Environment: removedEnvID, Workspace: "plan-3"}})
		assert.Nil(t, entryOf(t, runScopeList(t, e), "linux-server", "ip-1").ScopeParent)
	})
	t.Run("a remote entry whose environment is unknown gets null scope_parent and scope", func(t *testing.T) {
		e, rec := remoteChildSetup(t)
		putLedgerEntry(t, e.app, &envscope.Entry{Environment: rec.ID, Workspace: "ip-1", Scope: []string{envscope.Local, rec.ID}, Parent: &envscope.Parent{Environment: envscope.Local, Workspace: "plan-1"}})
		putLedgerEntry(t, e.app, &envscope.Entry{Environment: "env-1a2b3c4d", Workspace: "ip-1", Scope: []string{envscope.Local, "env-1a2b3c4d"}, Parent: &envscope.Parent{Environment: envscope.Local, Workspace: "plan-1"}})
		entries := []listEntryJSON{{ID: new("ip-1"), Environment: "env-1a2b3c4d"}}
		e.app.annotateScopes(t.Context(), entries, nil)
		assert.Nil(t, entries[0].ScopeParent)
		assert.Nil(t, entries[0].Scope)
	})
	t.Run("a ledger entry for another workspace id isn't used", func(t *testing.T) {
		e, rec := remoteChildSetup(t)
		putLedgerEntry(t, e.app, &envscope.Entry{Environment: rec.ID, Workspace: "ip-2", Scope: []string{envscope.Local, rec.ID}, Parent: &envscope.Parent{Environment: envscope.Local, Workspace: "plan-1"}})
		got := entryOf(t, runScopeList(t, e), "linux-server", "ip-1")
		assert.Nil(t, got.ScopeParent)
		assert.Nil(t, got.Scope)
	})
	t.Run("a remote ledger scope of exactly one other id is null", func(t *testing.T) {
		e, rec := remoteChildSetup(t)
		putLedgerEntry(t, e.app, &envscope.Entry{Environment: rec.ID, Workspace: "ip-1", Scope: []string{envscope.Local}})
		assert.Nil(t, entryOf(t, runScopeList(t, e), "linux-server", "ip-1").Scope)
	})
	t.Run("an unreadable identity is a warning and local is self", func(t *testing.T) {
		e := newRemoteListApp(t, []lifetime.Entry{{Workspace: listedWorkspace("ws-a")}})
		require.NoError(t, os.MkdirAll(e.app.paths.Environment, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(e.app.paths.Environment, "agent.toml"), []byte("{"), 0o600))
		got := runScopeList(t, e)
		assert.Nil(t, entryOf(t, got, "local", "ws-a").Scope)
		require.Len(t, got.Warnings, 1)
		assert.True(t, strings.HasPrefix(got.Warnings[0], "ignoring this machine's identity: "), got.Warnings[0])
	})
	t.Run("ws list --json dials the daemon once for listing and scope states", func(t *testing.T) {
		e, rec := remoteChildSetup(t)
		putLedgerEntry(t, e.app, &envscope.Entry{Environment: rec.ID, Workspace: "ip-1", Scope: []string{envscope.Local, rec.ID}})
		ln := listenDaemon(t, e.app)
		e.app.global.json = true
		done := make(chan error, 1)
		go func() { done <- e.app.runList(t.Context(), &listOptions{}) }()
		require.NoError(t, acceptDialled(t, ln))
		require.Error(t, acceptDialled(t, ln))
		require.NoError(t, <-done)
	})
	t.Run("a presence map handed in is used and the daemon isn't dialled", func(t *testing.T) {
		e := newRemoteListApp(t, []lifetime.Entry{{Workspace: listedWorkspace("ws-a")}})
		rec := addLinuxServer(t, e.app)
		enrollEnvironment(t, e.app, rec)
		enrollThisMachine(t, e.app, rec)
		self := envscope.Ref{ID: rec.ID, Name: rec.Name}
		scope, err := envscope.New(self, []envscope.Ref{{ID: envscope.Local, Name: envscope.Local}})
		require.NoError(t, err)
		require.NoError(t, envscope.Write(e.app.paths.State, "ws-a", envscope.FileFor(scope, "granted", nil, testNow())))
		ln := listenDaemon(t, e.app)
		entries := []listEntryJSON{{ID: new("ws-a"), Environment: localEnvironment}}
		e.app.annotateScopes(t.Context(), entries, map[string]string{"linux-server": "online"})
		require.Len(t, entries[0].Scope, 2)
		assert.Equal(t, "online", entries[0].Scope[0].State)
		assert.Error(t, acceptDialled(t, ln))
	})
}

func TestApp_fillScopeParents(t *testing.T) {
	t.Run("sets the ledger parent of a remote child and resets the rest", func(t *testing.T) {
		e, rec := remoteChildSetup(t)
		putLedgerEntry(t, e.app, &envscope.Entry{Environment: rec.ID, Workspace: "ip-1", Scope: []string{envscope.Local, rec.ID}, Parent: &envscope.Parent{Environment: envscope.Local, Workspace: "plan-1"}})
		entries := []listEntryJSON{
			{listWorkspaceJSON: listWorkspaceJSON{ID: new("ip-1")}, Environment: rec.Name},
			{listWorkspaceJSON: listWorkspaceJSON{ID: new("plan-1")}, Environment: localEnvironment, ScopeParent: &scopeParentJSON{Environment: localEnvironment, Workspace: "stale"}},
		}
		e.app.fillScopeParents(entries)
		assert.Equal(t, &scopeParentJSON{Environment: localEnvironment, Workspace: "plan-1"}, entries[0].ScopeParent)
		assert.Nil(t, entries[1].ScopeParent)
	})
	t.Run("never dials the daemon", func(t *testing.T) {
		e, rec := remoteChildSetup(t)
		putLedgerEntry(t, e.app, &envscope.Entry{Environment: rec.ID, Workspace: "ip-1", Scope: []string{envscope.Local, rec.ID}, Parent: &envscope.Parent{Environment: envscope.Local, Workspace: "plan-1"}})
		ln := listenDaemon(t, e.app)
		e.app.fillScopeParents([]listEntryJSON{{listWorkspaceJSON: listWorkspaceJSON{ID: new("ip-1")}, Environment: rec.Name}})
		assert.Error(t, acceptDialled(t, ln), "the daemon was dialled")
	})
}
