// Package gh runs the GitHub CLI. It is the only package that does
// (ADR 0039).
package gh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
)

// waitDelay bounds the wait for gh's output once ctx has killed it, as
// internal/git's does.
const waitDelay = 5 * time.Second

// prFields are the fields ws review start reads from `gh pr view`.
const prFields = "number,title,headRefOid,headRefName,baseRefName,isCrossRepository,url,body"

var (
	// ErrNotInstalled means gh isn't on PATH, or not at the given path.
	ErrNotInstalled = errors.New("gh not found")
	// ErrAuth means gh isn't signed in to the repo's host, or its token
	// was refused.
	ErrAuth = errors.New("gh isn't signed in")
	// ErrPRNotFound means the repo has no such pull request, or GitHub
	// doesn't know the repo.
	ErrPRNotFound = errors.New("no such pull request")
)

// authMarkers and notFoundMarkers are stderr fragments that identify a
// failure. gh has no error codes of its own: it exits 1 for both, so its
// message is all there is.
func authMarkers() []string {
	return []string{"gh auth login", "not logged in", "authentication required", "HTTP 401", "Bad credentials"}
}

func notFoundMarkers() []string {
	return []string{"Could not resolve to a PullRequest", "Could not resolve to a Repository", "no pull requests found"}
}

// CommandError is a gh command that ran and failed.
type CommandError struct {
	Args     []string
	ExitCode int
	Stderr   string
	Err      error
}

// Error reads "gh <args>: <err>: <stderr>".
func (e *CommandError) Error() string {
	msg := fmt.Sprintf("gh %s: %v", strings.Join(e.Args, " "), e.Err)
	if e.Stderr == "" {
		return msg
	}
	return msg + ": " + e.Stderr
}

func (e *CommandError) Unwrap() error {
	return e.Err
}

// Client runs gh with a fixed environment.
type Client struct {
	log     *slog.Logger
	path    string
	environ []string
}

// New returns a Client that runs path, normally "gh", with environ,
// normally os.Environ(), plus GH_PROMPT_DISABLED=1 and NO_COLOR=1, so gh
// never waits for input or colors its output.
func New(log *slog.Logger, path string, environ []string) *Client {
	return &Client{log: log, path: path, environ: environ}
}

// PR is a pull request as `gh pr view --json` reports it.
//
//nolint:tagliatelle // gh's field names, not ws output (ADR 0016).
type PR struct {
	Number            int    `json:"number"`
	Title             string `json:"title"`
	HeadRefOid        string `json:"headRefOid"`
	HeadRefName       string `json:"headRefName"`
	BaseRefName       string `json:"baseRefName"`
	IsCrossRepository bool   `json:"isCrossRepository"`
	URL               string `json:"url"`
	Body              string `json:"body"`
}

// PRView reads pull request number of repo, a host/owner/name id, with
// `gh pr view <number> --repo <repo> --json ...`.
func (c *Client) PRView(ctx context.Context, repo string, number int) (PR, error) {
	out, err := c.run(ctx, "pr", "view", strconv.Itoa(number), "--repo", repo, "--json", prFields)
	if err != nil {
		return PR{}, err
	}
	var pr PR
	if err := json.Unmarshal(out, &pr); err != nil {
		return PR{}, fmt.Errorf("reading gh pr view's output: %w", err)
	}
	if pr.HeadRefOid == "" {
		return PR{}, fmt.Errorf("reading gh pr view's output: no headRefOid for pull request %d of %s", number, repo)
	}
	return pr, nil
}

// run runs gh with args and returns its stdout, logging the command and
// its duration at debug level.
func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	//nolint:gosec // G204: running gh with arguments is this package's purpose.
	cmd := exec.CommandContext(ctx, c.path, args...)
	cmd.Env = append(slices.Clone(c.environ), "GH_PROMPT_DISABLED=1", "NO_COLOR=1")
	cmd.WaitDelay = waitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := cmd.Run()
	c.log.DebugContext(ctx, "gh",
		slog.Any("args", args),
		slog.Duration("took", time.Since(start)),
		slog.Int("exit", cmd.ProcessState.ExitCode()),
	)
	if err == nil {
		return stdout.Bytes(), nil
	}
	if cmd.ProcessState == nil && (errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist)) {
		return nil, fmt.Errorf("%w: %w", ErrNotInstalled, err)
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return nil, classify(&CommandError{Args: args, ExitCode: cmd.ProcessState.ExitCode(), Stderr: strings.TrimSpace(stderr.String()), Err: err})
}

// classify wraps a failed command's error in the sentinel its stderr
// identifies, if any, keeping the CommandError reachable with errors.As.
func classify(e *CommandError) error {
	switch {
	case containsAny(e.Stderr, notFoundMarkers()):
		return fmt.Errorf("%w: %w", ErrPRNotFound, e)
	case containsAny(e.Stderr, authMarkers()):
		return fmt.Errorf("%w: %w", ErrAuth, e)
	}
	return e
}

func containsAny(s string, markers []string) bool {
	for _, m := range markers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}
