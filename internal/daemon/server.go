package daemon

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"time"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// Origins say what started a daemon, as daemon.json's origin.
const (
	OriginTransient = "transient"
	OriginManaged   = "managed"
	OriginApp       = "app"
)

const (
	idleLimit      = 15 * time.Minute
	parentInterval = 2 * time.Second
	shutdownWait   = 5 * time.Second
	maxBodyBytes   = 64 << 10
	// callBodyBytes caps a call's body: its operation's arguments, at most a
	// request, and the JSON around them.
	callBodyBytes = envproto.MaxRequestBytes + 4<<10
)

// options are Serve's timings. Tests change them.
type options struct {
	idle       time.Duration
	parentPoll time.Duration
	getppid    func() int
}

type versionKey struct{}

// WithVersion returns ctx carrying the ws version that Serve writes to
// daemon.json as ws_version.
func WithVersion(ctx context.Context, version string) context.Context {
	return context.WithValue(ctx, versionKey{}, version)
}

// Serve takes the lock (ErrRunning with the holder's pid if held), writes
// daemon.json, prepares the socket directory (0700, owned by uid) and
// socket (0600), checks every connection's peer uid, and serves /v1/*
// until ctx ends, a shutdown request, or, for transient, 15 min of Idle()
// and no request. For app, it polls os.Getppid() every 2 s; once the
// parent pid differs from the one at start (the app quit), it logs
// "parent gone; acting as transient" and applies the transient rule. envs
// starts after the socket is listening, and is closed when serving ends.
//
//nolint:gocritic // hugeParam: the contract takes Paths by value.
func Serve(ctx context.Context, p Paths, origin string, sup RunnerSupervisor, envs EnvironmentHost, log *slog.Logger) error {
	opts := options{idle: idleLimit, parentPoll: parentInterval, getppid: os.Getppid}
	return serve(ctx, p, origin, sup, envs, log, opts)
}

//nolint:gocritic // hugeParam: the contract takes Paths by value.
func serve(ctx context.Context, p Paths, origin string, sup RunnerSupervisor, envs EnvironmentHost, log *slog.Logger, opts options) error {
	if !validOrigin(origin) {
		return fmt.Errorf("unknown origin %q", origin)
	}
	release, err := takeLock(p)
	if err != nil {
		return err
	}
	defer release()
	if err = sup.Reconcile(ctx); err != nil {
		return fmt.Errorf("reconciling runners: %w", err)
	}
	ln, err := listen(ctx, p.Socket)
	if err != nil {
		return err
	}
	info := Info{Version: 1, PID: os.Getpid(), StartedAt: time.Now().UTC().Truncate(time.Second), Origin: origin, Socket: p.Socket, WSVersion: versionFrom(ctx)}
	if err := writeInfo(p.Info, info); err != nil {
		_ = ln.Close()
		return err
	}
	watchCtx, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	go sup.Watch(watchCtx)
	s := &server{paths: p, info: info, sup: sup, envs: envs, log: log, opts: opts, stop: make(chan struct{}, 1), done: make(chan struct{})}
	s.startEnvironments(ctx)
	defer s.closeEnvironments()
	s.touch()
	return s.run(ctx, ln)
}

type server struct {
	paths       Paths
	info        Info
	sup         RunnerSupervisor
	envs        EnvironmentHost
	log         *slog.Logger
	opts        options
	lastRequest atomic.Int64 // UnixNano of the last request
	parentGone  atomic.Bool
	// terminals counts the open terminals: one is no request in flight, but
	// the daemon mustn't exit idle under it.
	terminals atomic.Int64
	stop      chan struct{}
	// done is closed when the daemon begins to shut down: a hijacked
	// connection outlives http.Server.Shutdown, so terminals watch it.
	done chan struct{}
	// envStartErr is why the environments didn't start, or nil; the daemon
	// keeps running and reports it as a warning.
	envStartErr error
}

// startEnvironments starts the environments. A failure is logged and kept
// as a warning, not an error that stops the daemon.
func (s *server) startEnvironments(ctx context.Context) {
	err := s.envs.Start(ctx)
	if err == nil {
		return
	}
	s.envStartErr = err
	s.log.ErrorContext(ctx, "environments didn't start", "error", err)
}

// closeEnvironments stops the environments, logging a failure.
func (s *server) closeEnvironments() {
	if err := s.envs.Close(); err != nil {
		s.log.Error("closing the environments", "error", err)
	}
}

// warnings are the supervisor's warnings, then the environments': why they
// didn't start, and the configuration problems they report.
func (s *server) warnings() []string {
	warnings := slices.Concat(s.sup.Warnings(), s.envs.Warnings())
	if s.envStartErr != nil {
		warnings = append(warnings, "environments didn't start: "+s.envStartErr.Error())
	}
	if warnings == nil {
		return []string{}
	}
	return warnings
}

func (s *server) run(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{Handler: s.touching(s.routes()), ReadHeaderTimeout: shutdownWait}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(peerListener{Listener: ln, uid: os.Getuid(), log: s.log}) }()
	if s.info.Origin == OriginApp {
		go s.watchParent(ctx)
	}
	s.log.InfoContext(ctx, "daemon listening", "pid", s.info.PID, "origin", s.info.Origin, "socket", s.info.Socket)
	err := s.waitForExit(ctx, serveErr)
	close(s.done)
	closeServer(ctx, srv)
	s.awaitTerminals()
	s.log.InfoContext(ctx, "daemon stopped")
	return err
}

// awaitTerminals waits, for at most shutdownWait, until every terminal has
// been closed after done.
func (s *server) awaitTerminals() {
	deadline := time.Now().Add(shutdownWait)
	for s.terminals.Load() > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
}

func closeServer(ctx context.Context, srv *http.Server) {
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownWait)
	defer cancel()
	_ = srv.Shutdown(closeCtx)
}

func (s *server) waitForExit(ctx context.Context, serveErr <-chan error) error {
	ticker := time.NewTicker(s.tick())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-s.stop:
			return nil
		case err := <-serveErr:
			return fmt.Errorf("serving: %w", err)
		case <-ticker.C:
			if s.idleExit(ctx) {
				return nil
			}
		}
	}
}

// tick is how often the idle rule is checked: a quarter of the idle limit,
// at most a second.
func (s *server) tick() time.Duration {
	return min(time.Second, max(s.opts.idle/4, time.Millisecond))
}

func (s *server) idleExit(ctx context.Context) bool {
	if !s.actsTransient() || !s.sup.Idle() || s.envs.Status().Listening || s.terminals.Load() > 0 {
		return false
	}
	if time.Since(time.Unix(0, s.lastRequest.Load())) < s.opts.idle {
		return false
	}
	s.log.InfoContext(ctx, "idle with no runner and no request; exiting")
	return true
}

// actsTransient is the transient rule: a transient daemon always, an app
// daemon once its parent has gone.
func (s *server) actsTransient() bool {
	return s.info.Origin == OriginTransient || (s.info.Origin == OriginApp && s.parentGone.Load())
}

func (s *server) watchParent(ctx context.Context) {
	start := s.opts.getppid()
	ticker := time.NewTicker(s.opts.parentPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.opts.getppid() == start {
				continue
			}
			s.parentGone.Store(true)
			s.log.InfoContext(ctx, "parent gone; acting as transient")
			return
		}
	}
}

func (s *server) touch() {
	s.lastRequest.Store(time.Now().UnixNano())
}

func (s *server) touching(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.touch()
		next.ServeHTTP(w, r)
	})
}

func (s *server) requestStop() {
	select {
	case s.stop <- struct{}{}:
	default:
	}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", s.handleStatus)
	mux.HandleFunc("POST /v1/preflight", s.handlePreflight)
	mux.HandleFunc("POST /v1/runners/{id}/start", s.handleStart)
	mux.HandleFunc("POST /v1/runners/{id}/stop", s.handleStop)
	mux.HandleFunc("POST /v1/shutdown", s.handleShutdown)
	mux.HandleFunc("POST /v1/environments/reload", s.handleReloadEnvironments)
	mux.HandleFunc("POST /v1/environments/{name}/call", s.handleCallEnvironment)
	mux.HandleFunc("POST /v1/environments/{name}/terminal", s.handleTerminal)
	return mux
}

func (s *server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	environments := s.envs.Status()
	writeJSON(w, http.StatusOK, Status{
		Running:      true,
		PID:          &s.info.PID,
		Origin:       &s.info.Origin,
		StartedAt:    &s.info.StartedAt,
		WSVersion:    &s.info.WSVersion,
		Socket:       s.info.Socket,
		ConfigDir:    filepath.Dir(s.paths.Dir),
		Runners:      s.sup.Runners(),
		Environments: &environments,
		Warnings:     s.warnings(),
	})
}

func (s *server) handleReloadEnvironments(w http.ResponseWriter, r *http.Request) {
	if err := s.envs.Reload(r.Context()); err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reloadResponse{Reloaded: true})
}

// handleCallEnvironment sends one operation to an environment and answers
// its reply unchanged.
func (s *server) handleCallEnvironment(w http.ResponseWriter, r *http.Request) {
	var req CallRequest
	if !readBodyUpTo(w, r, &req, callBodyBytes) {
		return
	}
	if spec, known := envproto.LookupOp(req.Op); known && spec.Caller == envproto.CallerTerminal {
		writeError(w, http.StatusBadRequest, "usage", req.Op+" isn't forwarded by call: open a terminal instead", nil)
		return
	}
	if !envproto.ValidRequestID(req.RequestID) {
		writeError(w, http.StatusBadRequest, "usage", "the request id isn't valid", nil)
		return
	}
	if req.Deadline.IsZero() {
		writeError(w, http.StatusBadRequest, "usage", "the call has no deadline", nil)
		return
	}
	if len(req.Args) > envproto.MaxRequestBytes {
		writeError(w, http.StatusBadRequest, "usage", "the call's args are too large", nil)
		return
	}
	if spec, known := envproto.LookupOp(req.Op); known && time.Until(req.Deadline) > spec.Timeout+envproto.Grace {
		writeError(w, http.StatusBadRequest, "usage", "the call's deadline is too far away", nil)
		return
	}
	reply, err := s.envs.Call(r.Context(), r.PathValue("name"), req)
	if err != nil {
		writeCallFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reply)
}

func (s *server) handlePreflight(w http.ResponseWriter, r *http.Request) {
	var req PreflightRequest
	if !readBody(w, r, &req) {
		return
	}
	res, err := s.sup.Preflight(r.Context(), req)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *server) handleStart(w http.ResponseWriter, r *http.Request) {
	var req startRequest
	if !readBody(w, r, &req) {
		return
	}
	pid, err := s.sup.Start(r.Context(), r.PathValue("id"), req.Resume)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, startResponse{PID: pid})
}

func (s *server) handleStop(w http.ResponseWriter, r *http.Request) {
	stopped, err := s.sup.Stop(r.Context(), r.PathValue("id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stopResponse{Stopped: stopped})
}

func (s *server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	var req shutdownRequest
	if !readBody(w, r, &req) {
		return
	}
	stopped, kept := s.sup.StopAll(r.Context(), req.KeepRunners)
	writeJSON(w, http.StatusOK, ShutdownResult{
		Stopped:        true,
		PID:            &s.info.PID,
		RunnersStopped: nonNil(stopped),
		RunnersKept:    nonNil(kept),
		Warnings:       []string{},
	})
	s.requestStop()
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

func validOrigin(origin string) bool {
	switch origin {
	case OriginTransient, OriginManaged, OriginApp:
		return true
	}
	return false
}

func versionFrom(ctx context.Context) string {
	version, _ := ctx.Value(versionKey{}).(string)
	return version
}

// boundBodyRead gives the request body shutdownWait to arrive, so a client
// that stalls it can't hold the handler. The returned func lifts the bound.
func boundBodyRead(w http.ResponseWriter) func() {
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(shutdownWait))
	return func() { _ = rc.SetReadDeadline(time.Time{}) }
}

func readBody(w http.ResponseWriter, r *http.Request, v any) bool {
	return readBodyUpTo(w, r, v, maxBodyBytes)
}

// readBodyUpTo decodes the request body into v, refusing one over limit bytes.
func readBodyUpTo(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	defer boundBodyRead(w)()
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	err := json.UnmarshalRead(r.Body, v)
	if err == nil {
		return true
	}
	if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
		writeError(w, http.StatusBadRequest, "usage", "the request body is too large", nil)
		return false
	}
	writeError(w, http.StatusBadRequest, "usage", "the request body isn't valid JSON", nil)
	return false
}

func writeFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrNotSupervising) {
		writeError(w, http.StatusNotImplemented, "not_supervising", err.Error(), nil)
		return
	}
	if remote, ok := errors.AsType[*RemoteError](err); ok {
		writeError(w, http.StatusUnprocessableEntity, remote.Code, remote.Message, remote.Details)
		return
	}
	writeError(w, http.StatusInternalServerError, "internal", err.Error(), nil)
}

// writeCallFailure answers a failed call with the status and code of its
// HostError, and any other failure as writeFailure does.
func writeCallFailure(w http.ResponseWriter, err error) {
	hostErr, ok := errors.AsType[*HostError](err)
	if !ok {
		writeFailure(w, err)
		return
	}
	status, code := callCode(hostErr.Err)
	writeError(w, status, code, hostErr.Error(), hostErr.Details)
}

// callCode is the status and error code a call's sentinel maps to.
func callCode(err error) (int, string) {
	switch {
	case errors.Is(err, ErrEnvironmentUnknown):
		return http.StatusNotFound, "environment_unknown"
	case errors.Is(err, ErrEnvironmentOffline):
		return http.StatusServiceUnavailable, "environment_offline"
	case errors.Is(err, ErrEnvironmentOutdated):
		return http.StatusConflict, "environment_outdated"
	case errors.Is(err, ErrNotForwarded), errors.Is(err, ErrRequestPending):
		return http.StatusBadRequest, "usage"
	}
	return http.StatusInternalServerError, "internal"
}

func writeError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	writeJSON(w, status, errorEnvelope{Error: RemoteError{Code: code, Message: message, Details: details}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "encoding the answer", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data) //nolint:gosec // G705: the body is JSON under application/json, not HTML.
}

var errNotUnixConn = errors.New("connection isn't a unix socket")

// peerListener accepts only connections from uid; any other connection is
// closed at once.
type peerListener struct {
	net.Listener
	uid int
	log *slog.Logger
}

func (l peerListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if uid, err := peerUID(conn); err == nil && uid == l.uid {
			return conn, nil
		}
		l.log.Warn("refused a connection from another user")
		_ = conn.Close()
	}
}
