package mqttbroker_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/cache"
	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/cli"
	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/envagent"
	"github.com/bobcob7/workspaces/internal/environment"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/git"
	"github.com/bobcob7/workspaces/internal/mqttbroker"
	"github.com/bobcob7/workspaces/internal/mqttclient"
	"github.com/bobcob7/workspaces/internal/repo"
	"github.com/bobcob7/workspaces/internal/review/record"
	"github.com/bobcob7/workspaces/internal/review/store"
	"github.com/bobcob7/workspaces/internal/workspace"
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
func dial(t *testing.T, e *enrolled, tlsConfig *tls.Config, addr string) (*mqttclient.Client, error) {
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

func (emptySnapshots) Agents(context.Context) ([]envproto.SnapshotAgent, error) {
	return []envproto.SnapshotAgent{}, nil
}

func (emptySnapshots) Loops(context.Context) ([]envproto.SnapshotLoop, error) {
	return []envproto.SnapshotLoop{}, nil
}

type noClaude struct{}

func (noClaude) Probe(context.Context) (string, *string) {
	return envproto.ClaudeMissing, nil
}

// runAgent runs the real agent over the paho client until the test ends.
func runAgent(t *testing.T, e *enrolled, mutate func(*envagent.Options)) {
	t.Helper()
	opts := envagent.DefaultOptions("test", 30)
	opts.WillDelay = time.Second
	opts.BackoffMin = 50 * time.Millisecond
	opts.BackoffMax = 200 * time.Millisecond
	if mutate != nil {
		mutate(&opts)
	}
	agent := envagent.NewAgent(discardLog(), e.files, mqttclient.New(discardLog()), emptySnapshots{}, noClaude{}, envagent.NoOperations{}, rand.Reader, opts)
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
	dialer := tls.Dialer{Config: &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true}}
	raw, err := dialer.DialContext(t.Context(), "tcp", addr)
	require.NoError(t, err)
	defer raw.Close()
	conn, ok := raw.(*tls.Conn)
	require.True(t, ok)
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
	ctx    context.Context //nolint:containedctx // The proxy's accept loop runs for the test's lifetime.
	ln     net.Listener
	mu     sync.Mutex
	conns  []net.Conn
	target string
}

func newKillableProxy(t *testing.T, target string) *killableProxy {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	p := &killableProxy{ctx: t.Context(), ln: ln, target: target}
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
		out, err := (&net.Dialer{}).DialContext(p.ctx, "tcp", p.target)
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
		_, err := dial(t, &e, bare, s.brokerAddr)
		require.Error(t, err)
		other := newTestCA(t, "other ca")
		foreign := other.issue(t, e.identity.ID, x509.ExtKeyUsageClientAuth)
		withForeign := e.identity.TLSConfig()
		withForeign.Certificates = []tls.Certificate{foreign}
		_, err = dial(t, &e, withForeign, s.brokerAddr)
		require.Error(t, err)
		assert.Empty(t, s.broker.Clients())
	})
}

func TestConformance_agent(t *testing.T) {
	t.Run("an enrolled agent connects, is online, then its snapshot arrives", func(t *testing.T) {
		s := newStack(t)
		e := s.enroll(t, "linux")
		runAgent(t, &e, nil)
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
		client, err := dial(t, &e, e.identity.TLSConfig(), s.brokerAddr)
		require.NoError(t, err)
		_ = client.Publish(t.Context(), envproto.Message{Topic: envproto.Topic(other.identity.ID, envproto.LeafStatus), Payload: envproto.OfflinePayload(), QoS: 1, Retain: true})
		waitClosed(t, client.Done())
		s.waitStatus(t, "mac", "offline")
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
		require.Error(t, err)
		assert.Empty(t, s.broker.Clients())
	})
	t.Run("a killed connection turns the environment offline after the will delay", func(t *testing.T) {
		s := newStack(t)
		e := s.enroll(t, "linux")
		proxy := newKillableProxy(t, s.brokerAddr)
		client, err := dial(t, &e, e.identity.TLSConfig(), proxy.Addr())
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
		client, err := dial(t, &e, e.identity.TLSConfig(), s.brokerAddr)
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
		client, err := dial(t, &e, e.identity.TLSConfig(), s.brokerAddr)
		require.NoError(t, err)
		_, _, err = s.store.Remove(t.Context(), "linux")
		require.NoError(t, err)
		require.NoError(t, s.ctrl.Reload(t.Context()))
		waitClosed(t, client.Done())
		_, err = dial(t, &e, e.identity.TLSConfig(), s.brokerAddr)
		require.Error(t, err)
	})
}

func TestConformance_caRotation(t *testing.T) {
	t.Run("a rotation keeps the agent connected; after a disconnect it reconnects to the new server certificate", func(t *testing.T) {
		s := newStack(t)
		e := s.enroll(t, "linux")
		runAgent(t, &e, nil)
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
		client, err := dial(t, &e, tlsConfig, s.brokerAddr)
		require.NoError(t, err)
		_, err = s.store.FinishCA(t.Context(), true)
		require.NoError(t, err)
		require.NoError(t, s.ctrl.Reload(t.Context()))
		waitClosed(t, client.Done())
		roots, _ = serverRoots(t, s.brokerAddr)
		tlsConfig.RootCAs = roots
		_, err = dial(t, &e, tlsConfig, s.brokerAddr)
		require.Error(t, err)
	})
}

func TestConformance_renewal(t *testing.T) {
	t.Run("cert.renew round-trips over MQTT 5 with its Response Topic and Correlation Data", func(t *testing.T) {
		s := newStack(t)
		e := s.enroll(t, "linux")
		before := e.identity.Cert.SerialNumber
		runAgent(t, &e, func(o *envagent.Options) {
			o.RenewBeforeDays = 3650
			o.RenewCheck = 50 * time.Millisecond
		})
		require.Eventually(t, func() bool {
			id, err := e.files.Load()
			return err == nil && id.Cert.SerialNumber.Cmp(before) != 0
		}, eventually, 20*time.Millisecond, "the certificate was never renewed")
	})
}

// TestMain lets the test binary stand in for ws: a terminal's shell child is
// the agent's own executable run with --config-dir (TerminalCommand).
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "--config-dir" {
		configs := config.NewLoader(os.Getenv, os.UserConfigDir)
		os.Exit(cli.New("test", os.Stdin, os.Stdout, os.Stderr, configs, os.Environ()).Run(context.Background(), os.Args[1:])) //nolint:forbidigo // A test binary run as ws exits with ws's code.
	}
	os.Exit(m.Run()) //nolint:forbidigo // TestMain exits with the tests' code.
}

// fakeClaudeScript answers what the agent asks of claude: its version, its
// session list, and attach, which is cat.
const fakeClaudeScript = `#!/bin/sh
case "$1" in
--version) echo "2.1.303 (Claude Code)" ;;
agents) printf '[{"id":"ab12cd34","sessionId":"s-1","name":"x","cwd":"/","kind":"background","state":"working","status":"idle","pid":%s,"startedAt":1700000000000}]' "$$" ;;
attach) exec cat ;;
esac
`

// cliAgent is a real ws agent run in this process, with its own CONFIG_DIR.
type cliAgent struct {
	dir     string
	attach  string // the workspace id of a Claude agent the agent knows
	stopped chan struct{}
}

// startCLIAgent enrolls and runs `ws env agent` as the environment "linux". A
// non-empty via is the address the agent dials instead of the broker's.
func startCLIAgent(t *testing.T, s *stack, via string) *cliAgent {
	t.Helper()
	return startCLIAgentWith(t, s, via, "")
}

// startCLIAgentWith is startCLIAgent for a machine whose config.toml holds
// configTOML before the agent runs.
func startCLIAgentWith(t *testing.T, s *stack, via, configTOML string) *cliAgent {
	t.Helper()
	_, code, _, err := s.store.Add(t.Context(), "linux", s.enrollAddr)
	require.NoError(t, err)
	dir := t.TempDir()
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "claude"), []byte(fakeClaudeScript), 0o700))
	path := bin + ":" + os.Getenv("PATH")
	t.Setenv("PATH", path)
	environ := []string{"PATH=" + path, "HOME=" + t.TempDir(), "SHELL=/bin/sh", "LANG=C"}
	configs := config.NewLoader(os.Getenv, os.UserConfigDir)
	var out, errOut bytes.Buffer
	enroll := cli.New("test", strings.NewReader(""), &out, &errOut, configs, environ)
	require.Equal(t, 0, enroll.Run(t.Context(), []string{"--config-dir", dir, "env", "agent", "enroll", code}), "%s", errOut.String())
	if via != "" {
		file := filepath.Join(dir, "environment", "agent.toml")
		raw, readErr := os.ReadFile(file)
		require.NoError(t, readErr)
		replaced := strings.Replace(string(raw), s.brokerAddr, via, 1)
		require.NotEqual(t, string(raw), replaced, "the broker address isn't in the identity file")
		require.NoError(t, os.WriteFile(file, []byte(replaced), 0o600))
	}
	if configTOML != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte(configTOML), 0o600))
	}
	a := &cliAgent{dir: dir, stopped: make(chan struct{})}
	a.attach = makeClaudeWorkspace(t, dir, environ)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() {
		cancel()
		<-a.stopped
	})
	go func() {
		defer close(a.stopped)
		var stderr bytes.Buffer
		run := cli.New("test", strings.NewReader(""), io.Discard, &stderr, configs, environ)
		run.Run(ctx, []string{"--config-dir", dir, "env", "agent", "run"})
	}()
	s.waitStatus(t, "linux", "online")
	return a
}

// makeClaudeWorkspace makes a workspace in dir with a Claude session record
// for the session the fake claude lists, and returns its id.
func makeClaudeWorkspace(t *testing.T, dir string, environ []string) string {
	t.Helper()
	paths, cfg, err := config.NewLoader(os.Getenv, os.UserConfigDir).Load(dir)
	require.NoError(t, err)
	log := discardLog()
	gitRunner := git.NewRunner(log, "git", environ)
	claudeClient := claude.New(log, "claude", environ)
	store := workspace.New(log, gitRunner, cache.New(log, gitRunner, paths.Cache, cfg.SpecDefaults()), claudeClient, paths.Workspaces, paths.State)
	ws, lock, err := store.Create(t.Context(), workspace.CreateOptions{Name: "attachme"})
	require.NoError(t, err)
	require.NoError(t, store.RecordSession(ws.ID, workspace.SessionClaude, "ab12cd34"))
	lock.Unlock()
	return ws.ID
}

func shellOpen() envproto.TerminalOpenArgs {
	return envproto.TerminalOpenArgs{Kind: envproto.TermShell, Specs: []string{}, Cols: 80, Rows: 24}
}

// readUntil reads term's output until it holds want, and returns what it
// read; the terminal ending first fails the test.
func readUntil(t *testing.T, term *environment.Terminal, want string, within time.Duration) string {
	t.Helper()
	got, _ := readMarking(t, term, want, "", within)
	return got
}

// readMarking is readUntil that also reports whether mark was ever seen in
// the output, however much of it has gone by since.
func readMarking(t *testing.T, term *environment.Terminal, want, mark string, within time.Duration) (got string, marked bool) {
	t.Helper()
	var buf []byte
	timeout := time.After(within)
	for {
		select {
		case b, ok := <-term.Output():
			if !ok {
				require.FailNow(t, "the terminal ended", "reason %q, read %q", term.Exit().Reason, tail(buf))
			}
			buf = append(buf, b...)
			marked = marked || (mark != "" && bytes.Contains(buf, []byte(mark)))
			if bytes.Contains(buf, []byte(want)) {
				return string(buf), marked
			}
			if len(buf) > 64<<10 {
				buf = buf[len(buf)-4<<10:]
			}
		case <-timeout:
			require.FailNow(t, "never read "+want, "read %q", tail(buf))
		}
	}
}

// awaitExit drains term's output and fails unless it ends within d.
func awaitExit(t *testing.T, term *environment.Terminal, d time.Duration) {
	t.Helper()
	timeout := time.After(d)
	for {
		select {
		case _, ok := <-term.Output():
			if !ok {
				return
			}
		case <-timeout:
			require.FailNow(t, "the terminal stayed open")
		}
	}
}

func tail(b []byte) []byte {
	return b[max(0, len(b)-300):]
}

// childProcesses counts this process's children, which is every pty child.
func childProcesses() int {
	entries, _ := filepath.Glob("/proc/[0-9]*/stat")
	n := 0
	for _, e := range entries {
		raw, err := os.ReadFile(e)
		if err != nil {
			continue
		}
		fields := strings.Fields(string(raw[bytes.LastIndexByte(raw, ')')+1:]))
		if len(fields) > 1 && fields[1] == strconv.Itoa(os.Getpid()) {
			n++
		}
	}
	return n
}

func TestConformance_terminals(t *testing.T) {
	goroutines := runtime.NumGoroutine()
	t.Run("a shell terminal echoes input", func(t *testing.T) {
		s := newStack(t)
		startCLIAgent(t, s, "")
		term, res, err := s.ctrl.OpenTerminal(t.Context(), "linux", shellOpen())
		require.NoError(t, err)
		assert.Nil(t, res.Workspace)
		require.NoError(t, term.Input(t.Context(), []byte("echo $((20+22))\n")))
		readUntil(t, term, "42\r\n", eventually)
		require.NoError(t, term.Close(t.Context()))
	})
	t.Run("an attach terminal runs the command the agent chose", func(t *testing.T) {
		s := newStack(t)
		a := startCLIAgent(t, s, "")
		args := envproto.TerminalOpenArgs{Kind: envproto.TermAttach, Workspace: &a.attach, Specs: []string{}, Cols: 80, Rows: 24}
		term, res, err := s.ctrl.OpenTerminal(t.Context(), "linux", args)
		require.NoError(t, err)
		require.NotNil(t, res.Session)
		assert.Equal(t, "ab12cd34", *res.Session)
		require.NoError(t, term.Input(t.Context(), []byte("hello cat\n")))
		readUntil(t, term, "hello cat\r\nhello cat", eventually)
		require.NoError(t, term.Close(t.Context()))
	})
	t.Run("a slow reader sees the dropped marker, not a hang", func(t *testing.T) {
		s := newStack(t)
		startCLIAgent(t, s, "")
		term, _, err := s.ctrl.OpenTerminal(t.Context(), "linux", shellOpen())
		require.NoError(t, err)
		require.NoError(t, term.Input(t.Context(), []byte("yes | head -c 4000000; echo done-$((1+1))\n")))
		time.Sleep(2 * time.Second)
		_, marked := readMarking(t, term, "done-2\r\n", "bytes of output dropped", eventually)
		assert.True(t, marked, "no dropped marker")
		require.NoError(t, term.Close(t.Context()))
	})
	t.Run("killing the agent's connection ends the terminal with disconnected within 15 s", func(t *testing.T) {
		s := newStack(t)
		proxy := newKillableProxy(t, s.brokerAddr)
		startCLIAgent(t, s, proxy.Addr())
		term, _, err := s.ctrl.OpenTerminal(t.Context(), "linux", shellOpen())
		require.NoError(t, err)
		require.NoError(t, proxy.ln.Close())
		proxy.Kill()
		awaitExit(t, term, 15*time.Second)
		assert.Equal(t, "disconnected", term.Exit().Reason)
	})
	t.Run("a connection that comes back inside the will delay still ends the terminal", func(t *testing.T) {
		s := newStack(t)
		proxy := newKillableProxy(t, s.brokerAddr)
		startCLIAgent(t, s, proxy.Addr())
		term, _, err := s.ctrl.OpenTerminal(t.Context(), "linux", shellOpen())
		require.NoError(t, err)
		proxy.Kill()
		awaitExit(t, term, 15*time.Second)
		assert.Equal(t, "disconnected", term.Exit().Reason)
	})
	t.Run("another environment's agent can't publish on this terminal's out topic", func(t *testing.T) {
		s := newStack(t)
		startCLIAgent(t, s, "")
		other := s.enroll(t, "mac")
		term, res, err := s.ctrl.OpenTerminal(t.Context(), "linux", shellOpen())
		require.NoError(t, err)
		linux, err := s.store.Get("linux")
		require.NoError(t, err)
		client, err := dial(t, &other, other.identity.TLSConfig(), s.brokerAddr)
		require.NoError(t, err)
		frame, err := envproto.MarshalFrame(envproto.Frame{Kind: envproto.FrameData, Seq: 1, Body: []byte("forged")})
		require.NoError(t, err)
		_ = client.Publish(t.Context(), envproto.Message{Topic: envproto.Topic(linux.ID, envproto.LeafStream, res.SID, "out"), Payload: frame})
		waitClosed(t, client.Done())
		require.NoError(t, term.Close(t.Context()))
		for b := range term.Output() {
			assert.NotContains(t, string(b), "forged")
		}
	})
	t.Run("a ninth terminal is terminal_limit", func(t *testing.T) {
		s := newStack(t)
		startCLIAgent(t, s, "")
		for range envproto.MaxTerminals {
			term, _, err := s.ctrl.OpenTerminal(t.Context(), "linux", shellOpen())
			require.NoError(t, err)
			t.Cleanup(func() { _ = term.Close(context.WithoutCancel(t.Context())) })
		}
		_, _, err := s.ctrl.OpenTerminal(t.Context(), "linux", shellOpen())
		re, ok := errors.AsType[*envproto.ReplyError](err)
		require.True(t, ok, "%v", err)
		assert.Equal(t, "terminal_limit", re.Code)
	})
	assert.Eventually(t, func() bool { return childProcesses() == 0 }, eventually, 50*time.Millisecond, "pty children were left behind")
	assert.Eventually(t, func() bool { return runtime.NumGoroutine() <= goroutines+5 }, eventually, 50*time.Millisecond, "goroutines were left behind: %d, was %d", runtime.NumGoroutine(), goroutines)
}

// callOp sends op with args (a value, marshalled as is) to the environment
// "linux" through the controller, as the controller's call endpoint does.
func callOp(t *testing.T, s *stack, op string, args any) envproto.Reply {
	t.Helper()
	raw, err := json.Marshal(args)
	require.NoError(t, err)
	spec, ok := envproto.LookupOp(op)
	require.True(t, ok, op)
	id := make([]byte, 8)
	_, err = rand.Read(id)
	require.NoError(t, err)
	reply, err := s.ctrl.Call(t.Context(), "linux", environment.CallRequest{
		Op: op, Args: raw, RequestID: hex.EncodeToString(id), Deadline: time.Now().Add(spec.Timeout).UTC(),
	})
	require.NoError(t, err, op)
	return reply
}

// requireRefused fails unless reply is a refusal with code.
func requireRefused(t *testing.T, reply envproto.Reply, code string) {
	t.Helper()
	require.False(t, reply.OK, "the agent ran it")
	require.NotNil(t, reply.Error)
	assert.Equal(t, code, reply.Error.Code, reply.Error.Message)
}

const (
	reviewID     = "review-buff-bear-42-3f2a9c1-d8efb24f"
	reviewCommit = "3f2a9c1e4b5d6f7a8b9c0d1e2f3a4b5c6d7e8f90"
)

// seedReview stores a finished review called id, with comments submitted for
// its security profile, in the agent's CONFIG_DIR.
func seedReview(t *testing.T, a *cliAgent, id string, comments []string) {
	t.Helper()
	paths := config.NewPaths(a.dir)
	reviews := store.New(paths.Reviews)
	rec := record.Record{
		Repo: "github.com/bobcob7/buff-bear", Ref: reviewCommit, StartedAt: time.Now().UTC(),
		Profiles: []record.Profile{{Name: "security", Model: "opus"}},
	}
	require.NoError(t, reviews.SaveRun(id, &rec))
	repoID, err := repo.ParseID(rec.Repo)
	require.NoError(t, err)
	doc := `{"version":"1","verdict":"comment","profile":"security","summary":"s","comments":[` + strings.Join(comments, ",") + `]}`
	_, err = reviews.Submit(t.Context(), repoID, reviewCommit, "security", []byte(doc))
	require.NoError(t, err)
}

// reviewComment is comment number n with body.
func reviewComment(n int, body string) string {
	return fmt.Sprintf(`{"id":"c-%d","priority":5,"category":"style","body":%q,"anchors":[{"file":"client.go","line":1}],"suggestions":[]}`, n, body)
}

// addLoop adds a loop called name through loop.add and requires it to work.
func addLoop(t *testing.T, s *stack, name string) {
	t.Helper()
	reply := callOp(t, s, envproto.OpLoopAdd, map[string]any{"name": name, "every": "1h", "prompt": "Do it"})
	require.True(t, reply.OK, "%+v", reply.Error)
}

func TestConformance_reviewsLoops(t *testing.T) {
	t.Run("review.start to a protocol 2 agent is environment_outdated", func(t *testing.T) {
		s := newStack(t)
		old := s.enroll(t, "old")
		hello := envproto.Hello{
			Status: envproto.StatusOnline, At: time.Now().UTC(), WSVersion: "0.30.0", OS: "linux", Arch: "amd64",
			Claude: envproto.ClaudeMissing, CertSerial: "1", CertNotAfter: time.Now().Add(time.Hour).UTC(),
			CAFingerprints: []string{}, Protocol: 2,
		}
		payload, err := json.Marshal(hello)
		require.NoError(t, err)
		client, err := dial(t, &old, old.identity.TLSConfig(), s.brokerAddr)
		require.NoError(t, err)
		require.NoError(t, client.Publish(t.Context(), envproto.Message{Topic: envproto.Topic(old.identity.ID, envproto.LeafStatus), Payload: payload, QoS: 1, Retain: true}))
		s.waitStatus(t, "old", "online")
		args, err := json.Marshal(&envproto.ReviewStartArgs{Repo: "o/n", PR: new(1)})
		require.NoError(t, err)
		_, err = s.ctrl.Call(t.Context(), "old", environment.CallRequest{Op: envproto.OpReviewStart, Args: args, RequestID: "0123456789abcdef", Deadline: time.Now().Add(time.Minute)})
		outdated, ok := errors.AsType[*environment.OutdatedError](err)
		require.True(t, ok, "%v", err)
		assert.Equal(t, 3, outdated.Need)
		assert.Equal(t, 2, outdated.Protocol)
	})
	t.Run("a 300-comment report arrives whole through pages", func(t *testing.T) {
		s := newStack(t)
		a := startCLIAgent(t, s, "")
		comments := make([]string, 300)
		for i := range comments {
			comments[i] = reviewComment(i, strings.Repeat("x", 3000))
		}
		seedReview(t, a, reviewID, comments)
		seen, offset := map[string]bool{}, 0
		for pages := 0; ; pages++ {
			require.Less(t, pages, 30)
			reply := callOp(t, s, envproto.OpReviewReport, &envproto.ReviewReportArgs{Review: reviewID, Section: "comments", Offset: offset})
			require.True(t, reply.OK, "%+v", reply.Error)
			assert.LessOrEqual(t, len(reply.Result), envproto.MaxPageBytes)
			var page struct {
				Comments []struct {
					ID string `json:"id"`
				} `json:"comments"`
				Next  *int `json:"next_offset"`
				Total int  `json:"total"`
			}
			require.NoError(t, json.Unmarshal(reply.Result, &page))
			assert.Equal(t, 300, page.Total)
			for _, c := range page.Comments {
				seen[c.ID] = true
			}
			if page.Next == nil {
				break
			}
			offset = *page.Next
		}
		assert.Len(t, seen, 300)
	})
	t.Run("a ghp token in a comment never appears in any MQTT payload", func(t *testing.T) {
		s := newStack(t)
		a := startCLIAgent(t, s, "")
		token := "ghp_" + strings.Repeat("Q", 36)
		var mu sync.Mutex
		var payloads [][]byte
		unsubscribe, err := s.broker.Subscribe(t.Context(), "#", func(msg envproto.Message) {
			mu.Lock()
			defer mu.Unlock()
			payloads = append(payloads, append([]byte{}, msg.Payload...))
		})
		require.NoError(t, err)
		defer unsubscribe()
		seedReview(t, a, reviewID, []string{reviewComment(0, "leaked "+token+" here")})
		reply := callOp(t, s, envproto.OpReviewReport, &envproto.ReviewReportArgs{Review: reviewID, Section: "comments", Offset: 0})
		require.True(t, reply.OK, "%+v", reply.Error)
		assert.Contains(t, string(reply.Result), "[redacted]")
		mu.Lock()
		defer mu.Unlock()
		require.NotEmpty(t, payloads)
		reports := 0
		for _, p := range payloads {
			assert.NotContains(t, string(p), token)
			assert.NotContains(t, string(p), "ghp_QQ")
			if bytes.Contains(p, []byte("[redacted]")) {
				reports++
			}
		}
		assert.Positive(t, reports, "the report never crossed the broker")
	})
	t.Run("loop.rm sent raw without confirm is refused by the agent", func(t *testing.T) {
		s := newStack(t)
		startCLIAgent(t, s, "")
		addLoop(t, s, "deps")
		requireRefused(t, callOp(t, s, envproto.OpLoopRm, map[string]any{"name": "deps", "confirm": false}), "usage")
		requireRefused(t, callOp(t, s, envproto.OpLoopRm, map[string]any{"name": "deps"}), "usage")
		reply := callOp(t, s, envproto.OpLoopShow, map[string]any{"name": "deps"})
		require.True(t, reply.OK, "the loop is still there")
		reply = callOp(t, s, envproto.OpLoopRm, map[string]any{"name": "deps", "confirm": true})
		require.True(t, reply.OK, "%+v", reply.Error)
		requireRefused(t, callOp(t, s, envproto.OpLoopShow, map[string]any{"name": "deps"}), "loop_not_found")
	})
	t.Run("loop.memory_set of a redacted value is refused", func(t *testing.T) {
		s := newStack(t)
		startCLIAgent(t, s, "")
		addLoop(t, s, "deps")
		requireRefused(t, callOp(t, s, envproto.OpLoopMemorySet, map[string]any{"name": "deps", "key": "token", "value": "[redacted]"}), "usage")
		requireRefused(t, callOp(t, s, envproto.OpLoopMemoryGet, map[string]any{"name": "deps", "key": "token"}), "memory_not_found")
	})
	t.Run("a script is refused when the remote turned scripts off", func(t *testing.T) {
		s := newStack(t)
		startCLIAgentWith(t, s, "", "[environments]\nagent_loop_scripts = false\n")
		requireRefused(t, callOp(t, s, envproto.OpLoopAdd, map[string]any{"name": "gated", "every": "1h", "prompt": "Do it", "script": "true", "confirm": true}), "usage")
		requireRefused(t, callOp(t, s, envproto.OpLoopShow, map[string]any{"name": "gated"}), "loop_not_found")
		addLoop(t, s, "plain")
	})
	t.Run("the 13th review.start in an hour is operation_limited", func(t *testing.T) {
		s := newStack(t)
		startCLIAgent(t, s, "")
		args := &envproto.ReviewStartArgs{Repo: "o/n", PR: new(1)}
		for range 12 {
			reply := callOp(t, s, envproto.OpReviewStart, args)
			require.False(t, reply.OK)
			require.NotEqual(t, envproto.CodeOperationLimited, reply.Error.Code)
		}
		reply := callOp(t, s, envproto.OpReviewStart, args)
		requireRefused(t, reply, envproto.CodeOperationLimited)
		assert.Contains(t, reply.Error.Details, "retry_after_seconds")
	})
	t.Run("the controller's snapshot lists the remote's loops without prompts", func(t *testing.T) {
		s := newStack(t)
		startCLIAgent(t, s, "")
		reply := callOp(t, s, envproto.OpLoopAdd, map[string]any{"name": "deps", "title": "Deps", "every": "1h", "prompt": "SECRET-PROMPT-TEXT", "vars": map[string]string{"K": "SECRET-VAR-VALUE"}})
		require.True(t, reply.OK, "%+v", reply.Error)
		require.Eventually(t, func() bool {
			_, snap, err := s.store.Snapshot("linux")
			return err == nil && len(snap.Loops) == 1
		}, eventually, 20*time.Millisecond, "the snapshot has no loops")
		raw, err := s.store.ReadSnapshot("linux")
		require.NoError(t, err)
		assert.Contains(t, string(raw), `"deps"`)
		assert.NotContains(t, string(raw), "SECRET-PROMPT-TEXT")
		assert.NotContains(t, string(raw), "SECRET-VAR-VALUE")
	})
	t.Run("a review id with .. is refused", func(t *testing.T) {
		s := newStack(t)
		startCLIAgent(t, s, "")
		for _, id := range []string{"..", "../x", "a/b", "-x"} {
			requireRefused(t, callOp(t, s, envproto.OpReviewStatus, map[string]any{"review": id}), "usage")
			requireRefused(t, callOp(t, s, envproto.OpReviewReport, map[string]any{"review": id, "section": "comments", "offset": 0}), "usage")
		}
	})
	t.Run("a loop name with / is refused", func(t *testing.T) {
		s := newStack(t)
		a := startCLIAgent(t, s, "")
		for _, name := range []string{"../x", "a/b", "A", ".hidden"} {
			requireRefused(t, callOp(t, s, envproto.OpLoopShow, map[string]any{"name": name}), "name_invalid")
			requireRefused(t, callOp(t, s, envproto.OpLoopAdd, map[string]any{"name": name, "every": "1h", "prompt": "x"}), "name_invalid")
		}
		written, err := filepath.Glob(filepath.Join(a.dir, "loops", "*.toml"))
		require.NoError(t, err)
		assert.Empty(t, written, "a loop was written")
	})
	t.Run("bypassPermissions is refused for review.start", func(t *testing.T) {
		s := newStack(t)
		startCLIAgent(t, s, "")
		requireRefused(t, callOp(t, s, envproto.OpReviewStart, map[string]any{"repo": "o/n", "pr": 1, "permission_mode": "bypassPermissions"}), "usage")
		requireRefused(t, callOp(t, s, envproto.OpReviewStart, map[string]any{"repo": "o/n", "pr": 1, "permission_mode": "bypassPermissions", "confirm": true}), "usage")
	})
}
