package daemon

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// Status is GET /v1/status: the daemon, and each runner it watches. The
// pointer fields are null when nothing answers.
type Status struct {
	Running   bool           `json:"running"`
	PID       *int           `json:"pid"`
	Origin    *string        `json:"origin"`
	StartedAt *time.Time     `json:"started_at"`
	WSVersion *string        `json:"ws_version"`
	Socket    string         `json:"socket"`
	ConfigDir string         `json:"config_dir"`
	Runners   []RunnerStatus `json:"runners"`
	Warnings  []string       `json:"warnings"`
}

// ShutdownResult is what ws daemon stop reports.
type ShutdownResult struct {
	Stopped        bool     `json:"stopped"`
	PID            *int     `json:"pid"`
	RunnersStopped []string `json:"runners_stopped"`
	RunnersKept    []string `json:"runners_kept"`
	Warnings       []string `json:"warnings"`
}

// RemoteError is an error the daemon answered with.
type RemoteError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

func (e *RemoteError) Error() string { return e.Message }

type errorEnvelope struct {
	Error RemoteError `json:"error"`
}

type startRequest struct {
	Resume bool `json:"resume"`
}

type startResponse struct {
	PID int `json:"pid"`
}

type stopResponse struct {
	Stopped bool `json:"stopped"`
}

type shutdownRequest struct {
	KeepRunners bool `json:"keep_runners"`
}

// Client talks to a running daemon over its socket.
type Client struct {
	http *http.Client
}

// Dial returns a client for the daemon at p's socket. Nothing is dialed
// until a call is made.
//
//nolint:gocritic // hugeParam: the contract takes Paths by value.
func Dial(p Paths) *Client {
	transport := &http.Transport{DialContext: dialSocket(p.Socket)}
	return &Client{http: &http.Client{Transport: transport}}
}

// dialSocket connects to the socket at path. A socket nothing listens on
// is ErrNotRunning.
func dialSocket(path string) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		conn, err := d.DialContext(ctx, "unix", path)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrNotRunning, err)
		}
		return conn, nil
	}
}

// Status returns the daemon's status. ErrNotRunning when nothing answers.
func (c *Client) Status(ctx context.Context) (Status, error) {
	var st Status
	err := c.do(ctx, http.MethodGet, "/v1/status", nil, &st)
	return st, err
}

// Preflight checks a model through the daemon.
func (c *Client) Preflight(ctx context.Context, req PreflightRequest) (PreflightResponse, error) {
	var res PreflightResponse
	err := c.do(ctx, http.MethodPost, "/v1/preflight", req, &res)
	return res, err
}

// StartRunner starts the runner session id and returns its pid.
func (c *Client) StartRunner(ctx context.Context, id string, resume bool) (int, error) {
	var res startResponse
	err := c.do(ctx, http.MethodPost, runnerPath(id, "start"), startRequest{Resume: resume}, &res)
	return res.PID, err
}

// StopRunner stops the runner session id. A runner that had already exited
// is not an error.
func (c *Client) StopRunner(ctx context.Context, id string) error {
	var res stopResponse
	return c.do(ctx, http.MethodPost, runnerPath(id, "stop"), nil, &res)
}

// Shutdown asks the daemon to exit, interrupting its runners unless
// keepRunners is set.
func (c *Client) Shutdown(ctx context.Context, keepRunners bool) (ShutdownResult, error) {
	var res ShutdownResult
	err := c.do(ctx, http.MethodPost, "/v1/shutdown", shutdownRequest{KeepRunners: keepRunners}, &res)
	return res, err
}

func runnerPath(id, action string) string {
	return "/v1/runners/" + url.PathEscape(id) + "/" + action
}

// do sends one request and decodes the answer into out (when not nil). An
// error answer is a *RemoteError.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding the request: %w", err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://ws-daemon"+path, reader)
	if err != nil {
		return fmt.Errorf("building the request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("calling the daemon: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusMultipleChoices {
		return remoteError(resp)
	}
	if out == nil {
		return nil
	}
	if err := json.UnmarshalRead(resp.Body, out); err != nil {
		return fmt.Errorf("reading the daemon's answer: %w", err)
	}
	return nil
}

func remoteError(resp *http.Response) error {
	var env errorEnvelope
	if err := json.UnmarshalRead(resp.Body, &env); err != nil {
		return &RemoteError{Code: "internal", Message: resp.Status}
	}
	return &env.Error
}
