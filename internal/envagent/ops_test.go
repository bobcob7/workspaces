package envagent

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/environment/envtest"
	"github.com/bobcob7/workspaces/internal/envproto"
)

// topicRecorder is an agent's client that records the topics it publishes to.
type topicRecorder struct {
	*envtest.Client
	recorder publishRecorder
}

func (c *topicRecorder) Publish(ctx context.Context, msg envproto.Message) error { //nolint:gocritic // hugeParam: Publish has the mqttClient signature.
	c.recorder.add(&msg)
	return c.Client.Publish(ctx, msg)
}

// opsOptions are testOptions with the workspaces root CheckWorkspaceRef checks against.
func opsOptions() Options {
	opts := testOptions()
	opts.WorkspacesRoot = "/cfg/workspaces"
	opts.MaxOps = 4
	return opts
}

// newOpsAgent is an agent over the fixture's files that runs ops for its operations.
func newOpsAgent(fx *agentFixture, ops operationRunner, opts Options) *Agent { //nolint:gocritic // hugeParam: Options is passed by value, as NewAgent takes it.
	return NewAgent(discardLog(), fx.files, fx.broker.NewClient(), testSnapshots("[]"), testProbe(), ops, rand.Reader, opts)
}

// opRequest is a request for op with args, due within op's timeout, with a request id of its own.
func opRequest(t *testing.T, requestID, op string, args any) []byte {
	t.Helper()
	spec, _ := envproto.LookupOp(op)
	return opRequestDue(t, requestID, op, args, time.Now().Add(spec.Timeout))
}

// opRequestDue is a request for op with args, due at deadline.
func opRequestDue(t *testing.T, requestID, op string, args any, deadline time.Time) []byte {
	t.Helper()
	return marshalTo(t, envproto.Request{
		Op:        op,
		Args:      json.RawMessage(marshalTo(t, args)),
		RequestID: requestID,
		Deadline:  &deadline,
	})
}

// askAgent sends payload to the agent and returns the reply on corr's response topic.
func askAgent(t *testing.T, fx *agentFixture, payload []byte, corr string) envproto.Reply {
	t.Helper()
	res := envproto.Topic(fx.id.ID, envproto.LeafRes, corr)
	replies := captureMessages(t, fx.broker, res)
	sendToAgent(t, fx, payload, res, corr)
	require.Eventually(t, func() bool { return len(replies.all()) == 1 }, 5*time.Second, 10*time.Millisecond)
	var reply envproto.Reply
	require.NoError(t, json.Unmarshal(replies.all()[0].Payload, &reply))
	return reply
}

// listRunner answers every operation with an empty list.
func listRunner() *operationRunnerMock {
	return &operationRunnerMock{RunFunc: func(context.Context, string, any) (json.RawMessage, error) {
		return json.RawMessage(`[]`), nil
	}}
}

func TestAgent_dispatch(t *testing.T) {
	t.Parallel()
	t.Run("workspace.list runs the runner and replies ok", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		runner := listRunner()
		runAgent(t, newOpsAgent(fx, runner, opsOptions()))
		awaitOnline(t, fx)
		reply := askAgent(t, fx, opRequest(t, "0123456789abcdef", envproto.OpWorkspaceList, envproto.ListArgs{}), "a1")
		assert.True(t, reply.OK)
		assert.JSONEq(t, `[]`, string(reply.Result))
		calls := runner.RunCalls()
		require.Len(t, calls, 1)
		assert.Equal(t, envproto.OpWorkspaceList, calls[0].Op)
		assert.IsType(t, &envproto.ListArgs{}, calls[0].Args)
	})
	t.Run("a runner ReplyError becomes the reply's error", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		runner := &operationRunnerMock{RunFunc: func(context.Context, string, any) (json.RawMessage, error) {
			return nil, &envproto.ReplyError{Code: "name_invalid", Message: "no such name"}
		}}
		runAgent(t, newOpsAgent(fx, runner, opsOptions()))
		awaitOnline(t, fx)
		reply := askAgent(t, fx, opRequest(t, "0123456789abcdef", envproto.OpWorkspaceRename,
			envproto.RenameArgs{Workspace: "fix-login", Name: "x"}), "a2")
		assert.False(t, reply.OK)
		require.NotNil(t, reply.Error)
		assert.Equal(t, "name_invalid", reply.Error.Code)
		assert.Equal(t, "no such name", reply.Error.Message)
	})
	t.Run("a plain runner error replies internal", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		runner := &operationRunnerMock{RunFunc: func(context.Context, string, any) (json.RawMessage, error) {
			return nil, errors.New("disk full")
		}}
		runAgent(t, newOpsAgent(fx, runner, opsOptions()))
		awaitOnline(t, fx)
		reply := askAgent(t, fx, opRequest(t, "0123456789abcdef", envproto.OpWorkspaceList, envproto.ListArgs{}), "a3")
		require.NotNil(t, reply.Error)
		assert.Equal(t, "internal", reply.Error.Code)
	})
	t.Run("a workspace path outside workspaces/ is refused", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		runner := listRunner()
		runAgent(t, newOpsAgent(fx, runner, opsOptions()))
		awaitOnline(t, fx)
		reply := askAgent(t, fx, opRequest(t, "0123456789abcdef", envproto.OpWorkspaceRename,
			envproto.RenameArgs{Workspace: "../etc", Name: "x"}), "a4")
		require.NotNil(t, reply.Error)
		assert.Equal(t, "outside_workspace", reply.Error.Code)
		assert.Empty(t, runner.RunCalls())
	})
	t.Run("an operation the controller may not send is refused", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		runner := listRunner()
		runAgent(t, newOpsAgent(fx, runner, opsOptions()))
		awaitOnline(t, fx)
		reply := askAgent(t, fx, opRequest(t, "0123456789abcdef", envproto.OpCertRenew, struct{}{}), "a5")
		require.NotNil(t, reply.Error)
		assert.Equal(t, "usage", reply.Error.Code)
		assert.Equal(t, "unknown operation cert.renew", reply.Error.Message)
		assert.Empty(t, runner.RunCalls())
	})
	t.Run("an operation without a deadline is refused", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		runAgent(t, newOpsAgent(fx, listRunner(), opsOptions()))
		awaitOnline(t, fx)
		payload := marshalTo(t, envproto.Request{Op: envproto.OpWorkspaceList, RequestID: "0123456789abcdef"})
		reply := askAgent(t, fx, payload, "a6")
		require.NotNil(t, reply.Error)
		assert.Equal(t, "usage", reply.Error.Code)
	})
	t.Run("an operation without a request id is refused", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		runner := listRunner()
		runAgent(t, newOpsAgent(fx, runner, opsOptions()))
		awaitOnline(t, fx)
		reply := askAgent(t, fx, opRequest(t, "", envproto.OpWorkspaceList, envproto.ListArgs{}), "a10")
		require.NotNil(t, reply.Error)
		assert.Equal(t, "usage", reply.Error.Code)
		assert.Empty(t, runner.RunCalls())
	})
	t.Run("ca.update still updates the roots", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		runner := listRunner()
		runAgent(t, newOpsAgent(fx, runner, opsOptions()))
		awaitOnline(t, fx)
		args := json.RawMessage(marshalTo(t, envproto.CAUpdateArgs{CABundle: pemCertificate(fx.id.Roots[0])}))
		reply := askAgent(t, fx, marshalTo(t, envproto.Request{Op: envproto.OpCAUpdate, Args: args}), "a7")
		require.True(t, reply.OK)
		var result envproto.CAUpdateResult
		require.NoError(t, json.Unmarshal(reply.Result, &result))
		assert.Equal(t, fx.id.Fingerprints(), result.Fingerprints)
		roots, err := fx.files.loadRoots()
		require.NoError(t, err)
		require.Len(t, roots, 1)
		assert.True(t, roots[0].Equal(fx.id.Roots[0]))
		assert.Empty(t, runner.RunCalls())
	})
	t.Run("ca.update refuses a bundle that isn't a certificate", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		runner := listRunner()
		runAgent(t, newOpsAgent(fx, runner, opsOptions()))
		awaitOnline(t, fx)
		args := json.RawMessage(marshalTo(t, envproto.CAUpdateArgs{CABundle: "not a certificate"}))
		reply := askAgent(t, fx, marshalTo(t, envproto.Request{Op: envproto.OpCAUpdate, Args: args}), "a8")
		require.NotNil(t, reply.Error)
		assert.Equal(t, "usage", reply.Error.Code)
		assert.Empty(t, runner.RunCalls())
	})
	t.Run("a reply too large is replaced", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		runner := &operationRunnerMock{RunFunc: func(context.Context, string, any) (json.RawMessage, error) {
			return json.RawMessage(`"` + strings.Repeat("x", envproto.MaxRequestBytes) + `"`), nil
		}}
		runAgent(t, newOpsAgent(fx, runner, opsOptions()))
		awaitOnline(t, fx)
		reply := askAgent(t, fx, opRequest(t, "0123456789abcdef", envproto.OpWorkspaceList, envproto.ListArgs{}), "a8")
		require.NotNil(t, reply.Error)
		assert.Equal(t, "internal", reply.Error.Code)
		assert.Equal(t, "the result is too large to send", reply.Error.Message)
	})
	t.Run("an op that changes workspaces republishes the snapshot", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		client := &topicRecorder{Client: fx.broker.NewClient()}
		runner := listRunner()
		agent := NewAgent(discardLog(), fx.files, client, testSnapshots("[]"), testProbe(), runner, rand.Reader, opsOptions())
		runAgent(t, agent)
		awaitOnline(t, fx)
		snapshot := envproto.Topic(fx.id.ID, envproto.LeafSnapshot)
		require.Eventually(t, func() bool { return len(client.recorder.on(snapshot)) == 1 }, 5*time.Second, 10*time.Millisecond)
		reply := askAgent(t, fx, opRequest(t, "0123456789abcdef", envproto.OpWorkspaceRename,
			envproto.RenameArgs{Workspace: "fix-login", Name: "x"}), "a9")
		assert.True(t, reply.OK)
		require.Eventually(t, func() bool { return len(client.recorder.on(snapshot)) == 2 }, 5*time.Second, 10*time.Millisecond)
	})
	t.Run("an op that fails doesn't republish the snapshot", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		client := &topicRecorder{Client: fx.broker.NewClient()}
		runner := &operationRunnerMock{RunFunc: func(context.Context, string, any) (json.RawMessage, error) {
			return nil, &envproto.ReplyError{Code: "name_invalid", Message: "no such name"}
		}}
		agent := NewAgent(discardLog(), fx.files, client, testSnapshots("[]"), testProbe(), runner, rand.Reader, opsOptions())
		runAgent(t, agent)
		awaitOnline(t, fx)
		snapshot := envproto.Topic(fx.id.ID, envproto.LeafSnapshot)
		require.Eventually(t, func() bool { return len(client.recorder.on(snapshot)) == 1 }, 5*time.Second, 10*time.Millisecond)
		reply := askAgent(t, fx, opRequest(t, "0123456789abcdef", envproto.OpWorkspaceRename,
			envproto.RenameArgs{Workspace: "fix-login", Name: "x"}), "a11")
		require.NotNil(t, reply.Error)
		require.Never(t, func() bool { return len(client.recorder.on(snapshot)) > 1 }, 300*time.Millisecond, 10*time.Millisecond)
	})
	t.Run("a request id seen before gets the stored reply", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		runner := listRunner()
		runAgent(t, newOpsAgent(fx, runner, opsOptions()))
		awaitOnline(t, fx)
		hide := envproto.HideArgs{Workspace: "fix-login", Hidden: true}
		first := askAgent(t, fx, opRequest(t, "0123456789abcdef", envproto.OpWorkspaceHide, hide), "b1")
		second := askAgent(t, fx, opRequest(t, "0123456789abcdef", envproto.OpWorkspaceHide, hide), "b2")
		assert.True(t, first.OK)
		assert.Equal(t, first.Result, second.Result)
		assert.Len(t, runner.RunCalls(), 1)
	})
	t.Run("a limited op answers operation_limited with retry_after_seconds", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		runner := listRunner()
		runAgent(t, newOpsAgent(fx, runner, opsOptions()))
		awaitOnline(t, fx)
		run := envproto.LoopRunArgs{Name: "nightly"}
		first := askAgent(t, fx, opRequest(t, "0123456789abcde1", envproto.OpLoopRun, run), "l1")
		require.True(t, first.OK)
		second := askAgent(t, fx, opRequest(t, "0123456789abcde2", envproto.OpLoopRun, run), "l2")
		require.False(t, second.OK)
		require.NotNil(t, second.Error)
		assert.Equal(t, envproto.CodeOperationLimited, second.Error.Code)
		assert.Equal(t, "loop.run", second.Error.Details["op"])
		assert.InDelta(t, 60, second.Error.Details["retry_after_seconds"], 1)
		assert.Len(t, runner.RunCalls(), 1, "a refused request never reaches the runner")
	})
	t.Run("a duplicate request id is not limited", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		runner := listRunner()
		runAgent(t, newOpsAgent(fx, runner, opsOptions()))
		awaitOnline(t, fx)
		run := envproto.LoopRunArgs{Name: "nightly"}
		first := askAgent(t, fx, opRequest(t, "0123456789abcde1", envproto.OpLoopRun, run), "d1")
		second := askAgent(t, fx, opRequest(t, "0123456789abcde1", envproto.OpLoopRun, run), "d2")
		assert.True(t, first.OK)
		assert.True(t, second.OK)
		assert.Len(t, runner.RunCalls(), 1)
	})
	t.Run("a slow op doesn't block the next request", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		started := make(chan struct{})
		release := make(chan struct{})
		var calls atomic.Int32
		runner := &operationRunnerMock{RunFunc: func(context.Context, string, any) (json.RawMessage, error) {
			if calls.Add(1) == 1 {
				close(started)
				<-release
			}
			return json.RawMessage(`[]`), nil
		}}
		runAgent(t, newOpsAgent(fx, runner, opsOptions()))
		awaitOnline(t, fx)
		slow := envproto.Topic(fx.id.ID, envproto.LeafRes, "c1")
		slowReplies := captureMessages(t, fx.broker, slow)
		sendToAgent(t, fx, opRequest(t, "1111111111111111", envproto.OpWorkspaceList, envproto.ListArgs{}), slow, "c1")
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("the slow operation never started")
		}
		fast := askAgent(t, fx, opRequest(t, "2222222222222222", envproto.OpWorkspaceList, envproto.ListArgs{}), "c2")
		assert.True(t, fast.OK)
		assert.Empty(t, slowReplies.all())
		close(release)
		require.Eventually(t, func() bool { return len(slowReplies.all()) == 1 }, 5*time.Second, 10*time.Millisecond)
	})
	t.Run("an unknown operation is refused", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		runner := listRunner()
		runAgent(t, newOpsAgent(fx, runner, opsOptions()))
		awaitOnline(t, fx)
		reply := askAgent(t, fx, opRequest(t, "0123456789abcdef", "files.read", struct{}{}), "d1")
		require.NotNil(t, reply.Error)
		assert.Equal(t, "usage", reply.Error.Code)
		assert.Equal(t, "unknown operation files.read", reply.Error.Message)
		assert.Empty(t, runner.RunCalls())
	})
	t.Run("unknown argument keys are refused", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		runner := listRunner()
		runAgent(t, newOpsAgent(fx, runner, opsOptions()))
		awaitOnline(t, fx)
		list := askAgent(t, fx, opRequest(t, "0123456789abcdef", envproto.OpWorkspaceList, json.RawMessage(`{"foo":1}`)), "d2")
		require.NotNil(t, list.Error)
		assert.Equal(t, "usage", list.Error.Code)
		assert.Contains(t, list.Error.Message, "foo")
		rename := askAgent(t, fx, opRequest(t, "fedcba9876543210", envproto.OpWorkspaceRename,
			json.RawMessage(`{"workspace":"fix-login","name":"x","foo":1}`)), "d3")
		require.NotNil(t, rename.Error)
		assert.Equal(t, "usage", rename.Error.Code)
		assert.Contains(t, rename.Error.Message, "foo")
		assert.Empty(t, runner.RunCalls())
	})
	t.Run("a spec that starts with - is refused", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		runner := listRunner()
		runAgent(t, newOpsAgent(fx, runner, opsOptions()))
		awaitOnline(t, fx)
		reply := askAgent(t, fx, opRequest(t, "0123456789abcdef", envproto.OpWorkspaceCreate,
			envproto.CreateArgs{Kind: envproto.KindClaude, Specs: []string{"-x"}}), "d4")
		require.NotNil(t, reply.Error)
		assert.Equal(t, "spec_invalid", reply.Error.Code)
		assert.Empty(t, runner.RunCalls())
	})
	t.Run("a request whose deadline has passed never reaches the runner", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		runner := listRunner()
		runAgent(t, newOpsAgent(fx, runner, opsOptions()))
		awaitOnline(t, fx)
		for i := range 20 {
			deadline := time.Now().Add(-time.Second)
			reply := askAgent(t, fx, opRequestDue(t, fmt.Sprintf("%016x", i+1), envproto.OpWorkspaceList, envproto.ListArgs{}, deadline), fmt.Sprintf("e%d", i))
			require.NotNil(t, reply.Error)
			assert.Equal(t, "internal", reply.Error.Code)
			assert.Equal(t, "the agent was busy until the deadline", reply.Error.Message)
		}
		assert.Empty(t, runner.RunCalls())
	})
	t.Run("the operation's context has the request's deadline", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		deadlines := make(chan time.Time, 1)
		runner := &operationRunnerMock{RunFunc: func(ctx context.Context, _ string, _ any) (json.RawMessage, error) {
			deadline, _ := ctx.Deadline()
			deadlines <- deadline
			return json.RawMessage(`[]`), nil
		}}
		runAgent(t, newOpsAgent(fx, runner, opsOptions()))
		awaitOnline(t, fx)
		deadline := time.Now().Add(20 * time.Second)
		reply := askAgent(t, fx, opRequestDue(t, "0123456789abcdef", envproto.OpWorkspaceList, envproto.ListArgs{}, deadline), "e20")
		assert.True(t, reply.OK)
		assert.True(t, deadline.Equal(<-deadlines))
	})
	t.Run("a request waiting for a busy agent is refused at its deadline", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		started := make(chan struct{})
		release := make(chan struct{})
		var calls atomic.Int32
		runner := &operationRunnerMock{RunFunc: func(context.Context, string, any) (json.RawMessage, error) {
			if calls.Add(1) == 1 {
				close(started)
				<-release
			}
			return json.RawMessage(`[]`), nil
		}}
		opts := opsOptions()
		opts.MaxOps = 1
		agent := newOpsAgent(fx, runner, opts)
		runAgent(t, agent)
		awaitOnline(t, fx)
		slow := envproto.Topic(fx.id.ID, envproto.LeafRes, "f1")
		slowReplies := captureMessages(t, fx.broker, slow)
		sendToAgent(t, fx, opRequest(t, "1111111111111111", envproto.OpWorkspaceList, envproto.ListArgs{}), slow, "f1")
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("the slow operation never started")
		}
		busy := askAgent(t, fx, opRequestDue(t, "2222222222222222", envproto.OpWorkspaceList, envproto.ListArgs{},
			time.Now().Add(200*time.Millisecond)), "f2")
		require.NotNil(t, busy.Error)
		assert.Equal(t, "internal", busy.Error.Code)
		assert.Equal(t, "the agent was busy until the deadline", busy.Error.Message)
		assert.Equal(t, int32(1), calls.Load())
		close(release)
		require.Eventually(t, func() bool { return len(slowReplies.all()) == 1 }, 5*time.Second, 10*time.Millisecond)
	})
	t.Run("a deadline further away than the timeout and grace is refused", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		runner := listRunner()
		runAgent(t, newOpsAgent(fx, runner, opsOptions()))
		awaitOnline(t, fx)
		reply := askAgent(t, fx, opRequestDue(t, "0123456789abcdef", envproto.OpWorkspaceList, envproto.ListArgs{},
			time.Now().Add(time.Hour)), "d5")
		require.NotNil(t, reply.Error)
		assert.Equal(t, "usage", reply.Error.Code)
		assert.Empty(t, runner.RunCalls())
	})
}

func TestRemembered(t *testing.T) {
	t.Parallel()
	for _, op := range []string{
		envproto.OpReviewStatus, envproto.OpReviewList, envproto.OpReviewReport, envproto.OpReviewProfiles,
		envproto.OpLoopList, envproto.OpLoopShow, envproto.OpLoopHistory, envproto.OpLoopHistoryRun,
		envproto.OpLoopMemoryList, envproto.OpLoopMemoryGet,
	} {
		assert.False(t, remembered(op), op)
	}
	for _, op := range []string{envproto.OpReviewStart, envproto.OpLoopRun, envproto.OpLoopAdd, envproto.OpLoopRm, envproto.OpWorkspaceHide} {
		assert.True(t, remembered(op), op)
	}
}

func TestCheckWorkspaceRef(t *testing.T) {
	const root = "/cfg/workspaces"
	tests := []struct {
		ref  string
		code string
	}{
		{ref: "fix-login", code: ""},
		{ref: "/cfg/workspaces/fix-login", code: ""},
		{ref: "/cfg/workspaces/a/../fix-login", code: ""},
		{ref: "../etc", code: "outside_workspace"},
		{ref: "a/b", code: "outside_workspace"},
		{ref: "/etc/passwd", code: "outside_workspace"},
		{ref: "/cfg/workspaces", code: "outside_workspace"},
		{ref: "/cfg/workspaces/..", code: "outside_workspace"},
		{ref: "/cfg/workspaces-evil/x", code: "outside_workspace"},
		{ref: "Fix Login", code: "outside_workspace"},
		{ref: "", code: "outside_workspace"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.code, refusalCode(CheckWorkspaceRef(root, tt.ref)), "ref %q", tt.ref)
	}
}

// refusalCode is the code of err as a ReplyError, or "" for nil.
func refusalCode(err error) string {
	if err == nil {
		return ""
	}
	re, ok := errors.AsType[*envproto.ReplyError](err)
	if !ok {
		return "not a reply error"
	}
	return re.Code
}

// bad is a workspace reference outside the workspaces.
const bad = "../x"

func TestWorkspaceRefs(t *testing.T) {
	t.Parallel()
	parent := bad
	tests := []struct {
		name string
		args any
		want []string
	}{
		{name: "create with a parent", args: &envproto.CreateArgs{Parent: &parent}, want: []string{bad}},
		{name: "create without a parent", args: &envproto.CreateArgs{}, want: nil},
		{name: "workspace args", args: &envproto.WorkspaceArgs{Workspace: bad}, want: []string{bad}},
		{name: "rm", args: &envproto.RmArgs{Workspace: bad}, want: []string{bad}},
		{name: "rename", args: &envproto.RenameArgs{Workspace: bad}, want: []string{bad}},
		{name: "hide", args: &envproto.HideArgs{Workspace: bad}, want: []string{bad}},
		{name: "logs", args: &envproto.LogsArgs{Workspace: bad}, want: []string{bad}},
		{name: "send", args: &envproto.SendArgs{Workspace: bad}, want: []string{bad}},
		{name: "list", args: &envproto.ListArgs{}, want: nil},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, workspaceRefs(tt.args), tt.name)
	}
}

func TestAgent_admitWorkspaceRefs(t *testing.T) {
	t.Parallel()
	a := &Agent{opts: opsOptions(), requests: newRequests(time.Now), limits: newLimiter(time.Now)}
	parent := bad
	tests := []struct {
		op   string
		args any
		code string
	}{
		{envproto.OpWorkspaceCreate, envproto.CreateArgs{Kind: envproto.KindClaude, Specs: []string{}, Parent: &parent}, "outside_workspace"},
		{envproto.OpWorkspaceCreate, envproto.CreateArgs{Kind: envproto.KindClaude, Specs: []string{}}, ""},
		{envproto.OpWorkspaceRm, envproto.RmArgs{Workspace: bad}, "outside_workspace"},
		{envproto.OpWorkspaceRename, envproto.RenameArgs{Workspace: bad, Name: "x"}, "outside_workspace"},
		{envproto.OpWorkspaceHide, envproto.HideArgs{Workspace: bad}, "outside_workspace"},
		{envproto.OpAgentStatus, envproto.WorkspaceArgs{Workspace: bad}, "outside_workspace"},
		{envproto.OpAgentStop, envproto.WorkspaceArgs{Workspace: bad}, "outside_workspace"},
		{envproto.OpAgentLogs, envproto.LogsArgs{Workspace: bad}, "outside_workspace"},
		{envproto.OpAgentSend, envproto.SendArgs{Workspace: bad, Message: "hi"}, "outside_workspace"},
	}
	for _, tt := range tests {
		var req envproto.Request
		require.NoError(t, json.Unmarshal(opRequest(t, "0123456789abcdef", tt.op, tt.args), &req))
		_, _, err := a.admit(&req)
		assert.Equal(t, tt.code, refusalCode(err), tt.op)
	}
}

// blockingRunner blocks the first call until release closes, closing started.
func blockingRunner(started, release chan struct{}) *operationRunnerMock {
	var calls atomic.Int32
	return &operationRunnerMock{RunFunc: func(context.Context, string, any) (json.RawMessage, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		return json.RawMessage(`[]`), nil
	}}
}

func TestAgent_dedupe(t *testing.T) {
	t.Parallel()
	hide := envproto.HideArgs{Workspace: "fix-login", Hidden: true}
	t.Run("duplicates of a running request don't hold a slot", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		started := make(chan struct{})
		release := make(chan struct{})
		runner := blockingRunner(started, release)
		opts := opsOptions()
		opts.MaxOps = 2
		agent := newOpsAgent(fx, runner, opts)
		runAgent(t, agent)
		releaseOnce := sync.OnceFunc(func() { close(release) })
		t.Cleanup(releaseOnce)
		awaitOnline(t, fx)
		slow := envproto.Topic(fx.id.ID, envproto.LeafRes, "g1")
		slowReplies := captureMessages(t, fx.broker, slow)
		sendToAgent(t, fx, opRequest(t, "1111111111111111", envproto.OpWorkspaceHide, hide), slow, "g1")
		<-started
		dup := envproto.Topic(fx.id.ID, envproto.LeafRes, "g2")
		dupReplies := captureMessages(t, fx.broker, dup)
		for range 3 {
			sendToAgent(t, fx, opRequest(t, "1111111111111111", envproto.OpWorkspaceHide, hide), dup, "g2")
		}
		require.Eventually(t, func() bool { return len(agent.queue) == 4 }, 5*time.Second, time.Millisecond)
		assert.Never(t, func() bool { return len(agent.slots) > 1 }, 200*time.Millisecond, time.Millisecond)
		other := askAgent(t, fx, opRequest(t, "2222222222222222", envproto.OpWorkspaceHide, hide), "g3")
		assert.True(t, other.OK)
		releaseOnce()
		require.Eventually(t, func() bool { return len(slowReplies.all()) == 1 && len(dupReplies.all()) == 3 }, 5*time.Second, 10*time.Millisecond)
		assert.Len(t, runner.RunCalls(), 2)
	})
	t.Run("a panic in an op replies internal and frees the request id", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		var calls atomic.Int32
		runner := &operationRunnerMock{RunFunc: func(context.Context, string, any) (json.RawMessage, error) {
			if calls.Add(1) == 1 {
				panic("boom")
			}
			return json.RawMessage(`[]`), nil
		}}
		runAgent(t, newOpsAgent(fx, runner, opsOptions()))
		awaitOnline(t, fx)
		first := askAgent(t, fx, opRequest(t, "0123456789abcdef", envproto.OpWorkspaceHide, hide), "h1")
		require.NotNil(t, first.Error)
		assert.Equal(t, "internal", first.Error.Code)
		second := askAgent(t, fx, opRequest(t, "0123456789abcdef", envproto.OpWorkspaceHide, hide), "h2")
		assert.True(t, second.OK)
	})
	t.Run("a reply from an op cut off by its deadline isn't remembered", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		var calls atomic.Int32
		runner := &operationRunnerMock{RunFunc: func(ctx context.Context, _ string, _ any) (json.RawMessage, error) {
			if calls.Add(1) == 1 {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return json.RawMessage(`[]`), nil
		}}
		runAgent(t, newOpsAgent(fx, runner, opsOptions()))
		awaitOnline(t, fx)
		first := askAgent(t, fx, opRequestDue(t, "0123456789abcdef", envproto.OpWorkspaceHide, hide,
			time.Now().Add(150*time.Millisecond)), "i1")
		assert.False(t, first.OK)
		second := askAgent(t, fx, opRequest(t, "0123456789abcdef", envproto.OpWorkspaceHide, hide), "i2")
		assert.True(t, second.OK)
		assert.EqualValues(t, 2, calls.Load())
	})
	t.Run("read-only ops are not stored", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		runner := listRunner()
		runAgent(t, newOpsAgent(fx, runner, opsOptions()))
		awaitOnline(t, fx)
		for i, op := range []string{envproto.OpWorkspaceList, envproto.OpAgentStatus, envproto.OpAgentLogs} {
			var args any = envproto.ListArgs{}
			if op != envproto.OpWorkspaceList {
				args = envproto.LogsArgs{Workspace: "fix-login"}
				if op == envproto.OpAgentStatus {
					args = envproto.WorkspaceArgs{Workspace: "fix-login"}
				}
			}
			id := fmt.Sprintf("%016d", i)
			askAgent(t, fx, opRequest(t, id, op, args), fmt.Sprintf("j%da", i))
			askAgent(t, fx, opRequest(t, id, op, args), fmt.Sprintf("j%db", i))
		}
		assert.Len(t, runner.RunCalls(), 6)
	})
	t.Run("a request id reused for another request is refused", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		runner := listRunner()
		runAgent(t, newOpsAgent(fx, runner, opsOptions()))
		awaitOnline(t, fx)
		askAgent(t, fx, opRequest(t, "0123456789abcdef", envproto.OpWorkspaceHide, hide), "k1")
		reply := askAgent(t, fx, opRequest(t, "0123456789abcdef", envproto.OpWorkspaceHide,
			envproto.HideArgs{Workspace: "other", Hidden: true}), "k2")
		require.NotNil(t, reply.Error)
		assert.Equal(t, "usage", reply.Error.Code)
		assert.Len(t, runner.RunCalls(), 1)
	})
}

// closeTracker is an agent's client that counts connects and the publishes made after Close.
type closeTracker struct {
	*envtest.Client
	connects atomic.Int32
	closed   atomic.Bool
	late     atomic.Int32
}

func (c *closeTracker) Connect(ctx context.Context, opts envproto.ConnectOptions) error {
	c.connects.Add(1)
	c.closed.Store(false)
	return c.Client.Connect(ctx, opts)
}

func (c *closeTracker) Publish(ctx context.Context, msg envproto.Message) error { //nolint:gocritic // hugeParam: Publish has the mqttClient signature.
	if c.closed.Load() {
		c.late.Add(1)
	}
	return c.Client.Publish(ctx, msg)
}

func (c *closeTracker) Close() error {
	c.closed.Store(true)
	return c.Client.Close()
}

func TestAgent_operationGoroutines(t *testing.T) {
	t.Parallel()
	hide := envproto.HideArgs{Workspace: "fix-login", Hidden: true}
	t.Run("stopping the agent waits for running operations", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		started := make(chan struct{})
		var finished atomic.Bool
		runner := &operationRunnerMock{RunFunc: func(ctx context.Context, _ string, _ any) (json.RawMessage, error) {
			close(started)
			<-ctx.Done()
			time.Sleep(100 * time.Millisecond)
			finished.Store(true)
			return nil, ctx.Err()
		}}
		run := runAgent(t, newOpsAgent(fx, runner, opsOptions()))
		awaitOnline(t, fx)
		res := envproto.Topic(fx.id.ID, envproto.LeafRes, "l1")
		sendToAgent(t, fx, opRequest(t, "0123456789abcdef", envproto.OpWorkspaceHide, hide), res, "l1")
		<-started
		require.NoError(t, run.stop())
		assert.True(t, finished.Load())
	})
	t.Run("a lost connection cancels running operations", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		started := make(chan struct{})
		cancelled := make(chan struct{})
		runner := &operationRunnerMock{RunFunc: func(ctx context.Context, _ string, _ any) (json.RawMessage, error) {
			close(started)
			<-ctx.Done()
			close(cancelled)
			return nil, ctx.Err()
		}}
		client := &closeTracker{Client: fx.broker.NewClient()}
		agent := NewAgent(discardLog(), fx.files, client, testSnapshots("[]"), testProbe(), runner, rand.Reader, opsOptions())
		runAgent(t, agent)
		awaitOnline(t, fx)
		res := envproto.Topic(fx.id.ID, envproto.LeafRes, "n1")
		sendToAgent(t, fx, opRequest(t, "0123456789abcdef", envproto.OpWorkspaceHide, hide), res, "n1")
		<-started
		require.Equal(t, 1, fx.broker.Disconnect(fx.id.Cert.SerialNumber.Text(16)))
		select {
		case <-cancelled:
		case <-time.After(5 * time.Second):
			t.Fatal("the operation kept running after the connection was lost")
		}
		require.Eventually(t, func() bool { return client.connects.Load() == 2 }, 5*time.Second, 10*time.Millisecond)
		assert.Zero(t, client.late.Load())
	})
	t.Run("operations one after another never fill the queue", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		opts := opsOptions()
		opts.MaxOps = 1
		agent := newOpsAgent(fx, listRunner(), opts)
		runAgent(t, agent)
		awaitOnline(t, fx)
		for i := range queueFactor + 3 {
			reply := askAgent(t, fx, opRequest(t, fmt.Sprintf("%016d", i), envproto.OpWorkspaceHide, hide), fmt.Sprintf("o%d", i))
			require.True(t, reply.OK, "request %d", i)
		}
		assert.Eventually(t, func() bool { return len(agent.queue) == 0 }, 5*time.Second, time.Millisecond)
	})
	t.Run("a burst beyond the queue is refused at once", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		started := make(chan struct{})
		release := make(chan struct{})
		runner := blockingRunner(started, release)
		opts := opsOptions()
		opts.MaxOps = 1
		runAgent(t, newOpsAgent(fx, runner, opts))
		awaitOnline(t, fx)
		res := envproto.Topic(fx.id.ID, envproto.LeafRes, "m1")
		replies := captureMessages(t, fx.broker, res)
		for i := range queueFactor {
			sendToAgent(t, fx, opRequest(t, fmt.Sprintf("%016d", i), envproto.OpWorkspaceHide, hide), res, "m1")
		}
		<-started
		refused := askAgent(t, fx, opRequest(t, fmt.Sprintf("%016d", queueFactor), envproto.OpWorkspaceHide, hide), "m2")
		require.NotNil(t, refused.Error)
		assert.Equal(t, "internal", refused.Error.Code)
		assert.Equal(t, "the agent has too many operations waiting", refused.Error.Message)
		close(release)
		require.Eventually(t, func() bool { return len(replies.all()) == queueFactor }, 5*time.Second, 10*time.Millisecond)
	})
}
