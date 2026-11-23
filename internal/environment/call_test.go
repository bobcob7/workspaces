package environment

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/environment/envtest"
	"github.com/bobcob7/workspaces/internal/envproto"
)

// callRequestID is the request id the call tests use.
const callRequestID = "0123456789abcdef"

// callFixture is an enrolled environment called linux-server, its agent
// connected, and a started controller.
type callFixture struct {
	store *Store
	b     *envtest.Broker
	c     *Controller
	id    string
	agent *envtest.Client
}

func newCallFixture(t *testing.T) callFixture {
	t.Helper()
	store := newTestStore(t)
	b := envtest.NewBroker()
	rec, cert := enrollAgent(t, store, "linux-server")
	c := startController(t, store, b)
	agent := connectAgent(t, store, b, rec.ID, &cert)
	return callFixture{store: store, b: b, c: c, id: rec.ID, agent: agent}
}

// hear makes the controller hear a status message from the fixture's
// environment, as if the agent had published it.
func (f callFixture) hear(t *testing.T, status string, protocol int) {
	t.Helper()
	tr, err := f.store.trust()
	require.NoError(t, err)
	payload, err := json.Marshal(envproto.Hello{
		Status:         status,
		At:             f.store.now().UTC(),
		WSVersion:      "0.9.0",
		CAFingerprints: tr.fingerprints(),
		Protocol:       protocol,
	})
	require.NoError(t, err)
	f.c.onStatus(t.Context(), &envproto.Message{Topic: envproto.Topic(f.id, envproto.LeafStatus), Payload: payload})
}

// requestCapture records the messages published on an environment's req topic.
type requestCapture struct {
	mu   sync.Mutex
	msgs []envproto.Message
}

// captureRequests records what the fixture's controller publishes on env/<id>/req.
func (f callFixture) captureRequests(t *testing.T) *requestCapture {
	t.Helper()
	rc := &requestCapture{}
	_, err := f.b.Subscribe(t.Context(), envproto.Topic(f.id, envproto.LeafReq), func(msg envproto.Message) {
		rc.mu.Lock()
		defer rc.mu.Unlock()
		rc.msgs = append(rc.msgs, msg)
	})
	require.NoError(t, err)
	return rc
}

// messages returns the recorded messages.
func (rc *requestCapture) messages() []envproto.Message {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return slices.Clone(rc.msgs)
}

// answerRequests makes the fixture's agent answer each request on env/<id>/req with reply.
func (f callFixture) answerRequests(t *testing.T, reply envproto.Reply) {
	t.Helper()
	err := f.agent.Subscribe(t.Context(), envproto.Topic(f.id, envproto.LeafReq), func(msg envproto.Message) {
		payload, err := json.Marshal(reply)
		assert.NoError(t, err)
		assert.NoError(t, f.agent.Publish(t.Context(), envproto.Message{
			Topic:           msg.ResponseTopic,
			Payload:         payload,
			QoS:             1,
			CorrelationData: msg.CorrelationData,
		}))
	})
	require.NoError(t, err)
}

// callResult is what a Call returned.
type callResult struct {
	reply envproto.Reply
	err   error
}

// callAsync runs Call on the fixture's environment in a goroutine.
//
//nolint:gocritic // hugeParam: the CallRequest value is the fixed API.
func (f callFixture) callAsync(ctx context.Context, req CallRequest) <-chan callResult {
	done := make(chan callResult, 1)
	go func() {
		reply, err := f.c.Call(ctx, "linux-server", req)
		done <- callResult{reply: reply, err: err}
	}()
	return done
}

// awaitCall returns the result of a call, failing the test if it doesn't finish.
func awaitCall(t *testing.T, done <-chan callResult) callResult {
	t.Helper()
	select {
	case res := <-done:
		return res
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the call didn't finish")
		return callResult{}
	}
}

// listRequest is a workspace.list call due in timeout.
func listRequest(timeout time.Duration) CallRequest {
	return listRequestWithID(callRequestID, timeout)
}

// listRequestWithID is a workspace.list call with request id reqID due in timeout.
func listRequestWithID(reqID string, timeout time.Duration) CallRequest {
	return CallRequest{
		Op:        envproto.OpWorkspaceList,
		Args:      json.RawMessage(`{}`),
		RequestID: reqID,
		Deadline:  time.Now().Add(timeout),
	}
}

// pendingCount returns how many calls the controller is waiting on.
func pendingCount(c *Controller) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

// mustJSON encodes v, failing the test on an error.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	payload, err := json.Marshal(v)
	require.NoError(t, err)
	return payload
}

// replyMessage is an agent's reply to a call on env/<id>/res/<req>.
func replyMessage(t *testing.T, id, reqID string, reply envproto.Reply) *envproto.Message {
	t.Helper()
	return &envproto.Message{
		Topic:           envproto.Topic(id, envproto.LeafRes, reqID),
		Payload:         mustJSON(t, reply),
		CorrelationData: []byte(reqID),
	}
}

func TestController_Call(t *testing.T) {
	okReply := envproto.Reply{OK: true, Result: json.RawMessage(`{"workspaces":[]}`)}
	t.Run("an unknown name is ErrUnknown", func(t *testing.T) {
		f := newCallFixture(t)
		_, err := f.c.Call(t.Context(), "nope", listRequest(time.Minute))
		assert.ErrorIs(t, err, ErrUnknown)
	})
	t.Run("an agent-only op is ErrNotForwarded", func(t *testing.T) {
		f := newCallFixture(t)
		req := listRequest(time.Minute)
		req.Op = envproto.OpCertRenew
		_, err := f.c.Call(t.Context(), "linux-server", req)
		assert.ErrorIs(t, err, ErrNotForwarded)
	})
	t.Run("a request id that isn't valid is refused", func(t *testing.T) {
		f := newCallFixture(t)
		req := listRequest(time.Minute)
		req.RequestID = "not-an-id"
		rc := f.captureRequests(t)
		_, err := f.c.Call(t.Context(), "linux-server", req)
		require.Error(t, err)
		assert.Empty(t, rc.messages())
	})
	t.Run("not listening is offline/not_listening", func(t *testing.T) {
		store := newTestStore(t)
		enrollAgent(t, store, "linux-server")
		c := newController(t, store, envtest.NewBroker(), discardLog(), testSettings())
		_, err := c.Call(t.Context(), "linux-server", listRequest(time.Minute))
		var offline *OfflineError
		require.ErrorAs(t, err, &offline)
		require.ErrorIs(t, err, ErrOffline)
		assert.Equal(t, "not_listening", offline.Reason)
		assert.Equal(t, "linux-server", offline.Environment)
	})
	t.Run("an offline environment is offline/offline and nothing is published", func(t *testing.T) {
		f := newCallFixture(t)
		f.hear(t, envproto.StatusOffline, envproto.Protocol)
		rc := f.captureRequests(t)
		_, err := f.c.Call(t.Context(), "linux-server", listRequest(time.Minute))
		var offline *OfflineError
		require.ErrorAs(t, err, &offline)
		assert.Equal(t, "offline", offline.Reason)
		assert.NotNil(t, offline.LastSeen)
		assert.Empty(t, rc.messages())
		assert.Zero(t, pendingCount(f.c))
	})
	t.Run("a protocol-0 agent is OutdatedError for workspace.list", func(t *testing.T) {
		f := newCallFixture(t)
		f.hear(t, envproto.StatusOnline, 0)
		_, err := f.c.Call(t.Context(), "linux-server", listRequest(time.Minute))
		var outdated *OutdatedError
		require.ErrorAs(t, err, &outdated)
		assert.Equal(t, envproto.OpWorkspaceList, outdated.Op)
		assert.Equal(t, 1, outdated.Need)
		assert.Equal(t, 0, outdated.Protocol)
		assert.Equal(t, "0.9.0", outdated.WSVersion)
	})
	t.Run("the agent's ok reply is returned", func(t *testing.T) {
		f := newCallFixture(t)
		f.hear(t, envproto.StatusOnline, envproto.Protocol)
		f.answerRequests(t, okReply)
		reply, err := f.c.Call(t.Context(), "linux-server", listRequest(time.Minute))
		require.NoError(t, err)
		assert.True(t, reply.OK)
		assert.JSONEq(t, `{"workspaces":[]}`, string(reply.Result))
		assert.Zero(t, pendingCount(f.c))
	})
	t.Run("the agent's error reply is returned as a Reply", func(t *testing.T) {
		f := newCallFixture(t)
		f.hear(t, envproto.StatusOnline, envproto.Protocol)
		f.answerRequests(t, envproto.Reply{OK: false, Error: &envproto.ReplyError{Code: "usage", Message: "bad"}})
		reply, err := f.c.Call(t.Context(), "linux-server", listRequest(time.Minute))
		require.NoError(t, err)
		assert.False(t, reply.OK)
		require.NotNil(t, reply.Error)
		assert.Equal(t, "usage", reply.Error.Code)
	})
	t.Run("no reply by deadline+grace is offline/timeout", func(t *testing.T) {
		f := newCallFixture(t)
		f.hear(t, envproto.StatusOnline, envproto.Protocol)
		f.c.callGrace = 10 * time.Millisecond
		_, err := f.c.Call(t.Context(), "linux-server", listRequest(10*time.Millisecond))
		var offline *OfflineError
		require.ErrorAs(t, err, &offline)
		assert.Equal(t, "timeout", offline.Reason)
		assert.Equal(t, callRequestID, offline.RequestID)
		assert.Equal(t, envproto.OpWorkspaceList, offline.Op)
		assert.Zero(t, pendingCount(f.c))
	})
	t.Run("a second call with a pending request id is refused and the first gets its reply", func(t *testing.T) {
		f := newCallFixture(t)
		f.hear(t, envproto.StatusOnline, envproto.Protocol)
		rc := f.captureRequests(t)
		f.c.callGrace = 10 * time.Millisecond
		first := f.callAsync(t.Context(), listRequest(time.Minute))
		require.Eventually(t, func() bool { return len(rc.messages()) == 1 }, time.Second, time.Millisecond)
		_, err := f.c.Call(t.Context(), "linux-server", listRequest(10*time.Millisecond))
		require.ErrorIs(t, err, ErrRequestPending)
		assert.Len(t, rc.messages(), 1)
		assert.Equal(t, 1, pendingCount(f.c))
		f.c.onRes(t.Context(), replyMessage(t, f.id, callRequestID, okReply))
		res := awaitCall(t, first)
		require.NoError(t, res.err)
		assert.True(t, res.reply.OK)
		assert.Zero(t, pendingCount(f.c))
	})
	t.Run("concurrent calls each get the reply for their own request id", func(t *testing.T) {
		f := newCallFixture(t)
		f.hear(t, envproto.StatusOnline, envproto.Protocol)
		rc := f.captureRequests(t)
		const otherID = "fedcba9876543210"
		first := f.callAsync(t.Context(), listRequest(time.Minute))
		second := f.callAsync(t.Context(), listRequestWithID(otherID, time.Minute))
		require.Eventually(t, func() bool { return len(rc.messages()) == 2 }, time.Second, time.Millisecond)
		f.c.onRes(t.Context(), replyMessage(t, f.id, otherID, envproto.Reply{OK: true, Result: json.RawMessage(`{"workspaces":["b"]}`)}))
		res := awaitCall(t, second)
		require.NoError(t, res.err)
		assert.JSONEq(t, `{"workspaces":["b"]}`, string(res.reply.Result))
		select {
		case <-first:
			require.FailNow(t, "a reply for another request finished the first call")
		case <-time.After(20 * time.Millisecond):
		}
		f.c.onRes(t.Context(), replyMessage(t, f.id, callRequestID, envproto.Reply{OK: true, Result: json.RawMessage(`{"workspaces":["a"]}`)}))
		res = awaitCall(t, first)
		require.NoError(t, res.err)
		assert.JSONEq(t, `{"workspaces":["a"]}`, string(res.reply.Result))
		assert.Zero(t, pendingCount(f.c))
	})
	t.Run("a reply from another environment's id is not delivered", func(t *testing.T) {
		f := newCallFixture(t)
		f.hear(t, envproto.StatusOnline, envproto.Protocol)
		rc := f.captureRequests(t)
		done := f.callAsync(t.Context(), listRequest(time.Minute))
		require.Eventually(t, func() bool { return len(rc.messages()) == 1 }, time.Second, time.Millisecond)
		f.c.onRes(t.Context(), replyMessage(t, "env-0a1b2c3d", callRequestID, okReply))
		select {
		case <-done:
			require.FailNow(t, "another environment's reply finished the call")
		case <-time.After(20 * time.Millisecond):
		}
		f.c.onRes(t.Context(), replyMessage(t, f.id, callRequestID, okReply))
		assert.NoError(t, awaitCall(t, done).err)
	})
	t.Run("a duplicate reply after the call answered is dropped and stored nowhere", func(t *testing.T) {
		f := newCallFixture(t)
		f.hear(t, envproto.StatusOnline, envproto.Protocol)
		f.answerRequests(t, okReply)
		reply, err := f.c.Call(t.Context(), "linux-server", listRequest(time.Minute))
		require.NoError(t, err)
		assert.True(t, reply.OK)
		f.c.onRes(t.Context(), replyMessage(t, f.id, callRequestID, envproto.Reply{OK: false, Error: &envproto.ReplyError{Code: "usage", Message: "late"}}))
		assert.Zero(t, pendingCount(f.c))
	})
	t.Run("a reconnect republishes the same request id once", func(t *testing.T) {
		f := newCallFixture(t)
		f.hear(t, envproto.StatusOnline, envproto.Protocol)
		rc := f.captureRequests(t)
		done := f.callAsync(t.Context(), listRequest(time.Minute))
		require.Eventually(t, func() bool { return len(rc.messages()) == 1 }, time.Second, time.Millisecond)
		f.hear(t, envproto.StatusOffline, envproto.Protocol)
		f.hear(t, envproto.StatusOnline, envproto.Protocol)
		require.Len(t, rc.messages(), 2)
		f.hear(t, envproto.StatusOffline, envproto.Protocol)
		f.hear(t, envproto.StatusOnline, envproto.Protocol)
		msgs := rc.messages()
		require.Len(t, msgs, 2)
		for _, msg := range msgs {
			assert.Equal(t, []byte(callRequestID), msg.CorrelationData)
			assert.Equal(t, envproto.Topic(f.id, envproto.LeafRes, callRequestID), msg.ResponseTopic)
			assert.Positive(t, msg.Expiry)
			assert.Equal(t, callRequestID, requestOf(t, &msg).RequestID)
		}
		f.c.onRes(t.Context(), replyMessage(t, f.id, callRequestID, okReply))
		assert.NoError(t, awaitCall(t, done).err)
	})
	t.Run("a reply for an unknown request id is ignored", func(t *testing.T) {
		f := newCallFixture(t)
		f.hear(t, envproto.StatusOnline, envproto.Protocol)
		rc := f.captureRequests(t)
		done := f.callAsync(t.Context(), listRequest(time.Minute))
		require.Eventually(t, func() bool { return len(rc.messages()) == 1 }, time.Second, time.Millisecond)
		f.c.onRes(t.Context(), replyMessage(t, f.id, "fedcba9876543210", okReply))
		select {
		case <-done:
			require.FailNow(t, "a reply for another request finished the call")
		case <-time.After(20 * time.Millisecond):
		}
		f.c.onRes(t.Context(), replyMessage(t, f.id, callRequestID, okReply))
		assert.NoError(t, awaitCall(t, done).err)
	})
	t.Run("ctx ending returns its error and forgets the call", func(t *testing.T) {
		f := newCallFixture(t)
		f.hear(t, envproto.StatusOnline, envproto.Protocol)
		rc := f.captureRequests(t)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := f.callAsync(ctx, listRequest(time.Minute))
		require.Eventually(t, func() bool { return len(rc.messages()) == 1 }, time.Second, time.Millisecond)
		cancel()
		require.ErrorIs(t, awaitCall(t, done).err, context.Canceled)
		f.c.mu.Lock()
		defer f.c.mu.Unlock()
		assert.Empty(t, f.c.calls)
	})
}
