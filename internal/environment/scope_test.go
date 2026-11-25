package environment

import (
	"crypto/tls"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/environment/envtest"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/envscope"
)

// scopeFixture is a started controller over enrolled environments, each with
// a connected agent (callFixture), a ledger in a temp dir and a fake
// LocalScopes whose workspaces are localWS.
type scopeFixture struct {
	c       *Controller
	b       *envtest.Broker
	store   *Store
	ledger  *envscope.Ledger
	localWS map[string]envscope.Scope
	envs    map[string]callFixture
}

// newScopeFixture enrolls names, starts a controller and connects each agent.
// Every environment says hello online at protocol 4.
func newScopeFixture(t *testing.T, names ...string) scopeFixture {
	t.Helper()
	store := newTestStore(t)
	b := envtest.NewBroker()
	recs := map[string]Record{}
	certs := map[string]*tls.Certificate{}
	for _, name := range names {
		rec, cert := enrollAgent(t, store, name)
		recs[name] = rec
		certs[name] = &cert
	}
	c := startController(t, store, b)
	f := scopeFixture{c: c, b: b, store: store, ledger: envscope.NewLedger(t.TempDir()), localWS: map[string]envscope.Scope{}, envs: map[string]callFixture{}}
	for _, name := range names {
		agent := connectAgent(t, store, b, recs[name].ID, certs[name])
		env := callFixture{store: store, b: b, c: c, id: recs[name].ID, agent: agent}
		env.hear(t, envproto.StatusOnline, envproto.Protocol)
		f.envs[name] = env
	}
	c.SetLocalScopes(localFake{ws: f.localWS}, f.ledger)
	return f
}

// localFake serves the fixture's local workspaces.
type localFake struct{ ws map[string]envscope.Scope }

func (l localFake) Scope(id string) (envscope.Scope, error) {
	s, ok := l.ws[id]
	if !ok {
		return envscope.Scope{}, ErrWorkspaceNotFound
	}
	return s, nil
}

func (l localFake) Exists(id string) (bool, error) {
	_, ok := l.ws[id]
	return ok, nil
}

// env is the fixture's environment called name.
func (f scopeFixture) env(name string) callFixture {
	return f.envs[name]
}

// grant writes a ledger entry for workspace ws on env with scope ids and no parent.
func (f scopeFixture) grant(t *testing.T, env, ws string, scope ...string) {
	t.Helper()
	entry := envscope.Entry{Environment: env, Workspace: ws, Scope: scope, CreatedAt: f.c.now().UTC()}
	require.NoError(t, f.ledger.Put(t.Context(), &entry))
}

// spawnArgs is a scope.spawn from workspace ws with claimed scope, to target, narrowed to narrow (nil: same as the caller).
func spawnArgs(ws string, claimed []string, target string, narrow []string) envproto.ScopeSpawnArgs {
	return envproto.ScopeSpawnArgs{
		ScopeCallerArgs: envproto.ScopeCallerArgs{Workspace: &ws, Ancestors: []string{}, Scope: claimed},
		Target:          target,
		Narrow:          narrow,
		Create:          envproto.CreateArgs{Kind: envproto.KindClaude, Specs: []string{}},
	}
}

// childArgs is a scope.child from workspace ws on the child's parent, on environment env.
func childArgs(ws, env, child, parent, op string) envproto.ScopeChildArgs {
	return envproto.ScopeChildArgs{
		ScopeCallerArgs: envproto.ScopeCallerArgs{Workspace: &ws, Ancestors: []string{}, Scope: []string{}},
		Environment:     env,
		Child:           child,
		Parent:          parent,
		Op:              op,
	}
}

// assertScopeError checks that reply failed with code and returns its error.
func assertScopeError(t *testing.T, reply envproto.Reply, code string) *envproto.ReplyError {
	t.Helper()
	require.False(t, reply.OK, "reply was ok: %s", reply.Result)
	require.NotNil(t, reply.Error)
	assert.Equal(t, code, reply.Error.Code)
	return reply.Error
}

// writeSnapshot stores a fresh snapshot of environment name listing workspaces (a JSON array).
func writeSnapshot(t *testing.T, f scopeFixture, name, workspaces string) {
	t.Helper()
	rec, err := f.store.Get(name)
	require.NoError(t, err)
	snap := envproto.Snapshot{At: f.c.now().UTC(), Protocol: envproto.Protocol, Workspaces: json.RawMessage(workspaces), Agents: []envproto.SnapshotAgent{}, Loops: []envproto.SnapshotLoop{}}
	data, err := json.Marshal(SnapshotFile{
		Version:    snapshotVersion,
		ID:         rec.ID,
		Name:       name,
		ReceivedAt: f.c.now().UTC().Format(time.RFC3339),
		Snapshot:   mustJSON(t, snap),
	})
	require.NoError(t, err)
	require.NoError(t, f.store.WriteSnapshot(name, data))
}

func TestController_Scope(t *testing.T) {
	t.Run("spawn refuses a claim wider than the ledger ceiling", func(t *testing.T) {
		f := newScopeFixture(t, "env-a", "env-b")
		a, b := f.env("env-a"), f.env("env-b")
		f.grant(t, a.id, "w", a.id)
		requests := b.captureRequests(t)
		reply := f.c.Scope(t.Context(), a.id, "spawn", mustJSON(t, spawnArgs("w", []string{a.id, b.id}, "env-b", nil)))
		e := assertScopeError(t, reply, "environment_out_of_scope")
		assert.Equal(t, []string{"env-a"}, e.Details["allowed"])
		assert.Empty(t, requests.messages())
	})
	t.Run("spawn ignores ledger entries on other environments", func(t *testing.T) {
		f := newScopeFixture(t, "env-a", "env-b", "env-c")
		a, b, c := f.env("env-a"), f.env("env-b"), f.env("env-c")
		f.grant(t, c.id, "w", c.id, b.id)
		requests := b.captureRequests(t)
		reply := f.c.Scope(t.Context(), a.id, "spawn", mustJSON(t, spawnArgs("w", []string{a.id, b.id}, "env-b", nil)))
		assertScopeError(t, reply, "environment_out_of_scope")
		assert.Empty(t, requests.messages())
	})
	t.Run("child refuses a ledger parent on another environment", func(t *testing.T) {
		f := newScopeFixture(t, "env-a", "env-b", "env-c")
		a, b, c := f.env("env-a"), f.env("env-b"), f.env("env-c")
		require.NoError(t, f.ledger.Put(t.Context(), &envscope.Entry{
			Environment: b.id,
			Workspace:   "c-1",
			Scope:       []string{b.id},
			Parent:      &envscope.Parent{Environment: c.id, Workspace: "p-1"},
			CreatedAt:   f.c.now().UTC(),
		}))
		requests := b.captureRequests(t)
		reply := f.c.Scope(t.Context(), a.id, "child", mustJSON(t, childArgs("p-1", "env-b", "c-1", "p-1", "rm")))
		assertScopeError(t, reply, "not_descendant")
		assert.Empty(t, requests.messages())
	})
	t.Run("spawn sends the scope and writes the ledger", func(t *testing.T) {
		f := newScopeFixture(t, "env-a", "env-b")
		a, b := f.env("env-a"), f.env("env-b")
		f.grant(t, a.id, "w", a.id, b.id)
		requests := b.captureRequests(t)
		b.answerRequests(t, envproto.Reply{OK: true, Result: json.RawMessage(`{"id":"h-1a2b3c4d"}`)})
		reply := f.c.Scope(t.Context(), a.id, "spawn", mustJSON(t, spawnArgs("w", []string{a.id, b.id}, "env-b", nil)))
		require.True(t, reply.OK, "%+v", reply.Error)

		sent := requests.messages()
		require.Len(t, sent, 1)
		req := requestOf(t, &sent[0])
		assert.Equal(t, envproto.OpWorkspaceCreate, req.Op)
		var create envproto.CreateArgs
		require.NoError(t, json.Unmarshal(req.Args, &create))
		require.NotNil(t, create.Scope)
		assert.Equal(t, []string{a.id, b.id}, envIDs(create.Scope.Environments))
		assert.Equal(t, &envproto.WorkspaceAt{Environment: a.id, Workspace: "w"}, create.Scope.Parent)

		entry, found, err := f.ledger.Get(b.id, "h-1a2b3c4d")
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, []string{a.id, b.id}, entry.Scope)
		assert.Equal(t, &envscope.Parent{Environment: a.id, Workspace: "w"}, entry.Parent)

		var result map[string]any
		require.NoError(t, json.Unmarshal(reply.Result, &result))
		assert.Equal(t, "h-1a2b3c4d", result["id"])
		assert.Equal(t, "env-b", result["environment"])
		assert.Equal(t, []any{"env-a", "env-b"}, result["scope"])
	})
	t.Run("spawn narrows the child scope", func(t *testing.T) {
		f := newScopeFixture(t, "env-a", "env-b", "env-c")
		a, b, c := f.env("env-a"), f.env("env-b"), f.env("env-c")
		f.grant(t, a.id, "w", a.id, b.id, c.id)
		requests := b.captureRequests(t)
		b.answerRequests(t, envproto.Reply{OK: true, Result: json.RawMessage(`{"id":"h-1a2b3c4d"}`)})
		reply := f.c.Scope(t.Context(), a.id, "spawn", mustJSON(t, spawnArgs("w", []string{a.id, b.id, c.id}, "env-b", []string{"env-b"})))
		require.True(t, reply.OK, "%+v", reply.Error)
		entry, _, err := f.ledger.Get(b.id, "h-1a2b3c4d")
		require.NoError(t, err)
		assert.Equal(t, []string{b.id}, entry.Scope)
		require.Len(t, requests.messages(), 1)
	})
	t.Run("spawn refuses a wider narrow", func(t *testing.T) {
		f := newScopeFixture(t, "env-a", "env-b", "env-c")
		a, b := f.env("env-a"), f.env("env-b")
		f.grant(t, a.id, "w", a.id, b.id)
		requests := b.captureRequests(t)
		reply := f.c.Scope(t.Context(), a.id, "spawn", mustJSON(t, spawnArgs("w", []string{a.id, b.id}, "env-b", []string{"env-c"})))
		assertScopeError(t, reply, "environment_out_of_scope")
		assert.Empty(t, requests.messages())
	})
	t.Run("spawn to an outdated agent is environment_outdated", func(t *testing.T) {
		f := newScopeFixture(t, "env-a", "env-b")
		a, b := f.env("env-a"), f.env("env-b")
		f.grant(t, a.id, "w", a.id, b.id)
		b.hear(t, envproto.StatusOnline, 3)
		requests := b.captureRequests(t)
		reply := f.c.Scope(t.Context(), a.id, "spawn", mustJSON(t, spawnArgs("w", []string{a.id, b.id}, "env-b", nil)))
		e := assertScopeError(t, reply, "environment_outdated")
		assert.EqualValues(t, envproto.ProtocolScopes, e.Details["need_protocol"])
		assert.Empty(t, requests.messages())
	})
	t.Run("spawn to an offline environment is environment_offline", func(t *testing.T) {
		f := newScopeFixture(t, "env-a", "env-b")
		a, b := f.env("env-a"), f.env("env-b")
		f.grant(t, a.id, "w", a.id, b.id)
		b.hear(t, envproto.StatusOffline, envproto.Protocol)
		reply := f.c.Scope(t.Context(), a.id, "spawn", mustJSON(t, spawnArgs("w", []string{a.id, b.id}, "env-b", nil)))
		assertScopeError(t, reply, "environment_offline")
	})
	t.Run("spawn to local from an environment is usage", func(t *testing.T) {
		f := newScopeFixture(t, "env-a")
		a := f.env("env-a")
		f.grant(t, a.id, "w", a.id, envscope.Local)
		reply := f.c.Scope(t.Context(), a.id, "spawn", mustJSON(t, spawnArgs("w", []string{a.id, envscope.Local}, "local", nil)))
		assertScopeError(t, reply, "usage")
	})
	t.Run("a retried spawn reuses the request id", func(t *testing.T) {
		f := newScopeFixture(t, "env-a", "env-b")
		a, b := f.env("env-a"), f.env("env-b")
		f.grant(t, a.id, "w", a.id, b.id)
		requests := b.captureRequests(t)
		b.answerRequests(t, envproto.Reply{OK: true, Result: json.RawMessage(`{"id":"h-1a2b3c4d"}`)})
		reply := f.c.scope(t.Context(), a.id, "spawn", mustJSON(t, spawnArgs("w", []string{a.id, b.id}, "env-b", nil)), callRequestID)
		require.True(t, reply.OK, "%+v", reply.Error)
		sent := requests.messages()
		require.Len(t, sent, 1)
		assert.Equal(t, callRequestID, requestOf(t, &sent[0]).RequestID)
	})
	t.Run("local caller uses the local scope file, not the claim", func(t *testing.T) {
		f := newScopeFixture(t, "env-b")
		b := f.env("env-b")
		local := envscope.Only(envscope.Ref{ID: envscope.Local, Name: envscope.Local})
		f.localWS["w"] = local
		requests := b.captureRequests(t)
		reply := f.c.Scope(t.Context(), envscope.Local, "spawn", mustJSON(t, spawnArgs("w", []string{envscope.Local, b.id}, "env-b", nil)))
		e := assertScopeError(t, reply, "environment_out_of_scope")
		assert.Equal(t, []string{envscope.Local}, e.Details["allowed"])
		assert.Empty(t, requests.messages())
	})
	t.Run("envs lists states", func(t *testing.T) {
		f := newScopeFixture(t, "env-a", "env-b")
		a, b := f.env("env-a"), f.env("env-b")
		b.hear(t, envproto.StatusOffline, envproto.Protocol)
		gone := envscope.Ref{ID: "env-1a2b3c4d", Name: "gone"}
		scope, err := envscope.New(envscope.Ref{ID: envscope.Local, Name: envscope.Local}, []envscope.Ref{
			{ID: a.id, Name: "env-a"}, {ID: b.id, Name: "env-b"}, gone,
		})
		require.NoError(t, err)
		f.localWS["w"] = scope
		reply := f.c.Scope(t.Context(), envscope.Local, "envs", mustJSON(t, envproto.ScopeEnvsArgs{ScopeCallerArgs: envproto.ScopeCallerArgs{Workspace: ptr("w")}}))
		require.True(t, reply.OK, "%+v", reply.Error)
		var result envproto.ScopeEnvsResult
		require.NoError(t, json.Unmarshal(reply.Result, &result))
		assert.Equal(t, []envproto.ScopeEnv{
			{Name: "local", ID: envscope.Local, State: "online", Current: true},
			{Name: "env-a", ID: a.id, State: "online"},
			{Name: "env-b", ID: b.id, State: "offline"},
			{Name: "gone", ID: gone.ID, State: "removed"},
		}, result.Environments)
	})
	t.Run("children lists ledger children of the caller and its descendants", func(t *testing.T) {
		f := newScopeFixture(t, "env-a", "env-b")
		a, b := f.env("env-a"), f.env("env-b")
		put := func(ws, parent string) {
			require.NoError(t, f.ledger.Put(t.Context(), &envscope.Entry{
				Environment: b.id, Workspace: ws, Scope: []string{b.id},
				Parent: &envscope.Parent{Environment: a.id, Workspace: parent}, CreatedAt: f.c.now().UTC(),
			}))
		}
		put("c-1", "w")
		put("c-2", "w-desc")
		put("c-3", "other")
		writeSnapshot(t, f, "env-b", `[{"id":"c-1","status":"running"}]`)
		reply := f.c.Scope(t.Context(), a.id, "children", mustJSON(t, envproto.ScopeChildrenArgs{
			ScopeCallerArgs: envproto.ScopeCallerArgs{Workspace: ptr("w")},
			Descendants:     []string{"w-desc"},
		}))
		require.True(t, reply.OK, "%+v", reply.Error)
		var result envproto.ScopeChildrenResult
		require.NoError(t, json.Unmarshal(reply.Result, &result))
		require.Len(t, result.Children, 2)
		assert.Equal(t, "c-1", result.Children[0].Workspace)
		assert.Equal(t, "env-b", result.Children[0].Environment)
		assert.JSONEq(t, `{"id":"c-1","status":"running"}`, string(result.Children[0].Entry))
		assert.Equal(t, "c-2", result.Children[1].Workspace)
		assert.JSONEq(t, `null`, string(result.Children[1].Entry))
	})
	t.Run("child forwards status, logs, record and rm, and rm removes the entry", func(t *testing.T) {
		f := newScopeFixture(t, "env-a", "env-b")
		a, b := f.env("env-a"), f.env("env-b")
		require.NoError(t, f.ledger.Put(t.Context(), &envscope.Entry{
			Environment: b.id, Workspace: "c-1", Scope: []string{b.id},
			Parent: &envscope.Parent{Environment: a.id, Workspace: "w"}, CreatedAt: f.c.now().UTC(),
		}))
		requests := b.captureRequests(t)
		b.answerRequests(t, envproto.Reply{OK: true, Result: json.RawMessage(`{}`)})
		for _, op := range []string{"status", "logs", "record", "rm"} {
			reply := f.c.Scope(t.Context(), a.id, "child", mustJSON(t, childArgs("w", "env-b", "c-1", "w", op)))
			require.True(t, reply.OK, "%s: %+v", op, reply.Error)
		}
		sent := requests.messages()
		require.Len(t, sent, 4)
		assert.Equal(t, envproto.OpAgentStatus, requestOf(t, &sent[0]).Op)
		assert.Equal(t, envproto.OpAgentLogs, requestOf(t, &sent[1]).Op)
		assert.Equal(t, envproto.OpMachineRecord, requestOf(t, &sent[2]).Op)
		assert.Equal(t, envproto.OpWorkspaceRm, requestOf(t, &sent[3]).Op)
		_, found, err := f.ledger.Get(b.id, "c-1")
		require.NoError(t, err)
		assert.False(t, found)
	})
}

// envIDs returns the ids of refs, in order.
func envIDs(refs []envproto.EnvRef) []string {
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		ids = append(ids, ref.ID)
	}
	return ids
}

// ptr is a pointer to v.
func ptr[T any](v T) *T {
	return &v
}

func TestController_sweepLedger(t *testing.T) {
	t.Run("a gone parent gets workspace.rm and the entry is dropped", func(t *testing.T) {
		f := newScopeFixture(t, "env-b")
		b := f.env("env-b")
		require.NoError(t, f.ledger.Put(t.Context(), &envscope.Entry{
			Environment: b.id, Workspace: "c-1", Scope: []string{b.id},
			Parent: &envscope.Parent{Environment: envscope.Local, Workspace: "w"}, CreatedAt: f.c.now().UTC(),
		}))
		writeSnapshot(t, f, "env-b", `[{"id":"c-1"}]`)
		requests := b.captureRequests(t)
		b.answerRequests(t, envproto.Reply{OK: true, Result: json.RawMessage(`{}`)})
		f.c.sweepLedger(t.Context())
		sent := requests.messages()
		require.Len(t, sent, 1)
		req := requestOf(t, &sent[0])
		assert.Equal(t, envproto.OpWorkspaceRm, req.Op)
		var rm envproto.RmArgs
		require.NoError(t, json.Unmarshal(req.Args, &rm))
		assert.Equal(t, envproto.RmArgs{Workspace: "c-1", KeepChildren: false}, rm)
		_, found, err := f.ledger.Get(b.id, "c-1")
		require.NoError(t, err)
		assert.False(t, found)
	})
	t.Run("a child whose environment is offline is kept", func(t *testing.T) {
		f := newScopeFixture(t, "env-b")
		b := f.env("env-b")
		require.NoError(t, f.ledger.Put(t.Context(), &envscope.Entry{
			Environment: b.id, Workspace: "c-1", Scope: []string{b.id},
			Parent: &envscope.Parent{Environment: envscope.Local, Workspace: "w"}, CreatedAt: f.c.now().UTC(),
		}))
		b.hear(t, envproto.StatusOffline, envproto.Protocol)
		requests := b.captureRequests(t)
		f.c.sweepLedger(t.Context())
		assert.Empty(t, requests.messages())
		_, found, err := f.ledger.Get(b.id, "c-1")
		require.NoError(t, err)
		assert.True(t, found)
	})
	t.Run("an entry whose environment record is gone is dropped", func(t *testing.T) {
		f := newScopeFixture(t, "env-b")
		require.NoError(t, f.ledger.Put(t.Context(), &envscope.Entry{
			Environment: "env-1a2b3c4d", Workspace: "c-1", Scope: []string{"env-1a2b3c4d"}, CreatedAt: f.c.now().UTC(),
		}))
		f.c.sweepLedger(t.Context())
		entries, err := f.ledger.List()
		require.NoError(t, err)
		assert.Empty(t, entries)
	})
	t.Run("a child absent from its fresh snapshot is dropped", func(t *testing.T) {
		f := newScopeFixture(t, "env-b")
		b := f.env("env-b")
		require.NoError(t, f.ledger.Put(t.Context(), &envscope.Entry{
			Environment: b.id, Workspace: "c-1", Scope: []string{b.id},
			Parent: &envscope.Parent{Environment: envscope.Local, Workspace: "w"}, CreatedAt: f.c.now().UTC(),
		}))
		writeSnapshot(t, f, "env-b", `[]`)
		requests := b.captureRequests(t)
		f.c.sweepLedger(t.Context())
		assert.Empty(t, requests.messages())
		_, found, err := f.ledger.Get(b.id, "c-1")
		require.NoError(t, err)
		assert.False(t, found)
	})
}
