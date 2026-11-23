package environment

import (
	"io/fs"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/environment/envtest"
	"github.com/bobcob7/workspaces/internal/envproto"
)

func TestStore_Snapshot(t *testing.T) {
	t.Run("reads back what onSnapshot wrote", func(t *testing.T) {
		store := newTestStore(t)
		rec, _ := enrollAgent(t, store, "linux-server")
		c := newController(t, store, envtest.NewBroker(), discardLog(), testSettings())
		c.onSnapshot(t.Context(), &envproto.Message{
			Topic:   envproto.Topic(rec.ID, envproto.LeafSnapshot),
			Payload: []byte(`{"at":"2026-11-22T10:00:00Z","protocol":2,"workspaces":[{"name":"api"}],"agents":[{"id":"a1","session_id":"s1","name":"api","state":"idle","status":"running","pid":42,"started_at":null}]}`),
		})
		file, snap, err := store.Snapshot("linux-server")
		require.NoError(t, err)
		assert.Equal(t, snapshotVersion, file.Version)
		assert.Equal(t, rec.ID, file.ID)
		assert.Equal(t, "linux-server", file.Name)
		assert.Equal(t, store.now().UTC().Format(time.RFC3339), file.ReceivedAt)
		assert.Equal(t, 2, snap.Protocol)
		require.Len(t, snap.Agents, 1)
		assert.Equal(t, "a1", snap.Agents[0].ID)
		assert.JSONEq(t, `[{"name":"api"}]`, string(snap.Workspaces))
	})
	t.Run("reads a PR 2 snapshot without protocol or agents", func(t *testing.T) {
		store := newTestStore(t)
		require.NoError(t, store.WriteSnapshot("linux-server", []byte(`{"version":1,"id":"env-0a1b2c3d","name":"linux-server","received_at":"2026-11-22T10:00:00Z","snapshot":{"at":"2026-11-22T10:00:00Z","workspaces":[]}}`)))
		_, snap, err := store.Snapshot("linux-server")
		require.NoError(t, err)
		assert.Equal(t, 0, snap.Protocol)
		assert.NotNil(t, snap.Agents)
		assert.Empty(t, snap.Agents)
	})
	t.Run("a missing snapshot is fs.ErrNotExist", func(t *testing.T) {
		store := newTestStore(t)
		_, _, err := store.Snapshot("linux-server")
		assert.ErrorIs(t, err, fs.ErrNotExist)
	})
}
