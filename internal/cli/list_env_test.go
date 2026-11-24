package cli

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/environment"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/repo"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// newRemoteApp is an App whose environments live in a temp config directory.
func newRemoteApp(t *testing.T) *App {
	t.Helper()
	app, _, _ := newTestApp()
	app.paths = config.NewPaths(t.TempDir())
	return app
}

// newRemoteListApp is a list App whose environments live in a temp config
// directory, with the local entries of newListApp.
func newRemoteListApp(t *testing.T, entries []lifetime.Entry) *mockedApp {
	t.Helper()
	e := newListApp(entries, nil)
	e.app.paths = config.NewPaths(t.TempDir())
	return e
}

// addLinuxServer adds a pending environment called linux-server.
func addLinuxServer(t *testing.T, app *App) *environment.Record {
	t.Helper()
	return addEnvironment(t, app, "linux-server")
}

// addEnvironment adds a pending environment called name.
func addEnvironment(t *testing.T, app *App, name string) *environment.Record {
	t.Helper()
	rec, _, _, err := app.envStore().Add(t.Context(), name, "127.0.0.1:8884")
	require.NoError(t, err)
	return &rec
}

// notFound is a workspace store's Find for an id no workspace has.
func notFound(arg string) (workspace.Workspace, error) {
	return workspace.Workspace{}, fmt.Errorf("%w: %q", workspace.ErrNotFound, arg)
}

// enrollEnvironment makes the pending environment rec enrolled, as its
// enrollment would.
func enrollEnvironment(t *testing.T, app *App, rec *environment.Record) {
	t.Helper()
	path := filepath.Join(app.paths.Environments, rec.ID+".toml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	enrolled := strings.Replace(string(data), `state = "pending"`, `state = "enrolled"`, 1)
	require.NoError(t, os.WriteFile(path, []byte(enrolled), 0o600))
}

// writeRemoteSnapshot stores rec's snapshot, received at receivedAt, with
// workspaces as the JSON array the remote's ws list printed.
func writeRemoteSnapshot(t *testing.T, app *App, rec *environment.Record, receivedAt time.Time, workspaces string) {
	t.Helper()
	writeRemoteSnapshotWithAgents(t, app, rec, receivedAt, workspaces, "[]")
}

// writeRemoteSnapshotWithAgents is writeRemoteSnapshot with agents as the
// JSON array of the snapshot's agents.
func writeRemoteSnapshotWithAgents(t *testing.T, app *App, rec *environment.Record, receivedAt time.Time, workspaces, agents string) {
	t.Helper()
	at := receivedAt.Format(time.RFC3339)
	data := fmt.Sprintf(`{"version":1,"id":%q,"name":%q,"received_at":%q,"snapshot":{"at":%q,"protocol":1,"workspaces":%s,"agents":%s}}`,
		rec.ID, rec.Name, at, at, workspaces, agents)
	require.NoError(t, app.envStore().WriteSnapshot(rec.Name, []byte(data)))
}

// remoteWorkspace is one entry of a snapshot's workspaces: workspace id with
// repo repoID, started from parent (or none), hidden or not.
func remoteWorkspace(id, repoID, parent string, hidden bool) string {
	parentJSON := "null"
	if parent != "" {
		parentJSON = fmt.Sprintf("%q", parent)
	}
	return fmt.Sprintf(`{"id":%q,"name":null,"path":"/srv/workspaces/%s","created_at":"2026-10-19T10:00:00Z","session":null,"hidden":%t,"parent":%s,"repos":[{"id":%q,"path":"repo","ref":"main","ref_kind":"branch"}],"review":null,"loop_run":null}`,
		id, id, hidden, parentJSON, repoID)
}

// remoteWorkspaces is a snapshot's workspaces array of items.
func remoteWorkspaces(items ...string) string {
	return "[" + strings.Join(items, ",") + "]"
}

func TestApp_selectEnvironments(t *testing.T) {
	t.Run("none is local", func(t *testing.T) {
		app := newRemoteApp(t)
		got, err := app.selectEnvironments(nil)
		require.NoError(t, err)
		assert.Equal(t, []string{"local"}, got)
	})
	t.Run("local and a known name keep the order given and drop duplicates", func(t *testing.T) {
		app := newRemoteApp(t)
		addLinuxServer(t, app)
		got, err := app.selectEnvironments([]string{"linux-server", "local", "linux-server"})
		require.NoError(t, err)
		assert.Equal(t, []string{"linux-server", "local"}, got)
	})
	t.Run("an unknown name is environment_unknown", func(t *testing.T) {
		app := newRemoteApp(t)
		_, err := app.selectEnvironments([]string{"nowhere"})
		assert.Equal(t, codeEnvironmentUnknown, agentErrorCode(err))
		assert.ErrorIs(t, err, environment.ErrUnknown)
	})
}

func TestApp_listedEnvironments(t *testing.T) {
	t.Run("plain ws list lists every enrolled environment after local, by name", func(t *testing.T) {
		app := newRemoteApp(t)
		enrollEnvironment(t, app, addEnvironment(t, app, "linux-server"))
		enrollEnvironment(t, app, addEnvironment(t, app, "build-box"))
		got, err := app.listedEnvironments(&listOptions{})
		require.NoError(t, err)
		assert.Equal(t, []string{"local", "build-box", "linux-server"}, got)
	})
	t.Run("a pending environment adds nothing", func(t *testing.T) {
		app := newRemoteApp(t)
		addLinuxServer(t, app)
		got, err := app.listedEnvironments(&listOptions{})
		require.NoError(t, err)
		assert.Equal(t, []string{"local"}, got)
	})
	t.Run("-q lists local ids only", func(t *testing.T) {
		app := newRemoteApp(t)
		enrollEnvironment(t, app, addLinuxServer(t, app))
		got, err := app.listedEnvironments(&listOptions{idsOnly: true})
		require.NoError(t, err)
		assert.Equal(t, []string{"local"}, got)
	})
	t.Run("-q with --env lists that environment", func(t *testing.T) {
		app := newRemoteApp(t)
		enrollEnvironment(t, app, addLinuxServer(t, app))
		got, err := app.listedEnvironments(&listOptions{idsOnly: true, envs: []string{"linux-server"}})
		require.NoError(t, err)
		assert.Equal(t, []string{"linux-server"}, got)
	})
	t.Run("--parent finds a remote parent's environment", func(t *testing.T) {
		e := newRemoteListApp(t, nil)
		e.store.FindFunc = notFound
		rec := addLinuxServer(t, e.app)
		enrollEnvironment(t, e.app, rec)
		writeRemoteSnapshot(t, e.app, rec, testNow(), remoteWorkspaces(remoteWorkspace("deps", "github.com/bobcob7/buff-bear", "", false)))
		got, err := e.app.listedEnvironments(&listOptions{parent: "deps"})
		require.NoError(t, err)
		assert.Equal(t, []string{"linux-server"}, got)
	})
	t.Run("--parent found in no environment is workspace_not_found", func(t *testing.T) {
		e := newRemoteListApp(t, nil)
		e.store.FindFunc = notFound
		enrollEnvironment(t, e.app, addLinuxServer(t, e.app))
		_, err := e.app.listedEnvironments(&listOptions{parent: "gone"})
		assert.Equal(t, codeWorkspaceNotFound, agentErrorCode(err))
	})
	t.Run("--parent nosuch with no environment enrolled is the base message", func(t *testing.T) {
		e := newRemoteListApp(t, nil)
		e.store.FindFunc = notFound
		_, err := e.app.listedEnvironments(&listOptions{parent: "nosuch"})
		require.EqualError(t, err, fmt.Sprintf(`%v: "nosuch"`, workspace.ErrNotFound))
		assert.ErrorIs(t, err, workspace.ErrNotFound)
	})
	t.Run("--parent in two environments is workspace_not_found", func(t *testing.T) {
		e := newRemoteListApp(t, nil)
		e.store.FindFunc = notFound
		for _, name := range []string{"box", "linux-server"} {
			rec := addEnvironment(t, e.app, name)
			enrollEnvironment(t, e.app, rec)
			writeRemoteSnapshot(t, e.app, rec, testNow(), remoteWorkspaces(remoteWorkspace("deps", "github.com/bobcob7/buff-bear", "", false)))
		}
		_, err := e.app.listedEnvironments(&listOptions{parent: "deps"})
		assert.Equal(t, codeWorkspaceNotFound, agentErrorCode(err))
	})
	t.Run("no daemon is dialled with no environment", func(t *testing.T) {
		e := newRemoteListApp(t, nil)
		ln := listenDaemon(t, e.app)
		require.NoError(t, e.app.runList(t.Context(), &listOptions{}))
		assert.Error(t, acceptDialled(t, ln), "the daemon was dialled")
	})
	t.Run("an enrolled environment dials the daemon", func(t *testing.T) {
		e := newRemoteListApp(t, nil)
		enrollEnvironment(t, e.app, addLinuxServer(t, e.app))
		ln := listenDaemon(t, e.app)
		require.NoError(t, e.app.runList(t.Context(), &listOptions{}))
		assert.NoError(t, acceptDialled(t, ln), "the daemon was not dialled")
	})
}

func TestApp_runListEnvironments(t *testing.T) {
	t.Run("a local and a remote workspace with the same id are two rows", func(t *testing.T) {
		e := newRemoteListApp(t, []lifetime.Entry{{Workspace: listedWorkspace("api-bump")}})
		rec := addLinuxServer(t, e.app)
		enrollEnvironment(t, e.app, rec)
		writeRemoteSnapshot(t, e.app, rec, testNow(), remoteWorkspaces(remoteWorkspace("api-bump", "github.com/bobcob7/buff-bear", "", false)))
		require.NoError(t, e.app.runList(t.Context(), &listOptions{}))
		assert.Equal(t, ""+
			"ID         ENV                      NAME   CREATED   SESSION   REPOS\n"+
			"api-bump   local                    -      2h ago    -         buff-bear@main\n"+
			"api-bump   linux-server (offline)   -      2h ago    -         repo@main\n", e.stdout.String())
	})
	t.Run("--json has one entry per environment, told apart by environment", func(t *testing.T) {
		e := newRemoteListApp(t, []lifetime.Entry{{Workspace: listedWorkspace("api-bump")}})
		rec := addLinuxServer(t, e.app)
		enrollEnvironment(t, e.app, rec)
		writeRemoteSnapshot(t, e.app, rec, testNow(), remoteWorkspaces(remoteWorkspace("api-bump", "github.com/bobcob7/buff-bear", "", false)))
		e.app.global.json = true
		require.NoError(t, e.app.runList(t.Context(), &listOptions{}))
		var got listJSON
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &got))
		require.Len(t, got.Workspaces, 2)
		assert.Equal(t, "api-bump", *got.Workspaces[0].ID)
		assert.Equal(t, "local", got.Workspaces[0].Environment)
		assert.Equal(t, "api-bump", *got.Workspaces[1].ID)
		assert.Equal(t, "linux-server", got.Workspaces[1].Environment)
		assert.True(t, got.Workspaces[1].Stale)
	})
}

// listenDaemon opens the daemon socket of app's config directory, which
// nothing answers, and returns its listener. A dial shows as a connection
// pending on it; see acceptDialled.
func listenDaemon(t *testing.T, app *App) *net.UnixListener {
	t.Helper()
	// Not t.TempDir: its name embeds the test name, and a socket path over
	// 104 bytes (macOS) fails to bind.
	dir, err := os.MkdirTemp("", "wsd") //nolint:usetesting // t.TempDir is too long for a socket path
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	require.NoError(t, os.Mkdir(filepath.Join(dir, "ws"), 0o700))
	app.environ = append(app.environ, "XDG_RUNTIME_DIR="+dir)
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "unix", app.daemonPaths().Socket)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	unixLn, ok := ln.(*net.UnixListener)
	require.True(t, ok)
	return unixLn
}

// acceptDialled is nil when a connection is pending on ln, that is, the
// daemon was dialled.
func acceptDialled(t *testing.T, ln *net.UnixListener) error {
	t.Helper()
	require.NoError(t, ln.SetDeadline(time.Now().Add(100*time.Millisecond)))
	conn, err := ln.Accept()
	if err == nil {
		_ = conn.Close()
	}
	return err
}

func TestApp_environmentPresence(t *testing.T) {
	t.Run("no daemon means every environment is offline, with no error", func(t *testing.T) {
		app := newRemoteApp(t)
		assert.Equal(t, map[string]string{}, app.environmentPresence(t.Context()))
	})
}

func TestApp_remoteEntries(t *testing.T) {
	t.Run("no snapshot has no entries and no error", func(t *testing.T) {
		app := newRemoteApp(t)
		addLinuxServer(t, app)
		got, err := app.remoteEntries("linux-server", map[string]string{})
		require.NoError(t, err)
		assert.Equal(t, "linux-server", got.name)
		assert.Equal(t, "offline", got.status)
		assert.Empty(t, got.entries)
	})
	t.Run("lists the snapshot's entries as the environment, and drops null ids", func(t *testing.T) {
		app := newRemoteApp(t)
		rec := addLinuxServer(t, app)
		writeRemoteSnapshot(t, app, rec, testNow(), remoteWorkspaces(
			remoteWorkspace("api-bump", "github.com/bobcob7/buff-bear", "", false),
			`{"id":null,"name":null,"path":"/srv/workspaces/stray","created_at":null,"session":null,"hidden":false,"parent":null,"repos":[],"review":null,"loop_run":null,"progress":null,"rollup":null}`,
		))
		got, err := app.remoteEntries("linux-server", map[string]string{"linux-server": "online"})
		require.NoError(t, err)
		require.Len(t, got.entries, 1)
		assert.Equal(t, "api-bump", *got.entries[0].ID)
		assert.Equal(t, "linux-server", got.entries[0].Environment)
		assert.Equal(t, "online", got.entries[0].EnvironmentStatus)
		assert.False(t, got.entries[0].Stale)
	})
	t.Run("an offline environment's entries are stale", func(t *testing.T) {
		app := newRemoteApp(t)
		rec := addLinuxServer(t, app)
		writeRemoteSnapshot(t, app, rec, testNow(), remoteWorkspaces(remoteWorkspace("api-bump", "github.com/bobcob7/buff-bear", "", false)))
		got, err := app.remoteEntries("linux-server", map[string]string{"linux-server": "offline"})
		require.NoError(t, err)
		require.Len(t, got.entries, 1)
		assert.Equal(t, "offline", got.entries[0].EnvironmentStatus)
		assert.True(t, got.entries[0].Stale)
	})
	t.Run("an online environment with an old snapshot is stale", func(t *testing.T) {
		app := newRemoteApp(t)
		rec := addLinuxServer(t, app)
		writeRemoteSnapshot(t, app, rec, testNow().Add(-2*time.Minute), remoteWorkspaces(remoteWorkspace("api-bump", "github.com/bobcob7/buff-bear", "", false)))
		got, err := app.remoteEntries("linux-server", map[string]string{"linux-server": "online"})
		require.NoError(t, err)
		require.Len(t, got.entries, 1)
		assert.Equal(t, "online", got.entries[0].EnvironmentStatus)
		assert.True(t, got.entries[0].Stale)
	})
	t.Run("agent_state is the state of the snapshot agent that runs the entry's session", func(t *testing.T) {
		app := newRemoteApp(t)
		rec := addLinuxServer(t, app)
		running := `{"id":"fix-login","name":null,"path":"/srv/workspaces/fix-login","created_at":"2026-10-19T10:00:00Z","session":{"kind":"agent","id":"e8cdf5d8","state":"running","pid":null,"stale":false,"runner":"claude","model":null,"ws_runner":null},"hidden":false,"parent":null,"repos":[],"review":null,"loop_run":null}`
		writeRemoteSnapshotWithAgents(t, app, rec, testNow(), remoteWorkspaces(running, remoteWorkspace("api-bump", "github.com/bobcob7/buff-bear", "", false)),
			`[{"id":"ag1","session_id":"e8cdf5d8","name":"fix-login","state":"working","status":"","pid":null,"started_at":null}]`)
		got, err := app.remoteEntries("linux-server", map[string]string{"linux-server": "online"})
		require.NoError(t, err)
		require.Len(t, got.entries, 2)
		require.NotNil(t, got.entries[0].AgentState)
		assert.Equal(t, "working", *got.entries[0].AgentState)
		assert.Nil(t, got.entries[1].AgentState)
	})
	t.Run("a snapshot that doesn't decode is an error", func(t *testing.T) {
		app := newRemoteApp(t)
		rec := addLinuxServer(t, app)
		writeRemoteSnapshot(t, app, rec, testNow(), `{"not":"an array"}`)
		_, err := app.remoteEntries("linux-server", nil)
		assert.Error(t, err)
	})
}

func TestRemoteHasRepo(t *testing.T) {
	repos := []listRepoJSON{{ID: "github.com/bobcob7/buff-bear", Ref: "main"}}
	t.Run("a name matches any owner", func(t *testing.T) {
		assert.True(t, remoteHasRepo(repos, &repo.Spec{Kind: repo.KindName, Name: "buff-bear"}))
	})
	t.Run("a ref must be the one checked out", func(t *testing.T) {
		assert.False(t, remoteHasRepo(repos, &repo.Spec{Kind: repo.KindName, Name: "buff-bear", Ref: "dev"}))
	})
}
