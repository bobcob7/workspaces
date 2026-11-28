package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
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
	"github.com/bobcob7/workspaces/internal/envproto"
)

// remoteArtifactFixture is a controller whose artifact API listens on
// loopback, and an agent machine enrolled with it, running in workspace plan.
type remoteArtifactFixture struct {
	ctl    *mockedApp
	agent  *mockedApp
	dir    string // plan's directory on the agent
	envID  string
	ctrl   *environment.Controller
	store  *environment.Store
	broker *envtest.Broker
}

// newRemoteArtifactFixture starts the controller and enrolls the agent
// through it, then saves the controller's API endpoint on the agent, as
// api.update would.
func newRemoteArtifactFixture(t *testing.T) *remoteArtifactFixture {
	t.Helper()
	ctl := newArtifactApp(t)
	store := environment.NewStore(ctl.app.paths.Environments, ctl.app.paths.State, time.Now, rand.Reader)
	broker := envtest.NewBroker()
	ctrl := environment.NewController(discardDaemonLog(), store, broker, environment.Settings{
		Listen:          "127.0.0.1:0",
		EnrollListen:    "127.0.0.1:0",
		APIListen:       "127.0.0.1:0",
		PublicName:      "127.0.0.1",
		RenewBeforeDays: 30,
	}, time.Now)
	ctrl.SetArtifactAPI(ctl.app.artifactAPI())
	// A subtest may have stopped the controller already.
	t.Cleanup(func() { _ = ctrl.Close() })
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
	return &remoteArtifactFixture{ctl: ctl, agent: agent, dir: dir, envID: rec.ID, ctrl: ctrl, store: store, broker: broker}
}

// remoteAddedID adds a file called name with data as the agent's caller,
// and returns the artifact's id.
func remoteAddedID(t *testing.T, f *remoteArtifactFixture, name string, data []byte) string {
	t.Helper()
	path := writeTestFile(t, name, data)
	require.Equal(t, exitOK, machineCall(t, f.agent, "add", path), f.agent.stderr.String())
	return jsonString(t, jsonObject(t, jsonObjectOf(t, f.agent.stdout.String())["artifact"])["id"])
}

// storedCount is how many artifacts the controller's store holds.
func storedCount(t *testing.T, f *remoteArtifactFixture) int {
	t.Helper()
	f.ctl.stdout.Reset()
	require.Equal(t, exitOK, runMachine(t, f.ctl, "artifact", "list", "--json"), f.ctl.stderr.String())
	return len(artifactsOf(t, f.ctl.stdout.String()))
}

// readByEnvironment reports whether the artifact's conversations hold a read
// by workspace plan on the environment linux-server.
func readByEnvironment(entry *artifact.Entry) bool {
	for i := range entry.Conversations {
		conv := &entry.Conversations[i]
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
		assert.EqualValues(t, 2, jsonObject(t, jsonObjectOf(t, f.agent.stdout.String())["artifact"])["latest"])
		out := t.TempDir()
		require.Equal(t, exitOK, machineCall(t, f.agent, "get", id, "--rev", "1", "--out", out), f.agent.stderr.String())
		data, err := os.ReadFile(filepath.Join(out, "pitch.md"))
		require.NoError(t, err)
		assert.Equal(t, "# Pitch\n", string(data))
	})
	t.Run("comments are added, replied to, listed open, and resolved over the API", func(t *testing.T) {
		f := newRemoteArtifactFixture(t)
		id := remoteAddedID(t, f, "pitch.md", []byte("# Pitch\n"))
		require.Equal(t, exitOK, machineCall(t, f.agent, "comment", "add", id, "--body", "first", "--quote", "Pitch"), f.agent.stderr.String())
		thread := jsonString(t, jsonObject(t, jsonObjectOf(t, f.agent.stdout.String())["thread"])["id"])
		require.Equal(t, exitOK, machineCall(t, f.agent, "comment", "add", id, "--body", "second", "--reply", thread), f.agent.stderr.String())
		require.Equal(t, exitOK, machineCall(t, f.agent, "comment", "list", id, "--open"), f.agent.stderr.String())
		assert.Len(t, jsonObjectOf(t, f.agent.stdout.String())["threads"], 1)
		require.Equal(t, exitOK, machineCall(t, f.agent, "comment", "resolve", id, thread), f.agent.stderr.String())
		require.Equal(t, exitOK, machineCall(t, f.agent, "comment", "list", id, "--open"), f.agent.stderr.String())
		assert.Empty(t, jsonObjectOf(t, f.agent.stdout.String())["threads"])
	})
	t.Run("rm removes only what the agent's own tree created", func(t *testing.T) {
		f := newRemoteArtifactFixture(t)
		f.ctl.stdout.Reset()
		person := writeTestFile(t, "person.txt", []byte("hi"))
		require.Equal(t, exitOK, runMachine(t, f.ctl, "artifact", "add", person, "--json"), f.ctl.stderr.String())
		personID := jsonString(t, jsonObject(t, jsonObjectOf(t, f.ctl.stdout.String())["artifact"])["id"])
		assert.Equal(t, exitRefused, machineCall(t, f.agent, "rm", personID))
		item := jsonObject(t, artifactsOf(t, f.agent.stdout.String())[0])
		assert.Equal(t, false, item["removed"])
		assert.Equal(t, string(codeArtifactForbidden), jsonObject(t, item["error"])["code"])
		own := remoteAddedID(t, f, "own.txt", []byte("mine"))
		require.Equal(t, exitOK, machineCall(t, f.agent, "rm", own), f.agent.stderr.String())
		assert.Equal(t, true, jsonObject(t, artifactsOf(t, f.agent.stdout.String())[0])["removed"])
	})
	t.Run("a file of exactly 10 MiB is stored, and one byte more is artifact_invalid with the store unchanged", func(t *testing.T) {
		f := newRemoteArtifactFixture(t)
		png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, artifact.MaxImageSize-8)...)
		remoteAddedID(t, f, "big.png", png)
		over := writeTestFile(t, "over.png", append(png, 0))
		assert.Equal(t, exitUsage, machineCall(t, f.agent, "add", over))
		assert.Equal(t, string(codeArtifactInvalid), errorCodeOf(t, f.agent.stderr))
		assert.Equal(t, 1, storedCount(t, f))
	})
	t.Run("with the controller stopped, a command is artifact_unreachable with reason offline", func(t *testing.T) {
		f := newRemoteArtifactFixture(t)
		require.NoError(t, f.ctrl.Close())
		assert.Equal(t, exitFailed, machineCall(t, f.agent, "list"))
		assert.Equal(t, string(codeArtifactUnreachable), errorCodeOf(t, f.agent.stderr))
		assert.Equal(t, envproto.ReasonOffline, errorDetailsOf(t, f.agent.stderr)["reason"])
	})
	t.Run("without api.toml, the reason is no_endpoint and there is no endpoint", func(t *testing.T) {
		f := newRemoteArtifactFixture(t)
		require.NoError(t, os.Remove(filepath.Join(f.agent.app.paths.Environment, "api.toml")))
		assert.Equal(t, exitFailed, machineCall(t, f.agent, "list"))
		assert.Equal(t, string(codeArtifactUnreachable), errorCodeOf(t, f.agent.stderr))
		details := errorDetailsOf(t, f.agent.stderr)
		assert.Equal(t, envproto.ReasonNoEndpoint, details["reason"])
		assert.Nil(t, details["endpoint"])
	})
	t.Run("after the agent's certificate is revoked, the reason is refused", func(t *testing.T) {
		f := newRemoteArtifactFixture(t)
		_, _, _, err := f.store.Rotate(t.Context(), "linux-server", f.ctrl.Status().EnrollListen)
		require.NoError(t, err)
		assert.Equal(t, exitFailed, machineCall(t, f.agent, "list"))
		assert.Equal(t, envproto.ReasonRefused, errorDetailsOf(t, f.agent.stderr)["reason"])
	})
	t.Run("discovery: the controller's api.update gives an agent with no api.toml the endpoint", func(t *testing.T) {
		f := newRemoteArtifactFixture(t)
		// runEnvAgentRun needs the stores of a full app, so this agent is one
		// built by newEnvTestApp, enrolled again with a new code.
		agent, _, _ := newEnvTestApp(t)
		_, code, _, err := f.store.Rotate(t.Context(), "linux-server", f.ctrl.Status().EnrollListen)
		require.NoError(t, err)
		require.NoError(t, agent.runEnvAgentEnroll(t.Context(), code))
		agent.newEnvClient = func(*slog.Logger) (envClient, error) { return f.broker.NewClient(), nil }
		files := envagent.NewFiles(agent.paths.Environment)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- agent.runEnvAgentRun(ctx) }()
		require.Eventually(t, func() bool {
			endpoint, err := files.APIEndpoint()
			return err == nil && endpoint == f.ctrl.APIEndpoint()
		}, 10*time.Second, 10*time.Millisecond)
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(10 * time.Second):
			require.FailNow(t, "the agent didn't stop after its context was cancelled")
		}
	})
	t.Run("an @artifact reference on the agent is checked and hinted, and an unreachable controller only warns", func(t *testing.T) {
		f := newRemoteArtifactFixture(t)
		id := remoteAddedID(t, f, "pitch.md", []byte("# Pitch\n"))
		prompt := "look at @artifact:" + id
		args, err := f.agent.app.artifactHints(t.Context(), []string{prompt}, "")
		require.NoError(t, err)
		assert.Equal(t, []string{prompt + "\n\n" + artifact.HintLine([]string{id})}, args)
		_, err = f.agent.app.artifactHints(t.Context(), []string{"look at @artifact:art-00000000"}, "")
		requireCode(t, codeArtifactUnknown, err)
		require.NoError(t, f.ctrl.Close())
		args, err = f.agent.app.artifactHints(t.Context(), []string{prompt}, "")
		require.NoError(t, err)
		assert.Equal(t, []string{prompt + "\n\n" + artifact.HintLine([]string{id})}, args)
		require.Len(t, f.agent.app.warned, 1)
		assert.Contains(t, f.agent.app.warned[0], "couldn't check the @artifact references")
	})
}

func TestWriteArtifactStream(t *testing.T) {
	data := []byte("hello")
	sum := sha256.Sum256(data)
	goodSum := hex.EncodeToString(sum[:])
	wrongSum := sha256.Sum256([]byte("other"))
	t.Run("writes a body of size bytes with the checksum, owner-only", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "r1")
		path, err := writeArtifactStream(dir, "a.txt", bytes.NewReader(data), int64(len(data)), goodSum)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(dir, "a.txt"), path)
		got, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, data, got)
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	})
	t.Run("a short body leaves no file", func(t *testing.T) {
		dir := t.TempDir()
		_, err := writeArtifactStream(dir, "a.txt", bytes.NewReader(data[:3]), int64(len(data)), goodSum)
		require.Error(t, err)
		assertNoStreamFile(t, dir)
	})
	t.Run("a long body leaves no file", func(t *testing.T) {
		dir := t.TempDir()
		_, err := writeArtifactStream(dir, "a.txt", bytes.NewReader(append(data, '!')), int64(len(data)), goodSum)
		require.Error(t, err)
		assertNoStreamFile(t, dir)
	})
	t.Run("a wrong checksum leaves no file", func(t *testing.T) {
		dir := t.TempDir()
		_, err := writeArtifactStream(dir, "a.txt", bytes.NewReader(data), int64(len(data)), hex.EncodeToString(wrongSum[:]))
		require.Error(t, err)
		assertNoStreamFile(t, dir)
	})
	t.Run("a name with a slash is refused", func(t *testing.T) {
		_, err := writeArtifactStream(t.TempDir(), "a/b.txt", bytes.NewReader(data), int64(len(data)), goodSum)
		require.Error(t, err)
	})
}

// assertNoStreamFile checks that dir holds no file, after a failed write.
func assertNoStreamFile(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries)
}
