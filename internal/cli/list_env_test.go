package cli

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/environment"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/repo"
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
	rec, _, _, err := app.envStore().Add(t.Context(), "linux-server", "127.0.0.1:8884")
	require.NoError(t, err)
	return &rec
}

// writeRemoteSnapshot stores rec's snapshot, received at receivedAt, with
// workspaces as the JSON array the remote's ws list printed.
func writeRemoteSnapshot(t *testing.T, app *App, rec *environment.Record, receivedAt time.Time, workspaces string) {
	t.Helper()
	at := receivedAt.Format(time.RFC3339)
	data := fmt.Sprintf(`{"version":1,"id":%q,"name":%q,"received_at":%q,"snapshot":{"at":%q,"protocol":1,"workspaces":%s,"agents":[]}}`,
		rec.ID, rec.Name, at, at, workspaces)
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
			`{"id":null,"name":null,"path":"/srv/workspaces/stray","created_at":null,"session":null,"hidden":false,"parent":null,"repos":[],"review":null,"loop_run":null}`,
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
