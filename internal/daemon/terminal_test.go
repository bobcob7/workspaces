package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// fakeTerminal is a TerminalStreamMock whose output the test controls, and
// the calls it recorded.
type fakeTerminal struct {
	*TerminalStreamMock
	out      chan []byte
	exit     envproto.TerminalExit
	mu       sync.Mutex
	inputs   [][]byte
	resizes  [][2]int
	ctxErrs  []error
	closed   chan struct{}
	closeCnt atomic.Int32
	// outClosedAtExit is whether Output was closed when Exit was read.
	exitEarly atomic.Bool
	outClosed atomic.Bool
	endOnce   sync.Once
}

func newFakeTerminal() *fakeTerminal {
	f := &fakeTerminal{out: make(chan []byte, 16), closed: make(chan struct{})}
	f.TerminalStreamMock = &TerminalStreamMock{
		OutputFunc: func() <-chan []byte { return f.out },
		ExitFunc: func() envproto.TerminalExit {
			if !f.outClosed.Load() {
				f.exitEarly.Store(true)
			}
			return f.exit
		},
		InputFunc: func(ctx context.Context, p []byte) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.ctxErrs = append(f.ctxErrs, ctx.Err())
			f.inputs = append(f.inputs, bytes.Clone(p))
			return nil
		},
		ResizeFunc: func(ctx context.Context, cols, rows int) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.ctxErrs = append(f.ctxErrs, ctx.Err())
			f.resizes = append(f.resizes, [2]int{cols, rows})
			return nil
		},
		CloseFunc: func(ctx context.Context) error {
			f.mu.Lock()
			f.ctxErrs = append(f.ctxErrs, ctx.Err())
			f.mu.Unlock()
			if f.closeCnt.Add(1) == 1 {
				close(f.closed)
			}
			f.endOutput(envproto.TerminalExit{Reason: "closed"})
			return nil
		},
	}
	return f
}

func (f *fakeTerminal) endOutput(exit envproto.TerminalExit) {
	f.endOnce.Do(func() {
		f.exit = exit
		f.outClosed.Store(true)
		close(f.out)
	})
}

func (f *fakeTerminal) inputBytes() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return bytes.Join(f.inputs, nil)
}

func (f *fakeTerminal) resizeCalls() [][2]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][2]int(nil), f.resizes...)
}

func (f *fakeTerminal) assertClosed(t *testing.T) {
	t.Helper()
	select {
	case <-f.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the terminal wasn't closed")
	}
}

func (f *fakeTerminal) assertContextsLive(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, err := range f.ctxErrs {
		assert.NoError(t, err, "a call got a cancelled context")
	}
}

type openRecord struct {
	mu   sync.Mutex
	name string
	args envproto.TerminalOpenArgs
	n    int
}

func (o *openRecord) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.n
}

// terminalHost is an environment host that opens term, or fails with err.
func terminalHost(rec *openRecord, term TerminalStream, err error) *EnvironmentHostMock {
	return &EnvironmentHostMock{
		StartFunc:    func(context.Context) error { return nil },
		CloseFunc:    func() error { return nil },
		WarningsFunc: func() []string { return []string{} },
		StatusFunc:   func() EnvironmentsStatus { return EnvironmentsStatus{Environments: []EnvironmentPresence{}} },
		CallFunc: func(context.Context, string, CallRequest) (CallReply, error) {
			return CallReply{}, &HostError{Err: ErrNotForwarded, Message: "terminal.open: operation not forwarded"}
		},
		OpenTerminalFunc: func(_ context.Context, name string, args envproto.TerminalOpenArgs) (TerminalStream, envproto.TerminalOpenResult, error) {
			rec.mu.Lock()
			rec.name, rec.args = name, args
			rec.n++
			rec.mu.Unlock()
			if err != nil {
				return nil, envproto.TerminalOpenResult{}, err
			}
			ws := "w1"
			return term, envproto.TerminalOpenResult{SID: "0123456789abcdef", Workspace: &ws}, nil
		},
	}
}

func terminalDaemon(t *testing.T, host EnvironmentHost, origin string) (Paths, <-chan error) {
	t.Helper()
	p := testPaths(t)
	done := startDaemon(t, p, daemonRun{origin: origin, sup: NopSupervisor{}, envs: host, opts: fastOptions()})
	return p, done
}

const shellArgs = `{"kind":"shell","workspace":null,"name":"s","specs":[],"offline":false,"cols":80,"rows":24}`

// upgradeRequest is a terminal.open request for body with headers.
func upgradeRequest(name, body string, headers map[string]string) string {
	var b strings.Builder
	b.WriteString("POST /v1/environments/" + name + "/terminal HTTP/1.1\r\nHost: ws-daemon\r\nContent-Type: application/json\r\n")
	for k, v := range headers {
		b.WriteString(k + ": " + v + "\r\n")
	}
	b.WriteString("Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" + body)
	return b.String()
}

var upgradeHeaders = map[string]string{"Connection": "Upgrade", "Upgrade": "ws-terminal.v1"}

// rawConn is a client connection to the daemon's socket.
type rawConn struct {
	net.Conn
	r *bufio.Reader
}

func dialRaw(t *testing.T, p Paths) *rawConn {
	t.Helper()
	conn, err := net.Dial("unix", p.Socket)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.SetDeadline(time.Now().Add(10*time.Second)))
	return &rawConn{Conn: conn, r: bufio.NewReader(conn)}
}

// send writes request and reads the response head.
func (c *rawConn) send(t *testing.T, request string) *http.Response {
	t.Helper()
	_, err := io.WriteString(c, request)
	require.NoError(t, err)
	resp, err := http.ReadResponse(c.r, nil)
	require.NoError(t, err)
	return resp
}

func (c *rawConn) writeFrame(t *testing.T, kind byte, body []byte) {
	t.Helper()
	header := make([]byte, 5)
	header[0] = kind
	binary.BigEndian.PutUint32(header[1:], uint32(len(body)))
	_, err := c.Write(append(header, body...))
	require.NoError(t, err)
}

func (c *rawConn) readFrame(t *testing.T) (byte, []byte) {
	t.Helper()
	header := make([]byte, 5)
	_, err := io.ReadFull(c.r, header)
	require.NoError(t, err)
	body := make([]byte, binary.BigEndian.Uint32(header[1:]))
	_, err = io.ReadFull(c.r, body)
	require.NoError(t, err)
	return header[0], body
}

// assertEnds fails unless the daemon closes the connection.
func (c *rawConn) assertEnds(t *testing.T) {
	t.Helper()
	_, err := io.Copy(io.Discard, c.r)
	assert.NoError(t, err)
}

func openTerminalRaw(t *testing.T, p Paths) *rawConn {
	t.Helper()
	c := dialRaw(t, p)
	resp := c.send(t, upgradeRequest("linux", shellArgs, upgradeHeaders))
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
	kind, _ := c.readFrame(t)
	require.Equal(t, byte('o'), kind)
	return c
}

func errorCode(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	var env errorEnvelope
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&env))
	return env.Error.Code
}

func TestServer_handleTerminal(t *testing.T) {
	t.Parallel()
	t.Run("upgrades and streams both ways", func(t *testing.T) {
		term := newFakeTerminal()
		rec := &openRecord{}
		p, _ := terminalDaemon(t, terminalHost(rec, term, nil), OriginManaged)
		c := dialRaw(t, p)
		resp := c.send(t, upgradeRequest("linux", shellArgs, upgradeHeaders))
		require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
		assert.Equal(t, "Upgrade", resp.Header.Get("Connection"))
		assert.Equal(t, "ws-terminal.v1", resp.Header.Get("Upgrade"))
		kind, body := c.readFrame(t)
		require.Equal(t, byte('o'), kind)
		var result envproto.TerminalOpenResult
		require.NoError(t, json.Unmarshal(body, &result))
		assert.Equal(t, "0123456789abcdef", result.SID)
		assert.Equal(t, "linux", rec.name)
		assert.Equal(t, "shell", rec.args.Kind)
		assert.Equal(t, 80, rec.args.Cols)
		term.out <- []byte("hello\x1b[0m")
		kind, body = c.readFrame(t)
		assert.Equal(t, byte('d'), kind)
		assert.Equal(t, "hello\x1b[0m", string(body))
		c.writeFrame(t, 'i', []byte("ls\r"))
		c.writeFrame(t, 'i', []byte("pwd\r"))
		c.writeFrame(t, 'r', envproto.ResizeBody(100, 30))
		require.Eventually(t, func() bool { return len(term.resizeCalls()) == 1 }, 5*time.Second, 5*time.Millisecond)
		assert.Equal(t, "ls\rpwd\r", string(term.inputBytes()))
		assert.Equal(t, [][2]int{{100, 30}}, term.resizeCalls())
		code := 7
		term.endOutput(envproto.TerminalExit{Code: &code, Reason: "exited"})
		kind, body = c.readFrame(t)
		require.Equal(t, byte('x'), kind)
		var exit envproto.TerminalExit
		require.NoError(t, json.Unmarshal(body, &exit))
		assert.Equal(t, "exited", exit.Reason)
		require.NotNil(t, exit.Code)
		assert.Equal(t, 7, *exit.Code)
		c.assertEnds(t)
		term.assertClosed(t)
		assert.False(t, term.exitEarly.Load(), "Exit was read before Output closed")
		term.assertContextsLive(t)
	})
	t.Run("a request without the upgrade header is usage and the host isn't called", func(t *testing.T) {
		for name, headers := range map[string]map[string]string{
			"no headers":         {},
			"no Upgrade":         {"Connection": "Upgrade"},
			"no Connection":      {"Upgrade": "ws-terminal.v1"},
			"another protocol":   {"Connection": "Upgrade", "Upgrade": "websocket"},
			"another connection": {"Connection": "keep-alive", "Upgrade": "ws-terminal.v1"},
		} {
			t.Run(name, func(t *testing.T) {
				rec := &openRecord{}
				p, _ := terminalDaemon(t, terminalHost(rec, newFakeTerminal(), nil), OriginManaged)
				resp := dialRaw(t, p).send(t, upgradeRequest("linux", shellArgs, headers))
				assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
				assert.Equal(t, "usage", errorCode(t, resp))
				assert.Zero(t, rec.count())
			})
		}
	})
	t.Run("a connection header with several tokens upgrades", func(t *testing.T) {
		rec := &openRecord{}
		p, _ := terminalDaemon(t, terminalHost(rec, newFakeTerminal(), nil), OriginManaged)
		resp := dialRaw(t, p).send(t, upgradeRequest("linux", shellArgs, map[string]string{"Connection": "keep-alive, Upgrade", "Upgrade": "ws-terminal.v1"}))
		assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
	})
	t.Run("a body that isn't valid, has an unknown member or is too large is usage and the host isn't called", func(t *testing.T) {
		for name, body := range map[string]string{
			"not JSON":       `{`,
			"unknown member": strings.Replace(shellArgs, `"rows":24`, `"rows":24,"extra":1`, 1),
			"too large":      `{"kind":"` + strings.Repeat("x", maxBodyBytes) + `"}`,
			"trailing data":  shellArgs + `{}`,
		} {
			t.Run(name, func(t *testing.T) {
				rec := &openRecord{}
				p, _ := terminalDaemon(t, terminalHost(rec, newFakeTerminal(), nil), OriginManaged)
				resp := dialRaw(t, p).send(t, upgradeRequest("linux", body, upgradeHeaders))
				assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
				assert.Equal(t, "usage", errorCode(t, resp))
				assert.Zero(t, rec.count())
			})
		}
	})
	t.Run("an offline environment answers environment_offline before upgrading", func(t *testing.T) {
		rec := &openRecord{}
		host := terminalHost(rec, nil, &HostError{Err: ErrEnvironmentOffline, Message: "environment linux is offline", Details: map[string]any{"environment": "linux"}})
		p, _ := terminalDaemon(t, host, OriginManaged)
		resp := dialRaw(t, p).send(t, upgradeRequest("linux", shellArgs, upgradeHeaders))
		assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
		assert.Equal(t, "environment_offline", errorCode(t, resp))
	})
	t.Run("an unknown or outdated environment is answered with the call's codes", func(t *testing.T) {
		for err, want := range map[error]string{
			&HostError{Err: ErrEnvironmentUnknown, Message: "no such environment"}:     "environment_unknown",
			&HostError{Err: ErrEnvironmentOutdated, Message: "the ws is too old"}:      "environment_outdated",
			&envproto.ReplyError{Code: "workspace_not_found", Message: "no workspace"}: "workspace_not_found",
			&envproto.ArgsError{Code: "spec_invalid", Message: "bad spec"}:             "spec_invalid",
		} {
			p, _ := terminalDaemon(t, terminalHost(&openRecord{}, nil, err), OriginManaged)
			resp := dialRaw(t, p).send(t, upgradeRequest("linux", shellArgs, upgradeHeaders))
			assert.NotEqual(t, http.StatusSwitchingProtocols, resp.StatusCode)
			assert.Equal(t, want, errorCode(t, resp))
		}
	})
	t.Run("closing the socket closes the terminal", func(t *testing.T) {
		term := newFakeTerminal()
		p, _ := terminalDaemon(t, terminalHost(&openRecord{}, term, nil), OriginManaged)
		c := openTerminalRaw(t, p)
		require.NoError(t, c.Close())
		term.assertClosed(t)
		term.assertContextsLive(t)
	})
	t.Run("a close frame closes the terminal and the socket", func(t *testing.T) {
		term := newFakeTerminal()
		p, _ := terminalDaemon(t, terminalHost(&openRecord{}, term, nil), OriginManaged)
		c := openTerminalRaw(t, p)
		c.writeFrame(t, 'c', nil)
		term.assertClosed(t)
		c.assertEnds(t)
	})
	t.Run("a frame the CLI mustn't send closes the terminal", func(t *testing.T) {
		huge := make([]byte, 5)
		huge[0] = 'i'
		binary.BigEndian.PutUint32(huge[1:], 0xffffffff)
		overLimit := make([]byte, 5)
		overLimit[0] = 'i'
		binary.BigEndian.PutUint32(overLimit[1:], 64<<10+1)
		closeWithBody := append([]byte{'c', 0, 0, 0, 1}, 'x')
		for name, raw := range map[string][]byte{
			"unknown kind":              {'z', 0, 0, 0, 0},
			"an output kind":            {'d', 0, 0, 0, 1, 'a'},
			"a length of four gigabyte": huge,
			"a body over the limit":     overLimit,
			"a short resize":            {'r', 0, 0, 0, 2, 0, 1},
			"a zero-sized resize":       append([]byte{'r', 0, 0, 0, 4}, 0, 0, 0, 0),
			"a close with a body":       closeWithBody,
		} {
			t.Run(name, func(t *testing.T) {
				term := newFakeTerminal()
				p, _ := terminalDaemon(t, terminalHost(&openRecord{}, term, nil), OriginManaged)
				c := openTerminalRaw(t, p)
				_, err := c.Write(raw)
				require.NoError(t, err)
				term.assertClosed(t)
				c.assertEnds(t)
				assert.Empty(t, term.inputBytes())
				assert.Empty(t, term.resizeCalls())
			})
		}
	})
	t.Run("an input frame of 64 KiB is passed on whole", func(t *testing.T) {
		term := newFakeTerminal()
		p, _ := terminalDaemon(t, terminalHost(&openRecord{}, term, nil), OriginManaged)
		c := openTerminalRaw(t, p)
		c.writeFrame(t, 'i', bytes.Repeat([]byte("k"), 64<<10))
		require.Eventually(t, func() bool { return len(term.inputBytes()) == 64<<10 }, 5*time.Second, 5*time.Millisecond)
	})
	t.Run("input is delivered in order, one call at a time", func(t *testing.T) {
		term := newFakeTerminal()
		var active, overlap atomic.Int32
		term.InputFunc = func(_ context.Context, p []byte) error {
			if active.Add(1) > 1 {
				overlap.Add(1)
			}
			time.Sleep(2 * time.Millisecond)
			term.mu.Lock()
			term.inputs = append(term.inputs, bytes.Clone(p))
			term.mu.Unlock()
			active.Add(-1)
			return nil
		}
		p, _ := terminalDaemon(t, terminalHost(&openRecord{}, term, nil), OriginManaged)
		c := openTerminalRaw(t, p)
		var want strings.Builder
		for i := range 40 {
			s := string(rune('a' + i%26))
			want.WriteString(s)
			c.writeFrame(t, 'i', []byte(s))
		}
		require.Eventually(t, func() bool { return len(term.inputBytes()) == 40 }, 5*time.Second, 5*time.Millisecond)
		assert.Equal(t, want.String(), string(term.inputBytes()))
		assert.Zero(t, overlap.Load())
	})
	t.Run("the socket closes even if the terminal's output stays open after Close", func(t *testing.T) {
		term := newFakeTerminal()
		term.CloseFunc = func(context.Context) error {
			close(term.closed)
			return nil
		}
		p, _ := terminalDaemon(t, terminalHost(&openRecord{}, term, nil), OriginManaged)
		c := openTerminalRaw(t, p)
		c.writeFrame(t, 'c', nil)
		term.assertClosed(t)
		c.assertEnds(t)
	})
	t.Run("a failed input closes the terminal", func(t *testing.T) {
		term := newFakeTerminal()
		term.InputFunc = func(context.Context, []byte) error { return io.ErrClosedPipe }
		p, _ := terminalDaemon(t, terminalHost(&openRecord{}, term, nil), OriginManaged)
		c := openTerminalRaw(t, p)
		c.writeFrame(t, 'i', []byte("x"))
		term.assertClosed(t)
		c.assertEnds(t)
	})
	t.Run("output the terminal ends with is flushed before the socket closes", func(t *testing.T) {
		term := newFakeTerminal()
		p, _ := terminalDaemon(t, terminalHost(&openRecord{}, term, nil), OriginManaged)
		c := openTerminalRaw(t, p)
		for range 5 {
			term.out <- []byte("line")
		}
		term.endOutput(envproto.TerminalExit{Reason: "closed"})
		var kinds []byte
		for {
			kind, _ := c.readFrame(t)
			kinds = append(kinds, kind)
			if kind == 'x' {
				break
			}
		}
		assert.Equal(t, "dddddx", string(kinds))
	})
	t.Run("a chunk over 64 KiB is split into frames", func(t *testing.T) {
		term := newFakeTerminal()
		p, _ := terminalDaemon(t, terminalHost(&openRecord{}, term, nil), OriginManaged)
		c := openTerminalRaw(t, p)
		term.out <- bytes.Repeat([]byte("z"), 64<<10+10)
		_, first := c.readFrame(t)
		_, second := c.readFrame(t)
		assert.Len(t, first, 64<<10)
		assert.Len(t, second, 10)
	})
	t.Run("the daemon doesn't exit idle while a terminal is open", func(t *testing.T) {
		term := newFakeTerminal()
		p, done := terminalDaemon(t, terminalHost(&openRecord{}, term, nil), OriginTransient)
		c := openTerminalRaw(t, p)
		select {
		case err := <-done:
			t.Fatalf("exited with a terminal open: %v", err)
		case <-time.After(500 * time.Millisecond):
		}
		c.writeFrame(t, 'c', nil)
		term.assertClosed(t)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("the daemon didn't exit once the terminal ended")
		}
	})
	t.Run("is served through the peer listener", func(t *testing.T) {
		rec := &openRecord{}
		s := &server{envs: terminalHost(rec, newFakeTerminal(), nil), log: discardLog(), opts: fastOptions()}
		p := testPaths(t)
		require.NoError(t, prepareSocketDir(p.Dir))
		ln, err := listen(t.Context(), p.Socket)
		require.NoError(t, err)
		srv := &http.Server{Handler: s.routes(), ReadHeaderTimeout: time.Second}
		go func() { _ = srv.Serve(peerListener{Listener: ln, uid: 1<<31 - 1, log: discardLog()}) }()
		t.Cleanup(func() { _ = srv.Close() })
		c := dialRaw(t, p)
		_, _ = io.WriteString(c, upgradeRequest("linux", shellArgs, upgradeHeaders))
		_, err = http.ReadResponse(c.r, nil)
		require.Error(t, err, "another user's connection was served")
		assert.Zero(t, rec.count())
	})
}

func TestServer_handleCallEnvironment_terminal(t *testing.T) {
	t.Parallel()
	t.Run("terminal.open isn't forwarded by call", func(t *testing.T) {
		var called atomic.Bool
		p := callDaemon(t, func(context.Context, string, CallRequest) (CallReply, error) {
			called.Store(true)
			return CallReply{}, nil
		})
		for _, op := range []string{envproto.OpTerminalOpen, envproto.OpTerminalClose} {
			_, err := Dial(p).CallEnvironment(t.Context(), "linux", callRequest(op))
			assertRemoteCode(t, err, "usage")
		}
		assert.False(t, called.Load())
	})
}

func TestClient_OpenTerminal(t *testing.T) {
	t.Parallel()
	t.Run("reads output and the exit", func(t *testing.T) {
		term := newFakeTerminal()
		p, _ := terminalDaemon(t, terminalHost(&openRecord{}, term, nil), OriginManaged)
		conn, result, err := Dial(p).OpenTerminal(t.Context(), "linux", envproto.TerminalOpenArgs{Kind: "shell", Cols: 80, Rows: 24, Specs: []string{}})
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		assert.Equal(t, "0123456789abcdef", result.SID)
		term.out <- []byte("one")
		term.out <- []byte("two")
		data, exit, err := conn.Next()
		require.NoError(t, err)
		assert.Nil(t, exit)
		assert.Equal(t, "one", string(data))
		data, _, err = conn.Next()
		require.NoError(t, err)
		assert.Equal(t, "two", string(data))
		require.NoError(t, conn.Input([]byte("ls\r")))
		require.NoError(t, conn.Resize(90, 40))
		require.Eventually(t, func() bool { return len(term.resizeCalls()) == 1 }, 5*time.Second, 5*time.Millisecond)
		assert.Equal(t, "ls\r", string(term.inputBytes()))
		assert.Equal(t, [][2]int{{90, 40}}, term.resizeCalls())
		code := 0
		term.endOutput(envproto.TerminalExit{Code: &code, Reason: "exited"})
		data, exit, err = conn.Next()
		require.ErrorIs(t, err, io.EOF)
		assert.Nil(t, data)
		require.NotNil(t, exit)
		assert.Equal(t, "exited", exit.Reason)
		_, _, err = conn.Next()
		require.ErrorIs(t, err, io.EOF)
	})
	t.Run("input over the frame limit is split", func(t *testing.T) {
		term := newFakeTerminal()
		p, _ := terminalDaemon(t, terminalHost(&openRecord{}, term, nil), OriginManaged)
		conn, _, err := Dial(p).OpenTerminal(t.Context(), "linux", envproto.TerminalOpenArgs{Kind: "shell", Cols: 80, Rows: 24, Specs: []string{}})
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		require.NoError(t, conn.Input(bytes.Repeat([]byte("a"), 3*envproto.MaxInputBytes+1)))
		require.Eventually(t, func() bool { return len(term.inputBytes()) == 3*envproto.MaxInputBytes+1 }, 5*time.Second, 5*time.Millisecond)
		term.mu.Lock()
		defer term.mu.Unlock()
		assert.Len(t, term.inputs, 4)
		for _, in := range term.inputs {
			assert.LessOrEqual(t, len(in), envproto.MaxInputBytes)
		}
	})
	t.Run("a refused open returns the remote error", func(t *testing.T) {
		host := terminalHost(&openRecord{}, nil, &HostError{Err: ErrEnvironmentOffline, Message: "environment linux is offline", Details: map[string]any{"reason": "offline"}})
		p, _ := terminalDaemon(t, host, OriginManaged)
		conn, _, err := Dial(p).OpenTerminal(t.Context(), "linux", envproto.TerminalOpenArgs{Kind: "shell", Cols: 80, Rows: 24, Specs: []string{}})
		assertRemoteCode(t, err, "environment_offline")
		assert.Nil(t, conn)
	})
	t.Run("a daemon that isn't running is ErrNotRunning", func(t *testing.T) {
		_, _, err := Dial(testPaths(t)).OpenTerminal(t.Context(), "linux", envproto.TerminalOpenArgs{Kind: "shell", Cols: 80, Rows: 24})
		require.ErrorIs(t, err, ErrNotRunning)
	})
	t.Run("a connection that ends without an exit is an error", func(t *testing.T) {
		term := newFakeTerminal()
		p, _ := terminalDaemon(t, terminalHost(&openRecord{}, term, nil), OriginManaged)
		conn, _, err := Dial(p).OpenTerminal(t.Context(), "linux", envproto.TerminalOpenArgs{Kind: "shell", Cols: 80, Rows: 24, Specs: []string{}})
		require.NoError(t, err)
		require.NoError(t, conn.Close())
		_, exit, err := conn.Next()
		require.Error(t, err)
		assert.Nil(t, exit)
	})
	t.Run("Close ends the terminal and is safe twice", func(t *testing.T) {
		term := newFakeTerminal()
		p, _ := terminalDaemon(t, terminalHost(&openRecord{}, term, nil), OriginManaged)
		conn, _, err := Dial(p).OpenTerminal(t.Context(), "linux", envproto.TerminalOpenArgs{Kind: "shell", Cols: 80, Rows: 24, Specs: []string{}})
		require.NoError(t, err)
		require.NoError(t, conn.Close())
		require.NoError(t, conn.Close())
		term.assertClosed(t)
	})
}
