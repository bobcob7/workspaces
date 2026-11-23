package envagent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/environment"
	"github.com/bobcob7/workspaces/internal/environment/envtest"
	"github.com/bobcob7/workspaces/internal/envproto"
)

// agentFixture is a started controller with one enrolled agent. The agent's
// files are in files.
type agentFixture struct {
	store  *environment.Store
	ctrl   *environment.Controller
	broker *envtest.Broker
	files  *Files
	id     Identity
}

// startAgentFixture starts a controller over an in-memory broker and enrolls
// fixtureName, so the agent's files hold a real identity.
func startAgentFixture(t *testing.T) *agentFixture {
	t.Helper()
	ctx := t.Context()
	root := t.TempDir()
	store := environment.NewStore(filepath.Join(root, "environments"), filepath.Join(root, "state"), time.Now, rand.Reader)
	broker := envtest.NewBroker()
	ctrl := environment.NewController(discardLog(), store, broker, environment.Settings{
		Listen:          "127.0.0.1:0",
		EnrollListen:    "127.0.0.1:0",
		PublicName:      "127.0.0.1",
		RenewBeforeDays: 30,
	}, time.Now)
	t.Cleanup(func() { assert.NoError(t, ctrl.Close()) })
	_, _, _, err := store.Add(ctx, fixtureName, "127.0.0.1:1")
	require.NoError(t, err)
	require.NoError(t, ctrl.Start(ctx))
	_, code, _, err := store.Rotate(ctx, fixtureName, ctrl.Status().EnrollListen)
	require.NoError(t, err)
	files := NewFiles(filepath.Join(root, "agent"))
	id, err := NewEnroller(discardLog(), files, rand.Reader).Enroll(ctx, code)
	require.NoError(t, err)
	return &agentFixture{store: store, ctrl: ctrl, broker: broker, files: files, id: id}
}

// hello is the controller's last online hello for the fixture's environment, or nil.
func (fx *agentFixture) hello() *envproto.Hello {
	for _, p := range fx.ctrl.Status().Environments {
		if p.Name == fixtureName && p.Status == envproto.StatusOnline {
			return p.Hello
		}
	}
	return nil
}

// testOptions are short intervals for the tests, with a jitter of 1.
func testOptions() Options {
	return Options{
		Version:         "test",
		RenewBeforeDays: 30,
		SnapshotCheck:   10 * time.Millisecond,
		SnapshotEvery:   time.Hour,
		RenewCheck:      10 * time.Millisecond,
		KeepAlive:       time.Minute,
		RequestTimeout:  5 * time.Second,
		BackoffMin:      time.Millisecond,
		BackoffMax:      10 * time.Millisecond,
		Now:             time.Now,
		After:           time.After,
		Jitter:          func() float64 { return 1 },
	}
}

// testProbe reports Claude as present at version 1.0.0.
func testProbe() *claudeProbeMock {
	return &claudeProbeMock{ProbeFunc: func(context.Context) (string, *string) {
		version := "1.0.0"
		return envproto.ClaudePresent, &version
	}}
}

// testSnapshots reports workspaces as the workspaces array.
func testSnapshots(workspaces string) *snapshotSourceMock {
	return &snapshotSourceMock{
		WorkspacesFunc: func(context.Context) (json.RawMessage, error) {
			return json.RawMessage(workspaces), nil
		},
		AgentsFunc: func(context.Context) ([]envproto.SnapshotAgent, error) {
			return []envproto.SnapshotAgent{}, nil
		},
	}
}

// runningAgent is an agent whose Run is in a goroutine.
type runningAgent struct {
	cancel   context.CancelFunc
	finished chan struct{}
	err      error
}

// runAgent runs agent until the test ends.
func runAgent(t *testing.T, agent *Agent) *runningAgent {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	run := &runningAgent{cancel: cancel, finished: make(chan struct{})}
	go func() {
		run.err = agent.Run(ctx)
		close(run.finished)
	}()
	t.Cleanup(func() { _ = run.stop() })
	return run
}

// stop cancels the agent and waits for Run to return its error.
func (r *runningAgent) stop() error {
	r.cancel()
	<-r.finished
	return r.err
}

// newFixtureAgent is an agent over the fixture's files with a real broker client.
func newFixtureAgent(fx *agentFixture, opts Options, snapshots string) *Agent { //nolint:gocritic // hugeParam: Options is passed by value, as NewAgent takes it.
	return NewAgent(discardLog(), fx.files, fx.broker.NewClient(), testSnapshots(snapshots), testProbe(), NoOperations{}, rand.Reader, opts)
}

// manualRenewCheck is a RenewCheck no other timer uses, so manualRenewal can tell its timer.
const manualRenewCheck = 3 * time.Hour

// manualRenewal makes the renewal timer fire only when the returned channel is
// sent to, once per send; other timers are real.
func manualRenewal(opts *Options) chan<- time.Time {
	ticks := make(chan time.Time, 1)
	opts.RenewCheck = manualRenewCheck
	opts.After = func(d time.Duration) <-chan time.Time {
		if d == manualRenewCheck {
			return ticks
		}
		return time.After(d)
	}
	return ticks
}

// publishRecorder is the publishes of a client mock, safe to read while the agent runs.
type publishRecorder struct {
	mu   sync.Mutex
	msgs []envproto.Message
}

func (r *publishRecorder) add(msg *envproto.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, *msg)
}

func (r *publishRecorder) on(topic string) []envproto.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []envproto.Message
	for _, msg := range r.msgs {
		if msg.Topic == topic {
			out = append(out, msg)
		}
	}
	return out
}

// connectedMock is a client mock whose connect, subscribe and close succeed and whose publish is publish.
func connectedMock(publish func(context.Context, envproto.Message) error) *mqttClientMock {
	return &mqttClientMock{
		ConnectFunc:   func(context.Context, envproto.ConnectOptions) error { return nil },
		SubscribeFunc: func(context.Context, string, func(envproto.Message)) error { return nil },
		PublishFunc:   publish,
		DoneFunc:      func() <-chan struct{} { return make(chan struct{}) },
		CloseFunc:     func() error { return nil },
	}
}

func TestBackoff(t *testing.T) {
	t.Parallel()
	t.Run("doubles from the minimum and caps at the maximum", func(t *testing.T) {
		t.Parallel()
		waits := make([]time.Duration, 0, 8)
		for attempt := range 8 {
			waits = append(waits, backoff(attempt, time.Second, time.Minute, 1))
		}
		assert.Equal(t, []time.Duration{
			time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
			16 * time.Second, 32 * time.Second, time.Minute, time.Minute,
		}, waits)
	})
	t.Run("jitter 0 gives half and jitter 1 gives the whole", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, 2*time.Second, backoff(2, time.Second, time.Minute, 0))
		assert.Equal(t, 4*time.Second, backoff(2, time.Second, time.Minute, 1))
	})
	t.Run("a large attempt doesn't overflow", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, time.Minute, backoff(1000, time.Second, time.Minute, 1))
	})
}

func TestAgent_Run(t *testing.T) {
	t.Parallel()
	t.Run("retries a failed connect with growing waits until it connects", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		attempts := 0
		client := &mqttClientMock{
			ConnectFunc: func(context.Context, envproto.ConnectOptions) error {
				attempts++
				if attempts <= 8 {
					return errors.New("refused")
				}
				cancel()
				return nil
			},
			SubscribeFunc: func(context.Context, string, func(envproto.Message)) error { return nil },
			PublishFunc:   func(context.Context, envproto.Message) error { return nil },
			DoneFunc:      func() <-chan struct{} { return make(chan struct{}) },
			CloseFunc:     func() error { return nil },
		}
		var waits []time.Duration
		opts := testOptions()
		opts.BackoffMax = 8 * time.Millisecond
		opts.SnapshotCheck = time.Hour
		opts.RenewCheck = time.Hour
		opts.After = func(d time.Duration) <-chan time.Time {
			waits = append(waits, d)
			return time.After(d)
		}
		agent := NewAgent(discardLog(), fx.files, client, testSnapshots("[]"), testProbe(), NoOperations{}, rand.Reader, opts)
		require.NoError(t, agent.Run(ctx))
		require.Len(t, client.ConnectCalls(), 9)
		require.GreaterOrEqual(t, len(waits), 8)
		assert.Equal(t, []time.Duration{
			time.Millisecond, 2 * time.Millisecond, 4 * time.Millisecond, 8 * time.Millisecond,
			8 * time.Millisecond, 8 * time.Millisecond, 8 * time.Millisecond, 8 * time.Millisecond,
		}, waits[:8])
	})
	t.Run("returns ErrCertificateExpired without connecting", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		opts := testOptions()
		opts.Now = func() time.Time { return fx.id.Cert.NotAfter.Add(time.Hour) }
		agent := newFixtureAgent(fx, opts, "[]")
		err := agent.Run(t.Context())
		require.ErrorIs(t, err, ErrCertificateExpired)
		require.ErrorContains(t, err, "ws env rotate "+fixtureName)
		assert.Empty(t, fx.broker.Clients())
	})
	t.Run("announces an enrolled agent with its hello and workspaces", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		runAgent(t, newFixtureAgent(fx, testOptions(), `[{"name":"alpha"}]`))
		require.Eventually(t, func() bool { return fx.hello() != nil }, 5*time.Second, 10*time.Millisecond)
		hello := fx.hello()
		assert.Equal(t, "test", hello.WSVersion)
		assert.Equal(t, runtime.GOOS, hello.OS)
		assert.Equal(t, envproto.ClaudePresent, hello.Claude)
		assert.Equal(t, fx.id.Fingerprints(), hello.CAFingerprints)
		assert.Equal(t, fx.id.Cert.SerialNumber.Text(16), hello.CertSerial)
		require.Eventually(t, func() bool {
			data, err := fx.store.ReadSnapshot(fixtureName)
			return err == nil && strings.Contains(string(data), `"name":"alpha"`)
		}, 5*time.Second, 10*time.Millisecond)
	})
	t.Run("keeps backing off after the environment is removed", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		run := runAgent(t, newFixtureAgent(fx, testOptions(), "[]"))
		require.Eventually(t, func() bool { return fx.hello() != nil }, 5*time.Second, 10*time.Millisecond)
		_, _, err := fx.store.Remove(t.Context(), fixtureName)
		require.NoError(t, err)
		require.NoError(t, fx.ctrl.Reload(t.Context()))
		require.Eventually(t, func() bool { return len(fx.broker.Clients()) == 0 }, 5*time.Second, 10*time.Millisecond)
		require.Never(t, func() bool {
			select {
			case <-run.finished:
				return true
			default:
				return false
			}
		}, 200*time.Millisecond, 10*time.Millisecond)
		assert.Empty(t, fx.broker.Clients())
	})
	t.Run("returns after ctx ends even when the offline status can't be published", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		status := envproto.Topic(fx.id.ID, envproto.LeafStatus)
		client := connectedMock(func(ctx context.Context, msg envproto.Message) error {
			if msg.Topic == status && bytes.Equal(msg.Payload, envproto.OfflinePayload()) {
				select {
				case <-ctx.Done():
				case <-t.Context().Done():
				}
				return ctx.Err()
			}
			return nil
		})
		opts := testOptions()
		opts.RequestTimeout = 50 * time.Millisecond
		agent := NewAgent(discardLog(), fx.files, client, testSnapshots("[]"), testProbe(), NoOperations{}, rand.Reader, opts)
		run := runAgent(t, agent)
		require.Eventually(t, func() bool { return len(client.ConnectCalls()) == 1 }, 5*time.Second, 10*time.Millisecond)
		run.cancel()
		select {
		case <-run.finished:
		case <-time.After(5 * time.Second):
			t.Fatal("Run didn't return while the offline status was blocked")
		}
		require.NoError(t, run.err)
		assert.Len(t, client.CloseCalls(), 1)
	})
	t.Run("publishes a null snapshot as an empty list", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		recorder := &publishRecorder{}
		client := connectedMock(func(_ context.Context, msg envproto.Message) error {
			recorder.add(&msg)
			return nil
		})
		agent := NewAgent(discardLog(), fx.files, client, testSnapshots("null"), testProbe(), NoOperations{}, rand.Reader, testOptions())
		runAgent(t, agent)
		snapshot := envproto.Topic(fx.id.ID, envproto.LeafSnapshot)
		require.Eventually(t, func() bool { return len(recorder.on(snapshot)) > 0 }, 5*time.Second, 10*time.Millisecond)
		payload := recorder.on(snapshot)[0].Payload
		var got envproto.Snapshot
		require.NoError(t, json.Unmarshal(payload, &got))
		assert.JSONEq(t, "[]", string(got.Workspaces))
		assert.Equal(t, envproto.Protocol, got.Protocol)
		assert.Contains(t, string(payload), `"agents":[]`)
	})
}

func TestAgent_refreshSnapshot(t *testing.T) {
	t.Parallel()
	t.Run("the snapshot carries the agents", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		recorder := &publishRecorder{}
		client := connectedMock(func(_ context.Context, msg envproto.Message) error {
			recorder.add(&msg)
			return nil
		})
		snapshots := testSnapshots("[]")
		snapshots.AgentsFunc = func(context.Context) ([]envproto.SnapshotAgent, error) {
			return []envproto.SnapshotAgent{{ID: "ab12cd34", SessionID: "s-1", Name: "fix", State: "working", Status: "busy", PID: new(42)}}, nil
		}
		agent := NewAgent(discardLog(), fx.files, client, snapshots, testProbe(), NoOperations{}, rand.Reader, testOptions())
		runAgent(t, agent)
		snapshot := envproto.Topic(fx.id.ID, envproto.LeafSnapshot)
		require.Eventually(t, func() bool { return len(recorder.on(snapshot)) > 0 }, 5*time.Second, 10*time.Millisecond)
		var got envproto.Snapshot
		require.NoError(t, json.Unmarshal(recorder.on(snapshot)[0].Payload, &got))
		require.Len(t, got.Agents, 1)
		assert.Equal(t, "ab12cd34", got.Agents[0].ID)
	})
	t.Run("a failing Agents still publishes the workspaces with agents []", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		recorder := &publishRecorder{}
		client := connectedMock(func(_ context.Context, msg envproto.Message) error {
			recorder.add(&msg)
			return nil
		})
		snapshots := testSnapshots(`[{"id":"api"}]`)
		snapshots.AgentsFunc = func(context.Context) ([]envproto.SnapshotAgent, error) {
			return nil, errors.New("claude is missing")
		}
		agent := NewAgent(discardLog(), fx.files, client, snapshots, testProbe(), NoOperations{}, rand.Reader, testOptions())
		runAgent(t, agent)
		snapshot := envproto.Topic(fx.id.ID, envproto.LeafSnapshot)
		require.Eventually(t, func() bool { return len(recorder.on(snapshot)) > 0 }, 5*time.Second, 10*time.Millisecond)
		payload := recorder.on(snapshot)[0].Payload
		var got envproto.Snapshot
		require.NoError(t, json.Unmarshal(payload, &got))
		assert.JSONEq(t, `[{"id":"api"}]`, string(got.Workspaces))
		assert.Contains(t, string(payload), `"agents":[]`)
	})
	t.Run("an oversized snapshot isn't published and is warned about once", func(t *testing.T) {
		t.Parallel()
		fx := startAgentFixture(t)
		oversized := `["` + strings.Repeat("x", envproto.MaxRequestBytes) + `"]`
		client := &mqttClientMock{PublishFunc: func(context.Context, envproto.Message) error { return nil }}
		var logs bytes.Buffer
		log := slog.New(slog.NewJSONHandler(&logs, nil))
		agent := NewAgent(log, fx.files, client, testSnapshots(oversized), testProbe(), NoOperations{}, rand.Reader, testOptions())
		id := fx.id
		agent.refreshSnapshot(t.Context(), &id, false)
		agent.refreshSnapshot(t.Context(), &id, false)
		assert.Empty(t, client.PublishCalls())
		assert.Equal(t, 1, strings.Count(logs.String(), "snapshot too large"))
		assert.Contains(t, logs.String(), `"bytes":`)
	})
}
