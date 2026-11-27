package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/daemon"
	"github.com/bobcob7/workspaces/internal/envagent"
	"github.com/bobcob7/workspaces/internal/environment"
	"github.com/bobcob7/workspaces/internal/environment/envtest"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/loop"
	"github.com/bobcob7/workspaces/internal/repo"
	"github.com/bobcob7/workspaces/internal/review/store"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// conformanceEnvName is the environment's name in the conformance tests.
const conformanceEnvName = "linux-server"

// conformanceProbe reports Claude Code as present, as the agent's hello does.
type conformanceProbe struct{}

// Probe is claude present at a fixed version.
func (conformanceProbe) Probe(context.Context) (string, *string) {
	version := "1.0.0"
	return envproto.ClaudePresent, &version
}

// hostCaller is the controller-side App's call endpoint without a daemon
// socket: it asks the controller directly and answers as the daemon's server
// would, with a RemoteError for a refused call.
type hostCaller struct{ host envHost }

// CallEnvironment calls name through the controller.
func (h hostCaller) CallEnvironment(ctx context.Context, name string, req daemon.CallRequest) (daemon.CallReply, error) { //nolint:gocritic // hugeParam: the daemon.CallRequest value is the fixed API.
	reply, err := h.host.Call(ctx, name, req)
	if err == nil {
		return reply, nil
	}
	hostErr, ok := errors.AsType[*daemon.HostError](err)
	if !ok {
		return daemon.CallReply{}, err
	}
	code := "internal"
	switch {
	case errors.Is(hostErr.Err, daemon.ErrEnvironmentUnknown):
		code = "environment_unknown"
	case errors.Is(hostErr.Err, daemon.ErrEnvironmentOffline):
		code = "environment_offline"
	case errors.Is(hostErr.Err, daemon.ErrEnvironmentOutdated):
		code = "environment_outdated"
	case errors.Is(hostErr.Err, daemon.ErrNotForwarded):
		code = "usage"
	}
	return daemon.CallReply{}, &daemon.RemoteError{Code: code, Message: hostErr.Error(), Details: hostErr.Details}
}

// fakeClaude is the one claudeClientMock both Apps of the agent share, with
// the sessions it has started.
type fakeClaude struct {
	*claudeClientMock
	mu       sync.Mutex
	checkErr error
	started  []claude.Session
	stopped  []string
}

// newFakeClaude is a Claude Code that is installed and starts every session
// working.
// conformanceSharedClaude keeps the agent on the base config directory, so
// no overlay is seeded and no sign-in is checked.
const conformanceSharedClaude = "[claude]\nconfig_dir = \"shared\"\n"

func newFakeClaude() *fakeClaude {
	f := &fakeClaude{}
	f.claudeClientMock = &claudeClientMock{
		CheckFunc: func(context.Context) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.checkErr
		},
		StartFunc: func(_ context.Context, opts claude.StartOptions) (claude.Started, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			id := fmt.Sprintf("e8cdf5%02d", len(f.started)+1)
			f.started = append(f.started, claude.Session{
				ID:        id,
				SessionID: "5f1e2d3c-0000-4000-8000-0000000000" + id[len(id)-2:],
				Name:      opts.Name,
				Cwd:       opts.Dir,
				State:     claude.StateWorking,
				Status:    "busy",
				PID:       4000 + len(f.started),
				StartedAt: time.Date(2026, 11, 22, 9, 30, 0, 0, time.UTC),
			})
			return claude.Started{ID: id}, nil
		},
		TrustFunc: func(context.Context, string, string, func(string, bool) error) error { return nil },
		AgentsFunc: func(context.Context, claude.ListOptions) ([]claude.Session, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			return append([]claude.Session{}, f.started...), nil
		},
		LogsFunc: func(context.Context, string) (string, error) { return "first line\nsecond line\n", nil },
		StopFunc: func(_ context.Context, id string) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.stopped = append(f.stopped, id)
			return nil
		},
		RemoveFunc:       func(context.Context, string) error { return nil },
		DaemonStatusFunc: func(context.Context) (claude.DaemonStatus, error) { return claude.DaemonStatus{}, nil },
		ModelFunc:        func(*claude.Session) (string, error) { return "", nil },
	}
	return f
}

// setCheckErr makes Check return err.
func (f *fakeClaude) setCheckErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checkErr = err
}

// stoppedIDs is the ids Stop was called with.
func (f *fakeClaude) stoppedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.stopped...)
}

// conformance is a controller, its broker and an enrolled agent in one process,
// each with its own CONFIG_DIR.
type conformance struct {
	t          *testing.T
	ctrlDir    string
	agentDir   string
	loader     *config.Loader
	broker     *envtest.Broker
	ctrl       *environment.Controller
	store      *environment.Store
	identity   envagent.Identity
	files      *envagent.Files
	agentApp   *App
	ops        *agentOperations
	agentClaud *fakeClaude
	ctrlClaude *claudeClientMock
	stopAgent  func()

	snapshotCheck time.Duration
	loopTick      time.Duration // the agent's loop pass interval; 0 keeps the default.
}

// conformanceLog discards logs.
func conformanceLog() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

// newConformance builds the controller and enrolls the agent. startAgent runs
// the agent too and waits until the controller sees it online with a snapshot.
func newConformance(t *testing.T, startAgent bool) *conformance {
	t.Helper()
	return newConformanceEvery(t, startAgent, 10*time.Millisecond)
}

// newConformanceEvery is newConformance with the agent's snapshot check
// interval, long when only an operation's change signal may republish.
func newConformanceEvery(t *testing.T, startAgent bool, snapshotCheck time.Duration) *conformance {
	t.Helper()
	ctx := t.Context()
	c := &conformance{
		t:             t,
		ctrlDir:       t.TempDir(),
		agentDir:      t.TempDir(),
		broker:        envtest.NewBroker(),
		agentClaud:    newFakeClaude(),
		stopAgent:     func() {},
		snapshotCheck: snapshotCheck,
	}
	c.loader = config.NewLoader(func(string) string { return "" }, func() (string, error) { return "", errors.New("no user config dir") })
	require.NoError(t, os.WriteFile(filepath.Join(c.agentDir, "config.toml"), []byte(conformanceSharedClaude), 0o600))
	c.ctrlClaude = &claudeClientMock{
		CheckFunc:  func(context.Context) error { return nil },
		AgentsFunc: func(context.Context, claude.ListOptions) ([]claude.Session, error) { return nil, nil },
	}
	ctrlPaths := config.NewPaths(c.ctrlDir)
	c.store = environment.NewStore(ctrlPaths.Environments, ctrlPaths.State, time.Now, rand.Reader)
	c.ctrl = environment.NewController(conformanceLog(), c.store, c.broker, environment.Settings{
		Listen:          "127.0.0.1:0",
		EnrollListen:    "127.0.0.1:0",
		PublicName:      "127.0.0.1",
		RenewBeforeDays: 30,
	}, time.Now)
	t.Cleanup(func() { assert.NoError(t, c.ctrl.Close()) })
	_, _, _, err := c.store.Add(ctx, conformanceEnvName, "127.0.0.1:1")
	require.NoError(t, err)
	require.NoError(t, c.ctrl.Start(ctx))
	_, code, _, err := c.store.Rotate(ctx, conformanceEnvName, c.ctrl.Status().EnrollListen)
	require.NoError(t, err)
	c.files = envagent.NewFiles(config.NewPaths(c.agentDir).Environment)
	c.identity, err = envagent.NewEnroller(conformanceLog(), c.files, rand.Reader).Enroll(ctx, code)
	require.NoError(t, err)
	c.agentApp = c.newApp(io.Discard, io.Discard)
	paths, cfg, err := c.loader.Load(c.agentDir)
	require.NoError(t, err)
	s := c.agentApp.newStores(paths, cfg)
	c.agentApp.paths, c.agentApp.config = paths, cfg
	c.agentApp.cache, c.agentApp.workspaces, c.agentApp.lifetime = s.cache, s.workspaces, s.lifetime
	c.agentApp.loops, c.agentApp.loopData = s.loops, s.loopData
	c.ops = newAgentOperations(c.agentApp, &c.identity)
	c.ops.newApp = func(stdout, stderr io.Writer) *App {
		app := c.newApp(stdout, stderr)
		app.environment = c.identity.Name
		return app
	}
	if startAgent {
		c.runAgent()
	}
	return c
}

// newApp is a new App on the agent's machine: real config loader, workspace
// store and lifetime manager, with only claude replaced.
func (c *conformance) newApp(stdout, stderr io.Writer) *App {
	app := New("test", strings.NewReader(""), stdout, stderr, c.loader, nil)
	app.claude = c.agentClaud
	return app
}

// controllerApp is an App on the controller's machine, calling environments
// through the controller directly.
func (c *conformance) controllerApp(stdout, stderr io.Writer) *App {
	app := New("test", strings.NewReader(""), stdout, stderr, c.loader, nil)
	app.claude = c.ctrlClaude
	app.dialEnvironments = func(context.Context) (environmentCaller, error) {
		return hostCaller{host: envHost{ctrl: c.ctrl}}, nil
	}
	app.presence = func(context.Context) map[string]string {
		status := envOffline
		if c.online() {
			status = envOnline
		}
		return map[string]string{conformanceEnvName: status}
	}
	return app
}

// runAgent starts the agent and waits until the controller has its hello and
// a snapshot.
func (c *conformance) runAgent() {
	c.t.Helper()
	opts := envagent.DefaultOptions("test", 30)
	opts.SnapshotCheck = c.snapshotCheck
	opts.RenewCheck = time.Hour
	opts.WorkspacesRoot = c.agentApp.paths.Workspaces
	if c.loopTick > 0 {
		opts.LoopTick = c.loopTick
	}
	opts.BackoffMin, opts.BackoffMax = time.Millisecond, 10*time.Millisecond
	agent := envagent.NewAgent(conformanceLog(), c.files, c.broker.NewClient(), agentSnapshots{app: c.agentApp}, conformanceProbe{}, c.ops, rand.Reader, opts)
	agent.SetLoopTicker(agentLoops{newApp: c.ops.newApp, root: c.agentApp.paths.Root})
	ctx, cancel := context.WithCancel(c.t.Context())
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		assert.NoError(c.t, agent.Run(ctx))
	}()
	var once sync.Once
	c.stopAgent = func() {
		once.Do(func() {
			cancel()
			<-finished
		})
	}
	c.t.Cleanup(c.stopAgent)
	require.Eventually(c.t, func() bool {
		if !c.online() {
			return false
		}
		_, _, err := c.store.Snapshot(conformanceEnvName)
		return err == nil
	}, 10*time.Second, 10*time.Millisecond, "the agent never came online with a snapshot")
}

// online is whether the controller sees the environment online.
func (c *conformance) online() bool {
	for _, p := range c.ctrl.Status().Environments {
		if p.Name == conformanceEnvName && p.Status == envproto.StatusOnline {
			return true
		}
	}
	return false
}

// controllerCLI runs ws --json with args on the controller's machine.
func (c *conformance) controllerCLI(args ...string) (stdout, stderr string, code int) {
	c.t.Helper()
	var out, errOut bytes.Buffer
	code = c.controllerApp(&out, &errOut).Run(c.t.Context(), append([]string{"--json", "--config-dir", c.ctrlDir}, args...))
	return out.String(), errOut.String(), code
}

// agentCLI runs ws --json with args on the agent's machine, as the agent's
// own operations do.
func (c *conformance) agentCLI(args ...string) (stdout, stderr string, code int) {
	c.t.Helper()
	var out, errOut bytes.Buffer
	code = c.ops.newApp(&out, &errOut).Run(c.t.Context(), append([]string{"--json", "--config-dir", c.agentDir}, args...))
	return out.String(), errOut.String(), code
}

// call sends op with args to the environment as the controller-side App does,
// and returns the result.
func (c *conformance) call(op string, args any) (json.RawMessage, error) {
	c.t.Helper()
	return c.controllerApp(io.Discard, io.Discard).callEnvironment(c.t.Context(), conformanceEnvName, op, args)
}

// createRemote creates a workspace on the agent through workspace.create and
// returns the result's id.
func (c *conformance) createRemote(name string) string {
	c.t.Helper()
	result, err := c.call(envproto.OpWorkspaceCreate, &envproto.CreateArgs{Kind: envproto.KindClaude, Specs: []string{}, Name: &name})
	require.NoError(c.t, err)
	return resultID(c.t, result)
}

// resultID is the "id" of a JSON object.
func resultID(t *testing.T, object json.RawMessage) string {
	t.Helper()
	var body struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(object, &body))
	require.NotEmpty(t, body.ID)
	return body.ID
}

// record is the workspace record of id on the agent's machine.
func (c *conformance) record(id string) workspace.Workspace {
	c.t.Helper()
	var ws workspace.Workspace
	_, err := toml.DecodeFile(filepath.Join(c.agentApp.paths.State, id, "workspace.toml"), &ws)
	require.NoError(c.t, err)
	return ws
}

// withoutIDs is out with each of ids replaced, so two runs on different
// workspaces compare equal.
func withoutIDs(out string, ids ...string) string {
	for _, id := range ids {
		out = strings.ReplaceAll(out, id, "<id>")
	}
	return out
}

// stderrCodeOf is the code of the {"error": {...}} line that stderr ends with.
func stderrCodeOf(t *testing.T, stderr string) string {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &body), stderr)
	return body.Error.Code
}

// replyCode is the code of an operation's error, or "".
func replyCode(err error) string {
	ce, ok := errors.AsType[*cliError](err)
	if !ok {
		return ""
	}
	return string(ce.code)
}

// rawRequest publishes op with args to the agent as the controller does, but
// without Call's own checks, and returns the agent's reply.
func (c *conformance) rawRequest(op string, args any) envproto.Reply {
	c.t.Helper()
	ctx := c.t.Context()
	id, err := newRequestID()
	require.NoError(c.t, err)
	body, err := json.Marshal(args)
	require.NoError(c.t, err)
	spec, known := envproto.LookupOp(op)
	timeout := time.Minute
	if known {
		timeout = min(timeout, spec.Timeout)
	}
	deadline := time.Now().Add(timeout).UTC()
	payload, err := json.Marshal(envproto.Request{Op: op, Args: body, RequestID: id, Deadline: &deadline})
	require.NoError(c.t, err)
	topic := envproto.Topic(c.identity.ID, envproto.LeafRes, id)
	got := make(chan envproto.Reply, 1)
	unsubscribe, err := c.broker.Subscribe(ctx, topic, func(msg envproto.Message) {
		var reply envproto.Reply
		if json.Unmarshal(msg.Payload, &reply) == nil {
			select {
			case got <- reply:
			default:
			}
		}
	})
	require.NoError(c.t, err)
	defer unsubscribe()
	require.NoError(c.t, c.broker.Publish(ctx, envproto.Message{
		Topic:           envproto.Topic(c.identity.ID, envproto.LeafReq),
		Payload:         payload,
		QoS:             1,
		ResponseTopic:   topic,
		CorrelationData: []byte(id),
	}))
	select {
	case reply := <-got:
		return reply
	case <-time.After(10 * time.Second):
		require.FailNow(c.t, "the agent never replied to "+op)
	}
	return envproto.Reply{}
}

// snapshotOf maps the files under dirs, relative to root, to a hash of their
// contents, directories to "dir".
func snapshotOf(t *testing.T, root string, dirs ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, dir := range dirs {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			if d.IsDir() {
				out[rel] = "dir"
				return nil
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			out[rel] = fmt.Sprintf("%x", sha256.Sum256(data))
			return nil
		})
		require.NoError(t, err)
	}
	return out
}

// listedIDs is the ids and stale flags of the entries of a ws list --json output.
func listedIDs(t *testing.T, out string) map[string]listEntryJSON {
	t.Helper()
	var listing listJSON
	require.NoError(t, json.Unmarshal([]byte(out), &listing))
	got := map[string]listEntryJSON{}
	for i := range listing.Workspaces {
		if id := listing.Workspaces[i].ID; id != nil {
			got[*id] = listing.Workspaces[i]
		}
	}
	return got
}

func TestConformance_EnvOps(t *testing.T) {
	t.Run("workspace.list equals ws list --json --all on the agent", func(t *testing.T) {
		c := newConformance(t, true)
		visible := c.createRemote("Visible")
		hidden := c.createRemote("Hidden")
		_, stderr, code := c.agentCLI("hide", "--", hidden)
		require.Equal(t, exitOK, code, stderr)
		got, err := c.call(envproto.OpWorkspaceList, &envproto.ListArgs{})
		require.NoError(t, err)
		want, stderr, code := c.agentCLI("list", "--all")
		require.Equal(t, exitOK, code, stderr)
		assert.JSONEq(t, want, string(got))
		assert.Contains(t, string(got), visible)
		assert.Contains(t, string(got), hidden, "list --all includes the hidden workspace")
	})
	t.Run("workspace.create (claude) equals ws claude --json, plus environment", func(t *testing.T) {
		c := newConformance(t, true)
		stdout, stderr, code := c.controllerCLI("claude", "--env", conformanceEnvName, "--name", "Same")
		require.Equal(t, exitOK, code, stderr)
		local, stderr, code := c.agentCLI("claude", "--parent", "none", "--name", "Same")
		require.Equal(t, exitOK, code, stderr)
		remoteID := resultID(t, json.RawMessage(stdout))
		localID := resultID(t, json.RawMessage(local))
		var remote map[string]json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(stdout), &remote))
		assert.JSONEq(t, `"`+conformanceEnvName+`"`, string(remote["environment"]))
		delete(remote, "environment")
		withoutEnv, err := json.Marshal(remote)
		require.NoError(t, err)
		var remoteObject, localObject map[string]any
		require.NoError(t, json.Unmarshal([]byte(withoutIDs(string(withoutEnv), remoteID)), &remoteObject))
		require.NoError(t, json.Unmarshal([]byte(withoutIDs(local, localID)), &localObject))
		assert.Equal(t, keysOf(localObject), keysOf(remoteObject), "the same fields")
		delete(remoteObject, "session")
		delete(localObject, "session")
		assert.Equal(t, localObject, remoteObject)
		rec := c.record(remoteID)
		assert.Equal(t, 4, rec.Version, "a record with environment is version 4")
		assert.Equal(t, conformanceEnvName, rec.Environment)
	})
	t.Run("a ws machine spawn child on the agent inherits environment", func(t *testing.T) {
		c := newConformance(t, true)
		parent := c.createRemote("Parent")
		var out, errOut bytes.Buffer
		app := c.newApp(&out, &errOut)
		app.getwd = func() (string, error) { return filepath.Join(c.agentApp.paths.Workspaces, parent), nil }
		code := app.Run(t.Context(), []string{"--json", "--config-dir", c.agentDir, "machine", "spawn", "--", "do the thing"})
		require.Equal(t, exitOK, code, errOut.String())
		child := resultID(t, json.RawMessage(out.Bytes()))
		rec := c.record(child)
		assert.Equal(t, parent, rec.Parent)
		assert.Equal(t, conformanceEnvName, rec.Environment, "the child inherits its parent's environment")
		assert.Equal(t, 4, rec.Version)
	})
	t.Run("workspace.rename equals the CLI", func(t *testing.T) {
		c := newConformance(t, true)
		remote, local := c.createRemote("Rename me"), c.createRemote("Rename me")
		got, err := c.call(envproto.OpWorkspaceRename, &envproto.RenameArgs{Workspace: remote, Name: "Renamed"})
		require.NoError(t, err)
		want, stderr, code := c.agentCLI("rename", "--", local, "Renamed")
		require.Equal(t, exitOK, code, stderr)
		assert.JSONEq(t, withoutIDs(want, local), withoutIDs(string(got), remote))
		assert.Equal(t, "Renamed", c.record(remote).Name)
	})
	t.Run("workspace.hide equals the CLI", func(t *testing.T) {
		c := newConformance(t, true)
		remote, local := c.createRemote("Hide me"), c.createRemote("Hide me")
		got, err := c.call(envproto.OpWorkspaceHide, &envproto.HideArgs{Workspace: remote, Hidden: true})
		require.NoError(t, err)
		want, stderr, code := c.agentCLI("hide", "--", local)
		require.Equal(t, exitOK, code, stderr)
		assert.JSONEq(t, withoutIDs(want, local), withoutIDs(string(got), remote))
		assert.True(t, c.record(remote).Hidden)
	})
	t.Run("workspace.rm equals the CLI", func(t *testing.T) {
		c := newConformance(t, true)
		remote, local := c.createRemote("Remove me"), c.createRemote("Remove me")
		got, err := c.call(envproto.OpWorkspaceRm, &envproto.RmArgs{Workspace: remote})
		require.NoError(t, err)
		want, stderr, code := c.agentCLI("rm", "--", local)
		require.Equal(t, exitOK, code, stderr)
		assert.JSONEq(t, withoutIDs(want, local), withoutIDs(string(got), remote))
		assert.NoDirExists(t, filepath.Join(c.agentApp.paths.Workspaces, remote))
	})
	t.Run("agent.status, agent.logs, agent.stop against the fake claude", func(t *testing.T) {
		c := newConformance(t, true)
		id := c.createRemote("Agent")
		c.agentClaud.mu.Lock()
		session := c.agentClaud.started[0]
		c.agentClaud.mu.Unlock()
		status, err := c.call(envproto.OpAgentStatus, &envproto.WorkspaceArgs{Workspace: id})
		require.NoError(t, err)
		var gotStatus envproto.AgentStatusResult
		require.NoError(t, json.Unmarshal(status, &gotStatus))
		assert.Equal(t, id, gotStatus.Workspace)
		assert.Equal(t, session.ID, gotStatus.Session)
		require.NotNil(t, gotStatus.Agent)
		assert.Equal(t, session.SessionID, gotStatus.Agent.SessionID)
		assert.Equal(t, "working", gotStatus.Agent.State)
		logs, err := c.call(envproto.OpAgentLogs, &envproto.LogsArgs{Workspace: id})
		require.NoError(t, err)
		var gotLogs envproto.AgentLogsResult
		require.NoError(t, json.Unmarshal(logs, &gotLogs))
		assert.Equal(t, []string{"first line", "second line"}, gotLogs.Lines)
		stop, err := c.call(envproto.OpAgentStop, &envproto.WorkspaceArgs{Workspace: id})
		require.NoError(t, err)
		var gotStop envproto.AgentStopResult
		require.NoError(t, json.Unmarshal(stop, &gotStop))
		assert.True(t, gotStop.Stopped)
		assert.Equal(t, []string{session.ID}, c.agentClaud.stoppedIDs())
	})
	t.Run("claude missing fails workspace.create with claude_missing", func(t *testing.T) {
		c := newConformance(t, true)
		c.agentClaud.setCheckErr(claude.ErrNotInstalled)
		_, err := c.call(envproto.OpWorkspaceCreate, &envproto.CreateArgs{Kind: envproto.KindClaude, Specs: []string{}})
		assert.Equal(t, string(codeClaudeMissing), replyCode(err))
		_, stderr, code := c.controllerCLI("claude", "--env", conformanceEnvName)
		assert.NotEqual(t, exitOK, code)
		assert.Equal(t, string(codeClaudeMissing), stderrCodeOf(t, stderr))
		assert.Empty(t, c.agentClaud.StartCalls(), "no session was started")
	})
	t.Run("a claude prompt that starts with a dash is refused", func(t *testing.T) {
		c := newConformance(t, true)
		prompt := "--model x"
		_, err := c.call(envproto.OpWorkspaceCreate, &envproto.CreateArgs{Kind: envproto.KindClaude, Specs: []string{}, Prompt: &prompt})
		assert.Equal(t, string(codeUsage), replyCode(err))
		assert.Empty(t, c.agentClaud.StartCalls(), "no session was started")
	})
	t.Run("workspace.hide with Hidden false unhides", func(t *testing.T) {
		c := newConformance(t, true)
		id := c.createRemote("Unhide me")
		_, err := c.call(envproto.OpWorkspaceHide, &envproto.HideArgs{Workspace: id, Hidden: true})
		require.NoError(t, err)
		require.True(t, c.record(id).Hidden)
		_, err = c.call(envproto.OpWorkspaceHide, &envproto.HideArgs{Workspace: id, Hidden: false})
		require.NoError(t, err)
		assert.False(t, c.record(id).Hidden)
	})
	t.Run("an unknown operation and a path outside workspaces/ are refused", func(t *testing.T) {
		c := newConformance(t, true)
		unknown := c.rawRequest("workspace.nuke", map[string]any{})
		assert.False(t, unknown.OK)
		require.NotNil(t, unknown.Error)
		assert.Equal(t, "usage", unknown.Error.Code)
		outside := c.rawRequest(envproto.OpWorkspaceRm, &envproto.RmArgs{Workspace: "/etc"})
		assert.False(t, outside.OK)
		require.NotNil(t, outside.Error)
		assert.Equal(t, "outside_workspace", outside.Error.Code)
		traversal := c.rawRequest(envproto.OpWorkspaceRm, &envproto.RmArgs{Workspace: filepath.Join(c.agentApp.paths.Workspaces, "..", "state")})
		require.NotNil(t, traversal.Error)
		assert.Equal(t, "outside_workspace", traversal.Error.Code)
		_, err := c.call("workspace.nuke", map[string]any{})
		assert.Equal(t, string(codeUsage), replyCode(err), "the controller side refuses an unknown operation too")
	})
	t.Run("ws list --env shows the new workspace after the operation", func(t *testing.T) {
		c := newConformanceEvery(t, true, time.Hour)
		stdout, stderr, code := c.controllerCLI("claude", "--env", conformanceEnvName, "--name", "Fresh")
		require.Equal(t, exitOK, code, stderr)
		id := resultID(t, json.RawMessage(stdout))
		require.Eventually(t, func() bool {
			out, _, listCode := c.controllerCLI("list", "--all", "--env", conformanceEnvName)
			if listCode != exitOK {
				return false
			}
			entry, ok := listedIDs(t, out)[id]
			return ok && entry.Environment == conformanceEnvName
		}, 10*time.Second, 10*time.Millisecond, "the snapshot was not republished after the operation")
	})
	t.Run("an offline environment: ws claude --env fails environment_offline and the local workspaces/ and state/ are unchanged", func(t *testing.T) {
		c := newConformance(t, false)
		_, stderr, code := c.controllerCLI("list")
		require.Equal(t, exitOK, code, stderr)
		before := snapshotOf(t, c.ctrlDir, "workspaces", "state")
		_, stderr, code = c.controllerCLI("claude", "--env", conformanceEnvName, "--name", "Never")
		assert.NotEqual(t, exitOK, code)
		assert.Equal(t, string(codeEnvironmentOffline), stderrCodeOf(t, stderr))
		assert.Equal(t, before, snapshotOf(t, c.ctrlDir, "workspaces", "state"))
		assert.Empty(t, c.ctrlClaude.StartCalls(), "nothing started a local session")
		assert.Empty(t, c.ctrlClaude.CheckCalls(), "nothing touched local")
	})
	t.Run("ws list --env marks the entries stale once the agent is gone", func(t *testing.T) {
		c := newConformance(t, true)
		id := c.createRemote("Stale")
		require.Eventually(t, func() bool {
			out, _, code := c.controllerCLI("list", "--all", "--env", conformanceEnvName)
			_, ok := listedIDs(t, out)[id]
			return code == exitOK && ok
		}, 10*time.Second, 10*time.Millisecond)
		c.stopAgent()
		require.Eventually(t, func() bool { return !c.online() }, 10*time.Second, 10*time.Millisecond, "the controller still sees the agent")
		out, stderr, code := c.controllerCLI("list", "--all", "--env", conformanceEnvName)
		require.Equal(t, exitOK, code, stderr)
		entry, ok := listedIDs(t, out)[id]
		require.True(t, ok)
		assert.True(t, entry.Stale)
		assert.Equal(t, envOffline, entry.EnvironmentStatus)
	})
	t.Run("review.start to a protocol 2 agent is environment_outdated", func(t *testing.T) {
		c := newConformance(t, false)
		c.publishHello(2)
		require.Eventually(t, c.online, 10*time.Second, 10*time.Millisecond)
		_, err := c.call(envproto.OpReviewStart, &envproto.ReviewStartArgs{Repo: "o/n", PR: new(1)})
		require.Error(t, err)
		assert.Equal(t, string(codeEnvironmentOutdated), replyCode(err))
		_, err = c.call(envproto.OpLoopList, &envproto.LoopListArgs{})
		assert.Equal(t, string(codeEnvironmentOutdated), replyCode(err))
	})
	t.Run("review list --env equals the remote's ws review list", func(t *testing.T) {
		c := newConformance(t, true)
		c.seedReview("review-buff-bear-42-3f2a9c1-d8efb24f", reviewDoc("security"))
		remote, stderr, code := c.agentCLI("review", "list")
		require.Equal(t, exitOK, code, stderr)
		got, stderr, code := c.controllerCLI("review", "list", "--env", conformanceEnvName)
		require.Equal(t, exitOK, code, stderr)
		var want, have map[string]any
		require.NoError(t, json.Unmarshal([]byte(remote), &want))
		require.NoError(t, json.Unmarshal([]byte(got), &have))
		assert.Equal(t, conformanceEnvName, have["environment"])
		assert.EqualValues(t, 0, have["redacted"])
		assert.Equal(t, false, have["truncated"])
		delete(have, "environment")
		delete(have, "redacted")
		delete(have, "truncated")
		assert.Equal(t, want, have)
		assert.NotEmpty(t, have["reviews"])
	})
	t.Run("loop list --env equals the remote's ws loop list", func(t *testing.T) {
		c := newConformance(t, true)
		c.addLoop("deps", time.Now().Add(time.Hour))
		remote, stderr, code := c.agentCLI("loop", "list")
		require.Equal(t, exitOK, code, stderr)
		got, stderr, code := c.controllerCLI("loop", "list", "--env", conformanceEnvName)
		require.Equal(t, exitOK, code, stderr)
		var want, have map[string]any
		require.NoError(t, json.Unmarshal([]byte(remote), &want))
		require.NoError(t, json.Unmarshal([]byte(got), &have))
		assert.Equal(t, conformanceEnvName, have["environment"])
		assert.EqualValues(t, 0, have["redacted"])
		delete(have, "environment")
		delete(have, "redacted")
		entries, ok := have["loops"].([]any)
		require.True(t, ok)
		require.Len(t, entries, 1)
		entry, ok := entries[0].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, conformanceEnvName, entry["environment"])
		assert.Equal(t, false, entry["stale"])
		delete(entry, "environment")
		delete(entry, "stale")
		assert.Equal(t, want, have)
	})
	t.Run("a 300-comment report arrives whole through pages", func(t *testing.T) {
		c := newConformance(t, true)
		id := "review-buff-bear-42-3f2a9c1-d8efb24f"
		c.seedReview(id, bigDoc("security", 300, 3000, ""))
		out, stderr, code := c.controllerCLI("review", "report", "--env", conformanceEnvName, id)
		require.Equal(t, exitOK, code, stderr)
		var report struct {
			Comments []json.RawMessage `json:"comments"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &report))
		assert.Len(t, report.Comments, 300)
		assert.Greater(t, len(out), envproto.MaxPageBytes, "the report is more than one page")
	})
	t.Run("a ghp token in a comment never appears in any MQTT payload", func(t *testing.T) {
		c := newConformance(t, true)
		token := "ghp_" + strings.Repeat("Q", 36)
		var mu sync.Mutex
		var payloads [][]byte
		unsubscribe, err := c.broker.Subscribe(t.Context(), "#", func(msg envproto.Message) {
			mu.Lock()
			defer mu.Unlock()
			payloads = append(payloads, append([]byte{}, msg.Payload...))
		})
		require.NoError(t, err)
		defer unsubscribe()
		id := "review-buff-bear-42-3f2a9c1-d8efb24f"
		c.seedReview(id, reviewDoc("security", "c.do ignores", "leaked "+token+" c.do ignores"))
		out, stderr, code := c.controllerCLI("review", "report", "--env", conformanceEnvName, id)
		require.Equal(t, exitOK, code, stderr)
		assert.NotContains(t, out, token)
		assert.Contains(t, out, "[redacted]")
		mu.Lock()
		defer mu.Unlock()
		require.NotEmpty(t, payloads)
		for _, p := range payloads {
			assert.NotContains(t, string(p), token)
			assert.NotContains(t, string(p), "ghp_")
		}
	})
	t.Run("loop.rm sent raw without confirm is refused by the agent", func(t *testing.T) {
		c := newConformance(t, true)
		c.addLoop("deps", time.Now().Add(time.Hour))
		reply := c.rawRequest(envproto.OpLoopRm, map[string]any{"name": "deps", "confirm": false})
		require.False(t, reply.OK)
		assert.Equal(t, string(codeUsage), reply.Error.Code)
		_, err := c.agentApp.loops.Get("deps")
		require.NoError(t, err, "the loop is still there")
		reply = c.rawRequest(envproto.OpLoopRm, map[string]any{"name": "deps", "confirm": true})
		require.True(t, reply.OK, "%+v", reply.Error)
		_, err = c.agentApp.loops.Get("deps")
		require.Error(t, err)
	})
	t.Run("loop.memory_set of a redacted value is refused", func(t *testing.T) {
		c := newConformance(t, true)
		c.addLoop("deps", time.Now().Add(time.Hour))
		reply := c.rawRequest(envproto.OpLoopMemorySet, map[string]any{"name": "deps", "key": "token", "value": "[redacted]"})
		require.False(t, reply.OK)
		assert.Equal(t, string(codeUsage), reply.Error.Code)
		_, err := c.call(envproto.OpLoopMemoryGet, &envproto.LoopMemoryKey{Name: "deps", Key: "token"})
		require.Error(t, err, "nothing was stored")
	})
	t.Run("a script is refused when the remote turned scripts off", func(t *testing.T) {
		c := newConformance(t, false)
		require.NoError(t, os.WriteFile(filepath.Join(c.agentDir, "config.toml"), []byte("[environments]\nagent_loop_scripts = false\n"+conformanceSharedClaude), 0o600))
		c.runAgent()
		_, err := c.call(envproto.OpLoopAdd, &envproto.LoopSpec{Name: "gated", Every: new("1h"), Prompt: new("Do it"), Script: new("true"), Confirm: true})
		require.Error(t, err)
		assert.Equal(t, string(codeUsage), replyCode(err))
		_, getErr := c.agentApp.loops.Get("gated")
		require.Error(t, getErr)
		_, err = c.call(envproto.OpLoopAdd, &envproto.LoopSpec{Name: "plain", Every: new("1h"), Prompt: new("Do it")})
		require.NoError(t, err)
	})
	t.Run("the 13th review.start in an hour is operation_limited", func(t *testing.T) {
		c := newConformance(t, true)
		args := &envproto.ReviewStartArgs{Repo: "o/n", PR: new(1)}
		for range 12 {
			_, err := c.call(envproto.OpReviewStart, args)
			require.Error(t, err)
			require.NotEqual(t, string(codeOperationLimited), replyCode(err))
		}
		_, err := c.call(envproto.OpReviewStart, args)
		require.Error(t, err)
		assert.Equal(t, string(codeOperationLimited), replyCode(err))
	})
	t.Run("a due loop starts on the remote within one LoopTick of connecting", func(t *testing.T) {
		c := newConformance(t, false)
		c.loopTick = 200 * time.Millisecond
		c.addLoop("deps", time.Now().Add(-time.Minute))
		c.runAgent()
		require.Eventually(t, func() bool { return c.agentClaud.startedCount() == 1 }, 2*time.Second, 10*time.Millisecond, "no loop run started")
	})
	t.Run("no loop starts while the agent is disconnected", func(t *testing.T) {
		c := newConformance(t, false)
		c.loopTick = 200 * time.Millisecond
		c.addLoop("deps", time.Now().Add(-time.Minute))
		time.Sleep(time.Second)
		assert.Zero(t, c.agentClaud.startedCount(), "a loop started with the agent never connected")
		c.runAgent()
		c.stopAgent()
		before := c.agentClaud.startedCount()
		time.Sleep(time.Second)
		assert.Equal(t, before, c.agentClaud.startedCount(), "a loop started after the agent disconnected")
	})
	t.Run("three overdue loops start two in the first pass and one in the next", func(t *testing.T) {
		c := newConformance(t, false)
		c.loopTick = 500 * time.Millisecond
		for _, name := range []string{"one", "two", "three"} {
			c.addLoop(name, time.Now().Add(-time.Minute))
		}
		c.runAgent()
		require.Eventually(t, func() bool { return c.agentClaud.startedCount() >= 2 }, 3*time.Second, 5*time.Millisecond)
		time.Sleep(100 * time.Millisecond)
		assert.Equal(t, 2, c.agentClaud.startedCount(), "the first pass started more than two")
		require.Eventually(t, func() bool { return c.agentClaud.startedCount() == 3 }, 3*time.Second, 10*time.Millisecond, "the third loop never started")
	})
	t.Run("the controller's snapshot lists the remote's loops without prompts", func(t *testing.T) {
		c := newConformance(t, true)
		_, err := c.call(envproto.OpLoopAdd, &envproto.LoopSpec{Name: "deps", Title: new("Deps"), Every: new("1h"), Prompt: new("SECRET-PROMPT-TEXT"), Vars: &map[string]string{"K": "SECRET-VAR-VALUE"}})
		require.NoError(t, err)
		var loops []envproto.SnapshotLoop
		require.Eventually(t, func() bool {
			_, snap, snapErr := c.store.Snapshot(conformanceEnvName)
			loops = snap.Loops
			return snapErr == nil && len(loops) == 1
		}, 10*time.Second, 10*time.Millisecond, "the snapshot has no loops")
		assert.Equal(t, "deps", loops[0].Name)
		assert.Equal(t, "Deps", loops[0].Title)
		file, err := c.store.ReadSnapshot(conformanceEnvName)
		require.NoError(t, err)
		assert.NotContains(t, string(file), "SECRET-PROMPT-TEXT")
		assert.NotContains(t, string(file), "SECRET-VAR-VALUE")
	})
	t.Run("ws review start --env from inside a workspace is refused", func(t *testing.T) {
		c := newConformance(t, true)
		app := c.controllerApp(io.Discard, io.Discard)
		stderr := &bytes.Buffer{}
		app.stderr = stderr
		app.getwd = func() (string, error) { return "/cfg/workspaces/fix-login-d8efb24f/buff-bear", nil }
		app.newStores = func(config.Paths, config.Config) stores {
			ws := stubWorkspaces()
			ws.ContainingFunc = func(string) (workspace.Workspace, error) { return workspace.Workspace{ID: "fix-login-d8efb24f"}, nil }
			return stores{cache: stubCache(), workspaces: ws, lifetime: stubLifetime(), loopData: app.loopData}
		}
		before := snapshotOf(t, c.agentDir, "workspaces", "state")
		code := app.Run(t.Context(), []string{"--json", "--config-dir", c.ctrlDir, "review", "start", "--env", conformanceEnvName, "--pr", "1", "buff-bear"})
		assert.NotEqual(t, exitOK, code)
		assert.Equal(t, string(codeUsage), stderrCodeOf(t, stderr.String()))
		assert.Contains(t, stderr.String(), "an agent never chooses an environment")
		assert.Equal(t, before, snapshotOf(t, c.agentDir, "workspaces", "state"), "nothing reached the remote")
	})
	t.Run("ws review start run by a remote workspace's agent creates the review on the remote", func(t *testing.T) {
		c := newConformance(t, true)
		before := snapshotOf(t, c.ctrlDir, "workspaces", "state", "reviews")
		e := newReviewStartEnv(t)
		e.app.environment = conformanceEnvName
		require.NoError(t, e.start(t, &reviewStartOptions{pr: 42}))
		rec := e.record(t)
		assert.Equal(t, "fix-login-d8efb24f", rec.Requester, "the review is the child of the remote workspace that ran it")
		assert.Equal(t, before, snapshotOf(t, c.ctrlDir, "workspaces", "state", "reviews"), "the controller's store stays empty")
		assert.Empty(t, e.cache.AddCalls(), "no environment was dialled")
	})
	t.Run("a review id with .. is refused", func(t *testing.T) {
		c := newConformance(t, true)
		for _, review := range []string{"..", "../x", "a/b", "-x"} {
			for op, args := range map[string]any{
				envproto.OpReviewStatus: map[string]any{"review": review},
				envproto.OpReviewReport: map[string]any{"review": review, "section": "comments", "offset": 0},
			} {
				reply := c.rawRequest(op, args)
				require.False(t, reply.OK, "%s %q", op, review)
				assert.Equal(t, string(codeUsage), reply.Error.Code, "%s %q", op, review)
			}
		}
	})
	t.Run("a loop name with / is refused", func(t *testing.T) {
		c := newConformance(t, true)
		before := snapshotOf(t, c.agentDir, "loops", "state")
		for _, name := range []string{"../x", "a/b", "A", ".hidden"} {
			reply := c.rawRequest(envproto.OpLoopShow, map[string]any{"name": name})
			require.False(t, reply.OK, "%q", name)
			assert.Equal(t, string(codeNameInvalid), reply.Error.Code, "%q", name)
			reply = c.rawRequest(envproto.OpLoopAdd, map[string]any{"name": name, "every": "1h", "prompt": "x"})
			require.False(t, reply.OK, "%q", name)
		}
		assert.Equal(t, before, snapshotOf(t, c.agentDir, "loops", "state"))
	})
	t.Run("bypassPermissions is refused for review.start", func(t *testing.T) {
		c := newConformance(t, true)
		reply := c.rawRequest(envproto.OpReviewStart, map[string]any{"repo": "o/n", "pr": 1, "permission_mode": "bypassPermissions"})
		require.False(t, reply.OK)
		assert.Equal(t, string(codeUsage), reply.Error.Code)
		reply = c.rawRequest(envproto.OpReviewStart, map[string]any{"repo": "o/n", "pr": 1, "permission_mode": "bypassPermissions", "confirm": true})
		require.False(t, reply.OK, "an extra confirm key doesn't unlock it")
	})
}

// publishHello makes the controller see the environment online as an agent
// that speaks protocol, as its hello says, with no agent running.
func (c *conformance) publishHello(protocol int) {
	c.t.Helper()
	hello := envproto.Hello{
		Status: envproto.StatusOnline, At: time.Now().UTC(), WSVersion: "0.30.0", OS: "linux", Arch: "amd64",
		Claude: envproto.ClaudePresent, CertSerial: "1", CertNotAfter: time.Now().Add(time.Hour).UTC(),
		CAFingerprints: []string{}, Protocol: protocol,
	}
	payload, err := json.Marshal(hello)
	require.NoError(c.t, err)
	require.NoError(c.t, c.broker.Publish(c.t.Context(), envproto.Message{
		Topic: envproto.Topic(c.identity.ID, envproto.LeafStatus), Payload: payload, QoS: 1, Retain: true,
	}))
}

// seedReview stores a finished review called id, with doc submitted for its
// profile, on the agent's machine.
func (c *conformance) seedReview(id, doc string) {
	c.t.Helper()
	rec := testRecord()
	rec.Requester = ""
	reviews := store.New(c.agentApp.paths.Reviews)
	require.NoError(c.t, reviews.SaveRun(id, &rec))
	repoID, err := repo.ParseID(reviewedRepo)
	require.NoError(c.t, err)
	_, err = reviews.Submit(c.t.Context(), repoID, testCommit, "security", []byte(doc))
	require.NoError(c.t, err)
}

// addLoop stores a loop called name, next due at due, on the agent's machine.
func (c *conformance) addLoop(name string, due time.Time) {
	c.t.Helper()
	l := loop.Loop{Version: loop.FormatVersion, Name: name, Every: "1h", Prompt: "Do it", Keep: 5, CreatedAt: due.Add(-2 * time.Hour), NextRunAt: due}
	require.NoError(c.t, c.agentApp.loops.Create(&l))
}

// startedCount is how many Claude sessions have been started.
func (f *fakeClaude) startedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.started)
}

// keysOf is the sorted keys of m.
func keysOf(m map[string]any) []string { return slices.Sorted(maps.Keys(m)) }
