package mcp

import (
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	json "encoding/json/v2"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultStartTimeout = 30 * time.Second
	callTimeout         = 120 * time.Second
	stderrTailBytes     = 1024
	stateConnected      = "connected"
	stateFailed         = "failed"
	clientName          = "ws-runner"
	clientVersion       = "1.0.0"
	redactedText        = "[redacted]"
)

// StartOptions is how servers are started for one run.
type StartOptions struct {
	Dir     string        // stdio servers' working directory (the session directory)
	Environ []string      // the runner's unscrubbed environment (ADR 0068)
	Stderr  io.Writer     // runner.log
	Timeout time.Duration // initialize + list tools; 30 s
	HTTP    *http.Client
}

// Server is one started server.
type Server struct {
	name        string
	sess        session
	tools       []ToolInfo
	callTimeout time.Duration
	stop        func() error
	pid         int
	secrets     []string

	mu    sync.Mutex
	state string
	err   string
}

// Start expands def, connects (stdio: own process group; http; sse), and
// lists its tools. The error never contains an expanded value. The secrets
// are every expanded value, for the redactor; they come back with an error too.
//
//nolint:gocritic // ADR 0069 fixes the by-value signature
func Start(ctx context.Context, def ServerDef, opts StartOptions) (*Server, []string, error) {
	expanded, secrets, err := Expand(def, lookupEnv(opts.Environ))
	if err != nil {
		return nil, nil, err
	}
	srv, err := connect(ctx, &expanded, opts)
	if err != nil {
		return nil, secrets, redactError(err, secrets)
	}
	srv.secrets = secrets
	return srv, secrets, nil
}

// connect starts def's transport and lists its tools. A start that times out
// kills the stdio process, so a hung server can't outlive its start.
func connect(ctx context.Context, def *ServerDef, opts StartOptions) (*Server, error) {
	procCtx, stopProc := context.WithCancel(ctx)
	startCtx, cancelStart := context.WithTimeout(ctx, startTimeout(opts))
	defer cancelStart()
	stopOnTimeout := context.AfterFunc(startCtx, stopProc)
	transport, proc, err := newTransport(procCtx, def, opts)
	if err != nil {
		stopProc()
		return nil, fmt.Errorf("starting MCP server %q: %w", def.Name, err)
	}
	sess, err := mcp.NewClient(&mcp.Implementation{Name: clientName, Version: clientVersion}, nil).Connect(startCtx, transport, nil)
	if err != nil {
		stopProc()
		return nil, fmt.Errorf("connecting to MCP server %q: %w%s", def.Name, err, proc.stderrNote())
	}
	stop := func() error {
		stopProc()
		return sess.Close()
	}
	srv, err := newServer(startCtx, def.Name, sess, stop)
	stopOnTimeout()
	if err != nil {
		_ = stop()
		return nil, err
	}
	srv.pid = proc.pid()
	return srv, nil
}

// newServer lists sess's tools and builds the Server; stop closes it.
func newServer(ctx context.Context, name string, sess session, stop func() error) (*Server, error) {
	res, err := sess.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		return nil, fmt.Errorf("listing tools of MCP server %q: %w", name, err)
	}
	tools := make([]ToolInfo, 0, len(res.Tools))
	for _, tool := range res.Tools {
		tools = append(tools, toToolInfo(name, tool))
	}
	return &Server{
		name:        name,
		sess:        sess,
		tools:       tools,
		callTimeout: callTimeout,
		stop:        stop,
		state:       stateConnected,
	}, nil
}

func toToolInfo(server string, tool *mcp.Tool) ToolInfo {
	schema, _ := tool.InputSchema.(map[string]any)
	info := ToolInfo{
		Server:      server,
		Tool:        tool.Name,
		Name:        ToolName(server, tool.Name),
		Description: tool.Description,
		Schema:      schema,
	}
	if tool.Annotations != nil {
		info.ReadOnly = tool.Annotations.ReadOnlyHint
		info.Destructive = tool.Annotations.DestructiveHint != nil && *tool.Annotations.DestructiveHint
	}
	return info
}

// Tools returns the server's tools.
func (s *Server) Tools() []ToolInfo {
	return s.tools
}

// Call runs tool with JSON arguments under a 120 s limit. A failed call marks
// the server failed; it is not restarted.
func (s *Server) Call(ctx context.Context, tool, arguments string) (CallResult, error) {
	if err := s.checkRunning(); err != nil {
		return CallResult{}, err
	}
	values, err := decodeArguments(arguments)
	if err != nil {
		return CallResult{}, err
	}
	callCtx, cancel := context.WithTimeout(ctx, s.callTimeout)
	defer cancel()
	res, err := s.sess.CallTool(callCtx, &mcp.CallToolParams{Name: tool, Arguments: values})
	if err != nil {
		if ctx.Err() == nil {
			s.fail(err)
		}
		return CallResult{}, redactError(fmt.Errorf("calling %s on MCP server %q: %w", tool, s.name, err), s.secrets)
	}
	return resultOf(res), nil
}

// Close stops the server's process group and closes the session.
func (s *Server) Close() error {
	return s.stop()
}

func (s *Server) checkRunning() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == stateFailed {
		return fmt.Errorf("MCP server %q is not running: %s", s.name, s.err)
	}
	return nil
}

func (s *Server) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = stateFailed
	s.err = redactError(err, s.secrets).Error()
}

func (s *Server) status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{Server: s.name, State: s.state, Error: errorField(s.err)}
	if s.state == stateConnected {
		st.Tools = len(s.tools)
	}
	return st
}

func decodeArguments(arguments string) (map[string]any, error) {
	values := map[string]any{}
	if strings.TrimSpace(arguments) == "" {
		return values, nil
	}
	if err := json.Unmarshal([]byte(arguments), &values); err != nil {
		return nil, fmt.Errorf("tool arguments are not a JSON object: %w", err)
	}
	return values, nil
}

// newTransport is the SDK transport for def. A stdio process is bound to ctx.
func newTransport(ctx context.Context, def *ServerDef, opts StartOptions) (mcp.Transport, *serverProcess, error) {
	switch def.Type {
	case typeStdio:
		return newStdioTransport(ctx, def, opts)
	case typeHTTP:
		return &mcp.StreamableClientTransport{Endpoint: def.URL, HTTPClient: httpClient(opts.HTTP, def.Headers)}, nil, nil
	case typeSSE:
		return &mcp.SSEClientTransport{Endpoint: def.URL, HTTPClient: httpClient(opts.HTTP, def.Headers)}, nil, nil
	default:
		return nil, nil, fmt.Errorf("unsupported transport %q", def.Type)
	}
}

// newStdioTransport builds the stdio command in its own process group.
// Cancelling ctx kills the group.
func newStdioTransport(ctx context.Context, def *ServerDef, opts StartOptions) (mcp.Transport, *serverProcess, error) {
	cmd := exec.CommandContext(ctx, def.Command, def.Args...) //nolint:gosec // the command is the server's definition, run on purpose (ADR 0069)
	cmd.Dir = opts.Dir
	cmd.Env = stdioEnv(opts.Environ, def.Env)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	tail := &tailWriter{limit: stderrTailBytes}
	cmd.Stderr = io.MultiWriter(orDiscard(opts.Stderr), tail)
	return &mcp.CommandTransport{Command: cmd}, &serverProcess{cmd: cmd, stderr: tail}, nil
}

// serverProcess is a stdio server's process and the tail of its stderr.
type serverProcess struct {
	cmd    *exec.Cmd
	stderr *tailWriter
}

func (p *serverProcess) pid() int {
	if p == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// stderrNote is the end of a stdio server's stderr, for a start error.
func (p *serverProcess) stderrNote() string {
	if p == nil {
		return ""
	}
	text := p.stderr.String()
	if text == "" {
		return ""
	}
	return "; stderr: " + text
}

// stdioEnv is the runner's environment plus the server's env block. exec keeps
// the last of a repeated name, so the block wins.
func stdioEnv(environ []string, extra map[string]string) []string {
	env := slices.Clone(environ)
	for _, key := range slices.Sorted(maps.Keys(extra)) {
		env = append(env, key+"="+extra[key])
	}
	return env
}

// httpClient is base (or the default client) with headers on every request.
func httpClient(base *http.Client, headers map[string]string) *http.Client {
	if base == nil {
		base = http.DefaultClient
	}
	if len(headers) == 0 {
		return base
	}
	client := *base
	inner := client.Transport
	if inner == nil {
		inner = http.DefaultTransport
	}
	client.Transport = &headerTransport{base: inner, headers: headers}
	return &client
}

type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	for name, value := range t.headers {
		clone.Header.Set(name, value)
	}
	return t.base.RoundTrip(clone)
}

// tailWriter keeps the last limit bytes written to it.
type tailWriter struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.data = append(w.data, p...)
	if extra := len(w.data) - w.limit; extra > 0 {
		w.data = w.data[extra:]
	}
	return len(p), nil
}

func (w *tailWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.TrimSpace(string(w.data))
}

func startTimeout(opts StartOptions) time.Duration {
	if opts.Timeout > 0 {
		return opts.Timeout
	}
	return defaultStartTimeout
}

// lookupEnv reads name from environ; the last entry wins, as exec's does.
func lookupEnv(environ []string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, found := "", false
		for _, kv := range environ {
			if key, v, ok := strings.Cut(kv, "="); ok && key == name {
				value, found = v, true
			}
		}
		return value, found
	}
}

// redactError is err with every secret replaced, for messages that can carry
// expanded values.
func redactError(err error, secrets []string) error {
	if err == nil {
		return nil
	}
	pairs := make([]string, 0, 2*len(secrets))
	for _, secret := range secrets {
		pairs = append(pairs, secret, redactedText)
	}
	return &redactedError{message: strings.NewReplacer(pairs...).Replace(err.Error()), err: err}
}

// redactedError carries the redacted message and keeps the original error in
// its chain, so errors.Is and errors.As still see the cause.
type redactedError struct {
	message string
	err     error
}

func (e *redactedError) Error() string { return e.message }

func (e *redactedError) Unwrap() error { return e.err }

func errorField(message string) *string {
	if message == "" {
		return nil
	}
	return &message
}

func orDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}
