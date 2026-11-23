package envagent

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
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

// opRequest is a request for op with args, due in a minute, with a request id of its own.
func opRequest(t *testing.T, requestID, op string, args any) []byte {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
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
	t.Run("a workspace outside the workspaces is refused before the runner runs", func(t *testing.T) {
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
	t.Run("an operation the controller doesn't send is refused", func(t *testing.T) {
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
	t.Run("ca.update is still answered on the session loop", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		runner := listRunner()
		runAgent(t, newOpsAgent(fx, runner, opsOptions()))
		awaitOnline(t, fx)
		args := json.RawMessage(marshalTo(t, envproto.CAUpdateArgs{CABundle: "not a certificate"}))
		reply := askAgent(t, fx, marshalTo(t, envproto.Request{Op: envproto.OpCAUpdate, Args: args}), "a7")
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
	t.Run("a request id seen before gets the stored reply", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		runner := listRunner()
		runAgent(t, newOpsAgent(fx, runner, opsOptions()))
		awaitOnline(t, fx)
		first := askAgent(t, fx, opRequest(t, "0123456789abcdef", envproto.OpWorkspaceList, envproto.ListArgs{}), "b1")
		second := askAgent(t, fx, opRequest(t, "0123456789abcdef", envproto.OpWorkspaceList, envproto.ListArgs{}), "b2")
		assert.True(t, first.OK)
		assert.Equal(t, first.Result, second.Result)
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
