package envagent

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/redact"
)

// testLoopTick is a LoopTick no other timer uses, so loopTimer can tell its timer.
const testLoopTick = 7 * time.Hour

// loopTimer makes the loop timer fire only when the returned channel is sent
// to, once per send; other timers are real. asked counts the loop timers set.
func loopTimer(opts *Options) (ticks chan<- time.Time, asked *atomic.Int32) {
	ch := make(chan time.Time)
	asked = &atomic.Int32{}
	opts.LoopTick = testLoopTick
	opts.After = func(d time.Duration) <-chan time.Time {
		if d == testLoopTick {
			asked.Add(1)
			return ch
		}
		return time.After(d)
	}
	return ch, asked
}

// fire delivers one loop timer, failing when the ticker doesn't take it.
func fire(t *testing.T, ticks chan<- time.Time) {
	t.Helper()
	select {
	case ticks <- time.Now():
	case <-time.After(5 * time.Second):
		t.Fatal("the ticker never waited for its timer")
	}
}

// tickerMock is a loopTicker whose pass is fn.
func tickerMock(fn func(context.Context) (bool, error)) *loopTickerMock {
	return &loopTickerMock{TickFunc: fn}
}

// loopAgent is a started agent with ticker, over a client that stays connected.
func loopAgent(t *testing.T, ticker loopTicker, opts Options) (*Agent, *publishRecorder, *agentFixture) { //nolint:gocritic // hugeParam: Options is passed by value, as NewAgent takes it.
	t.Helper()
	fx := startAgentFixture(t)
	recorder := &publishRecorder{}
	client := connectedMock(func(_ context.Context, msg envproto.Message) error {
		recorder.add(&msg)
		return nil
	})
	agent := NewAgent(discardLog(), fx.files, client, testSnapshots("[]"), testProbe(), NoOperations{}, rand.Reader, opts)
	agent.SetLoopTicker(ticker)
	return agent, recorder, fx
}

func TestAgent_tickLoops(t *testing.T) {
	t.Parallel()
	t.Run("ticks every LoopTick while connected", func(t *testing.T) {
		t.Parallel()
		opts := testOptions()
		ticks, _ := loopTimer(&opts)
		ticker := tickerMock(func(context.Context) (bool, error) { return false, nil })
		agent, _, _ := loopAgent(t, ticker, opts)
		runAgent(t, agent)
		for want := 1; want <= 3; want++ {
			fire(t, ticks)
			require.Eventually(t, func() bool { return len(ticker.TickCalls()) == want && !agent.ticking.Load() }, 5*time.Second, 5*time.Millisecond)
		}
	})
	t.Run("does not tick while offline", func(t *testing.T) {
		t.Parallel()
		opts := testOptions()
		_, asked := loopTimer(&opts)
		ticker := tickerMock(func(context.Context) (bool, error) { return false, nil })
		fx := startAgentFixture(t)
		client := &mqttClientMock{ConnectFunc: func(context.Context, envproto.ConnectOptions) error { return errors.New("no broker") }}
		agent := NewAgent(discardLog(), fx.files, client, testSnapshots("[]"), testProbe(), NoOperations{}, rand.Reader, opts)
		agent.SetLoopTicker(ticker)
		runAgent(t, agent)
		require.Eventually(t, func() bool { return len(client.ConnectCalls()) >= 3 }, 5*time.Second, 5*time.Millisecond)
		assert.Zero(t, asked.Load())
		assert.Empty(t, ticker.TickCalls())
	})
	t.Run("the first pass after a reconnect waits LoopTick", func(t *testing.T) {
		t.Parallel()
		opts := testOptions()
		_, asked := loopTimer(&opts)
		ticker := tickerMock(func(context.Context) (bool, error) { return false, nil })
		fx := startAgentFixture(t)
		lost := make(chan struct{})
		var doneCalls atomic.Int32
		client := connectedMock(func(context.Context, envproto.Message) error { return nil })
		client.DoneFunc = func() <-chan struct{} {
			if doneCalls.Add(1) == 1 {
				return lost
			}
			return make(chan struct{})
		}
		agent := NewAgent(discardLog(), fx.files, client, testSnapshots("[]"), testProbe(), NoOperations{}, rand.Reader, opts)
		agent.SetLoopTicker(ticker)
		runAgent(t, agent)
		require.Eventually(t, func() bool { return asked.Load() == 1 }, 5*time.Second, 5*time.Millisecond)
		close(lost)
		require.Eventually(t, func() bool { return asked.Load() == 2 }, 5*time.Second, 5*time.Millisecond, "the new connection waits for its own timer")
		assert.Empty(t, ticker.TickCalls())
	})
	t.Run("a pass still running skips the next timer", func(t *testing.T) {
		t.Parallel()
		opts := testOptions()
		ticks, _ := loopTimer(&opts)
		gate := make(chan struct{})
		ticker := tickerMock(func(ctx context.Context) (bool, error) {
			select {
			case <-gate:
			case <-ctx.Done():
			}
			return false, nil
		})
		agent, _, _ := loopAgent(t, ticker, opts)
		runAgent(t, agent)
		fire(t, ticks)
		require.Eventually(t, func() bool { return len(ticker.TickCalls()) == 1 }, 5*time.Second, 5*time.Millisecond)
		fire(t, ticks)
		fire(t, ticks)
		fire(t, ticks)
		assert.Len(t, ticker.TickCalls(), 1)
		close(gate)
		require.Eventually(t, func() bool { return !agent.ticking.Load() }, 5*time.Second, 5*time.Millisecond)
		fire(t, ticks)
		require.Eventually(t, func() bool { return len(ticker.TickCalls()) == 2 }, 5*time.Second, 5*time.Millisecond)
	})
	t.Run("a pass that changed something republishes the snapshot", func(t *testing.T) {
		t.Parallel()
		opts := testOptions()
		ticks, _ := loopTimer(&opts)
		ticker := tickerMock(func(context.Context) (bool, error) { return true, nil })
		agent, recorder, fx := loopAgent(t, ticker, opts)
		runAgent(t, agent)
		snapshot := envproto.Topic(fx.id.ID, envproto.LeafSnapshot)
		require.Eventually(t, func() bool { return len(recorder.on(snapshot)) == 1 }, 5*time.Second, 5*time.Millisecond)
		fire(t, ticks)
		require.Eventually(t, func() bool { return len(recorder.on(snapshot)) == 2 }, 5*time.Second, 5*time.Millisecond)
	})
	t.Run("a pass that changed nothing does not republish", func(t *testing.T) {
		t.Parallel()
		opts := testOptions()
		ticks, _ := loopTimer(&opts)
		ticker := tickerMock(func(context.Context) (bool, error) { return false, nil })
		agent, recorder, fx := loopAgent(t, ticker, opts)
		runAgent(t, agent)
		snapshot := envproto.Topic(fx.id.ID, envproto.LeafSnapshot)
		require.Eventually(t, func() bool { return len(recorder.on(snapshot)) == 1 }, 5*time.Second, 5*time.Millisecond)
		fire(t, ticks)
		require.Eventually(t, func() bool { return len(ticker.TickCalls()) == 1 }, 5*time.Second, 5*time.Millisecond)
		time.Sleep(50 * time.Millisecond)
		assert.Len(t, recorder.on(snapshot), 1)
	})
	t.Run("a tick error does not stop the ticker", func(t *testing.T) {
		t.Parallel()
		opts := testOptions()
		ticks, _ := loopTimer(&opts)
		var calls atomic.Int32
		ticker := tickerMock(func(context.Context) (bool, error) {
			if calls.Add(1) == 1 {
				return false, errors.New("the pass failed")
			}
			return false, nil
		})
		agent, _, _ := loopAgent(t, ticker, opts)
		runAgent(t, agent)
		fire(t, ticks)
		require.Eventually(t, func() bool { return len(ticker.TickCalls()) == 1 && !agent.ticking.Load() }, 5*time.Second, 5*time.Millisecond)
		fire(t, ticks)
		require.Eventually(t, func() bool { return len(ticker.TickCalls()) == 2 }, 5*time.Second, 5*time.Millisecond)
	})
	t.Run("LoopTick 0 never ticks", func(t *testing.T) {
		t.Parallel()
		opts := testOptions()
		ticker := tickerMock(func(context.Context) (bool, error) { return false, nil })
		agent, recorder, fx := loopAgent(t, ticker, opts)
		runAgent(t, agent)
		snapshot := envproto.Topic(fx.id.ID, envproto.LeafSnapshot)
		require.Eventually(t, func() bool { return len(recorder.on(snapshot)) == 1 }, 5*time.Second, 5*time.Millisecond)
		time.Sleep(50 * time.Millisecond)
		assert.Empty(t, ticker.TickCalls())
	})
	t.Run("a pass running when the connection ends is not cancelled", func(t *testing.T) {
		t.Parallel()
		opts := testOptions()
		ticks, _ := loopTimer(&opts)
		fx := startAgentFixture(t)
		lost := make(chan struct{})
		var doneCalls atomic.Int32
		client := connectedMock(func(context.Context, envproto.Message) error { return nil })
		client.DoneFunc = func() <-chan struct{} {
			if doneCalls.Add(1) == 1 {
				return lost
			}
			return make(chan struct{})
		}
		var cancelled atomic.Bool
		started := make(chan struct{})
		gate := make(chan struct{})
		ticker := tickerMock(func(ctx context.Context) (bool, error) {
			close(started)
			<-gate
			cancelled.Store(ctx.Err() != nil)
			return false, nil
		})
		agent := NewAgent(discardLog(), fx.files, client, testSnapshots("[]"), testProbe(), NoOperations{}, rand.Reader, opts)
		agent.SetLoopTicker(ticker)
		runAgent(t, agent)
		fire(t, ticks)
		<-started
		close(lost)
		require.Eventually(t, func() bool { return doneCalls.Load() >= 2 }, 5*time.Second, 5*time.Millisecond)
		close(gate)
		require.Eventually(t, func() bool { return !agent.ticking.Load() }, 5*time.Second, 5*time.Millisecond)
		assert.False(t, cancelled.Load())
	})
}

// publishedLoops waits for the n-th snapshot and returns it.
func publishedLoops(t *testing.T, recorder *publishRecorder, fx *agentFixture, n int) envproto.Snapshot {
	t.Helper()
	snapshot := envproto.Topic(fx.id.ID, envproto.LeafSnapshot)
	require.Eventually(t, func() bool { return len(recorder.on(snapshot)) >= n }, 5*time.Second, 5*time.Millisecond)
	var got envproto.Snapshot
	require.NoError(t, json.Unmarshal(recorder.on(snapshot)[n-1].Payload, &got))
	return got
}

func TestAgent_refreshSnapshotLoops(t *testing.T) {
	t.Parallel()
	t.Run("the snapshot carries loops", func(t *testing.T) {
		t.Parallel()
		agent, recorder, fx := loopAgent(t, nil, testOptions())
		agent.snapshots = loopSource(func() ([]envproto.SnapshotLoop, error) {
			return []envproto.SnapshotLoop{{Name: "nightly", Title: "Nightly", Every: "1h", Runner: "claude"}}, nil
		})
		runAgent(t, agent)
		got := publishedLoops(t, recorder, fx, 1)
		require.Len(t, got.Loops, 1)
		assert.Equal(t, "nightly", got.Loops[0].Name)
	})
	t.Run("a loops read error publishes []", func(t *testing.T) {
		t.Parallel()
		agent, recorder, fx := loopAgent(t, nil, testOptions())
		agent.snapshots = loopSource(func() ([]envproto.SnapshotLoop, error) { return nil, errors.New("no loops dir") })
		runAgent(t, agent)
		publishedLoops(t, recorder, fx, 1)
		snapshot := envproto.Topic(fx.id.ID, envproto.LeafSnapshot)
		assert.Contains(t, string(recorder.on(snapshot)[0].Payload), `"loops":[]`)
	})
	t.Run("a loop change alone publishes", func(t *testing.T) {
		t.Parallel()
		agent, recorder, fx := loopAgent(t, nil, testOptions())
		var paused atomic.Bool
		agent.snapshots = loopSource(func() ([]envproto.SnapshotLoop, error) {
			return []envproto.SnapshotLoop{{Name: "nightly", Paused: paused.Load()}}, nil
		})
		runAgent(t, agent)
		publishedLoops(t, recorder, fx, 1)
		paused.Store(true)
		got := publishedLoops(t, recorder, fx, 2)
		assert.True(t, got.Loops[0].Paused)
	})
	t.Run("the loops are redacted with the agent's redactor", func(t *testing.T) {
		t.Parallel()
		opts := testOptions()
		opts.Redactor = redact.New("hunter2-secret-value")
		agent, recorder, fx := loopAgent(t, nil, opts)
		agent.snapshots = loopSource(func() ([]envproto.SnapshotLoop, error) {
			return []envproto.SnapshotLoop{{Name: "nightly", Title: "uses hunter2-secret-value now"}}, nil
		})
		runAgent(t, agent)
		got := publishedLoops(t, recorder, fx, 1)
		require.Len(t, got.Loops, 1)
		assert.NotContains(t, got.Loops[0].Title, "hunter2-secret-value")
	})
}

// loopSource is a snapshot source with no workspaces or agents and the loops fn returns.
func loopSource(fn func() ([]envproto.SnapshotLoop, error)) *snapshotSourceMock {
	source := testSnapshots("[]")
	source.LoopsFunc = func(context.Context) ([]envproto.SnapshotLoop, error) { return fn() }
	return source
}
