package environment

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/environment/envtest"
	"github.com/bobcob7/workspaces/internal/envproto"
)

// syncBuffer is a log sink that the server's goroutines and the test may share.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func testSettings() Settings {
	return Settings{Listen: "127.0.0.1:0", EnrollListen: "127.0.0.1:0", PublicName: "127.0.0.1", RenewBeforeDays: 30}
}

// newController returns a controller over store and b that is closed at cleanup.
func newController(t *testing.T, store *Store, b broker, log *slog.Logger, settings Settings) *Controller {
	t.Helper()
	c := NewController(log, store, b, settings, store.now)
	t.Cleanup(func() { assert.NoError(t, c.Close()) })
	return c
}

// startController starts a controller with testSettings over store's CA.
func startController(t *testing.T, store *Store, b broker) *Controller {
	t.Helper()
	c := newController(t, store, b, discardLog(), testSettings())
	require.NoError(t, c.Start(t.Context()))
	return c
}

// enrollAgent enrolls an environment called name and returns its record and
// the client certificate its agent holds.
func enrollAgent(t *testing.T, store *Store, name string) (Record, tls.Certificate) {
	t.Helper()
	_, code := addEnvironment(t, store, name)
	parsed, err := envproto.ParseCode(code)
	require.NoError(t, err)
	key := newTestKey(t)
	resp, err := store.Enroll(t.Context(), parsed.Secret, newTestCSR(t, key), parsed.Host, "127.0.0.1")
	require.NoError(t, err)
	rec, err := store.Get(name)
	require.NoError(t, err)
	return rec, tls.Certificate{Certificate: [][]byte{parseCertPEM(t, resp.Cert).Raw}, PrivateKey: key}
}

// agentTLS is an agent's TLS config: the store's roots and its certificate.
func agentTLS(t *testing.T, store *Store, cert *tls.Certificate) *tls.Config {
	t.Helper()
	tr, err := store.trust()
	require.NoError(t, err)
	return clientTLS(tr.pool(), cert)
}

// dialAgent connects an agent as id with cert to b, with its offline will.
func dialAgent(t *testing.T, store *Store, b *envtest.Broker, id string, cert *tls.Certificate) (*envtest.Client, error) {
	t.Helper()
	client := b.NewClient()
	err := client.Connect(t.Context(), envproto.ConnectOptions{
		Address:  "127.0.0.1:8883",
		TLS:      agentTLS(t, store, cert),
		ClientID: id,
		Will: &envproto.Message{
			Topic:   envproto.Topic(id, envproto.LeafStatus),
			Payload: envproto.OfflinePayload(),
			QoS:     1,
		},
	})
	return client, err
}

// connectAgent connects an agent as id and fails the test if it is refused.
func connectAgent(t *testing.T, store *Store, b *envtest.Broker, id string, cert *tls.Certificate) *envtest.Client {
	t.Helper()
	client, err := dialAgent(t, store, b, id, cert)
	require.NoError(t, err)
	return client
}

// helloPayload is the hello an agent publishes that trusts the store's roots.
func helloPayload(t *testing.T, store *Store) []byte {
	t.Helper()
	tr, err := store.trust()
	require.NoError(t, err)
	payload, err := json.Marshal(envproto.Hello{Status: envproto.StatusOnline, At: store.now().UTC(), CAFingerprints: tr.fingerprints()})
	require.NoError(t, err)
	return payload
}

// captureMessages subscribes the controller side of b to filter and returns what it receives.
func captureMessages(t *testing.T, b *envtest.Broker, filter string) *[]envproto.Message {
	t.Helper()
	var msgs []envproto.Message
	_, err := b.Subscribe(t.Context(), filter, func(msg envproto.Message) { msgs = append(msgs, msg) })
	require.NoError(t, err)
	return &msgs
}

// isClosed reports whether ch is closed.
func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// bundleFingerprints returns the fingerprints of every certificate in a PEM bundle, sorted.
func bundleFingerprints(t *testing.T, bundle string) []string {
	t.Helper()
	var fps []string
	for rest := []byte(bundle); len(rest) > 0; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		require.NoError(t, err)
		fps = append(fps, envproto.Fingerprint(cert))
	}
	slices.Sort(fps)
	return fps
}

// sortedFingerprints returns the store's trusted fingerprints, sorted.
func sortedFingerprints(t *testing.T, store *Store) []string {
	t.Helper()
	tr, err := store.trust()
	require.NoError(t, err)
	fps := tr.fingerprints()
	slices.Sort(fps)
	return fps
}

// requestOf decodes the request in msg.
func requestOf(t *testing.T, msg *envproto.Message) envproto.Request {
	t.Helper()
	var req envproto.Request
	require.NoError(t, json.Unmarshal(msg.Payload, &req))
	return req
}

// replyOf decodes the reply in msg.
func replyOf(t *testing.T, msg *envproto.Message) envproto.Reply {
	t.Helper()
	var reply envproto.Reply
	require.NoError(t, json.Unmarshal(msg.Payload, &reply))
	return reply
}

func TestController_Start(t *testing.T) {
	t.Run("listens on nothing without a CA", func(t *testing.T) {
		c := newController(t, newTestStore(t), envtest.NewBroker(), discardLog(), testSettings())
		require.NoError(t, c.Start(t.Context()))
		assert.False(t, c.Status().Listening)
	})
	t.Run("warns instead of listening without public_name", func(t *testing.T) {
		store := newTestStore(t)
		addEnvironment(t, store, "linux-server")
		settings := testSettings()
		settings.PublicName = ""
		c := newController(t, store, envtest.NewBroker(), discardLog(), settings)
		require.NoError(t, c.Start(t.Context()))
		assert.False(t, c.Status().Listening)
		assert.Contains(t, strings.Join(c.Warnings(), "\n"), "public_name")
	})
	t.Run("listens on the broker and enrollment addresses", func(t *testing.T) {
		store := newTestStore(t)
		addEnvironment(t, store, "linux-server")
		c := startController(t, store, envtest.NewBroker())
		status := c.Status()
		assert.True(t, status.Listening)
		assert.NotEmpty(t, status.Listen)
		assert.NotEmpty(t, status.EnrollListen)
		assert.Empty(t, c.Warnings())
	})
	t.Run("reports a broker start error", func(t *testing.T) {
		store := newTestStore(t)
		addEnvironment(t, store, "linux-server")
		b := &brokerMock{
			StartFunc: func(context.Context, string, *tls.Config, envproto.Rules) (string, error) {
				return "", errors.New("boom")
			},
		}
		c := newController(t, store, b, discardLog(), testSettings())
		require.ErrorContains(t, c.Start(t.Context()), "boom")
		assert.False(t, c.Status().Listening)
		assert.Contains(t, strings.Join(c.Warnings(), "\n"), "boom")
	})
	t.Run("closes the broker when a subscription fails", func(t *testing.T) {
		store := newTestStore(t)
		addEnvironment(t, store, "linux-server")
		b := &brokerMock{
			StartFunc: func(context.Context, string, *tls.Config, envproto.Rules) (string, error) {
				return "127.0.0.1:0", nil
			},
			SubscribeFunc: func(context.Context, string, func(envproto.Message)) (func(), error) {
				return nil, errors.New("no subscriptions")
			},
			CloseFunc: func() error { return nil },
		}
		c := newController(t, store, b, discardLog(), testSettings())
		require.ErrorContains(t, c.Start(t.Context()), "no subscriptions")
		assert.Len(t, b.CloseCalls(), 1)
		assert.False(t, c.Status().Listening)
	})
}

func TestController_Reload(t *testing.T) {
	t.Run("starts when the CA is new", func(t *testing.T) {
		store := newTestStore(t)
		addEnvironment(t, store, "linux-server")
		c := newController(t, store, envtest.NewBroker(), discardLog(), testSettings())
		require.NoError(t, c.Reload(t.Context()))
		assert.True(t, c.Status().Listening)
	})
	t.Run("disconnects a removed environment that can't reconnect", func(t *testing.T) {
		store := newTestStore(t)
		b := envtest.NewBroker()
		rec, cert := enrollAgent(t, store, "linux-server")
		c := startController(t, store, b)
		agent := connectAgent(t, store, b, rec.ID, &cert)
		_, _, err := store.Remove(t.Context(), "linux-server")
		require.NoError(t, err)
		require.NoError(t, c.Reload(t.Context()))
		assert.True(t, isClosed(agent.Done()))
		_, err = dialAgent(t, store, b, rec.ID, &cert)
		assert.ErrorIs(t, err, envtest.ErrRefused)
	})
	t.Run("keeps a connected agent after rotation and sends it both roots", func(t *testing.T) {
		store := newTestStore(t)
		b := envtest.NewBroker()
		rec, cert := enrollAgent(t, store, "linux-server")
		c := startController(t, store, b)
		agent := connectAgent(t, store, b, rec.ID, &cert)
		var caUpdates []envproto.Message
		require.NoError(t, agent.Subscribe(t.Context(), envproto.Topic(rec.ID, envproto.LeafReq), func(msg envproto.Message) {
			caUpdates = append(caUpdates, msg)
		}))
		require.NoError(t, agent.Publish(t.Context(), envproto.Message{
			Topic:   envproto.Topic(rec.ID, envproto.LeafStatus),
			Payload: helloPayload(t, store),
		}))
		require.Empty(t, caUpdates)
		_, err := store.RotateCA(t.Context())
		require.NoError(t, err)
		require.NoError(t, c.Reload(t.Context()))
		assert.False(t, isClosed(agent.Done()))
		current, err := store.currentCert()
		require.NoError(t, err)
		served := c.currentState().server
		require.Len(t, served.Certificate, 2)
		assert.Equal(t, current.Raw, served.Certificate[1])
		leaf, err := x509.ParseCertificate(served.Certificate[0])
		require.NoError(t, err)
		assert.NoError(t, leaf.CheckSignatureFrom(current))
		require.Len(t, caUpdates, 1)
		req := requestOf(t, &caUpdates[0])
		assert.Equal(t, envproto.OpCAUpdate, req.Op)
		var args envproto.CAUpdateArgs
		require.NoError(t, json.Unmarshal(req.Args, &args))
		assert.Equal(t, sortedFingerprints(t, store), bundleFingerprints(t, args.CABundle))
		assert.Len(t, bundleFingerprints(t, args.CABundle), 2)
	})
	t.Run("disconnects an old-CA client once the old CA is dropped", func(t *testing.T) {
		store := newTestStore(t)
		b := envtest.NewBroker()
		rec, cert := enrollAgent(t, store, "linux-server")
		c := startController(t, store, b)
		agent := connectAgent(t, store, b, rec.ID, &cert)
		_, err := store.RotateCA(t.Context())
		require.NoError(t, err)
		_, err = store.FinishCA(t.Context(), true)
		require.NoError(t, err)
		require.NoError(t, c.Reload(t.Context()))
		assert.True(t, isClosed(agent.Done()))
		_, err = dialAgent(t, store, b, rec.ID, &cert)
		assert.ErrorIs(t, err, envtest.ErrRefused)
	})
}

func TestController_Status(t *testing.T) {
	t.Run("lists environments by name with what the controller has heard", func(t *testing.T) {
		store := newTestStore(t)
		b := envtest.NewBroker()
		rec, cert := enrollAgent(t, store, "linux-server")
		addEnvironment(t, store, "mac-laptop")
		c := startController(t, store, b)
		agent := connectAgent(t, store, b, rec.ID, &cert)
		require.NoError(t, agent.Publish(t.Context(), envproto.Message{
			Topic:   envproto.Topic(rec.ID, envproto.LeafStatus),
			Payload: helloPayload(t, store),
		}))
		envs := c.Status().Environments
		require.Len(t, envs, 2)
		assert.Equal(t, "linux-server", envs[0].Name)
		assert.Equal(t, envproto.StatusOnline, envs[0].Status)
		assert.NotNil(t, envs[0].LastSeen)
		require.NotNil(t, envs[0].Hello)
		assert.Equal(t, envproto.StatusOnline, envs[0].Hello.Status)
		assert.Equal(t, "mac-laptop", envs[1].Name)
		assert.Equal(t, statusPending, envs[1].Status)
		assert.Nil(t, envs[1].Hello)
	})
	t.Run("reports offline after the will, keeping the last hello", func(t *testing.T) {
		store := newTestStore(t)
		b := envtest.NewBroker()
		rec, cert := enrollAgent(t, store, "linux-server")
		c := startController(t, store, b)
		agent := connectAgent(t, store, b, rec.ID, &cert)
		require.NoError(t, agent.Publish(t.Context(), envproto.Message{
			Topic:   envproto.Topic(rec.ID, envproto.LeafStatus),
			Payload: helloPayload(t, store),
		}))
		b.Disconnect(rec.CertSerial)
		envs := c.Status().Environments
		require.Len(t, envs, 1)
		assert.Equal(t, envproto.StatusOffline, envs[0].Status)
		assert.NotNil(t, envs[0].Hello)
	})
	t.Run("lists no environments as an empty list", func(t *testing.T) {
		c := newController(t, newTestStore(t), envtest.NewBroker(), discardLog(), testSettings())
		envs := c.Status().Environments
		assert.NotNil(t, envs)
		assert.Empty(t, envs)
	})
}

func TestController_Warnings(t *testing.T) {
	t.Run("warns when the CA is within renew_before_days of expiry", func(t *testing.T) {
		store := newTestStore(t)
		addEnvironment(t, store, "linux-server")
		settings := testSettings()
		settings.RenewBeforeDays = 36500
		c := newController(t, store, envtest.NewBroker(), discardLog(), settings)
		require.NoError(t, c.Start(t.Context()))
		warnings := c.Warnings()
		require.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], "expires")
	})
	t.Run("has no warnings far from expiry", func(t *testing.T) {
		store := newTestStore(t)
		addEnvironment(t, store, "linux-server")
		c := startController(t, store, envtest.NewBroker())
		assert.NotNil(t, c.Warnings())
		assert.Empty(t, c.Warnings())
	})
}

func TestController_Close(t *testing.T) {
	t.Run("does nothing before Start", func(t *testing.T) {
		c := NewController(discardLog(), newTestStore(t), envtest.NewBroker(), testSettings(), fixedClock())
		assert.NoError(t, c.Close())
	})
	t.Run("stops listening and drops the clients", func(t *testing.T) {
		store := newTestStore(t)
		b := envtest.NewBroker()
		rec, cert := enrollAgent(t, store, "linux-server")
		c := startController(t, store, b)
		agent := connectAgent(t, store, b, rec.ID, &cert)
		require.NoError(t, c.Close())
		status := c.Status()
		assert.False(t, status.Listening)
		assert.Empty(t, status.Listen)
		assert.Empty(t, b.Clients())
		assert.True(t, isClosed(agent.Done()))
	})
}

func TestController_enroll(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	_, code := addEnvironment(t, store, "linux-server")
	parsed, err := envproto.ParseCode(code)
	require.NoError(t, err)
	logs := &syncBuffer{}
	c := newController(t, store, envtest.NewBroker(), slog.New(slog.NewJSONHandler(logs, nil)), testSettings())
	require.NoError(t, c.Start(t.Context()))
	tr, err := store.trust()
	require.NoError(t, err)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: clientTLS(tr.pool(), nil)}}
	t.Cleanup(client.CloseIdleConnections)
	base := "https://" + c.Status().EnrollListen
	csr := string(newTestCSR(t, newTestKey(t)))
	enrollBody := func(secret string) []byte {
		body, err := json.Marshal(envproto.EnrollRequest{Secret: secret, CSR: csr})
		require.NoError(t, err)
		return body
	}
	t.Run("refuses a wrong secret with 403", func(t *testing.T) {
		resp := doRequest(t, client, http.MethodPost, base+envproto.EnrollPath, enrollBody("wrong"))
		defer func() { assert.NoError(t, resp.Body.Close()) }()
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})
	t.Run("refuses GET with 405", func(t *testing.T) {
		resp := doRequest(t, client, http.MethodGet, base+envproto.EnrollPath, nil)
		defer func() { assert.NoError(t, resp.Body.Close()) }()
		assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	})
	t.Run("answers other paths with 404", func(t *testing.T) {
		resp := doRequest(t, client, http.MethodPost, base+"/v1/other", []byte("{}"))
		defer func() { assert.NoError(t, resp.Body.Close()) }()
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
	t.Run("enrolls with the right secret and refuses it again with 409", func(t *testing.T) {
		resp := doRequest(t, client, http.MethodPost, base+envproto.EnrollPath, enrollBody(parsed.Secret))
		defer func() { assert.NoError(t, resp.Body.Close()) }()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var got envproto.EnrollResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
		rec, err := store.Get("linux-server")
		require.NoError(t, err)
		assert.Equal(t, rec.ID, got.ID)
		assert.Equal(t, "127.0.0.1", got.ServerName)
		again := doRequest(t, client, http.MethodPost, base+envproto.EnrollPath, enrollBody(parsed.Secret))
		defer func() { assert.NoError(t, again.Body.Close()) }()
		assert.Equal(t, http.StatusConflict, again.StatusCode)
	})
	t.Run("never logs the secret", func(t *testing.T) {
		assert.Contains(t, logs.String(), "environment enrolled")
		assert.NotContains(t, logs.String(), parsed.Secret)
	})
}

// doRequest sends body to url with method on client and fails the test on a transport error.
func doRequest(t *testing.T, client *http.Client, method, url string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	require.NoError(t, err)
	return resp
}

func TestController_onStatus(t *testing.T) {
	t.Run("ignores an environment it doesn't know", func(t *testing.T) {
		store := newTestStore(t)
		enrollAgent(t, store, "linux-server")
		c := newController(t, store, envtest.NewBroker(), discardLog(), testSettings())
		c.onStatus(t.Context(), &envproto.Message{
			Topic:   envproto.Topic("env-ffffffff", envproto.LeafStatus),
			Payload: helloPayload(t, store),
		})
		assert.Empty(t, c.presence)
	})
	t.Run("ignores a payload that isn't a hello", func(t *testing.T) {
		store := newTestStore(t)
		rec, _ := enrollAgent(t, store, "linux-server")
		c := newController(t, store, envtest.NewBroker(), discardLog(), testSettings())
		c.onStatus(t.Context(), &envproto.Message{
			Topic:   envproto.Topic(rec.ID, envproto.LeafStatus),
			Payload: []byte(`[`),
		})
		assert.Empty(t, c.presence)
	})
	t.Run("ignores a status that isn't online or offline and keeps what it heard", func(t *testing.T) {
		store := newTestStore(t)
		rec, _ := enrollAgent(t, store, "linux-server")
		c := startController(t, store, envtest.NewBroker())
		c.onStatus(t.Context(), &envproto.Message{
			Topic:   envproto.Topic(rec.ID, envproto.LeafStatus),
			Payload: helloPayload(t, store),
		})
		c.onStatus(t.Context(), &envproto.Message{
			Topic:   envproto.Topic(rec.ID, envproto.LeafStatus),
			Payload: []byte(`{"status":"sleeping"}`),
		})
		assert.True(t, c.presence[rec.ID].online)
	})
	t.Run("asks an online environment that lacks a root for ca.update", func(t *testing.T) {
		store := newTestStore(t)
		b := envtest.NewBroker()
		rec, _ := enrollAgent(t, store, "linux-server")
		c := startController(t, store, b)
		updates := captureMessages(t, b, envproto.Topic(rec.ID, envproto.LeafReq))
		c.onStatus(t.Context(), &envproto.Message{
			Topic:   envproto.Topic(rec.ID, envproto.LeafStatus),
			Payload: []byte(`{"status":"online","ca_fingerprints":[]}`),
		})
		require.Len(t, *updates, 1)
		update := (*updates)[0]
		assert.Equal(t, envproto.OpCAUpdate, requestOf(t, &update).Op)
		assert.Regexp(t, `^ws/v1/env/env-[0-9a-f]{8}/res/[0-9a-f]{16}$`, update.ResponseTopic)
		assert.Equal(t, update.ResponseTopic[len(update.ResponseTopic)-16:], string(update.CorrelationData))
		assert.Equal(t, caUpdateExpiry, update.Expiry)
	})
	t.Run("doesn't ask an environment that trusts every root", func(t *testing.T) {
		store := newTestStore(t)
		b := envtest.NewBroker()
		rec, _ := enrollAgent(t, store, "linux-server")
		c := startController(t, store, b)
		updates := captureMessages(t, b, envproto.Topic(rec.ID, envproto.LeafReq))
		c.onStatus(t.Context(), &envproto.Message{
			Topic:   envproto.Topic(rec.ID, envproto.LeafStatus),
			Payload: helloPayload(t, store),
		})
		assert.Empty(t, *updates)
	})
}

func TestController_onSnapshot(t *testing.T) {
	t.Run("writes the snapshot under the environment's name", func(t *testing.T) {
		store := newTestStore(t)
		rec, _ := enrollAgent(t, store, "linux-server")
		c := newController(t, store, envtest.NewBroker(), discardLog(), testSettings())
		c.onSnapshot(t.Context(), &envproto.Message{
			Topic:   envproto.Topic(rec.ID, envproto.LeafSnapshot),
			Payload: []byte(`{"at":"2026-11-22T10:00:00Z","workspaces":[{"name":"api"}]}`),
		})
		data, err := store.ReadSnapshot("linux-server")
		require.NoError(t, err)
		var got struct {
			Version    int             `json:"version"`
			ID         string          `json:"id"`
			Name       string          `json:"name"`
			ReceivedAt string          `json:"received_at"`
			Snapshot   json.RawMessage `json:"snapshot"`
		}
		require.NoError(t, json.Unmarshal(data, &got))
		assert.Equal(t, snapshotVersion, got.Version)
		assert.Equal(t, rec.ID, got.ID)
		assert.Equal(t, "linux-server", got.Name)
		assert.Equal(t, store.now().UTC().Format(time.RFC3339), got.ReceivedAt)
		assert.JSONEq(t, `{"at":"2026-11-22T10:00:00Z","workspaces":[{"name":"api"}]}`, string(got.Snapshot))
	})
	t.Run("ignores an environment it doesn't know", func(t *testing.T) {
		store := newTestStore(t)
		enrollAgent(t, store, "linux-server")
		c := newController(t, store, envtest.NewBroker(), discardLog(), testSettings())
		c.onSnapshot(t.Context(), &envproto.Message{
			Topic:   envproto.Topic("env-ffffffff", envproto.LeafSnapshot),
			Payload: []byte(`{"workspaces":[]}`),
		})
		_, err := store.ReadSnapshot("linux-server")
		assert.ErrorIs(t, err, fs.ErrNotExist)
	})
	t.Run("refuses a null payload", func(t *testing.T) {
		store := newTestStore(t)
		rec, _ := enrollAgent(t, store, "linux-server")
		c := newController(t, store, envtest.NewBroker(), discardLog(), testSettings())
		c.onSnapshot(t.Context(), &envproto.Message{
			Topic:   envproto.Topic(rec.ID, envproto.LeafSnapshot),
			Payload: []byte("null"),
		})
		_, err := store.ReadSnapshot("linux-server")
		assert.ErrorIs(t, err, fs.ErrNotExist)
	})
	t.Run("refuses a payload that isn't a snapshot", func(t *testing.T) {
		store := newTestStore(t)
		rec, _ := enrollAgent(t, store, "linux-server")
		c := newController(t, store, envtest.NewBroker(), discardLog(), testSettings())
		c.onSnapshot(t.Context(), &envproto.Message{
			Topic:   envproto.Topic(rec.ID, envproto.LeafSnapshot),
			Payload: []byte(`[`),
		})
		_, err := store.ReadSnapshot("linux-server")
		assert.ErrorIs(t, err, fs.ErrNotExist)
	})
}

func TestController_onUp(t *testing.T) {
	// renewMessage is a cert.renew from rec's agent, answered on env/<id>/req.
	renewMessage := func(t *testing.T, id, op string) *envproto.Message {
		t.Helper()
		csr := string(newTestCSR(t, newTestKey(t)))
		payload, err := marshalRequest(op, envproto.RenewArgs{CSR: csr})
		require.NoError(t, err)
		return &envproto.Message{
			Topic:           envproto.Topic(id, envproto.LeafUp),
			Payload:         payload,
			ResponseTopic:   envproto.Topic(id, envproto.LeafReq),
			CorrelationData: []byte("abc"),
		}
	}
	t.Run("renews the certificate and replies on env/<id>/req", func(t *testing.T) {
		store := newTestStore(t)
		b := envtest.NewBroker()
		rec, _ := enrollAgent(t, store, "linux-server")
		c := startController(t, store, b)
		replies := captureMessages(t, b, envproto.Topic(rec.ID, envproto.LeafReq))
		c.onUp(t.Context(), renewMessage(t, rec.ID, envproto.OpCertRenew))
		require.Len(t, *replies, 1)
		reply := (*replies)[0]
		assert.Equal(t, []byte("abc"), reply.CorrelationData)
		got := replyOf(t, &reply)
		require.True(t, got.OK)
		var result envproto.RenewResult
		require.NoError(t, json.Unmarshal(got.Result, &result))
		assert.Equal(t, rec.ID, parseCertPEM(t, result.Cert).Subject.CommonName)
		assert.Equal(t, sortedFingerprints(t, store), bundleFingerprints(t, result.CABundle))
	})
	t.Run("renews from the current CA after rotation", func(t *testing.T) {
		store := newTestStore(t)
		b := envtest.NewBroker()
		rec, _ := enrollAgent(t, store, "linux-server")
		c := startController(t, store, b)
		renew := func() *x509.Certificate {
			replies := captureMessages(t, b, envproto.Topic(rec.ID, envproto.LeafReq))
			c.onUp(t.Context(), renewMessage(t, rec.ID, envproto.OpCertRenew))
			require.Len(t, *replies, 1)
			reply := replyOf(t, &(*replies)[0])
			require.True(t, reply.OK)
			var result envproto.RenewResult
			require.NoError(t, json.Unmarshal(reply.Result, &result))
			return parseCertPEM(t, result.Cert)
		}
		signedBy := func(cert, ca *x509.Certificate) error {
			roots := x509.NewCertPool()
			roots.AddCert(ca)
			_, err := cert.Verify(x509.VerifyOptions{
				Roots:       roots,
				CurrentTime: store.now(),
				KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
			})
			return err
		}
		oldCA, err := store.currentCert()
		require.NoError(t, err)
		require.NoError(t, signedBy(renew(), oldCA))
		_, err = store.RotateCA(t.Context())
		require.NoError(t, err)
		require.NoError(t, c.Reload(t.Context()))
		newCA, err := store.currentCert()
		require.NoError(t, err)
		renewed := renew()
		assert.Equal(t, rec.ID, renewed.Subject.CommonName)
		assert.Equal(t, newCA.SubjectKeyId, renewed.AuthorityKeyId)
		require.NoError(t, signedBy(renewed, newCA))
		assert.Error(t, signedBy(renewed, oldCA))
	})
	t.Run("an agent's cert.renew over the broker gets its reply on env/<id>/req", func(t *testing.T) {
		store := newTestStore(t)
		b := envtest.NewBroker()
		rec, cert := enrollAgent(t, store, "linux-server")
		startController(t, store, b)
		agent := connectAgent(t, store, b, rec.ID, &cert)
		var replies []envproto.Message
		require.NoError(t, agent.Subscribe(t.Context(), envproto.Topic(rec.ID, envproto.LeafReq), func(msg envproto.Message) {
			replies = append(replies, msg)
		}))
		require.NoError(t, agent.Publish(t.Context(), *renewMessage(t, rec.ID, envproto.OpCertRenew)))
		require.Len(t, replies, 1)
		assert.Equal(t, []byte("abc"), replies[0].CorrelationData)
		reply := replyOf(t, &replies[0])
		require.True(t, reply.OK)
		var result envproto.RenewResult
		require.NoError(t, json.Unmarshal(reply.Result, &result))
		assert.Equal(t, rec.ID, parseCertPEM(t, result.Cert).Subject.CommonName)
		assert.False(t, isClosed(agent.Done()))
	})
	t.Run("doesn't act or reply when the response topic isn't env/<id>/req", func(t *testing.T) {
		store := newTestStore(t)
		b := envtest.NewBroker()
		rec, _ := enrollAgent(t, store, "linux-server")
		before, err := store.Get("linux-server")
		require.NoError(t, err)
		c := startController(t, store, b)
		msg := renewMessage(t, rec.ID, envproto.OpCertRenew)
		msg.ResponseTopic = envproto.Topic(rec.ID, "other")
		replies := captureMessages(t, b, msg.ResponseTopic)
		c.onUp(t.Context(), msg)
		assert.Empty(t, *replies)
		after, err := store.Get("linux-server")
		require.NoError(t, err)
		assert.Equal(t, before.CertSerial, after.CertSerial)
	})
	t.Run("answers usage for an unknown operation", func(t *testing.T) {
		store := newTestStore(t)
		b := envtest.NewBroker()
		rec, _ := enrollAgent(t, store, "linux-server")
		c := startController(t, store, b)
		replies := captureMessages(t, b, envproto.Topic(rec.ID, envproto.LeafReq))
		c.onUp(t.Context(), renewMessage(t, rec.ID, "cert.delete"))
		assert.Equal(t, "usage", replyOf(t, &(*replies)[0]).Error.Code)
	})
	t.Run("answers usage for a malformed request", func(t *testing.T) {
		store := newTestStore(t)
		b := envtest.NewBroker()
		rec, _ := enrollAgent(t, store, "linux-server")
		c := startController(t, store, b)
		replies := captureMessages(t, b, envproto.Topic(rec.ID, envproto.LeafReq))
		msg := renewMessage(t, rec.ID, envproto.OpCertRenew)
		msg.Payload = []byte(`{`)
		c.onUp(t.Context(), msg)
		assert.Equal(t, "usage", replyOf(t, &(*replies)[0]).Error.Code)
	})
	t.Run("answers usage for a request over the size limit", func(t *testing.T) {
		store := newTestStore(t)
		b := envtest.NewBroker()
		rec, _ := enrollAgent(t, store, "linux-server")
		c := startController(t, store, b)
		replies := captureMessages(t, b, envproto.Topic(rec.ID, envproto.LeafReq))
		msg := renewMessage(t, rec.ID, envproto.OpCertRenew)
		msg.Payload = []byte(strings.Repeat(" ", envproto.MaxRequestBytes+1))
		c.onUp(t.Context(), msg)
		assert.Equal(t, "usage", replyOf(t, &(*replies)[0]).Error.Code)
	})
	t.Run("answers usage for malformed cert.renew arguments", func(t *testing.T) {
		store := newTestStore(t)
		b := envtest.NewBroker()
		rec, _ := enrollAgent(t, store, "linux-server")
		c := startController(t, store, b)
		replies := captureMessages(t, b, envproto.Topic(rec.ID, envproto.LeafReq))
		msg := renewMessage(t, rec.ID, envproto.OpCertRenew)
		msg.Payload = []byte(`{"op":"cert.renew","args":"x"}`)
		c.onUp(t.Context(), msg)
		assert.Equal(t, "usage", replyOf(t, &(*replies)[0]).Error.Code)
	})
	t.Run("answers enrollment_failed for an environment it doesn't know", func(t *testing.T) {
		store := newTestStore(t)
		b := envtest.NewBroker()
		enrollAgent(t, store, "linux-server")
		c := startController(t, store, b)
		const unknown = "env-ffffffff"
		replies := captureMessages(t, b, envproto.Topic(unknown, envproto.LeafReq))
		c.onUp(t.Context(), renewMessage(t, unknown, envproto.OpCertRenew))
		assert.Equal(t, "enrollment_failed", replyOf(t, &(*replies)[0]).Error.Code)
	})
}

func TestController_onRes(t *testing.T) {
	t.Run("logs an acknowledged ca.update", func(t *testing.T) {
		logs := &syncBuffer{}
		c := newController(t, newTestStore(t), envtest.NewBroker(), slog.New(slog.NewJSONHandler(logs, nil)), testSettings())
		c.onRes(t.Context(), &envproto.Message{
			Topic:   envproto.Topic("env-0a1b2c3d", envproto.LeafRes, "0123456789abcdef"),
			Payload: []byte(`{"ok":true,"result":{"fingerprints":[]},"error":null}`),
		})
		assert.Contains(t, logs.String(), "ca.update acknowledged")
	})
	t.Run("logs a refused ca.update with its code", func(t *testing.T) {
		logs := &syncBuffer{}
		c := newController(t, newTestStore(t), envtest.NewBroker(), slog.New(slog.NewJSONHandler(logs, nil)), testSettings())
		c.onRes(t.Context(), &envproto.Message{
			Topic:   envproto.Topic("env-0a1b2c3d", envproto.LeafRes, "0123456789abcdef"),
			Payload: []byte(`{"ok":false,"result":null,"error":{"code":"usage","message":"bad","details":null}}`),
		})
		assert.Contains(t, logs.String(), "ca.update refused")
		assert.Contains(t, logs.String(), "usage")
	})
	t.Run("logs a reply it can't read", func(t *testing.T) {
		logs := &syncBuffer{}
		c := newController(t, newTestStore(t), envtest.NewBroker(), slog.New(slog.NewJSONHandler(logs, nil)), testSettings())
		c.onRes(t.Context(), &envproto.Message{
			Topic:   envproto.Topic("env-0a1b2c3d", envproto.LeafRes, "0123456789abcdef"),
			Payload: []byte(`[`),
		})
		assert.Contains(t, logs.String(), "reply refused")
	})
}
