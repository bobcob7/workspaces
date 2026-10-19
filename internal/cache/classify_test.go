package cache

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bobcob7/workspaces/internal/git"
)

// gitFailure is a git command that failed with stderr.
func gitFailure(stderr string) error {
	return &git.CommandError{Args: []string{"ls-remote"}, ExitCode: 128, Stderr: stderr, Err: errTest}
}

func TestRepoMissing(t *testing.T) {
	t.Run("GitHub over SSH", func(t *testing.T) {
		assert.True(t, repoMissing(gitFailure("ERROR: Repository not found.\nfatal: Could not read from remote repository.")))
	})
	t.Run("GitHub over HTTPS with credentials", func(t *testing.T) {
		assert.True(t, repoMissing(gitFailure("remote: Repository not found.\nfatal: repository 'https://github.com/o/r.git/' not found")))
	})
	t.Run("Codeberg", func(t *testing.T) {
		assert.True(t, repoMissing(gitFailure("remote: Not found.")))
	})
	t.Run("HTTP 404", func(t *testing.T) {
		assert.True(t, repoMissing(gitFailure("fatal: repository 'https://example.com/o/r.git/' not found")))
	})
	t.Run("missing path", func(t *testing.T) {
		assert.True(t, repoMissing(gitFailure("fatal: '/srv/up/o/r.git' does not appear to be a git repository")))
	})
	t.Run("login asked for, without a terminal", func(t *testing.T) {
		assert.True(t, repoMissing(gitFailure("fatal: could not read Username for 'https://github.com': terminal prompts disabled")))
	})
	t.Run("wrapped", func(t *testing.T) {
		assert.True(t, repoMissing(fmt.Errorf("probing: %w", gitFailure("remote: Not found."))))
	})
	t.Run("DNS failure isn't missing", func(t *testing.T) {
		assert.False(t, repoMissing(gitFailure("fatal: unable to access 'https://x.invalid/o/r.git/': Could not resolve host: x.invalid")))
	})
	t.Run("refused connection isn't missing", func(t *testing.T) {
		assert.False(t, repoMissing(gitFailure("fatal: unable to access 'https://127.0.0.1:9/o/r.git/': Failed to connect to 127.0.0.1 port 9 after 0 ms: Couldn't connect to server")))
	})
	t.Run("SSH host unreachable isn't missing", func(t *testing.T) {
		assert.False(t, repoMissing(gitFailure("ssh: connect to host 127.0.0.1 port 9: Connection refused\nfatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights\nand the repository exists.")))
	})
	t.Run("TLS failure isn't missing", func(t *testing.T) {
		assert.False(t, repoMissing(gitFailure("fatal: unable to access 'https://h/o/r.git/': SSL certificate problem: self signed certificate")))
	})
	t.Run("rejected login isn't missing", func(t *testing.T) {
		assert.False(t, repoMissing(gitFailure("remote: Invalid username or token.\nfatal: Authentication failed for 'https://github.com/o/r.git/'")))
	})
	t.Run("unknown message isn't missing", func(t *testing.T) {
		assert.False(t, repoMissing(gitFailure("fatal: something new")))
	})
	t.Run("non-git error isn't missing", func(t *testing.T) {
		assert.False(t, repoMissing(errTest))
	})
}

func TestObjectMissing(t *testing.T) {
	t.Run("not our ref", func(t *testing.T) {
		assert.True(t, objectMissing(gitFailure("fatal: remote error: upload-pack: not our ref 0123456789abcdef0123456789abcdef01234567")))
	})
	t.Run("couldn't find remote ref", func(t *testing.T) {
		assert.True(t, objectMissing(gitFailure("fatal: couldn't find remote ref refs/heads/x")))
	})
	t.Run("unreachable upstream isn't missing", func(t *testing.T) {
		assert.False(t, objectMissing(gitFailure("fatal: unable to access 'https://x.invalid/o/r.git/': Could not resolve host: x.invalid")))
	})
	t.Run("non-git error isn't missing", func(t *testing.T) {
		assert.False(t, objectMissing(errTest))
	})
}

func TestStderrMatches(t *testing.T) {
	t.Run("matches stderr", func(t *testing.T) {
		assert.True(t, stderrMatches(gitFailure("x not our ref y"), objectMissingPattern))
	})
	t.Run("no match", func(t *testing.T) {
		assert.False(t, stderrMatches(gitFailure("abc"), objectMissingPattern))
	})
	t.Run("ignores the error text outside stderr", func(t *testing.T) {
		err := &git.CommandError{Args: []string{"not our ref"}, Err: errTest}
		assert.False(t, stderrMatches(err, objectMissingPattern))
	})
}

func TestIsGitUnusable(t *testing.T) {
	t.Run("not installed", func(t *testing.T) {
		assert.True(t, isGitUnusable(fmt.Errorf("x: %w", git.ErrNotInstalled)))
	})
	t.Run("too old", func(t *testing.T) {
		assert.True(t, isGitUnusable(git.ErrTooOld))
	})
	t.Run("a failed command", func(t *testing.T) {
		assert.False(t, isGitUnusable(gitFailure("fatal: x")))
	})
}
