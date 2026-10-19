// Package gittest builds local upstream repos for tests. They're reached
// through https://example.test/ by a git insteadOf rule, so code under test
// uses the same specs and URLs as with a real forge (ADR 0020).
package gittest

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/git"
)

// Host is the fake forge every Upstream answers for.
const Host = "example.test"

// emptyTree is the empty tree's hash. Repo stores it as an object, so
// commits need no work tree and `git fsck --full` finds every object.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// Upstream is a directory of bare repos served as https://example.test/.
type Upstream struct {
	// Dir holds the bare repos, as <owner>/<name>.git.
	Dir string
	// Environ runs git with the insteadOf rule and a test identity, and
	// without the system or user git config.
	Environ []string
	runner  *git.Runner
}

// NewUpstream creates an empty Upstream under tb.TempDir().
func NewUpstream(tb testing.TB) *Upstream {
	tb.Helper()
	base := tb.TempDir()
	dir := filepath.Join(base, "up")
	require.NoError(tb, os.MkdirAll(dir, 0o750))
	gitconfig := filepath.Join(base, "gitconfig")
	content := "[user]\n\tname = ws test\n\temail = test@example.test\n" +
		"[init]\n\tdefaultBranch = main\n" +
		"[url \"" + dir + "/\"]\n\tinsteadOf = https://" + Host + "/\n"
	require.NoError(tb, os.WriteFile(gitconfig, []byte(content), 0o600))
	environ := append(os.Environ(), "GIT_CONFIG_GLOBAL="+gitconfig, "GIT_CONFIG_NOSYSTEM=1")
	return &Upstream{Dir: dir, Environ: environ, runner: git.NewRunner(slog.New(slog.DiscardHandler), "git", environ)}
}

// URL is the https URL code under test uses for owner/name.
func URL(owner, name string) string {
	return "https://" + Host + "/" + owner + "/" + name + ".git"
}

// Path is the bare repo behind owner/name.
func (u *Upstream) Path(owner, name string) string {
	return filepath.Join(u.Dir, owner, name+".git")
}

// Repo creates a bare repo with one commit on main and returns that commit.
func (u *Upstream) Repo(tb testing.TB, owner, name string) string {
	tb.Helper()
	path := u.Path(owner, name)
	require.NoError(tb, os.MkdirAll(filepath.Dir(path), 0o750))
	u.Git(tb, u.Dir, "init", "-q", "--bare", "-b", "main", path)
	u.Git(tb, path, "hash-object", "-w", "-t", "tree", os.DevNull)
	return u.Commit(tb, owner, name, "refs/heads/main")
}

// Commit adds an empty commit on top of ref, creating ref if it doesn't
// exist, and returns the new commit.
func (u *Upstream) Commit(tb testing.TB, owner, name, ref string) string {
	tb.Helper()
	path := u.Path(owner, name)
	args := []string{"commit-tree", emptyTree, "-m", "commit on " + ref}
	if parent, ok := u.rev(tb, path, ref); ok {
		args = append(args, "-p", parent)
	}
	commit := u.Git(tb, path, args...)
	u.Git(tb, path, "update-ref", ref, commit)
	return commit
}

// Tag creates a lightweight tag at rev.
func (u *Upstream) Tag(tb testing.TB, owner, name, tag, rev string) {
	tb.Helper()
	u.Git(tb, u.Path(owner, name), "tag", tag, rev)
}

// Git runs git in dir with the Upstream's environment and returns its
// trimmed stdout.
func (u *Upstream) Git(tb testing.TB, dir string, args ...string) string {
	tb.Helper()
	out, err := u.runner.Run(tb.Context(), dir, args...)
	require.NoError(tb, err)
	return strings.TrimSpace(string(out))
}

func (u *Upstream) rev(tb testing.TB, path, ref string) (string, bool) {
	tb.Helper()
	out, err := u.runner.Run(tb.Context(), path, "rev-parse", "--verify", "-q", ref)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}
