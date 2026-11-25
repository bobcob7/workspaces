package environment

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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

// brokenLocal is a LocalScopes whose workspace store can't be read.
type brokenLocal struct{}

func (brokenLocal) Scope(string) (envscope.Scope, error) {
	return envscope.Scope{}, errors.New("store unreadable")
}

func (brokenLocal) Exists(string) (bool, error) {
	return false, errors.New("store unreadable")
}

// env is the fixture's environment called name.
func (f scopeFixture) env(name string) callFixture {
	return f.envs[name]
}

// grant writes a ledger entry for workspace ws on env with scope ids and no parent.
func (f scopeFixture) grant(t *testing.T, env string, scope ...string) {
	t.Helper()
	entry := envscope.Entry{Environment: env, Workspace: "w", Scope: scope, CreatedAt: f.c.now().UTC()}
	require.NoError(t, f.ledger.Put(t.Context(), &entry))
}

// callerArgs is the caller fields of a scope request: workspace ws (nil for a
// person's grant) and the claimed scope.
func callerArgs(ws *string, claimed []string) envproto.ScopeCallerArgs {
	return envproto.ScopeCallerArgs{Workspace: ws, Ancestors: []string{}, Scope: claimed}
}

// spawnArgs is a scope.spawn from workspace w with claimed scope, to target, narrowed to narrow (nil: same as the caller).
func spawnArgs(claimed []string, target string, narrow []string) envproto.ScopeSpawnArgs {
	return envproto.ScopeSpawnArgs{
		ScopeCallerArgs: callerArgs(new("w"), claimed),
		Target:          target,
		Narrow:          narrow,
		Create:          envproto.CreateArgs{Kind: envproto.KindClaude, Specs: []string{}},
	}
}

// childArgs is a scope.child from workspace ws on the child's parent, on environment env-b.
func childArgs(ws, child, parent, op string) envproto.ScopeChildArgs {
	return envproto.ScopeChildArgs{
		ScopeCallerArgs: callerArgs(&ws, nil),
		Environment:     "env-b",
		Child:           child,
		Parent:          parent,
		Op:              op,
	}
}

// assertScopeError checks that reply failed with code and returns its details.
func assertScopeError(t *testing.T, reply envproto.Reply, code string) map[string]any {
	t.Helper()
	require.False(t, reply.OK, "reply was ok: %s", reply.Result)
	require.NotNil(t, reply.Error)
	assert.Equal(t, code, reply.Error.Code)
	return reply.Error.Details
}

// guardDeadline bounds a scope call in a test that expects it to send nothing, so a call that wrongly sends fails fast.
const guardDeadline = 2 * time.Second

// guardContext is a context that ends after guardDeadline.
func guardContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), guardDeadline)
	t.Cleanup(cancel)
	return ctx
}

// writeSnapshot stores a fresh snapshot of environment env-b listing workspaces (a JSON array).
func writeSnapshot(t *testing.T, f scopeFixture, workspaces string) {
	t.Helper()
	writeSnapshotAt(t, f, workspaces, f.c.now())
}

// writeSnapshotAt stores a snapshot of environment env-b listing workspaces (a JSON array), received at received.
func writeSnapshotAt(t *testing.T, f scopeFixture, workspaces string, received time.Time) {
	t.Helper()
	rec, err := f.store.Get("env-b")
	require.NoError(t, err)
	snap := envproto.Snapshot{At: received.UTC(), Protocol: envproto.Protocol, Workspaces: json.RawMessage(workspaces), Agents: []envproto.SnapshotAgent{}, Loops: []envproto.SnapshotLoop{}}
	data, err := json.Marshal(SnapshotFile{
		Version:    snapshotVersion,
		ID:         rec.ID,
		Name:       "env-b",
		ReceivedAt: received.UTC().Format(time.RFC3339),
		Snapshot:   mustJSON(t, snap),
	})
	require.NoError(t, err)
	require.NoError(t, f.store.WriteSnapshot("env-b", data))
}

func TestController_Scope(t *testing.T) {
	t.Run("spawn refuses a claim wider than the ledger ceiling", func(t *testing.T) {
		ctx := guardContext(t)
		f := newScopeFixture(t, "env-a", "env-b")
		a, b := f.env("env-a"), f.env("env-b")
		f.grant(t, a.id, a.id)
		requests := b.captureRequests(t)
		reply := f.c.Scope(ctx, a.id, "spawn", mustJSON(t, spawnArgs([]string{a.id, b.id}, "env-b", nil)))
		details := assertScopeError(t, reply, "environment_out_of_scope")
		assert.Equal(t, []string{"env-a"}, details["allowed"])
		assert.Empty(t, requests.messages())
	})
	t.Run("spawn ignores ledger entries on other environments", func(t *testing.T) {
		ctx := guardContext(t)
		f := newScopeFixture(t, "env-a", "env-b", "env-c")
		a, b, c := f.env("env-a"), f.env("env-b"), f.env("env-c")
		f.grant(t, c.id, c.id, b.id)
		requests := b.captureRequests(t)
		reply := f.c.Scope(ctx, a.id, "spawn", mustJSON(t, spawnArgs([]string{a.id, b.id}, "env-b", nil)))
		assertScopeError(t, reply, "environment_out_of_scope")
		assert.Empty(t, requests.messages())
	})
	t.Run("child refuses a ledger parent on another environment", func(t *testing.T) {
		ctx := guardContext(t)
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
		reply := f.c.Scope(ctx, a.id, "child", mustJSON(t, childArgs("p-1", "c-1", "p-1", "rm")))
		assertScopeError(t, reply, "not_descendant")
		assert.Empty(t, requests.messages())
	})
	t.Run("spawn sends the scope and writes the ledger", func(t *testing.T) {
		f := newScopeFixture(t, "env-a", "env-b")
		a, b := f.env("env-a"), f.env("env-b")
		f.grant(t, a.id, a.id, b.id)
		requests := b.captureRequests(t)
		b.answerRequests(t, envproto.Reply{OK: true, Result: json.RawMessage(`{"id":"h-1a2b3c4d"}`)})
		reply := f.c.Scope(t.Context(), a.id, "spawn", mustJSON(t, spawnArgs([]string{a.id, b.id}, "env-b", nil)))
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
	t.Run("spawn records the role in the ledger", func(t *testing.T) {
		f := newScopeFixture(t, "env-a", "env-b")
		a, b := f.env("env-a"), f.env("env-b")
		f.grant(t, a.id, a.id, b.id)
		b.answerRequests(t, envproto.Reply{OK: true, Result: json.RawMessage(`{"id":"h-1a2b3c4d"}`)})
		args := spawnArgs([]string{a.id, b.id}, "env-b", nil)
		args.Spawn = &envproto.SpawnInfo{Role: "reviewer"}
		reply := f.c.Scope(t.Context(), a.id, "spawn", mustJSON(t, args))
		require.True(t, reply.OK, "%+v", reply.Error)
		entry, found, err := f.ledger.Get(b.id, "h-1a2b3c4d")
		require.NoError(t, err)
		require.True(t, found)
		require.NotNil(t, entry.Role)
		assert.Equal(t, "reviewer", *entry.Role)
	})
	t.Run("spawn without a spawn record leaves the ledger role unset", func(t *testing.T) {
		f := newScopeFixture(t, "env-a", "env-b")
		a, b := f.env("env-a"), f.env("env-b")
		f.grant(t, a.id, a.id, b.id)
		b.answerRequests(t, envproto.Reply{OK: true, Result: json.RawMessage(`{"id":"h-1a2b3c4d"}`)})
		reply := f.c.Scope(t.Context(), a.id, "spawn", mustJSON(t, spawnArgs([]string{a.id, b.id}, "env-b", nil)))
		require.True(t, reply.OK, "%+v", reply.Error)
		entry, found, err := f.ledger.Get(b.id, "h-1a2b3c4d")
		require.NoError(t, err)
		require.True(t, found)
		assert.Nil(t, entry.Role)
	})
	t.Run("spawn narrows the child scope", func(t *testing.T) {
		f := newScopeFixture(t, "env-a", "env-b", "env-c")
		a, b, c := f.env("env-a"), f.env("env-b"), f.env("env-c")
		f.grant(t, a.id, a.id, b.id, c.id)
		requests := b.captureRequests(t)
		b.answerRequests(t, envproto.Reply{OK: true, Result: json.RawMessage(`{"id":"h-1a2b3c4d"}`)})
		reply := f.c.Scope(t.Context(), a.id, "spawn", mustJSON(t, spawnArgs([]string{a.id, b.id, c.id}, "env-b", []string{"env-b"})))
		require.True(t, reply.OK, "%+v", reply.Error)
		entry, _, err := f.ledger.Get(b.id, "h-1a2b3c4d")
		require.NoError(t, err)
		assert.Equal(t, []string{b.id}, entry.Scope)
		require.Len(t, requests.messages(), 1)
	})
	t.Run("spawn refuses a wider narrow", func(t *testing.T) {
		ctx := guardContext(t)
		f := newScopeFixture(t, "env-a", "env-b", "env-c")
		a, b := f.env("env-a"), f.env("env-b")
		f.grant(t, a.id, a.id, b.id)
		requests := b.captureRequests(t)
		reply := f.c.Scope(ctx, a.id, "spawn", mustJSON(t, spawnArgs([]string{a.id, b.id}, "env-b", []string{"env-c"})))
		assertScopeError(t, reply, "environment_out_of_scope")
		assert.Empty(t, requests.messages())
	})
	t.Run("spawn to an unenrolled name outside the scope is environment_out_of_scope", func(t *testing.T) {
		ctx := guardContext(t)
		f := newScopeFixture(t, "env-a")
		a := f.env("env-a")
		f.grant(t, a.id, a.id)
		reply := f.c.Scope(ctx, a.id, "spawn", mustJSON(t, spawnArgs([]string{a.id}, "nope", nil)))
		details := assertScopeError(t, reply, "environment_out_of_scope")
		assert.Equal(t, "nope", details["environment"])
		assert.Equal(t, []string{"env-a"}, details["allowed"])
	})
	t.Run("spawn to a removed environment in the local scope is environment_unknown", func(t *testing.T) {
		f := newScopeFixture(t, "env-a")
		gone := envscope.Ref{ID: "env-1a2b3c4d", Name: "gone"}
		scope, err := envscope.New(envscope.Ref{ID: envscope.Local, Name: envscope.Local}, []envscope.Ref{gone})
		require.NoError(t, err)
		f.localWS["w"] = scope
		reply := f.c.Scope(t.Context(), envscope.Local, "spawn", mustJSON(t, spawnArgs(nil, "gone", nil)))
		details := assertScopeError(t, reply, "environment_unknown")
		assert.Equal(t, "gone", details["environment"])
	})
	t.Run("spawn refuses a target the claim leaves out of the ceiling", func(t *testing.T) {
		ctx := guardContext(t)
		f := newScopeFixture(t, "env-a", "env-b")
		a, b := f.env("env-a"), f.env("env-b")
		f.grant(t, a.id, a.id, b.id)
		requests := b.captureRequests(t)
		b.answerRequests(t, envproto.Reply{OK: true, Result: json.RawMessage(`{"id":"h-1a2b3c4d"}`)})
		reply := f.c.Scope(ctx, a.id, "spawn", mustJSON(t, spawnArgs([]string{a.id}, "env-b", nil)))
		assertScopeError(t, reply, "environment_out_of_scope")
		assert.Empty(t, requests.messages())
	})
	t.Run("spawn narrowed to a name outside the scope is environment_out_of_scope", func(t *testing.T) {
		ctx := guardContext(t)
		f := newScopeFixture(t, "env-a", "env-b")
		a, b := f.env("env-a"), f.env("env-b")
		f.grant(t, a.id, a.id, b.id)
		requests := b.captureRequests(t)
		reply := f.c.Scope(ctx, a.id, "spawn", mustJSON(t, spawnArgs([]string{a.id, b.id}, "env-b", []string{"nope"})))
		details := assertScopeError(t, reply, "environment_out_of_scope")
		assert.Equal(t, "nope", details["environment"])
		assert.Empty(t, requests.messages())
	})
	t.Run("spawn on the caller's own environment is usage", func(t *testing.T) {
		ctx := guardContext(t)
		f := newScopeFixture(t, "env-a")
		a := f.env("env-a")
		f.grant(t, a.id, a.id)
		requests := a.captureRequests(t)
		reply := f.c.Scope(ctx, a.id, "spawn", mustJSON(t, spawnArgs([]string{a.id}, "env-a", nil)))
		assertScopeError(t, reply, "usage")
		assert.Empty(t, requests.messages())
	})
	t.Run("a failed ledger write removes the child and is internal", func(t *testing.T) {
		f := newScopeFixture(t, "env-b")
		b := f.env("env-b")
		scope, err := envscope.New(envscope.Ref{ID: envscope.Local, Name: envscope.Local}, []envscope.Ref{{ID: b.id, Name: "env-b"}})
		require.NoError(t, err)
		f.localWS["w"] = scope
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "scopes.json"), []byte("not json"), 0o600))
		f.c.SetLocalScopes(localFake{ws: f.localWS}, envscope.NewLedger(dir))
		requests := b.captureRequests(t)
		b.answerRequests(t, envproto.Reply{OK: true, Result: json.RawMessage(`{"id":"h-1a2b3c4d"}`)})
		reply := f.c.Scope(t.Context(), envscope.Local, "spawn", mustJSON(t, spawnArgs(nil, "env-b", nil)))
		assertScopeError(t, reply, "internal")
		sent := requests.messages()
		require.Len(t, sent, 2)
		assert.Equal(t, envproto.OpWorkspaceCreate, requestOf(t, &sent[0]).Op)
		req := requestOf(t, &sent[1])
		assert.Equal(t, envproto.OpWorkspaceRm, req.Op)
		var rm envproto.RmArgs
		require.NoError(t, json.Unmarshal(req.Args, &rm))
		assert.Equal(t, "h-1a2b3c4d", rm.Workspace)
	})
	t.Run("spawn takes the ceiling from an ancestor's ledger entry", func(t *testing.T) {
		f := newScopeFixture(t, "env-a", "env-b")
		a, b := f.env("env-a"), f.env("env-b")
		require.NoError(t, f.ledger.Put(t.Context(), &envscope.Entry{
			Environment: a.id, Workspace: "anc", Scope: []string{a.id, b.id}, CreatedAt: f.c.now().UTC(),
		}))
		requests := b.captureRequests(t)
		b.answerRequests(t, envproto.Reply{OK: true, Result: json.RawMessage(`{"id":"h-1a2b3c4d"}`)})
		withAncestor := spawnArgs([]string{a.id, b.id}, "env-b", nil)
		withAncestor.Ancestors = []string{"anc"}
		reply := f.c.Scope(t.Context(), a.id, "spawn", mustJSON(t, withAncestor))
		require.True(t, reply.OK, "%+v", reply.Error)
		require.Len(t, requests.messages(), 1)
		withoutAncestor := spawnArgs([]string{a.id, b.id}, "env-b", nil)
		reply = f.c.Scope(t.Context(), a.id, "spawn", mustJSON(t, withoutAncestor))
		assertScopeError(t, reply, "environment_out_of_scope")
	})
	t.Run("child of a workspace the ledger doesn't hold is workspace_not_found", func(t *testing.T) {
		f := newScopeFixture(t, "env-a", "env-b")
		a := f.env("env-a")
		reply := f.c.Scope(t.Context(), a.id, "child", mustJSON(t, childArgs("w", "c-9", "w", "status")))
		assertScopeError(t, reply, "workspace_not_found")
	})
	t.Run("spawn to an outdated agent is environment_outdated", func(t *testing.T) {
		ctx := guardContext(t)
		f := newScopeFixture(t, "env-a", "env-b")
		a, b := f.env("env-a"), f.env("env-b")
		f.grant(t, a.id, a.id, b.id)
		b.hear(t, envproto.StatusOnline, 3)
		requests := b.captureRequests(t)
		reply := f.c.Scope(ctx, a.id, "spawn", mustJSON(t, spawnArgs([]string{a.id, b.id}, "env-b", nil)))
		details := assertScopeError(t, reply, "environment_outdated")
		assert.EqualValues(t, envproto.ProtocolScopes, details["need_protocol"])
		assert.Empty(t, requests.messages())
	})
	t.Run("spawn to an offline environment is environment_offline", func(t *testing.T) {
		ctx := guardContext(t)
		f := newScopeFixture(t, "env-a", "env-b")
		a, b := f.env("env-a"), f.env("env-b")
		f.grant(t, a.id, a.id, b.id)
		b.hear(t, envproto.StatusOffline, envproto.Protocol)
		reply := f.c.Scope(ctx, a.id, "spawn", mustJSON(t, spawnArgs([]string{a.id, b.id}, "env-b", nil)))
		assertScopeError(t, reply, "environment_offline")
	})
	t.Run("spawn to local from an environment is usage", func(t *testing.T) {
		ctx := guardContext(t)
		f := newScopeFixture(t, "env-a")
		a := f.env("env-a")
		f.grant(t, a.id, a.id, envscope.Local)
		reply := f.c.Scope(ctx, a.id, "spawn", mustJSON(t, spawnArgs([]string{a.id, envscope.Local}, "local", nil)))
		assertScopeError(t, reply, "usage")
	})
	t.Run("a retried spawn reuses the request id", func(t *testing.T) {
		f := newScopeFixture(t, "env-a", "env-b")
		a, b := f.env("env-a"), f.env("env-b")
		f.grant(t, a.id, a.id, b.id)
		requests := b.captureRequests(t)
		b.answerRequests(t, envproto.Reply{OK: true, Result: json.RawMessage(`{"id":"h-1a2b3c4d"}`)})
		reply := f.c.scope(t.Context(), a.id, "spawn", mustJSON(t, spawnArgs([]string{a.id, b.id}, "env-b", nil)), callRequestID)
		require.True(t, reply.OK, "%+v", reply.Error)
		sent := requests.messages()
		require.Len(t, sent, 1)
		assert.Equal(t, callRequestID, requestOf(t, &sent[0]).RequestID)
	})
	t.Run("local caller uses the local scope file, not the claim", func(t *testing.T) {
		ctx := guardContext(t)
		f := newScopeFixture(t, "env-b")
		b := f.env("env-b")
		local := envscope.Only(envscope.Ref{ID: envscope.Local, Name: envscope.Local})
		f.localWS["w"] = local
		requests := b.captureRequests(t)
		reply := f.c.Scope(ctx, envscope.Local, "spawn", mustJSON(t, spawnArgs([]string{envscope.Local, b.id}, "env-b", nil)))
		details := assertScopeError(t, reply, "environment_out_of_scope")
		assert.Equal(t, []string{envscope.Local}, details["allowed"])
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
		reply := f.c.Scope(t.Context(), envscope.Local, "envs", mustJSON(t, envproto.ScopeEnvsArgs{ScopeCallerArgs: callerArgs(new("w"), nil)}))
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
		writeSnapshot(t, f, `[{"id":"c-1","status":"running"}]`)
		reply := f.c.Scope(t.Context(), a.id, "children", mustJSON(t, envproto.ScopeChildrenArgs{
			ScopeCallerArgs: callerArgs(new("w"), nil),
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
	t.Run("children skips entries whose parent workspace is on another environment", func(t *testing.T) {
		f := newScopeFixture(t, "env-a", "env-b", "env-c")
		a, b, c := f.env("env-a"), f.env("env-b"), f.env("env-c")
		require.NoError(t, f.ledger.Put(t.Context(), &envscope.Entry{
			Environment: b.id, Workspace: "c-1", Scope: []string{b.id},
			Parent: &envscope.Parent{Environment: a.id, Workspace: "w"}, CreatedAt: f.c.now().UTC(),
		}))
		require.NoError(t, f.ledger.Put(t.Context(), &envscope.Entry{
			Environment: b.id, Workspace: "c-2", Scope: []string{b.id},
			Parent: &envscope.Parent{Environment: c.id, Workspace: "w"}, CreatedAt: f.c.now().UTC(),
		}))
		reply := f.c.Scope(t.Context(), a.id, "children", mustJSON(t, envproto.ScopeChildrenArgs{
			ScopeCallerArgs: callerArgs(new("w"), nil),
		}))
		require.True(t, reply.OK, "%+v", reply.Error)
		var result envproto.ScopeChildrenResult
		require.NoError(t, json.Unmarshal(reply.Result, &result))
		require.Len(t, result.Children, 1)
		assert.Equal(t, "c-1", result.Children[0].Workspace)
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
			reply := f.c.Scope(t.Context(), a.id, "child", mustJSON(t, childArgs("w", "c-1", "w", op)))
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
	t.Run("child refuses a ledger parent workspace other than the claimed parent", func(t *testing.T) {
		ctx := guardContext(t)
		f := newScopeFixture(t, "env-a", "env-b")
		a, b := f.env("env-a"), f.env("env-b")
		require.NoError(t, f.ledger.Put(t.Context(), &envscope.Entry{
			Environment: b.id, Workspace: "c-1", Scope: []string{b.id},
			Parent: &envscope.Parent{Environment: a.id, Workspace: "p-1"}, CreatedAt: f.c.now().UTC(),
		}))
		requests := b.captureRequests(t)
		reply := f.c.Scope(ctx, a.id, "child", mustJSON(t, childArgs("other", "c-1", "other", "rm")))
		assertScopeError(t, reply, "not_descendant")
		assert.Empty(t, requests.messages())
	})
	t.Run("child refuses a child of a person's grant", func(t *testing.T) {
		ctx := guardContext(t)
		f := newScopeFixture(t, "env-a", "env-b")
		a, b := f.env("env-a"), f.env("env-b")
		require.NoError(t, f.ledger.Put(t.Context(), &envscope.Entry{
			Environment: b.id, Workspace: "c-1", Scope: []string{b.id}, CreatedAt: f.c.now().UTC(),
		}))
		requests := b.captureRequests(t)
		reply := f.c.Scope(ctx, a.id, "child", mustJSON(t, childArgs("w", "c-1", "w", "status")))
		assertScopeError(t, reply, "not_descendant")
		assert.Empty(t, requests.messages())
	})
	t.Run("rm of a child the environment doesn't have removes the ledger entry", func(t *testing.T) {
		f := newScopeFixture(t, "env-a", "env-b")
		a, b := f.env("env-a"), f.env("env-b")
		require.NoError(t, f.ledger.Put(t.Context(), &envscope.Entry{
			Environment: b.id, Workspace: "c-1", Scope: []string{b.id},
			Parent: &envscope.Parent{Environment: a.id, Workspace: "w"}, CreatedAt: f.c.now().UTC(),
		}))
		b.answerRequests(t, envproto.Reply{Error: &envproto.ReplyError{Code: "workspace_not_found", Message: "no workspace c-1"}})
		reply := f.c.Scope(t.Context(), a.id, "child", mustJSON(t, childArgs("w", "c-1", "w", "rm")))
		assertScopeError(t, reply, "workspace_not_found")
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

func TestController_sweepLedger(t *testing.T) {
	t.Run("a gone parent gets workspace.rm and the entry is dropped", func(t *testing.T) {
		f := newScopeFixture(t, "env-b")
		b := f.env("env-b")
		require.NoError(t, f.ledger.Put(t.Context(), &envscope.Entry{
			Environment: b.id, Workspace: "c-1", Scope: []string{b.id},
			Parent: &envscope.Parent{Environment: envscope.Local, Workspace: "w"}, CreatedAt: f.c.now().UTC(),
		}))
		writeSnapshot(t, f, `[{"id":"c-1"}]`)
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
		ctx := guardContext(t)
		f := newScopeFixture(t, "env-b")
		b := f.env("env-b")
		require.NoError(t, f.ledger.Put(t.Context(), &envscope.Entry{
			Environment: b.id, Workspace: "c-1", Scope: []string{b.id},
			Parent: &envscope.Parent{Environment: envscope.Local, Workspace: "w"}, CreatedAt: f.c.now().UTC(),
		}))
		b.hear(t, envproto.StatusOffline, envproto.Protocol)
		requests := b.captureRequests(t)
		f.c.sweepLedger(ctx)
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
	t.Run("a child written after the last snapshot is kept", func(t *testing.T) {
		ctx := guardContext(t)
		f := newScopeFixture(t, "env-b")
		b := f.env("env-b")
		f.localWS["w"] = envscope.Only(envscope.Ref{ID: envscope.Local, Name: envscope.Local})
		writeSnapshot(t, f, `[]`)
		require.NoError(t, f.ledger.Put(t.Context(), &envscope.Entry{
			Environment: b.id, Workspace: "c-1", Scope: []string{b.id},
			Parent: &envscope.Parent{Environment: envscope.Local, Workspace: "w"}, CreatedAt: f.c.now().UTC().Add(time.Minute),
		}))
		requests := b.captureRequests(t)
		f.c.sweepLedger(ctx)
		assert.Empty(t, requests.messages())
		_, found, err := f.ledger.Get(b.id, "c-1")
		require.NoError(t, err)
		assert.True(t, found)
	})
	t.Run("a store read error keeps the entry and sends nothing", func(t *testing.T) {
		ctx := guardContext(t)
		f := newScopeFixture(t, "env-b")
		b := f.env("env-b")
		require.NoError(t, f.ledger.Put(t.Context(), &envscope.Entry{
			Environment: b.id, Workspace: "c-1", Scope: []string{b.id},
			Parent: &envscope.Parent{Environment: envscope.Local, Workspace: "w"}, CreatedAt: f.c.now().UTC(),
		}))
		require.NoError(t, os.WriteFile(f.store.recordPath(b.id), []byte("{"), 0o600))
		requests := b.captureRequests(t)
		f.c.sweepLedger(ctx)
		assert.Empty(t, requests.messages())
		_, found, err := f.ledger.Get(b.id, "c-1")
		require.NoError(t, err)
		assert.True(t, found)
	})
	t.Run("a parent check that fails keeps the entry", func(t *testing.T) {
		ctx := guardContext(t)
		f := newScopeFixture(t, "env-b")
		b := f.env("env-b")
		f.c.SetLocalScopes(brokenLocal{}, f.ledger)
		require.NoError(t, f.ledger.Put(t.Context(), &envscope.Entry{
			Environment: b.id, Workspace: "c-1", Scope: []string{b.id},
			Parent: &envscope.Parent{Environment: envscope.Local, Workspace: "w"}, CreatedAt: f.c.now().UTC(),
		}))
		requests := b.captureRequests(t)
		f.c.sweepLedger(ctx)
		assert.Empty(t, requests.messages())
		_, found, err := f.ledger.Get(b.id, "c-1")
		require.NoError(t, err)
		assert.True(t, found)
	})
	t.Run("a child of a person's grant is kept while its environment is online", func(t *testing.T) {
		ctx := guardContext(t)
		f := newScopeFixture(t, "env-b")
		b := f.env("env-b")
		require.NoError(t, f.ledger.Put(t.Context(), &envscope.Entry{
			Environment: b.id, Workspace: "c-1", Scope: []string{b.id}, CreatedAt: f.c.now().UTC(),
		}))
		requests := b.captureRequests(t)
		f.c.sweepLedger(ctx)
		assert.Empty(t, requests.messages())
		_, found, err := f.ledger.Get(b.id, "c-1")
		require.NoError(t, err)
		assert.True(t, found)
	})
	t.Run("a child absent from a stale snapshot is kept", func(t *testing.T) {
		ctx := guardContext(t)
		f := newScopeFixture(t, "env-b")
		b := f.env("env-b")
		f.localWS["w"] = envscope.Only(envscope.Ref{ID: envscope.Local, Name: envscope.Local})
		require.NoError(t, f.ledger.Put(t.Context(), &envscope.Entry{
			Environment: b.id, Workspace: "c-1", Scope: []string{b.id},
			Parent: &envscope.Parent{Environment: envscope.Local, Workspace: "w"}, CreatedAt: f.c.now().UTC().Add(-5 * time.Minute),
		}))
		writeSnapshotAt(t, f, `[]`, f.c.now().UTC().Add(-2*time.Minute))
		requests := b.captureRequests(t)
		f.c.sweepLedger(ctx)
		assert.Empty(t, requests.messages())
		_, found, err := f.ledger.Get(b.id, "c-1")
		require.NoError(t, err)
		assert.True(t, found)
	})
	t.Run("a gone parent answered workspace_not_found drops the entry", func(t *testing.T) {
		f := newScopeFixture(t, "env-b")
		b := f.env("env-b")
		require.NoError(t, f.ledger.Put(t.Context(), &envscope.Entry{
			Environment: b.id, Workspace: "c-1", Scope: []string{b.id},
			Parent: &envscope.Parent{Environment: envscope.Local, Workspace: "w"}, CreatedAt: f.c.now().UTC(),
		}))
		requests := b.captureRequests(t)
		b.answerRequests(t, envproto.Reply{Error: &envproto.ReplyError{Code: "workspace_not_found", Message: "no workspace c-1"}})
		f.c.sweepLedger(t.Context())
		require.Len(t, requests.messages(), 1)
		_, found, err := f.ledger.Get(b.id, "c-1")
		require.NoError(t, err)
		assert.False(t, found)
	})
	t.Run("a child absent from its fresh snapshot is dropped", func(t *testing.T) {
		ctx := guardContext(t)
		f := newScopeFixture(t, "env-b")
		b := f.env("env-b")
		require.NoError(t, f.ledger.Put(t.Context(), &envscope.Entry{
			Environment: b.id, Workspace: "c-1", Scope: []string{b.id},
			Parent: &envscope.Parent{Environment: envscope.Local, Workspace: "w"}, CreatedAt: f.c.now().UTC(),
		}))
		writeSnapshot(t, f, `[]`)
		requests := b.captureRequests(t)
		f.c.sweepLedger(ctx)
		assert.Empty(t, requests.messages())
		_, found, err := f.ledger.Get(b.id, "c-1")
		require.NoError(t, err)
		assert.False(t, found)
	})
}
