package envagent

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/environment/envtest"
	"github.com/bobcob7/workspaces/internal/envproto"
)

// messageCapture records the messages a subscription sees.
type messageCapture struct {
	mu   sync.Mutex
	msgs []envproto.Message
}

// captureMessages subscribes to filter on b and records what it sees until the test ends.
func captureMessages(t *testing.T, b *envtest.Broker, filter string) *messageCapture {
	t.Helper()
	c := &messageCapture{}
	unsubscribe, err := b.Subscribe(t.Context(), filter, func(msg envproto.Message) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.msgs = append(c.msgs, msg)
	})
	require.NoError(t, err)
	t.Cleanup(unsubscribe)
	return c
}

// all returns the messages recorded so far.
func (c *messageCapture) all() []envproto.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]envproto.Message(nil), c.msgs...)
}

// sendToAgent publishes payload to the agent's request topic as the controller, asking for its reply on responseTopic.
func sendToAgent(t *testing.T, fx *agentFixture, payload []byte, responseTopic, correlation string) {
	t.Helper()
	require.NoError(t, fx.broker.Publish(t.Context(), envproto.Message{
		Topic:           envproto.Topic(fx.id.ID, envproto.LeafReq),
		Payload:         payload,
		QoS:             1,
		ResponseTopic:   responseTopic,
		CorrelationData: []byte(correlation),
	}))
}

// marshalTo encodes v for a test request.
func marshalTo(t *testing.T, v any) []byte {
	t.Helper()
	payload, err := json.Marshal(v)
	require.NoError(t, err)
	return payload
}

// awaitOnline waits until the controller sees the fixture's agent online, so the agent is subscribed.
func awaitOnline(t *testing.T, fx *agentFixture) {
	t.Helper()
	require.Eventually(t, func() bool { return fx.hello() != nil }, 5*time.Second, 10*time.Millisecond)
}

// countingClient counts the connects its envtest client makes.
type countingClient struct {
	*envtest.Client
	connects atomic.Int32
}

func (c *countingClient) Connect(ctx context.Context, opts envproto.ConnectOptions) error {
	c.connects.Add(1)
	return c.Client.Connect(ctx, opts)
}

func TestAgent_handle(t *testing.T) {
	t.Parallel()
	t.Run("answers an unknown operation with a usage error", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		runAgent(t, newFixtureAgent(fx, testOptions(), "[]"))
		awaitOnline(t, fx)
		res := envproto.Topic(fx.id.ID, envproto.LeafRes, "c0ffee")
		replies := captureMessages(t, fx.broker, res)
		sendToAgent(t, fx, marshalTo(t, envproto.Request{Op: "nope"}), res, "c0ffee")
		require.Eventually(t, func() bool { return len(replies.all()) == 1 }, 5*time.Second, 10*time.Millisecond)
		var reply envproto.Reply
		require.NoError(t, json.Unmarshal(replies.all()[0].Payload, &reply))
		assert.False(t, reply.OK)
		require.NotNil(t, reply.Error)
		assert.Equal(t, "usage", reply.Error.Code)
		assert.Equal(t, "unknown operation nope", reply.Error.Message)
		assert.Equal(t, []byte("c0ffee"), replies.all()[0].CorrelationData)
	})
	t.Run("answers a malformed request with a usage error", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		runAgent(t, newFixtureAgent(fx, testOptions(), "[]"))
		awaitOnline(t, fx)
		res := envproto.Topic(fx.id.ID, envproto.LeafRes, "bad")
		replies := captureMessages(t, fx.broker, res)
		sendToAgent(t, fx, []byte("not json"), res, "bad")
		require.Eventually(t, func() bool { return len(replies.all()) == 1 }, 5*time.Second, 10*time.Millisecond)
		var reply envproto.Reply
		require.NoError(t, json.Unmarshal(replies.all()[0].Payload, &reply))
		require.NotNil(t, reply.Error)
		assert.Equal(t, "usage", reply.Error.Code)
	})
	t.Run("never publishes to a response topic that isn't the agent's", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		runAgent(t, newFixtureAgent(fx, testOptions(), "[]"))
		awaitOnline(t, fx)
		other := envproto.Topic("env-00000000", envproto.LeafRes, "beef")
		otherReplies := captureMessages(t, fx.broker, other)
		own := envproto.Topic(fx.id.ID, envproto.LeafRes, "a11")
		ownReplies := captureMessages(t, fx.broker, own)
		sendToAgent(t, fx, marshalTo(t, envproto.Request{Op: "nope"}), other, "beef")
		sendToAgent(t, fx, marshalTo(t, envproto.Request{Op: "nope"}), own, "a11")
		require.Eventually(t, func() bool { return len(ownReplies.all()) == 1 }, 5*time.Second, 10*time.Millisecond)
		assert.Empty(t, otherReplies.all())
	})
	t.Run("applies a CA rotation without reconnecting", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		client := &countingClient{Client: fx.broker.NewClient()}
		agent := NewAgent(discardLog(), fx.files, client, testSnapshots("[]"), testProbe(), rand.Reader, testOptions())
		runAgent(t, agent)
		awaitOnline(t, fx)
		rotation, err := fx.store.RotateCA(t.Context())
		require.NoError(t, err)
		require.NoError(t, fx.ctrl.Reload(t.Context()))
		require.Eventually(t, func() bool {
			current, loadErr := fx.files.loadRoots()
			return loadErr == nil && len(current) == 2
		}, 5*time.Second, 10*time.Millisecond)
		roots, err := fx.files.loadRoots()
		require.NoError(t, err)
		fingerprints := []string{envproto.Fingerprint(roots[0]), envproto.Fingerprint(roots[1])}
		assert.ElementsMatch(t, []string{fx.id.Fingerprints()[0], rotation.Current.Fingerprint}, fingerprints)
		require.Eventually(t, func() bool { return len(fx.hello().CAFingerprints) == 2 }, 5*time.Second, 10*time.Millisecond)
		select {
		case <-client.Done():
			t.Fatal("the agent disconnected during the rotation")
		default:
		}
		assert.EqualValues(t, 1, client.connects.Load())
		assert.Equal(t, 1, fx.broker.Disconnect(fx.id.Cert.SerialNumber.Text(16)))
		require.Eventually(t, func() bool { return client.connects.Load() == 2 }, 5*time.Second, 10*time.Millisecond)
		require.Eventually(t, func() bool {
			peers := fx.broker.Clients()
			return len(peers) == 1 && peers[0].CN == fx.id.ID
		}, 5*time.Second, 10*time.Millisecond)
	})
}

func TestAgent_hello(t *testing.T) {
	t.Parallel()
	t.Run("reports the agent's platform, Claude and certificate", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		probe := &claudeProbeMock{ProbeFunc: func(context.Context) (string, *string) {
			return envproto.ClaudeMissing, nil
		}}
		agent := NewAgent(discardLog(), fx.files, nil, testSnapshots("[]"), probe, rand.Reader, testOptions())
		id := fx.id
		hello := agent.hello(t.Context(), &id)
		assert.Equal(t, envproto.StatusOnline, hello.Status)
		assert.Equal(t, "test", hello.WSVersion)
		assert.Equal(t, runtime.GOOS, hello.OS)
		assert.Equal(t, runtime.GOARCH, hello.Arch)
		assert.Equal(t, envproto.ClaudeMissing, hello.Claude)
		assert.Nil(t, hello.ClaudeVersion)
		assert.Equal(t, id.Cert.SerialNumber.Text(16), hello.CertSerial)
		assert.True(t, id.Cert.NotAfter.Equal(hello.CertNotAfter))
		assert.Equal(t, id.Fingerprints(), hello.CAFingerprints)
	})
}
