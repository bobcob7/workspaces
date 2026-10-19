package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// backgroundedOutput is what `claude --bg` printed in Claude Code 2.1.303
// for a session started without a prompt.
const backgroundedOutput = "backgrounded · f381de70 · fix-login (idle — send a prompt to start)\n" +
	"  claude agents             list sessions\n" +
	"  claude attach f381de70    open in this terminal\n" +
	"  claude logs f381de70      show recent output\n" +
	"  claude stop f381de70      stop this session\n"

// startedIn returns a fakeClaude body that prints junk for --bg and lists
// one background session in dir.
func startedIn(dir string) string {
	return `if [ "$1" = --bg ]; then echo started; exit 0; fi
printf '%s' '[{"id":"b2","kind":"background","cwd":"` + dir + `"}]'`
}

func TestClient_Start(t *testing.T) {
	t.Parallel()
	t.Run("passes ws's options, then the caller's", func(t *testing.T) {
		t.Parallel()
		environ, args := argsFile(t)
		c := New(discardLogger(), fakeClaude(t, recordArgs+"; printf '%s' '"+backgroundedOutput+"'"), environ)
		_, err := c.Start(t.Context(), StartOptions{Dir: t.TempDir(), Name: "fix-login", Args: []string{"--model", "opus", "fix it"}})
		require.NoError(t, err)
		assert.Equal(t, []string{
			"--bg", "-n", "fix-login", "--settings", `{"worktree":{"bgIsolation":"none"}}`,
			"--model", "opus", "fix it",
		}, args())
	})
	t.Run("no name", func(t *testing.T) {
		t.Parallel()
		environ, args := argsFile(t)
		c := New(discardLogger(), fakeClaude(t, recordArgs+"; printf '%s' '"+backgroundedOutput+"'"), environ)
		_, err := c.Start(t.Context(), StartOptions{Dir: t.TempDir()})
		require.NoError(t, err)
		assert.Equal(t, []string{"--bg", "--settings", `{"worktree":{"bgIsolation":"none"}}`}, args())
	})
	t.Run("runs in the directory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		c := New(discardLogger(), fakeClaude(t, `echo "backgrounded · a1 · x"; pwd > "$ARGS"`), []string{"ARGS=" + filepath.Join(dir, "pwd")})
		_, err := c.Start(t.Context(), StartOptions{Dir: dir})
		require.NoError(t, err)
		data, err := os.ReadFile(filepath.Join(dir, "pwd"))
		require.NoError(t, err)
		assert.Equal(t, resolved(dir), strings.TrimSpace(string(data)))
	})
	t.Run("returns the printed id", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), fakeClaude(t, "printf '%s' '"+backgroundedOutput+"'"), nil)
		id, err := c.Start(t.Context(), StartOptions{Dir: t.TempDir()})
		require.NoError(t, err)
		assert.Equal(t, "f381de70", id)
	})
	t.Run("looks the id up when none is printed", func(t *testing.T) {
		t.Parallel()
		dir := resolved(t.TempDir())
		c := New(discardLogger(), fakeClaude(t, startedIn(dir)), nil)
		id, err := c.Start(t.Context(), StartOptions{Dir: dir})
		require.NoError(t, err)
		assert.Equal(t, "b2", id)
	})
	t.Run("refuses the caller's --settings", func(t *testing.T) {
		t.Parallel()
		environ, count := countCalls(t)
		c := New(discardLogger(), fakeClaude(t, "echo ran"), environ)
		_, err := c.Start(t.Context(), StartOptions{Dir: t.TempDir(), Args: []string{"--settings", "{}", "fix it"}})
		require.ErrorIs(t, err, ErrSettingsArg)
		assert.Zero(t, count(), "claude didn't run")
	})
	t.Run("untrusted directory", func(t *testing.T) {
		t.Parallel()
		body := "echo 'Workspace not trusted. Run `claude` in /w once and accept the trust prompt, then retry.' >&2; exit 1"
		c := New(discardLogger(), fakeClaude(t, body), nil)
		_, err := c.Start(t.Context(), StartOptions{Dir: t.TempDir()})
		assert.ErrorIs(t, err, ErrUntrusted)
	})
	t.Run("other failure", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), fakeClaude(t, "echo 'error: bad flag' >&2; exit 1"), nil)
		_, err := c.Start(t.Context(), StartOptions{Dir: t.TempDir()})
		var cmdErr *CommandError
		require.ErrorAs(t, err, &cmdErr)
		assert.Equal(t, "error: bad flag", cmdErr.Stderr)
	})
}

func TestHasSettingsArg(t *testing.T) {
	t.Run("separate value", func(t *testing.T) {
		assert.True(t, hasSettingsArg([]string{"--model", "opus", "--settings", "s.json"}))
	})
	t.Run("joined value", func(t *testing.T) {
		assert.True(t, hasSettingsArg([]string{`--settings={"a":1}`}))
	})
	t.Run("none", func(t *testing.T) {
		assert.False(t, hasSettingsArg([]string{"--model", "opus", "fix it"}))
	})
	t.Run("a similar option", func(t *testing.T) {
		assert.False(t, hasSettingsArg([]string{"--settings-file", "x", "--setting-sources", "user"}))
	})
	t.Run("after --", func(t *testing.T) {
		assert.False(t, hasSettingsArg([]string{"--", "--settings"}))
	})
	t.Run("empty", func(t *testing.T) {
		assert.False(t, hasSettingsArg(nil))
	})
}

func TestParseBackgrounded(t *testing.T) {
	t.Run("with hints after it", func(t *testing.T) {
		id, ok := parseBackgrounded([]byte(backgroundedOutput))
		require.True(t, ok)
		assert.Equal(t, "f381de70", id)
	})
	t.Run("with a prompt", func(t *testing.T) {
		id, ok := parseBackgrounded([]byte("backgrounded · 5d8d652c · ws-bgsid\n"))
		require.True(t, ok)
		assert.Equal(t, "5d8d652c", id)
	})
	t.Run("id only", func(t *testing.T) {
		id, ok := parseBackgrounded([]byte("backgrounded · 5d8d652c"))
		require.True(t, ok)
		assert.Equal(t, "5d8d652c", id)
	})
	t.Run("after a warning", func(t *testing.T) {
		id, ok := parseBackgrounded([]byte("warning: --bg manages the session id; ignoring --session-id\nbackgrounded · 5d8d652c · x\n"))
		require.True(t, ok)
		assert.Equal(t, "5d8d652c", id)
	})
	t.Run("no backgrounded line", func(t *testing.T) {
		_, ok := parseBackgrounded([]byte("started\n  claude agents  list sessions\n"))
		assert.False(t, ok)
	})
	t.Run("ascii dots aren't the separator", func(t *testing.T) {
		_, ok := parseBackgrounded([]byte("backgrounded . 5d8d652c . x\n"))
		assert.False(t, ok)
	})
	t.Run("an id that is an option", func(t *testing.T) {
		_, ok := parseBackgrounded([]byte("backgrounded · --all · x\n"))
		assert.False(t, ok)
	})
	t.Run("no id", func(t *testing.T) {
		_, ok := parseBackgrounded([]byte("backgrounded · · x"))
		assert.False(t, ok)
	})
	t.Run("empty", func(t *testing.T) {
		_, ok := parseBackgrounded(nil)
		assert.False(t, ok)
	})
}

func TestClient_findStarted(t *testing.T) {
	t.Parallel()
	t.Run("lists every session under the directory", func(t *testing.T) {
		t.Parallel()
		environ, args := argsFile(t)
		c := New(discardLogger(), fakeClaude(t, recordArgs+"; echo '[]'"), environ)
		_, err := c.findStarted(t.Context(), "/w/a")
		require.Error(t, err)
		assert.Equal(t, []string{"agents", "--json", "--all", "--cwd", "/w/a"}, args())
	})
	t.Run("matches the directory through symlinks", func(t *testing.T) {
		t.Parallel()
		dir := resolved(t.TempDir())
		link := filepath.Join(t.TempDir(), "link")
		require.NoError(t, os.Symlink(dir, link))
		c := New(discardLogger(), fakeClaude(t, startedIn(dir)), nil)
		id, err := c.findStarted(t.Context(), link)
		require.NoError(t, err)
		assert.Equal(t, "b2", id)
	})
	t.Run("ignores sessions below the directory", func(t *testing.T) {
		t.Parallel()
		body := `printf '%s' '[{"id":"b1","kind":"background","cwd":"/w/a/repo"},{"id":"b2","kind":"background","cwd":"/w/a"}]'`
		c := New(discardLogger(), fakeClaude(t, body), nil)
		id, err := c.findStarted(t.Context(), "/w/a")
		require.NoError(t, err)
		assert.Equal(t, "b2", id)
	})
	t.Run("none", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), fakeClaude(t, "echo '[]'"), nil)
		_, err := c.findStarted(t.Context(), "/w/a")
		assert.ErrorIs(t, err, ErrUnexpectedOutput)
	})
	t.Run("more than one", func(t *testing.T) {
		t.Parallel()
		body := `printf '%s' '[{"id":"b1","kind":"background","cwd":"/w/a"},{"id":"b2","kind":"background","cwd":"/w/a"}]'`
		c := New(discardLogger(), fakeClaude(t, body), nil)
		_, err := c.findStarted(t.Context(), "/w/a")
		assert.ErrorIs(t, err, ErrUnexpectedOutput)
	})
	t.Run("listing fails", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), fakeClaude(t, "exit 1"), nil)
		_, err := c.findStarted(t.Context(), "/w/a")
		var cmdErr *CommandError
		assert.ErrorAs(t, err, &cmdErr)
	})
}

func TestResolved(t *testing.T) {
	t.Parallel()
	t.Run("follows symlinks", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		link := filepath.Join(t.TempDir(), "link")
		require.NoError(t, os.Symlink(dir, link))
		want, err := filepath.EvalSymlinks(dir)
		require.NoError(t, err)
		assert.Equal(t, want, resolved(link))
	})
	t.Run("cleans a missing path", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "/no/such/dir", resolved("/no/such//dir/"))
	})
}
