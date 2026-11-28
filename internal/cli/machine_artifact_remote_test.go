package cli

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/artifact"
	"github.com/bobcob7/workspaces/internal/envagent"
	"github.com/bobcob7/workspaces/internal/environment"
	"github.com/bobcob7/workspaces/internal/environment/envtest"
)

// remoteArtifactFixture is a controller whose artifact API listens on
// loopback, and an agent machine enrolled with it, running in workspace plan.
type remoteArtifactFixture struct {
	ctl   *mockedApp
	agent *mockedApp
	dir   string // plan's directory on the agent
	envID string
}

// newRemoteArtifactFixture starts the controller and enrolls the agent
// through it, then saves the controller's API endpoint on the agent, as
// api.update would.
func newRemoteArtifactFixture(t *testing.T) *remoteArtifactFixture {
	t.Helper()
	ctl := newArtifactApp(t)
	store := environment.NewStore(ctl.app.paths.Environments, ctl.app.paths.State, time.Now, rand.Reader)
	ctrl := environment.NewController(discardDaemonLog(), store, envtest.NewBroker(), environment.Settings{
		Listen:          "127.0.0.1:0",
		EnrollListen:    "127.0.0.1:0",
		APIListen:       "127.0.0.1:0",
		PublicName:      "127.0.0.1",
		RenewBeforeDays: 30,
	}, time.Now)
	ctrl.SetArtifactAPI(ctl.app.artifactAPI())
	t.Cleanup(func() { assert.NoError(t, ctrl.Close()) })
	_, _, _, err := store.Add(t.Context(), "linux-server", "127.0.0.1:1")
	require.NoError(t, err)
	require.NoError(t, ctrl.Start(t.Context()))
	_, code, _, err := store.Rotate(t.Context(), "linux-server", ctrl.Status().EnrollListen)
	require.NoError(t, err)
	rec, err := store.Get("linux-server")
	require.NoError(t, err)

	agent, dir := newMachineArtifactApp(t)
	require.NoError(t, agent.app.runEnvAgentEnroll(t.Context(), code))
	require.NoError(t, envagent.NewFiles(agent.app.paths.Environment).SaveAPIEndpoint(ctrl.APIEndpoint(), time.Now()))
	return &remoteArtifactFixture{ctl: ctl, agent: agent, dir: dir, envID: rec.ID}
}

// remoteAddedID adds a file called name with data as the agent's caller,
// and returns the artifact's id.
func remoteAddedID(t *testing.T, f *remoteArtifactFixture, name string, data []byte) string {
	t.Helper()
	path := writeTestFile(t, name, data)
	require.Equal(t, exitOK, machineCall(t, f.agent, "add", path), f.agent.stderr.String())
	return jsonString(t, jsonObject(t, jsonObjectOf(t, f.agent.stdout.String())["artifact"])["id"])
}

// readByEnvironment reports whether the artifact's conversations hold a read
// by workspace plan on the environment linux-server.
func readByEnvironment(entry *artifact.Entry) bool {
	for _, conv := range entry.Conversations {
		if conv.Workspace == "plan" && conv.Environment != nil && *conv.Environment == "linux-server" && slices.Contains(conv.Actions, artifact.ActionRead) {
			return true
		}
	}
	return false
}

func TestApp_machineArtifactRemote(t *testing.T) {
	t.Run("add stores the file on the controller, owned by the agent's tree in its environment", func(t *testing.T) {
		f := newRemoteArtifactFixture(t)
		id := remoteAddedID(t, f, "pitch.md", []byte("# Pitch\n"))
		store := f.ctl.app.artifactStore()
		entry, err := store.Show(id)
		require.NoError(t, err)
		_, _, data, err := store.Read(id, 0)
		require.NoError(t, err)
		assert.Equal(t, "# Pitch\n", string(data))
		require.NotNil(t, entry.Record.Root)
		assert.Equal(t, f.envID+"/boss", *entry.Record.Root)
		require.NotNil(t, entry.Record.CreatedBy.Environment)
		assert.Equal(t, "linux-server", *entry.Record.CreatedBy.Environment)
	})
	t.Run("get copies the revision into the workspace, owner-only, and the controller records the read", func(t *testing.T) {
		f := newRemoteArtifactFixture(t)
		id := remoteAddedID(t, f, "pitch.md", []byte("# Pitch\n"))
		require.Equal(t, exitOK, machineCall(t, f.agent, "get", id), f.agent.stderr.String())
		path := filepath.Join(f.dir, ".ws-artifacts", id, "r1", "pitch.md")
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "# Pitch\n", string(data))
		entry, err := f.ctl.app.artifactStore().Show(id)
		require.NoError(t, err)
		assert.True(t, readByEnvironment(&entry))
	})
	t.Run("list and show print the artifact, and put adds a revision that get --rev 1 still reads", func(t *testing.T) {
		f := newRemoteArtifactFixture(t)
		id := remoteAddedID(t, f, "pitch.md", []byte("# Pitch\n"))
		require.Equal(t, exitOK, machineCall(t, f.agent, "list", "--mine"), f.agent.stderr.String())
		listed := artifactsOf(t, f.agent.stdout.String())
		require.Len(t, listed, 1)
		assert.Equal(t, id, jsonObject(t, listed[0])["id"])
		require.Equal(t, exitOK, machineCall(t, f.agent, "show", id), f.agent.stderr.String())
		assert.Equal(t, id, jsonObject(t, jsonObjectOf(t, f.agent.stdout.String())["artifact"])["id"])
		next := writeTestFile(t, "pitch-2.md", []byte("# Pitch v2\n"))
		require.Equal(t, exitOK, machineCall(t, f.agent, "put", id, next), f.agent.stderr.String())
		assert.Equal(t, float64(2), jsonObject(t, jsonObjectOf(t, f.agent.stdout.String())["artifact"])["latest"])
		out := t.TempDir()
		require.Equal(t, exitOK, machineCall(t, f.agent, "get", id, "--rev", "1", "--out", out), f.agent.stderr.String())
		data, err := os.ReadFile(filepath.Join(out, "pitch.md"))
		require.NoError(t, err)
		assert.Equal(t, "# Pitch\n", string(data))
	})
}
