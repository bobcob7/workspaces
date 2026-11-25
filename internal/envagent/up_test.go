package envagent

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// fakeLink is the agent's connection for the scope tests: it records the
// agent's request subscription, and calls onUp for every publish on env/<id>/up
// in place of a controller.
type fakeLink struct {
	mu     sync.Mutex
	onReq  func(envproto.Message)
	onUp   func(envproto.Message)
	closed chan struct{}
}

func newFakeLink() *fakeLink {
	return &fakeLink{closed: make(chan struct{})}
}

func (*fakeLink) Connect(context.Context, envproto.ConnectOptions) error { return nil }

func (l *fakeLink) Publish(_ context.Context, msg envproto.Message) error { //nolint:gocritic // hugeParam: Publish has the mqttClient signature.
	if !strings.HasSuffix(msg.Topic, "/"+envproto.LeafUp) {
		return nil
	}
	l.mu.Lock()
	onUp := l.onUp
	l.mu.Unlock()
	if onUp != nil {
		onUp(msg)
	}
	return nil
}

func (l *fakeLink) Subscribe(_ context.Context, filter string, fn func(envproto.Message)) error {
	if strings.HasSuffix(filter, "/"+envproto.LeafReq) {
		l.mu.Lock()
		l.onReq = fn
		l.mu.Unlock()
	}
	return nil
}

func (l *fakeLink) Done() <-chan struct{} { return l.closed }

func (*fakeLink) Close() error { return nil }

// deliver hands msg to the agent's request subscription, as the broker would.
func (l *fakeLink) deliver(msg envproto.Message) { //nolint:gocritic // hugeParam: the subscription takes the message by value.
	l.mu.Lock()
	fn := l.onReq
	l.mu.Unlock()
	fn(msg)
}

// answerUps makes the controller answer every scope request with its
// arguments as the result, so a test can tell the replies apart.
func answerUps(link *fakeLink) {
	link.onUp = func(msg envproto.Message) {
		var req envproto.Request
		if err := json.Unmarshal(msg.Payload, &req); err != nil {
			return
		}
		reply, err := envproto.OKReply(req.Args)
		if err != nil {
			return
		}
		go link.deliver(envproto.Message{
			Topic:           msg.ResponseTopic,
			Payload:         reply,
			CorrelationData: msg.CorrelationData,
		})
	}
}

// newUpAgent is an agent over a fresh fixture's identity and a fake link.
// Its workspaces root is empty, and renewal is kept off the link.
func newUpAgent(t *testing.T, opts Options) (*Agent, *fakeLink, Identity) { //nolint:gocritic // hugeParam: Options is passed by value, as NewAgent takes it.
	t.Helper()
	fx := startAgentFixture(t)
	opts.WorkspacesRoot = t.TempDir()
	opts.RenewCheck = time.Hour
	link := newFakeLink()
	agent := NewAgent(discardLog(), fx.files, link, testSnapshots("[]"), testProbe(), NoOperations{}, rand.Reader, opts)
	return agent, link, fx.id
}

// connectAgent connects agent as id and serves it until the returned cancel runs.
func connectAgent(t *testing.T, agent *Agent, id *Identity) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	require.NoError(t, agent.connect(ctx, id))
	go agent.serve(ctx, id)
	t.Cleanup(cancel)
	return cancel
}

// postScope posts body to the agent socket's route and decodes the reply.
func postScope(t *testing.T, agent *Agent, route, body string) (int, envproto.Reply) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/scope/"+route, strings.NewReader(body))
	agent.SocketHandler().ServeHTTP(rec, req)
	var reply envproto.Reply
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &reply))
	}
	return rec.Code, reply
}

func TestAgent_SocketHandler(t *testing.T) {
	t.Run("a null workspace is usage", func(t *testing.T) {
		agent, _, id := newUpAgent(t, testOptions())
		connectAgent(t, agent, &id)
		code, reply := postScope(t, agent, "envs", `{"workspace":null,"ancestors":[],"scope":[]}`)
		assert.Equal(t, http.StatusOK, code)
		assert.False(t, reply.OK)
		require.NotNil(t, reply.Error)
		assert.Equal(t, "usage", reply.Error.Code)
	})
	t.Run("an unknown workspace is workspace_not_found", func(t *testing.T) {
		agent, _, id := newUpAgent(t, testOptions())
		connectAgent(t, agent, &id)
		code, reply := postScope(t, agent, "envs", `{"workspace":"plan-9f8e7d6c","ancestors":[],"scope":[]}`)
		assert.Equal(t, http.StatusOK, code)
		require.NotNil(t, reply.Error)
		assert.Equal(t, "workspace_not_found", reply.Error.Code)
	})
	t.Run("an agent that isn't connected is environment_offline", func(t *testing.T) {
		agent, _, _ := newUpAgent(t, testOptions())
		require.NoError(t, os.MkdirAll(filepath.Join(agent.opts.WorkspacesRoot, "plan-9f8e7d6c"), 0o700))
		code, reply := postScope(t, agent, "envs", `{"workspace":"plan-9f8e7d6c","ancestors":[],"scope":[]}`)
		assert.Equal(t, http.StatusOK, code)
		require.NotNil(t, reply.Error)
		assert.Equal(t, "environment_offline", reply.Error.Code)
		assert.Equal(t, map[string]any{"reason": "agent_unreachable"}, reply.Error.Details)
	})
	t.Run("only the four scope routes are served", func(t *testing.T) {
		agent, _, id := newUpAgent(t, testOptions())
		connectAgent(t, agent, &id)
		for _, route := range []string{"other", "machine/record", "ca/update"} {
			code, _ := postScope(t, agent, route, `{}`)
			assert.Equal(t, http.StatusNotFound, code, route)
		}
	})
}

func TestAgent_Up(t *testing.T) {
	t.Run("the controller's reply comes back", func(t *testing.T) {
		agent, link, id := newUpAgent(t, testOptions())
		answerUps(link)
		connectAgent(t, agent, &id)
		reply, err := agent.Up(t.Context(), envproto.OpScopeEnvs, json.RawMessage(`{"workspace":"plan-9f8e7d6c"}`))
		require.NoError(t, err)
		assert.True(t, reply.OK)
		assert.JSONEq(t, `{"workspace":"plan-9f8e7d6c"}`, string(reply.Result))
	})
	t.Run("two concurrent calls each get their own reply", func(t *testing.T) {
		agent, link, id := newUpAgent(t, testOptions())
		answerUps(link)
		connectAgent(t, agent, &id)
		var wg sync.WaitGroup
		for _, n := range []string{"1", "2"} {
			wg.Go(func() {
				reply, err := agent.Up(t.Context(), envproto.OpScopeEnvs, json.RawMessage(`{"n":`+n+`}`))
				if err != nil {
					t.Errorf("Up: %v", err)
					return
				}
				assert.JSONEq(t, `{"n":`+n+`}`, string(reply.Result))
			})
		}
		wg.Wait()
	})
	t.Run("the wait is the op's timeout plus the grace", func(t *testing.T) {
		spec, ok := envproto.LookupOp(envproto.OpScopeEnvs)
		require.True(t, ok)
		wait := spec.Timeout + envproto.Grace
		waited := make(chan time.Duration, 4)
		opts := testOptions()
		opts.After = func(d time.Duration) <-chan time.Time {
			if d == wait {
				waited <- d
				ch := make(chan time.Time, 1)
				ch <- time.Time{}
				return ch
			}
			return time.After(d)
		}
		agent, _, id := newUpAgent(t, opts)
		connectAgent(t, agent, &id)
		_, err := agent.Up(t.Context(), envproto.OpScopeEnvs, json.RawMessage(`{}`))
		require.Error(t, err)
		select {
		case <-waited:
		default:
			t.Fatal("Up didn't wait for the op's timeout plus the grace")
		}
	})
	t.Run("a disconnect while waiting is errDisconnected", func(t *testing.T) {
		agent, link, id := newUpAgent(t, testOptions())
		published := make(chan struct{}, 1)
		link.onUp = func(envproto.Message) {
			published <- struct{}{}
		}
		cancel := connectAgent(t, agent, &id)
		errs := make(chan error, 1)
		go func() {
			_, err := agent.Up(t.Context(), envproto.OpScopeEnvs, json.RawMessage(`{}`))
			errs <- err
		}()
		select {
		case <-published:
		case <-time.After(5 * time.Second):
			t.Fatal("Up didn't publish its request")
		}
		cancel()
		select {
		case err := <-errs:
			require.ErrorIs(t, err, errDisconnected)
		case <-time.After(5 * time.Second):
			t.Fatal("Up still waits after the disconnect")
		}
	})
	t.Run("a non-scope op is refused before anything is published", func(t *testing.T) {
		agent, link, id := newUpAgent(t, testOptions())
		published := false
		link.onUp = func(envproto.Message) { published = true }
		connectAgent(t, agent, &id)
		_, err := agent.Up(t.Context(), envproto.OpWorkspaceList, nil)
		re, ok := errors.AsType[*envproto.ReplyError](err)
		require.True(t, ok, "got %v", err)
		assert.Equal(t, "usage", re.Code)
		assert.False(t, published)
	})
}
