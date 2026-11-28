package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/artifact"
	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/envagent"
	"github.com/bobcob7/workspaces/internal/environment"
	"github.com/bobcob7/workspaces/internal/environment/envtest"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/lockfile"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// apiTap wraps the controller's artifact API, letting tweak change the
// answer of the request it is given.
type apiTap struct {
	tweak func(r *http.Request, rec *httptest.ResponseRecorder)
}

func (tap *apiTap) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		next.ServeHTTP(rec, r)
		tap.tweak(r, rec)
		maps.Copy(w.Header(), rec.Header())
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	})
}

// wireSpawn gives the agent a fake Claude client and a workspace store that
// creates workspaces, so ws machine spawn runs on it. It returns the client.
func wireSpawn(t *testing.T, e *mockedApp) *claudeClientMock {
	t.Helper()
	lockPath := filepath.Join(t.TempDir(), "lock")
	e.store.CreateFunc = func(context.Context, workspace.CreateOptions) (workspace.Workspace, *lockfile.Lock, error) {
		lock, ok, err := lockfile.TryExclusiveLock(lockPath)
		require.NoError(t, err)
		require.True(t, ok)
		return workspace.Workspace{ID: "x", Path: "/cfg/workspaces/x", Parent: "plan"}, lock, nil
	}
	e.store.RecordSessionFunc = func(string, workspace.SessionKind, string) error { return nil }
	e.store.RemoveFunc = func(string, workspace.RemoveOptions) error { return nil }
	e.store.FindFunc = func(arg string) (workspace.Workspace, error) {
		list, _ := e.store.ListFunc()
		for i := range list {
			if list[i].ID == arg {
				return list[i], nil
			}
		}
		return workspace.Workspace{}, workspace.ErrNotFound
	}
	claudeMock := &claudeClientMock{
		CheckFunc:    func(context.Context) error { return nil },
		SignedInFunc: func(context.Context, string, []string) (bool, string, error) { return true, "", nil },
		StartFunc: func(context.Context, claude.StartOptions) (claude.Started, error) {
			return claude.Started{ID: "e8cdf5d8"}, nil
		},
		TrustFunc:  func(context.Context, string, string, func(string, bool) error) error { return nil },
		AttachFunc: func(context.Context, string) error { return nil },
		RemoveFunc: func(context.Context, string) error { return nil },
	}
	e.app.claude = claudeMock
	return claudeMock
}

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
	return newRemoteArtifactFixtureWith(t, nil)
}

// newRemoteArtifactFixtureWith is newRemoteArtifactFixture with the
// controller's artifact API wrapped by wrap, when it isn't nil.
func newRemoteArtifactFixtureWith(t *testing.T, wrap func(http.Handler) http.Handler) *remoteArtifactFixture {
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
	api := ctl.app.artifactAPI()
	if wrap != nil {
		api = wrap(api)
	}
	ctrl.SetArtifactAPI(api)
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
		f.ctl.stdout.Reset()
		person := writeTestFile(t, "person.txt", []byte("hi"))
		require.Equal(t, exitOK, runMachine(t, f.ctl, "artifact", "add", person, "--json"), f.ctl.stderr.String())
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
		endpoint := f.ctrl.APIEndpoint()
		require.NoError(t, f.ctrl.Close())
		assert.Equal(t, exitFailed, machineCall(t, f.agent, "list"))
		assert.Equal(t, string(codeArtifactUnreachable), errorCodeOf(t, f.agent.stderr))
		details := errorDetailsOf(t, f.agent.stderr)
		assert.Equal(t, envproto.ReasonOffline, details["reason"])
		assert.Equal(t, endpoint, details["endpoint"])
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
	t.Run("ws machine spawn on the agent appends the hint to the prompt the client gets", func(t *testing.T) {
		f := newRemoteArtifactFixture(t)
		id := remoteAddedID(t, f, "pitch.md", []byte("# Pitch\n"))
		client := wireSpawn(t, f.agent)
		prompt := "look at @artifact:" + id
		require.Equal(t, exitOK, runMachine(t, f.agent, "machine", "spawn", "buff-bear", "--", prompt), f.agent.stderr.String())
		calls := client.StartCalls()
		require.Len(t, calls, 1)
		assert.Equal(t, []string{prompt + "\n\n" + artifact.HintLine([]string{id})}, calls[0].Opts.Args)
		assert.Empty(t, f.agent.app.warned)
	})
	t.Run("ws machine spawn on the agent with an unknown reference is artifact_unknown and creates no workspace", func(t *testing.T) {
		f := newRemoteArtifactFixture(t)
		client := wireSpawn(t, f.agent)
		code := runMachine(t, f.agent, "machine", "spawn", "buff-bear", "--", "look at @artifact:art-00000000")
		assert.NotEqual(t, exitOK, code)
		assert.Equal(t, string(codeArtifactUnknown), errorCodeOf(t, f.agent.stderr))
		assert.Empty(t, f.agent.store.CreateCalls())
		assert.Empty(t, client.StartCalls())
	})
	t.Run("ws machine spawn on the agent with the controller closed still starts, hinted, with a warning", func(t *testing.T) {
		f := newRemoteArtifactFixture(t)
		id := remoteAddedID(t, f, "pitch.md", []byte("# Pitch\n"))
		client := wireSpawn(t, f.agent)
		require.NoError(t, f.ctrl.Close())
		prompt := "look at @artifact:" + id
		require.Equal(t, exitOK, runMachine(t, f.agent, "machine", "spawn", "buff-bear", "--", prompt), f.agent.stderr.String())
		calls := client.StartCalls()
		require.Len(t, calls, 1)
		assert.Equal(t, []string{prompt + "\n\n" + artifact.HintLine([]string{id})}, calls[0].Opts.Args)
		require.Len(t, f.agent.app.warned, 1)
		assert.Contains(t, f.agent.app.warned[0], "couldn't check the @artifact references")
	})
	t.Run("add sends the title and the note", func(t *testing.T) {
		f := newRemoteArtifactFixture(t)
		path := writeTestFile(t, "pitch.md", []byte("# Pitch\n"))
		require.Equal(t, exitOK, machineCall(t, f.agent, "add", path, "--title", "Cache sharing", "--note", "first draft"), f.agent.stderr.String())
		id := jsonString(t, jsonObject(t, jsonObjectOf(t, f.agent.stdout.String())["artifact"])["id"])
		entry, err := f.ctl.app.artifactStore().Show(id)
		require.NoError(t, err)
		require.NotNil(t, entry.Record.Title)
		assert.Equal(t, "Cache sharing", *entry.Record.Title)
		require.NotNil(t, entry.Record.Latest().Note)
		assert.Equal(t, "first draft", *entry.Record.Latest().Note)
	})
	t.Run("put sends the note and the base revision, and a stale base is artifact_conflict", func(t *testing.T) {
		f := newRemoteArtifactFixture(t)
		id := remoteAddedID(t, f, "pitch.md", []byte("# Pitch\n"))
		next := writeTestFile(t, "pitch.md", []byte("# Pitch v2\n"))
		require.Equal(t, exitOK, machineCall(t, f.agent, "put", id, next, "--note", "second", "--base-rev", "1"), f.agent.stderr.String())
		entry, err := f.ctl.app.artifactStore().Show(id)
		require.NoError(t, err)
		require.Len(t, entry.Record.Revisions, 2)
		require.NotNil(t, entry.Record.Latest().Note)
		assert.Equal(t, "second", *entry.Record.Latest().Note)
		stale := writeTestFile(t, "pitch.md", []byte("# Pitch v3\n"))
		assert.NotEqual(t, exitOK, machineCall(t, f.agent, "put", id, stale, "--base-rev", "1"))
		assert.Equal(t, string(codeArtifactConflict), errorCodeOf(t, f.agent.stderr))
		entry, err = f.ctl.app.artifactStore().Show(id)
		require.NoError(t, err)
		assert.Len(t, entry.Record.Revisions, 2)
		require.Equal(t, exitOK, machineCall(t, f.agent, "put", id, stale, "--base-rev", "2"), f.agent.stderr.String())
		entry, err = f.ctl.app.artifactStore().Show(id)
		require.NoError(t, err)
		assert.Len(t, entry.Record.Revisions, 3)
	})
	t.Run("list sends the kind and the limit", func(t *testing.T) {
		f := newRemoteArtifactFixture(t)
		remoteAddedID(t, f, "pitch.md", []byte("# Pitch\n"))
		remoteAddedID(t, f, "notes.md", []byte("# Notes\n"))
		textID := remoteAddedID(t, f, "plain.txt", []byte("plain"))
		require.Equal(t, exitOK, machineCall(t, f.agent, "list", "--kind", "text"), f.agent.stderr.String())
		listed := artifactsOf(t, f.agent.stdout.String())
		require.Len(t, listed, 1)
		assert.Equal(t, textID, jsonObject(t, listed[0])["id"])
		require.Equal(t, exitOK, machineCall(t, f.agent, "list", "--limit", "2"), f.agent.stderr.String())
		assert.Len(t, artifactsOf(t, f.agent.stdout.String()), 2)
		require.Equal(t, exitOK, machineCall(t, f.agent, "list"), f.agent.stderr.String())
		assert.Len(t, artifactsOf(t, f.agent.stdout.String()), 3)
	})
	t.Run("comment add sends the quote and the line, and resolve --reopen reopens the thread", func(t *testing.T) {
		f := newRemoteArtifactFixture(t)
		id := remoteAddedID(t, f, "dup.md", []byte("dup\ndup\n"))
		require.Equal(t, exitOK, machineCall(t, f.agent, "comment", "add", id, "--body", "second one", "--quote", "dup", "--line", "2"), f.agent.stderr.String())
		thread := jsonObject(t, jsonObjectOf(t, f.agent.stdout.String())["thread"])
		anchor := jsonObject(t, thread["anchor"])
		assert.Equal(t, "dup", anchor["quote"])
		assert.EqualValues(t, 2, anchor["line_start"])
		threadID := jsonString(t, thread["id"])
		require.Equal(t, exitOK, machineCall(t, f.agent, "comment", "resolve", id, threadID), f.agent.stderr.String())
		require.Equal(t, exitOK, machineCall(t, f.agent, "comment", "list", id, "--open"), f.agent.stderr.String())
		assert.Empty(t, jsonObjectOf(t, f.agent.stdout.String())["threads"])
		require.Equal(t, exitOK, machineCall(t, f.agent, "comment", "resolve", id, threadID, "--reopen"), f.agent.stderr.String())
		require.Equal(t, exitOK, machineCall(t, f.agent, "comment", "list", id, "--open"), f.agent.stderr.String())
		assert.Len(t, jsonObjectOf(t, f.agent.stdout.String())["threads"], 1)
	})
	t.Run("a workspace name with spaces, non-ASCII, plus and percent characters reaches the controller whole", func(t *testing.T) {
		f := newRemoteArtifactFixture(t)
		name := "Plan ner ünï a+b 100%41"
		tree := []workspace.Workspace{
			{ID: "boss", Path: filepath.Join(f.dir, "boss")},
			{ID: "plan", Name: name, Path: f.dir, Parent: "boss"},
		}
		f.agent.store.ListFunc = func() ([]workspace.Workspace, error) { return tree, nil }
		id := remoteAddedID(t, f, "pitch.md", []byte("# Pitch\n"))
		entry, err := f.ctl.app.artifactStore().Show(id)
		require.NoError(t, err)
		require.NotNil(t, entry.Record.CreatedBy.Name)
		assert.Equal(t, name, *entry.Record.CreatedBy.Name)
	})
	t.Run("the controller's warnings come first, then ws's own", func(t *testing.T) {
		tap := &apiTap{tweak: func(r *http.Request, rec *httptest.ResponseRecorder) {
			if r.Method != http.MethodGet || r.URL.Path != envproto.ArtifactsPath {
				return
			}
			var fields map[string]any
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &fields))
			fields["warnings"] = []string{"from the controller"}
			data, err := json.Marshal(fields)
			require.NoError(t, err)
			rec.Body.Reset()
			rec.Body.Write(data)
			rec.Header().Del("Content-Length")
		}}
		f := newRemoteArtifactFixtureWith(t, tap.wrap)
		f.agent.app.warned = []string{"from ws"}
		require.Equal(t, exitOK, machineCall(t, f.agent, "list"), f.agent.stderr.String())
		assert.Equal(t, []any{"from the controller", "from ws"}, jsonObjectOf(t, f.agent.stdout.String())["warnings"])
	})
	t.Run("an answer over the size limit is internal", func(t *testing.T) {
		tap := &apiTap{tweak: func(r *http.Request, rec *httptest.ResponseRecorder) {
			if r.Method != http.MethodGet || r.URL.Path != envproto.ArtifactsPath {
				return
			}
			rec.Body.Reset()
			rec.Body.Write(bytes.Repeat([]byte(" "), envproto.MaxAPIJSONBytes+1))
			rec.Header().Del("Content-Length")
		}}
		f := newRemoteArtifactFixtureWith(t, tap.wrap)
		assert.NotEqual(t, exitOK, machineCall(t, f.agent, "list"))
		assert.Equal(t, string(codeInternal), errorCodeOf(t, f.agent.stderr))
		assert.Contains(t, f.agent.stderr.String(), "too large")
	})
	t.Run("get refuses an answer for another revision, and writes no file", func(t *testing.T) {
		tap := &apiTap{tweak: func(r *http.Request, rec *httptest.ResponseRecorder) {
			if strings.HasSuffix(r.URL.Path, "/content") {
				rec.Header().Set(envproto.HeaderRev, "9")
			}
		}}
		f := newRemoteArtifactFixtureWith(t, tap.wrap)
		id := remoteAddedID(t, f, "pitch.md", []byte("# Pitch\n"))
		assert.NotEqual(t, exitOK, machineCall(t, f.agent, "get", id))
		assert.Equal(t, string(codeInternal), errorCodeOf(t, f.agent.stderr))
		assert.NoDirExists(t, filepath.Join(f.dir, ".ws-artifacts", id, "r1"))
	})
	t.Run("get refuses a file declared over the image size limit, and writes no file", func(t *testing.T) {
		tap := &apiTap{tweak: func(r *http.Request, rec *httptest.ResponseRecorder) {
			if strings.HasSuffix(r.URL.Path, "/content") {
				rec.Header().Set("Content-Length", strconv.FormatInt(artifact.MaxImageSize+1, 10))
			}
		}}
		f := newRemoteArtifactFixtureWith(t, tap.wrap)
		id := remoteAddedID(t, f, "pitch.md", []byte("# Pitch\n"))
		assert.NotEqual(t, exitOK, machineCall(t, f.agent, "get", id))
		assert.Equal(t, string(codeInternal), errorCodeOf(t, f.agent.stderr))
		assert.NoDirExists(t, filepath.Join(f.dir, ".ws-artifacts", id, "r1"))
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
		requireCode(t, codeInternal, err)
	})
}

// assertNoStreamFile checks that dir holds no file, after a failed write.
func assertNoStreamFile(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries)
}
