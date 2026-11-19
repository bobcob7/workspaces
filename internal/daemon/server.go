package daemon

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"
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
// "parent gone; acting as transient" and applies the transient rule.
//
//nolint:gocritic // hugeParam: the contract takes Paths by value.
func Serve(ctx context.Context, p Paths, origin string, sup RunnerSupervisor, log *slog.Logger) error {
	opts := options{idle: idleLimit, parentPoll: parentInterval, getppid: os.Getppid}
	return serve(ctx, p, origin, sup, log, opts)
}

//nolint:gocritic // hugeParam: the contract takes Paths by value.
func serve(ctx context.Context, p Paths, origin string, sup RunnerSupervisor, log *slog.Logger, opts options) error {
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
	s := &server{paths: p, info: info, sup: sup, log: log, opts: opts, stop: make(chan struct{}, 1)}
	s.touch()
	return s.run(ctx, ln)
}

type server struct {
	paths       Paths
	info        Info
	sup         RunnerSupervisor
	log         *slog.Logger
	opts        options
	lastRequest atomic.Int64 // UnixNano of the last request
	parentGone  atomic.Bool
	stop        chan struct{}
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
	closeServer(ctx, srv)
	s.log.InfoContext(ctx, "daemon stopped")
	return err
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
	if !s.actsTransient() || !s.sup.Idle() {
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
	return mux
}

func (s *server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, Status{
		Running:   true,
		PID:       &s.info.PID,
		Origin:    &s.info.Origin,
		StartedAt: &s.info.StartedAt,
		WSVersion: &s.info.WSVersion,
		Socket:    s.info.Socket,
		ConfigDir: filepath.Dir(s.paths.Dir),
		Runners:   s.sup.Runners(),
		Warnings:  s.sup.Warnings(),
	})
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

func readBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.UnmarshalRead(r.Body, v); err != nil {
		writeError(w, http.StatusBadRequest, "usage", "the request body isn't valid JSON", nil)
		return false
	}
	return true
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
	_, _ = w.Write(data)
}

var errNotUnixConn = errors.New("connection isn't a unix socket")

// prepareSocketDir creates the socket's directory, which must be private to
// the user: 0700 and owned by them.
func prepareSocketDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating the socket directory: %w", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("checking the socket directory: %w", err)
	}
	owner, ok := ownerUID(info)
	if !ok || owner != os.Getuid() || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("socket directory %s must be 0700 and owned by you", dir)
	}
	return nil
}

func ownerUID(info fs.FileInfo) (int, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Uid), true
}

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
