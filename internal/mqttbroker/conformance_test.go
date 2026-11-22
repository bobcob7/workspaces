package mqttbroker_test

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/envagent"
	"github.com/bobcob7/workspaces/internal/environment"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/mqttbroker"
	"github.com/bobcob7/workspaces/internal/mqttclient"
)

const eventually = 15 * time.Second

func discardLog() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// stack is the real controller, store and broker, on loopback.
type stack struct {
	store      *environment.Store
	ctrl       *environment.Controller
	broker     *mqttbroker.Broker
	brokerAddr string
	enrollAddr string
}

func newStack(t *testing.T) *stack {
	t.Helper()
	dir := t.TempDir()
	store := environment.NewStore(filepath.Join(dir, "environments"), filepath.Join(dir, "state"), time.Now, rand.Reader)
	_, err := store.EnsureCA(t.Context())
	require.NoError(t, err)
	_, _, _, err = store.Add(t.Context(), "bootstrap", "127.0.0.1:1")
	require.NoError(t, err)
	broker := mqttbroker.New(discardLog())
	settings := environment.Settings{Listen: "127.0.0.1:0", EnrollListen: "127.0.0.1:0", PublicName: "127.0.0.1", RenewBeforeDays: 30}
	ctrl := environment.NewController(discardLog(), store, broker, settings, time.Now)
	t.Cleanup(func() { assert.NoError(t, ctrl.Close()) })
	require.NoError(t, ctrl.Start(t.Context()))
	status := ctrl.Status()
	require.True(t, status.Listening)
	return &stack{store: store, ctrl: ctrl, broker: broker, brokerAddr: status.Listen, enrollAddr: status.EnrollListen}
}

// enrolled is one enrolled machine: its files and identity.
type enrolled struct {
	files    *envagent.Files
	identity envagent.Identity
}

// enroll adds an environment called name and enrolls a machine for it over
// the real enrollment listener.
func (s *stack) enroll(t *testing.T, name string) enrolled {
	t.Helper()
	_, code, _, err := s.store.Add(t.Context(), name, s.enrollAddr)
	require.NoError(t, err)
	files := envagent.NewFiles(filepath.Join(t.TempDir(), "environment"))
	id, err := envagent.NewEnroller(discardLog(), files, rand.Reader).Enroll(t.Context(), code)
	require.NoError(t, err)
	return enrolled{files: files, identity: id}
}

// dial connects a bare client as e, with the offline will and a 1 s Will Delay.
func (s *stack) dial(t *testing.T, e *enrolled, tlsConfig *tls.Config, addr string) (*mqttclient.Client, error) {
	t.Helper()
	client := mqttclient.New(discardLog())
	t.Cleanup(func() { assert.NoError(t, client.Close()) })
	err := client.Connect(t.Context(), envproto.ConnectOptions{
		Address:   addr,
		TLS:       tlsConfig,
		ClientID:  e.identity.ID,
		KeepAlive: 30 * time.Second,
		Will:      &envproto.Message{Topic: envproto.Topic(e.identity.ID, envproto.LeafStatus), Payload: envproto.OfflinePayload(), QoS: 1, Retain: true},
		WillDelay: time.Second,
	})
	return client, err
}

// presence is the controller's status for the environment called name.
func (s *stack) presence(name string) string {
	for _, p := range s.ctrl.Status().Environments {
		if p.Name == name {
			return p.Status
		}
	}
	return ""
}

func (s *stack) waitStatus(t *testing.T, name, want string) {
	t.Helper()
	require.Eventually(t, func() bool { return s.presence(name) == want }, eventually, 20*time.Millisecond, "%s never became %s", name, want)
}

// emptySnapshots and noClaude stand in for the machine the agent runs on.
type emptySnapshots struct{}

func (emptySnapshots) Workspaces(context.Context) (json.RawMessage, error) {
	return json.RawMessage("[]"), nil
}

type noClaude struct{}

func (noClaude) Probe(context.Context) (string, *string) {
	return envproto.ClaudeMissing, nil
}

// runAgent runs the real agent over the paho client until the test ends.
func (s *stack) runAgent(t *testing.T, e *enrolled, mutate func(*envagent.Options)) {
	t.Helper()
	opts := envagent.DefaultOptions("test", 30)
	opts.WillDelay = time.Second
	opts.BackoffMin = 50 * time.Millisecond
	opts.BackoffMax = 200 * time.Millisecond
	if mutate != nil {
		mutate(&opts)
	}
	agent := envagent.NewAgent(discardLog(), e.files, mqttclient.New(discardLog()), emptySnapshots{}, noClaude{}, rand.Reader, opts)
	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	wg.Go(func() { assert.NoError(t, agent.Run(ctx)) })
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})
}

// serverRoots is the certificates the broker presents above its leaf.
func serverRoots(t *testing.T, addr string) (roots *x509.CertPool, leafIssuer string) {
	t.Helper()
	conn, err := tls.Dial("tcp", addr, &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true}) //nolint:gosec // The test reads the chain to learn the roots.
	require.NoError(t, err)
	defer conn.Close()
	chain := conn.ConnectionState().PeerCertificates
	require.GreaterOrEqual(t, len(chain), 2)
	roots = x509.NewCertPool()
	for _, cert := range chain[1:] {
		roots.AddCert(cert)
	}
	return roots, envproto.Fingerprint(chain[1])
}

// killableProxy forwards TCP to target and can reset every connection.
type killableProxy struct {
	ln     net.Listener
	mu     sync.Mutex
	conns  []net.Conn
	target string
}

func newKillableProxy(t *testing.T, target string) *killableProxy {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	p := &killableProxy{ln: ln, target: target}
	t.Cleanup(func() { _ = ln.Close(); p.Kill() })
	go p.serve()
	return p
}

func (p *killableProxy) Addr() string { return p.ln.Addr().String() }

func (p *killableProxy) serve() {
	for {
		in, err := p.ln.Accept()
		if err != nil {
			return
		}
		out, err := net.Dial("tcp", p.target)
		if err != nil {
			_ = in.Close()
			continue
		}
		p.mu.Lock()
		p.conns = append(p.conns, in, out)
		p.mu.Unlock()
		go func() { _, _ = io.Copy(out, in) }()
		go func() { _, _ = io.Copy(in, out) }()
	}
}

// Kill resets every connection without a DISCONNECT.
func (p *killableProxy) Kill() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		if tcp, ok := c.(*net.TCPConn); ok {
			_ = tcp.SetLinger(0)
		}
		_ = c.Close()
	}
	p.conns = nil
}

func TestConformance_certificates(t *testing.T) {
	t.Run("a client without a certificate, and one from another CA, can't connect", func(t *testing.T) {
		s := newStack(t)
		e := s.enroll(t, "linux")
		bare := e.identity.TLSConfig()
		bare.Certificates = nil
		_, err := s.dial(t, &e, bare, s.brokerAddr)
		assert.Error(t, err)
		other := newTestCA(t, "other ca")
		foreign := other.issue(t, e.identity.ID, x509.ExtKeyUsageClientAuth)
		withForeign := e.identity.TLSConfig()
		withForeign.Certificates = []tls.Certificate{foreign}
		_, err = s.dial(t, &e, withForeign, s.brokerAddr)
		assert.Error(t, err)
		assert.Empty(t, s.broker.Clients())
	})
}

func TestConformance_agent(t *testing.T) {
	t.Run("an enrolled agent connects, is online, then its snapshot arrives", func(t *testing.T) {
		s := newStack(t)
		e := s.enroll(t, "linux")
		s.runAgent(t, &e, nil)
		s.waitStatus(t, "linux", "online")
		_, err := s.store.ReadSnapshot("linux")
		require.Eventually(t, func() bool { _, err = s.store.ReadSnapshot("linux"); return err == nil }, eventually, 20*time.Millisecond)
		peers := s.broker.Clients()
		require.Len(t, peers, 1)
		assert.Equal(t, e.identity.ID, peers[0].ClientID)
	})
	t.Run("an agent publishing to another environment's topic is disconnected", func(t *testing.T) {
		s := newStack(t)
		e := s.enroll(t, "linux")
		other := s.enroll(t, "mac")
		client, err := s.dial(t, &e, e.identity.TLSConfig(), s.brokerAddr)
		require.NoError(t, err)
		_ = client.Publish(t.Context(), envproto.Message{Topic: envproto.Topic(other.identity.ID, envproto.LeafStatus), Payload: envproto.OfflinePayload(), QoS: 1, Retain: true})
		waitClosed(t, client.Done())
		fn, got := receiver()
		_, err = s.broker.Subscribe(t.Context(), envproto.Topic(other.identity.ID, envproto.LeafStatus), fn)
		require.NoError(t, err)
		select {
		case msg := <-got:
			require.FailNow(t, "the other environment's status was written", string(msg.Payload))
		case <-time.After(300 * time.Millisecond):
		}
	})
	t.Run("an agent can't set a will on another environment's status", func(t *testing.T) {
		s := newStack(t)
		e := s.enroll(t, "linux")
		other := s.enroll(t, "mac")
		client := mqttclient.New(discardLog())
		err := client.Connect(t.Context(), envproto.ConnectOptions{
			Address:  s.brokerAddr,
			TLS:      e.identity.TLSConfig(),
			ClientID: e.identity.ID,
			Will:     &envproto.Message{Topic: envproto.Topic(other.identity.ID, envproto.LeafStatus), Payload: envproto.OfflinePayload(), QoS: 1},
		})
		assert.Error(t, err)
		assert.Empty(t, s.broker.Clients())
	})
	t.Run("a killed connection turns the environment offline after the will delay", func(t *testing.T) {
		s := newStack(t)
		e := s.enroll(t, "linux")
		proxy := newKillableProxy(t, s.brokerAddr)
		client, err := s.dial(t, &e, e.identity.TLSConfig(), proxy.Addr())
		require.NoError(t, err)
		hello, err := json.Marshal(envproto.Hello{Status: envproto.StatusOnline, At: time.Now().UTC(), CAFingerprints: e.identity.Fingerprints()})
		require.NoError(t, err)
		require.NoError(t, client.Publish(t.Context(), envproto.Message{Topic: envproto.Topic(e.identity.ID, envproto.LeafStatus), Payload: hello, QoS: 1, Retain: true}))
		s.waitStatus(t, "linux", "online")
		proxy.Kill()
		waitClosed(t, client.Done())
		time.Sleep(300 * time.Millisecond)
		assert.Equal(t, "online", s.presence("linux"), "the will must wait for its delay")
		s.waitStatus(t, "linux", "offline")
	})
	t.Run("a clean close sends no will", func(t *testing.T) {
		s := newStack(t)
		e := s.enroll(t, "linux")
		client, err := s.dial(t, &e, e.identity.TLSConfig(), s.brokerAddr)
		require.NoError(t, err)
		hello, err := json.Marshal(envproto.Hello{Status: envproto.StatusOnline, At: time.Now().UTC(), CAFingerprints: e.identity.Fingerprints()})
		require.NoError(t, err)
		require.NoError(t, client.Publish(t.Context(), envproto.Message{Topic: envproto.Topic(e.identity.ID, envproto.LeafStatus), Payload: hello, QoS: 1, Retain: true}))
		s.waitStatus(t, "linux", "online")
		require.NoError(t, client.Close())
		time.Sleep(2500 * time.Millisecond)
		assert.Equal(t, "online", s.presence("linux"))
	})
}

func TestConformance_revocation(t *testing.T) {
	t.Run("a revoked serial is disconnected and then refused", func(t *testing.T) {
		s := newStack(t)
		e := s.enroll(t, "linux")
		client, err := s.dial(t, &e, e.identity.TLSConfig(), s.brokerAddr)
		require.NoError(t, err)
		_, _, err = s.store.Remove(t.Context(), "linux")
		require.NoError(t, err)
		require.NoError(t, s.ctrl.Reload(t.Context()))
		waitClosed(t, client.Done())
		_, err = s.dial(t, &e, e.identity.TLSConfig(), s.brokerAddr)
		assert.Error(t, err)
	})
}

func TestConformance_caRotation(t *testing.T) {
	t.Run("a rotation keeps the agent connected; after a disconnect it reconnects to the new server certificate", func(t *testing.T) {
		s := newStack(t)
		e := s.enroll(t, "linux")
		s.runAgent(t, &e, nil)
		s.waitStatus(t, "linux", "online")
		rotation, err := s.store.RotateCA(t.Context())
		require.NoError(t, err)
		require.NoError(t, s.ctrl.Reload(t.Context()))
		assert.Len(t, s.broker.Clients(), 1)
		require.Eventually(t, func() bool {
			id, err := e.files.Load()
			return err == nil && len(id.Roots) == 2
		}, eventually, 20*time.Millisecond, "the agent never received ca.update")
		serial := s.broker.Clients()[0].Serial
		assert.Equal(t, 1, s.broker.Disconnect(serial))
		require.Eventually(t, func() bool { return len(s.broker.Clients()) == 1 }, eventually, 20*time.Millisecond, "the agent never reconnected")
		_, issuer := serverRoots(t, s.brokerAddr)
		assert.Equal(t, rotation.Current.Fingerprint, issuer)
	})
	t.Run("an old-CA agent connects until the rotation is finished, then is refused", func(t *testing.T) {
		s := newStack(t)
		e := s.enroll(t, "linux")
		_, err := s.store.RotateCA(t.Context())
		require.NoError(t, err)
		require.NoError(t, s.ctrl.Reload(t.Context()))
		roots, _ := serverRoots(t, s.brokerAddr)
		tlsConfig := e.identity.TLSConfig()
		tlsConfig.RootCAs = roots
		client, err := s.dial(t, &e, tlsConfig, s.brokerAddr)
		require.NoError(t, err)
		_, err = s.store.FinishCA(t.Context(), true)
		require.NoError(t, err)
		require.NoError(t, s.ctrl.Reload(t.Context()))
		waitClosed(t, client.Done())
		roots, _ = serverRoots(t, s.brokerAddr)
		tlsConfig.RootCAs = roots
		_, err = s.dial(t, &e, tlsConfig, s.brokerAddr)
		assert.Error(t, err)
	})
}

func TestConformance_renewal(t *testing.T) {
	t.Run("cert.renew round-trips over MQTT 5 with its Response Topic and Correlation Data", func(t *testing.T) {
		s := newStack(t)
		e := s.enroll(t, "linux")
		before := e.identity.Cert.SerialNumber
		s.runAgent(t, &e, func(o *envagent.Options) {
			o.RenewBeforeDays = 3650
			o.RenewCheck = 50 * time.Millisecond
		})
		require.Eventually(t, func() bool {
			id, err := e.files.Load()
			return err == nil && id.Cert.SerialNumber.Cmp(before) != 0
		}, eventually, 20*time.Millisecond, "the certificate was never renewed")
	})
}
