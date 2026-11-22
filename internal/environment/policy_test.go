package environment

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/environment/envtest"
	"github.com/bobcob7/workspaces/internal/envproto"
)

const testEnvID = "env-0a1b2c3d"

func TestAllowed(t *testing.T) {
	peer := envproto.Peer{ClientID: testEnvID, CN: testEnvID, Serial: "1"}
	own := func(levels string) string { return "ws/v1/env/" + testEnvID + "/" + levels }
	cases := []struct {
		name   string
		access envproto.Access
		topic  string
		want   bool
	}{
		{"publishes status", envproto.AccessPublish, own("status"), true},
		{"publishes snapshot", envproto.AccessPublish, own("snapshot"), true},
		{"publishes up", envproto.AccessPublish, own("up"), true},
		{"publishes a reply", envproto.AccessPublish, own("res/0123456789abcdef"), true},
		{"publishes a stream", envproto.AccessPublish, own("stream/s1/out"), true},
		{"publishes to req", envproto.AccessPublish, own("req"), false},
		{"publishes to in", envproto.AccessPublish, own("stream/s1/in"), false},
		{"publishes a reply with a plus", envproto.AccessPublish, own("res/+"), false},
		{"publishes a reply with a hash", envproto.AccessPublish, own("res/#"), false},
		{"publishes a stream with a plus", envproto.AccessPublish, own("stream/+/out"), false},
		{"publishes a stream with a hash", envproto.AccessPublish, own("stream/#"), false},
		{"publishes a level with a plus inside", envproto.AccessPublish, own("res/a+b"), false},
		{"publishes a level with a hash inside", envproto.AccessPublish, own("stream/a#/out"), false},
		{"publishes status with a plus id", envproto.AccessPublish, "ws/v1/env/+/status", false},
		{"publishes status with a hash id", envproto.AccessPublish, "ws/v1/env/#", false},
		{"publishes a plus level", envproto.AccessPublish, own("+"), false},
		{"publishes a hash level", envproto.AccessPublish, own("status/#"), false},
		{"publishes an extra level", envproto.AccessPublish, own("status/extra"), false},
		{"publishes another environment's status", envproto.AccessPublish, "ws/v1/env/env-ffffffff/status", false},
		{"publishes outside the prefix", envproto.AccessPublish, "ws/v1/other/" + testEnvID + "/status", false},
		{"publishes ctl events", envproto.AccessPublish, "ws/v1/env/ctl/events", false},
		{"subscribes to req", envproto.AccessSubscribe, own("req"), true},
		{"subscribes to a stream's in", envproto.AccessSubscribe, own("stream/s1/in"), true},
		{"subscribes to every stream's in", envproto.AccessSubscribe, own("stream/+/in"), true},
		{"subscribes to a stream's out", envproto.AccessSubscribe, own("stream/s1/out"), false},
		{"subscribes to every stream's out", envproto.AccessSubscribe, own("stream/+/out"), false},
		{"subscribes to a stream with a hash", envproto.AccessSubscribe, own("stream/#"), false},
		{"subscribes to a level with a plus inside", envproto.AccessSubscribe, own("stream/a+b/in"), false},
		{"subscribes to status", envproto.AccessSubscribe, own("status"), false},
		{"subscribes to replies", envproto.AccessSubscribe, own("res/+"), false},
		{"subscribes with a hash", envproto.AccessSubscribe, own("#"), false},
		{"subscribes below req", envproto.AccessSubscribe, own("req/#"), false},
		{"subscribes to req of every environment", envproto.AccessSubscribe, "ws/v1/env/+/req", false},
		{"subscribes to another environment's req", envproto.AccessSubscribe, "ws/v1/env/env-ffffffff/req", false},
		{"subscribes outside the prefix", envproto.AccessSubscribe, "ws/v1/other/" + testEnvID + "/req", false},
		{"unknown access", envproto.Access(0), own("status"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, allowed(peer, tc.access, tc.topic))
		})
	}
}

func TestPolicy_connect(t *testing.T) {
	t.Run("accepts an enrolled environment's certificate", func(t *testing.T) {
		store := newTestStore(t)
		rec := enrolledEnvironment(t, store)
		p := testPolicy(t, store)
		assert.NoError(t, p.connect(peerOf(&rec)))
	})
	t.Run("refuses a client id that isn't the common name", func(t *testing.T) {
		store := newTestStore(t)
		rec := enrolledEnvironment(t, store)
		peer := peerOf(&rec)
		peer.ClientID = "env-ffffffff"
		assert.ErrorIs(t, testPolicy(t, store).connect(peer), errClientIDMismatch)
	})
	t.Run("refuses a common name that isn't an environment id", func(t *testing.T) {
		store := newTestStore(t)
		enrolledEnvironment(t, store)
		peer := envproto.Peer{ClientID: "linux-server", CN: "linux-server", Serial: "1"}
		assert.ErrorIs(t, testPolicy(t, store).connect(peer), errInvalidID)
	})
	t.Run("refuses an unknown environment", func(t *testing.T) {
		store := newTestStore(t)
		enrolledEnvironment(t, store)
		peer := envproto.Peer{ClientID: testEnvID, CN: testEnvID, Serial: "1"}
		assert.ErrorIs(t, testPolicy(t, store).connect(peer), ErrUnknown)
	})
	t.Run("refuses a pending environment", func(t *testing.T) {
		store := newTestStore(t)
		rec, _ := addEnvironment(t, store, "linux-server")
		peer := envproto.Peer{ClientID: rec.ID, CN: rec.ID, Serial: "1", Issuer: currentFingerprint(t, store)}
		assert.ErrorIs(t, testPolicy(t, store).connect(peer), errNotEnrolled)
	})
	t.Run("refuses a removed environment", func(t *testing.T) {
		store := newTestStore(t)
		rec := enrolledEnvironment(t, store)
		_, _, err := store.Remove(t.Context(), "linux-server")
		require.NoError(t, err)
		assert.ErrorIs(t, testPolicy(t, store).connect(peerOf(&rec)), ErrUnknown)
	})
	t.Run("refuses a revoked serial", func(t *testing.T) {
		store := newTestStore(t)
		old := enrolledEnvironment(t, store)
		_, code, _, err := store.Rotate(t.Context(), "linux-server", testBroker)
		require.NoError(t, err)
		_, err = enrollWith(t, store, code)
		require.NoError(t, err)
		current, err := store.Get("linux-server")
		require.NoError(t, err)
		require.NotEqual(t, old.CertSerial, current.CertSerial)
		p := testPolicy(t, store)
		require.ErrorIs(t, p.connect(peerOf(&old)), errRevoked)
		assert.NoError(t, p.connect(peerOf(&current)))
	})
	t.Run("refuses a certificate from an untrusted CA", func(t *testing.T) {
		store := newTestStore(t)
		rec := enrolledEnvironment(t, store)
		peer := peerOf(&rec)
		peer.Issuer = "not-a-trusted-root"
		assert.ErrorIs(t, testPolicy(t, store).connect(peer), errUntrustedCA)
	})
}

func TestPolicy_rules(t *testing.T) {
	t.Run("a connected agent that publishes to another environment is disconnected", func(t *testing.T) {
		store := newTestStore(t)
		own, cert := enrollAgent(t, store, "linux-server")
		other, _ := enrollAgent(t, store, "mac-laptop")
		b := envtest.NewBroker()
		startController(t, store, b)
		agent := connectAgent(t, store, b, own.ID, &cert)
		err := agent.Publish(t.Context(), envproto.Message{
			Topic:   envproto.Topic(other.ID, envproto.LeafStatus),
			Payload: []byte(`{"status":"online"}`),
		})
		require.ErrorIs(t, err, envtest.ErrDisconnected)
		assert.True(t, isClosed(agent.Done()))
	})
	t.Run("a connected agent that publishes a wildcard topic is disconnected", func(t *testing.T) {
		store := newTestStore(t)
		own, cert := enrollAgent(t, store, "linux-server")
		b := envtest.NewBroker()
		startController(t, store, b)
		agent := connectAgent(t, store, b, own.ID, &cert)
		err := agent.Publish(t.Context(), envproto.Message{
			Topic:   envproto.Topic(own.ID, envproto.LeafRes, "+"),
			Payload: []byte(`{}`),
		})
		require.ErrorIs(t, err, envtest.ErrDisconnected)
		assert.True(t, isClosed(agent.Done()))
	})
}

// testPolicy is a policy over store's current trust.
func testPolicy(t *testing.T, store *Store) *policy {
	t.Helper()
	tr, err := store.trust()
	require.NoError(t, err)
	return &policy{store: store, trust: func() trust { return tr }}
}

// peerOf is the peer an agent with rec's certificate connects as.
func peerOf(rec *Record) envproto.Peer {
	return envproto.Peer{ClientID: rec.ID, CN: rec.ID, Serial: rec.CertSerial, Issuer: rec.CertCA}
}

// currentFingerprint is the fingerprint of store's current CA.
func currentFingerprint(t *testing.T, store *Store) string {
	t.Helper()
	fp, err := store.currentFingerprint()
	require.NoError(t, err)
	return fp
}
