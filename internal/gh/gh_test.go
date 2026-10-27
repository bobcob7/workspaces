package gh

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/exectest"
)

// fakeGh writes a gh that records its arguments and environment in dir,
// then runs body.
func fakeGh(t *testing.T, body string) (path, dir string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "gh")
	exectest.WriteScript(t, path, "#!/bin/sh\necho \"$@\" > \""+dir+"/args\"\nenv > \""+dir+"/env\"\n"+body)
	return path, dir
}

func newTestClient(path string) *Client {
	return New(slog.New(slog.NewJSONHandler(io.Discard, nil)), path, []string{"PATH=" + os.Getenv("PATH")})
}

func TestClient_PRView(t *testing.T) {
	t.Parallel()
	t.Run("reads the pull request", func(t *testing.T) {
		t.Parallel()
		path, dir := fakeGh(t, `echo '{"number":42,"title":"Fix login","headRefOid":"3f2a9c1e4b5d6f7a8b9c0d1e2f3a4b5c6d7e8f90","headRefName":"feat/login","baseRefName":"main","isCrossRepository":true,"url":"https://github.com/bobcob7/buff-bear/pull/42","body":"Fixes #1"}'`)
		pr, err := newTestClient(path).PRView(t.Context(), "github.com/bobcob7/buff-bear", 42)
		require.NoError(t, err)
		assert.Equal(t, PR{
			Number: 42, Title: "Fix login", HeadRefOid: "3f2a9c1e4b5d6f7a8b9c0d1e2f3a4b5c6d7e8f90", HeadRefName: "feat/login",
			BaseRefName: "main", IsCrossRepository: true, URL: "https://github.com/bobcob7/buff-bear/pull/42", Body: "Fixes #1",
		}, pr)
		args, err := os.ReadFile(filepath.Join(dir, "args"))
		require.NoError(t, err)
		assert.Equal(t, "pr view 42 --repo github.com/bobcob7/buff-bear --json "+prFields+"\n", string(args))
	})
	t.Run("never prompts or colors", func(t *testing.T) {
		t.Parallel()
		path, dir := fakeGh(t, `echo '{"headRefOid":"x"}'`)
		_, err := newTestClient(path).PRView(t.Context(), "github.com/a/b", 1)
		require.NoError(t, err)
		env, err := os.ReadFile(filepath.Join(dir, "env"))
		require.NoError(t, err)
		assert.Contains(t, string(env), "GH_PROMPT_DISABLED=1\n")
		assert.Contains(t, string(env), "NO_COLOR=1\n")
	})
	t.Run("a missing pull request is ErrPRNotFound", func(t *testing.T) {
		t.Parallel()
		path, _ := fakeGh(t, `echo 'GraphQL: Could not resolve to a PullRequest with the number of 999. (repository.pullRequest)' >&2; exit 1`)
		_, err := newTestClient(path).PRView(t.Context(), "github.com/a/b", 999)
		require.ErrorIs(t, err, ErrPRNotFound)
		_, ok := errors.AsType[*CommandError](err)
		assert.True(t, ok)
	})
	t.Run("a missing repo is ErrPRNotFound", func(t *testing.T) {
		t.Parallel()
		path, _ := fakeGh(t, `echo "GraphQL: Could not resolve to a Repository with the name 'a/b'. (repository)" >&2; exit 1`)
		_, err := newTestClient(path).PRView(t.Context(), "github.com/a/b", 1)
		assert.ErrorIs(t, err, ErrPRNotFound)
	})
	t.Run("signed out is ErrAuth", func(t *testing.T) {
		t.Parallel()
		path, _ := fakeGh(t, `echo 'To get started with GitHub CLI, please run:  gh auth login' >&2; exit 4`)
		_, err := newTestClient(path).PRView(t.Context(), "github.com/a/b", 1)
		assert.ErrorIs(t, err, ErrAuth)
	})
	t.Run("other failures are CommandErrors", func(t *testing.T) {
		t.Parallel()
		path, _ := fakeGh(t, `echo 'boom' >&2; exit 1`)
		_, err := newTestClient(path).PRView(t.Context(), "github.com/a/b", 1)
		ce, ok := errors.AsType[*CommandError](err)
		require.True(t, ok)
		assert.Equal(t, "boom", ce.Stderr)
		assert.Equal(t, 1, ce.ExitCode)
		assert.NotErrorIs(t, err, ErrAuth)
	})
	t.Run("no gh is ErrNotInstalled", func(t *testing.T) {
		t.Parallel()
		_, err := newTestClient(filepath.Join(t.TempDir(), "gh")).PRView(t.Context(), "github.com/a/b", 1)
		assert.ErrorIs(t, err, ErrNotInstalled)
	})
	t.Run("unreadable output is an error", func(t *testing.T) {
		t.Parallel()
		path, _ := fakeGh(t, `echo 'nope'`)
		_, err := newTestClient(path).PRView(t.Context(), "github.com/a/b", 1)
		assert.ErrorContains(t, err, "reading gh pr view's output")
	})
	t.Run("output without a head commit is an error", func(t *testing.T) {
		t.Parallel()
		path, _ := fakeGh(t, `echo '{"number":1}'`)
		_, err := newTestClient(path).PRView(t.Context(), "github.com/a/b", 1)
		assert.ErrorContains(t, err, "no headRefOid")
	})
}

func TestCommandError_Error(t *testing.T) {
	t.Run("includes stderr", func(t *testing.T) {
		e := &CommandError{Args: []string{"pr", "view"}, Stderr: "boom", Err: errors.New("exit status 1")}
		assert.Equal(t, "gh pr view: exit status 1: boom", e.Error())
	})
	t.Run("without stderr", func(t *testing.T) {
		e := &CommandError{Args: []string{"pr"}, Err: errors.New("exit status 1")}
		assert.Equal(t, "gh pr: exit status 1", e.Error())
	})
}
