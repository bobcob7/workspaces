package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// terminalProtocol is the Upgrade token of the terminal route.
const terminalProtocol = "ws-terminal.v1"

// Socket frame kinds: [kind byte][uint32 big-endian length][body].
const (
	frameOpened byte = 'o' // daemon to CLI, first: TerminalOpenResult JSON
	frameOutput byte = 'd' // daemon to CLI: output bytes, markers included
	frameExit   byte = 'x' // daemon to CLI, last: TerminalExit JSON
	frameInput  byte = 'i' // CLI to daemon: input bytes
	frameResize byte = 'r' // CLI to daemon: envproto.ResizeBody
	frameClose  byte = 'c' // CLI to daemon: empty, close

	frameHeaderBytes = 5
	maxFrameBytes    = 64 << 10
)

// switchingProtocols is the daemon's answer to an accepted upgrade.
const switchingProtocols = "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: " + terminalProtocol + "\r\n\r\n"

// headerHasToken reports whether the comma-separated header values of key
// include token, ignoring case.
func headerHasToken(h http.Header, key, token string) bool {
	for _, value := range h.Values(key) {
		for part := range strings.SplitSeq(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

// handleTerminal opens a terminal on an environment and, once it is open,
// upgrades the connection to the terminal's socket frames.
func (s *server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	if !headerHasToken(r.Header, "Connection", "Upgrade") || !headerHasToken(r.Header, "Upgrade", terminalProtocol) {
		writeError(w, http.StatusBadRequest, "usage", "a terminal needs Connection: Upgrade and Upgrade: "+terminalProtocol, nil)
		return
	}
	args, ok := readTerminalArgs(w, r)
	if !ok {
		return
	}
	term, result, ok := s.openTerminal(w, r, args)
	if !ok {
		return
	}
	conn, rw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		s.closeTerminal(r.Context(), term)
		writeError(w, http.StatusInternalServerError, "internal", "the connection can't be upgraded", nil)
		return
	}
	s.terminals.Add(1)
	defer func() {
		s.terminals.Add(-1)
		s.touch()
	}()
	s.serveTerminal(r.Context(), conn, rw.Reader, term, result)
}

// readTerminalArgs decodes the body strictly: an unknown member is refused.
func readTerminalArgs(w http.ResponseWriter, r *http.Request) (envproto.TerminalOpenArgs, bool) {
	var args envproto.TerminalOpenArgs
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	err := json.UnmarshalRead(r.Body, &args, json.RejectUnknownMembers(true))
	if err == nil {
		return args, true
	}
	if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
		writeError(w, http.StatusBadRequest, "usage", "the request body is too large", nil)
		return args, false
	}
	writeError(w, http.StatusBadRequest, "usage", "the request body isn't valid terminal arguments", nil)
	return args, false
}

// openTerminal opens the terminal, answering the failure itself.
func (s *server) openTerminal(w http.ResponseWriter, r *http.Request, args envproto.TerminalOpenArgs) (TerminalStream, envproto.TerminalOpenResult, bool) { //nolint:gocritic // hugeParam: the args value is the fixed API.
	spec, _ := envproto.LookupOp(envproto.OpTerminalOpen)
	ctx, cancel := context.WithTimeout(r.Context(), spec.Timeout+envproto.Grace)
	defer cancel()
	term, result, err := s.envs.OpenTerminal(ctx, r.PathValue("name"), args)
	if err != nil {
		writeTerminalFailure(w, err)
		return nil, result, false
	}
	if term == nil {
		writeError(w, http.StatusInternalServerError, "internal", "the environment opened no terminal", nil)
		return nil, result, false
	}
	return term, result, true
}

// writeTerminalFailure answers a failed open: an agent's or the arguments'
// refusal with its own code, any other failure as a call's.
func writeTerminalFailure(w http.ResponseWriter, err error) {
	if args, ok := errors.AsType[*envproto.ArgsError](err); ok {
		status := http.StatusUnprocessableEntity
		if args.Code == "usage" {
			status = http.StatusBadRequest
		}
		writeError(w, status, args.Code, args.Message, nil)
		return
	}
	if reply, ok := errors.AsType[*envproto.ReplyError](err); ok {
		writeError(w, http.StatusUnprocessableEntity, reply.Code, reply.Message, reply.Details)
		return
	}
	writeCallFailure(w, err)
}

// closeTerminal closes term with a context that outlives ctx.
func (s *server) closeTerminal(ctx context.Context, term TerminalStream) {
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownWait)
	defer cancel()
	if err := term.Close(closeCtx); err != nil {
		s.log.WarnContext(ctx, "closing a terminal", "error", err)
	}
}

// serveTerminal runs the socket's frames until either side ends, then closes
// the terminal and the connection. Input and Resize get a context that lasts
// until both directions have stopped: a cancelled one would end the terminal.
func (s *server) serveTerminal(ctx context.Context, conn net.Conn, in *bufio.Reader, term TerminalStream, result envproto.TerminalOpenResult) {
	termCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	opened, err := json.Marshal(result)
	if err != nil {
		_ = conn.Close()
		s.closeTerminal(ctx, term)
		return
	}
	if _, err = io.WriteString(conn, switchingProtocols); err != nil || writeFrame(conn, frameOpened, opened) != nil {
		_ = conn.Close()
		s.closeTerminal(ctx, term)
		return
	}
	ended := make(chan struct{}, 2)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer func() { ended <- struct{}{} }()
		s.readFrames(termCtx, in, term)
	}()
	go func() {
		defer wg.Done()
		defer func() { ended <- struct{}{} }()
		writeOutput(conn, term, stop)
	}()
	<-ended
	close(stop)
	_ = conn.Close()
	s.closeTerminal(ctx, term)
	wg.Wait()
}

// readFrames applies the CLI's frames to term until the connection ends, a
// frame is refused or term fails.
func (s *server) readFrames(ctx context.Context, in io.Reader, term TerminalStream) {
	header := make([]byte, frameHeaderBytes)
	for {
		if _, err := io.ReadFull(in, header); err != nil {
			return
		}
		kind, size := header[0], binary.BigEndian.Uint32(header[1:])
		if size > maxFrameBytes {
			return
		}
		body := make([]byte, size)
		if _, err := io.ReadFull(in, body); err != nil {
			return
		}
		if !s.applyFrame(ctx, term, kind, body) {
			return
		}
	}
}

// applyFrame applies one frame and reports whether the terminal goes on.
func (s *server) applyFrame(ctx context.Context, term TerminalStream, kind byte, body []byte) bool {
	switch kind {
	case frameInput:
		if len(body) == 0 {
			return true
		}
		return s.logFailure(ctx, "terminal input", term.Input(ctx, body))
	case frameResize:
		cols, rows, err := envproto.ParseResize(body)
		if err != nil {
			return false
		}
		return s.logFailure(ctx, "terminal resize", term.Resize(ctx, cols, rows))
	}
	return false
}

// logFailure logs err and reports whether there was none.
func (s *server) logFailure(ctx context.Context, what string, err error) bool {
	if err == nil {
		return true
	}
	s.log.DebugContext(ctx, "terminal frame failed", "frame", what, "error", err)
	return false
}

// writeOutput sends term's output as data frames, then its exit, and returns
// at the exit, when the connection fails or when stop closes.
func writeOutput(conn net.Conn, term TerminalStream, stop <-chan struct{}) {
	output := term.Output()
	for {
		select {
		case <-stop:
			return
		case chunk, open := <-output:
			if !open {
				writeExit(conn, term)
				return
			}
			if !writeChunk(conn, chunk) {
				return
			}
		}
	}
}

// writeChunk sends chunk as data frames and reports whether the connection took them.
func writeChunk(conn net.Conn, chunk []byte) bool {
	for len(chunk) > 0 {
		n := min(len(chunk), maxFrameBytes)
		if writeFrame(conn, frameOutput, chunk[:n]) != nil {
			return false
		}
		chunk = chunk[n:]
	}
	return true
}

// writeExit sends term's exit, which is valid once its output has closed.
func writeExit(conn net.Conn, term TerminalStream) {
	exit, err := json.Marshal(term.Exit())
	if err != nil {
		return
	}
	_ = writeFrame(conn, frameExit, exit)
}

// writeFrame writes one frame in a single write.
func writeFrame(w io.Writer, kind byte, body []byte) error {
	if len(body) > maxFrameBytes {
		return fmt.Errorf("a terminal frame is at most %d bytes", maxFrameBytes)
	}
	frame := make([]byte, frameHeaderBytes, frameHeaderBytes+len(body))
	frame[0] = kind
	binary.BigEndian.PutUint32(frame[1:], uint32(len(body))) //nolint:gosec // G115: bounded by maxFrameBytes above.
	_, err := w.Write(append(frame, body...))
	return err
}

// TerminalConn is the CLI's side of an open terminal.
type TerminalConn struct {
	rwc io.ReadWriteCloser
	in  *bufio.Reader

	writeMu sync.Mutex
	// ended is the exit once the daemon sent it; Next returns it again.
	ended     *envproto.TerminalExit
	closeOnce sync.Once
}

// OpenTerminal dials the socket and upgrades; on a non-101 answer it
// returns the RemoteError as Do does. The context bounds the opening only.
//
//nolint:gocritic // hugeParam: the args value is the fixed API.
func (c *Client) OpenTerminal(ctx context.Context, name string, args envproto.TerminalOpenArgs) (*TerminalConn, envproto.TerminalOpenResult, error) {
	var none envproto.TerminalOpenResult
	body, err := json.Marshal(args)
	if err != nil {
		return nil, none, fmt.Errorf("encoding the request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://ws-daemon/v1/environments/"+url.PathEscape(name)+"/terminal", bytes.NewReader(body))
	if err != nil {
		return nil, none, fmt.Errorf("building the request: %w", err)
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", terminalProtocol)
	req.Header.Set("Content-Type", "application/json")
	conn, err := dialSocket(c.socket)(ctx, "unix", "")
	if err != nil {
		return nil, none, err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	term, result, err := handshake(conn, req)
	if !stop() && err == nil {
		err = ctx.Err()
	}
	if err != nil {
		_ = conn.Close()
		return nil, none, err
	}
	return term, result, nil
}

// handshake sends req on conn and reads the answer: a 101 and the first
// frame, or the error the daemon answered with.
func handshake(conn net.Conn, req *http.Request) (*TerminalConn, envproto.TerminalOpenResult, error) {
	var none envproto.TerminalOpenResult
	if err := req.Write(conn); err != nil {
		return nil, none, fmt.Errorf("calling the daemon: %w", err)
	}
	in := bufio.NewReader(conn)
	resp, err := http.ReadResponse(in, req)
	if err != nil {
		return nil, none, fmt.Errorf("calling the daemon: %w", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode >= http.StatusMultipleChoices {
			return nil, none, remoteError(resp)
		}
		return nil, none, fmt.Errorf("the daemon answered %s, not an upgrade", resp.Status)
	}
	term := &TerminalConn{rwc: conn, in: in}
	kind, body, err := term.readFrame()
	if err != nil {
		return nil, none, fmt.Errorf("reading the terminal's first frame: %w", err)
	}
	var result envproto.TerminalOpenResult
	if kind != frameOpened || json.Unmarshal(body, &result) != nil {
		return nil, none, errors.New("the daemon's first terminal frame isn't the open result")
	}
	return term, result, nil
}

// readFrame reads one frame, refusing a body over the limit.
func (t *TerminalConn) readFrame() (byte, []byte, error) {
	header := make([]byte, frameHeaderBytes)
	if _, err := io.ReadFull(t.in, header); err != nil {
		return 0, nil, err
	}
	size := binary.BigEndian.Uint32(header[1:])
	if size > maxFrameBytes {
		return 0, nil, fmt.Errorf("a terminal frame of %d bytes is over the limit", size)
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(t.in, body); err != nil {
		return 0, nil, err
	}
	return header[0], body, nil
}

// Next returns the next output chunk, or the exit with io.EOF. A connection
// that ends without an exit is an error.
func (t *TerminalConn) Next() (data []byte, exit *envproto.TerminalExit, err error) {
	if t.ended != nil {
		return nil, t.ended, io.EOF
	}
	kind, body, err := t.readFrame()
	if err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return nil, nil, fmt.Errorf("reading the terminal: %w", err)
	}
	switch kind {
	case frameOutput:
		return body, nil, nil
	case frameExit:
		var last envproto.TerminalExit
		if err := json.Unmarshal(body, &last); err != nil {
			return nil, nil, fmt.Errorf("reading the terminal's exit: %w", err)
		}
		t.ended = &last
		return nil, t.ended, io.EOF
	}
	return nil, nil, fmt.Errorf("the daemon sent an unknown terminal frame %q", kind)
}

// Input sends p as input frames of at most envproto.MaxInputBytes.
func (t *TerminalConn) Input(p []byte) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	for len(p) > 0 {
		n := min(len(p), envproto.MaxInputBytes)
		if err := writeFrame(t.rwc, frameInput, p[:n]); err != nil {
			return fmt.Errorf("sending input: %w", err)
		}
		p = p[n:]
	}
	return nil
}

// Resize sends the terminal's new size; one outside 1 to envproto.MaxTermSize
// is refused.
func (t *TerminalConn) Resize(cols, rows int) error {
	if cols < 1 || cols > envproto.MaxTermSize || rows < 1 || rows > envproto.MaxTermSize {
		return fmt.Errorf("a terminal size is 1 to %d: %dx%d", envproto.MaxTermSize, cols, rows)
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if err := writeFrame(t.rwc, frameResize, envproto.ResizeBody(cols, rows)); err != nil {
		return fmt.Errorf("sending the size: %w", err)
	}
	return nil
}

// Close asks the daemon to close the terminal, then closes the connection.
// It is safe to call twice.
func (t *TerminalConn) Close() error {
	var err error
	t.closeOnce.Do(func() {
		// A writer stuck on a full socket holds the lock; the daemon closes
		// the terminal on the connection's end as well.
		if t.writeMu.TryLock() {
			_ = writeFrame(t.rwc, frameClose, nil)
			t.writeMu.Unlock()
		}
		err = t.rwc.Close()
	})
	return err
}
