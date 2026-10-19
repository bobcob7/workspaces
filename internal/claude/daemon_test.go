package claude

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// daemonStatusOutput is `claude daemon status` output from Claude Code
// 2.1.303, shortened.
const daemonStatusOutput = "pid:     49849\n" +
	"version: 2.1.303\n" +
	"uptime:  56504s\n" +
	"origin:  transient — started on-demand by `claude` (pid 49109) in /Users/me/Dev/ws\n" +
	"config:  /Users/me/.claude/daemon.json\n" +
	"\n" +
	"bg sessions:\n" +
	"  sock dir:     /tmp/cc-daemon-501/4f1400ee\n" +
	"  pid:          1\n" +
	"  bg workers:   1 running (control.sock), 1 in roster.json\n"

// daemonNotRunning is `claude daemon status` output with no service, which
// exited 1 in Claude Code 2.1.303, shortened.
const daemonNotRunning = "not running\n" +
	"\n" +
	"bg sessions:\n" +
	"  sock dir:     /tmp/cc-daemon-501/3352915c\n" +
	"  bg workers:   0 in roster.json (control unreachable)\n"

func TestClient_DaemonStatus(t *testing.T) {
	t.Parallel()
	t.Run("running", func(t *testing.T) {
		t.Parallel()
		environ, args := argsFile(t)
		c := New(discardLogger(), fakeClaude(t, recordArgs+"; printf '%s' '"+daemonStatusOutput+"'"), environ)
		st, err := c.DaemonStatus(t.Context())
		require.NoError(t, err)
		assert.Equal(t, []string{"daemon", "status"}, args())
		assert.Equal(t, 49849, st.PID)
	})
	t.Run("not running", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), fakeClaude(t, "printf '%s' '"+daemonNotRunning+"'"), nil)
		st, err := c.DaemonStatus(t.Context())
		require.NoError(t, err)
		assert.False(t, st.Running)
	})
	t.Run("not running, with a failing exit status", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), fakeClaude(t, "printf '%s' '"+daemonNotRunning+"'; exit 1"), nil)
		st, err := c.DaemonStatus(t.Context())
		require.NoError(t, err)
		assert.False(t, st.Running)
	})
	t.Run("failure that mentions not running", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), fakeClaude(t, "echo 'error: keychain not running' >&2; exit 1"), nil)
		_, err := c.DaemonStatus(t.Context())
		var cmdErr *CommandError
		assert.ErrorAs(t, err, &cmdErr)
	})
	t.Run("failure", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), fakeClaude(t, "echo 'permission denied' >&2; exit 1"), nil)
		_, err := c.DaemonStatus(t.Context())
		var cmdErr *CommandError
		require.ErrorAs(t, err, &cmdErr)
		assert.Equal(t, "permission denied", cmdErr.Stderr)
	})
}

func TestClient_StopDaemon(t *testing.T) {
	t.Parallel()
	t.Run("keeps the sessions running", func(t *testing.T) {
		t.Parallel()
		environ, args := argsFile(t)
		c := New(discardLogger(), fakeClaude(t, recordArgs+"; echo stopped"), environ)
		require.NoError(t, c.StopDaemon(t.Context()))
		assert.Equal(t, []string{"daemon", "stop", "--any", "--keep-workers"}, args())
	})
	t.Run("failure", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), fakeClaude(t, "exit 1"), nil)
		var cmdErr *CommandError
		assert.ErrorAs(t, c.StopDaemon(t.Context()), &cmdErr)
	})
}

func TestClient_RunDaemon(t *testing.T) {
	t.Parallel()
	t.Run("starts a detached daemon with the environment", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		out := filepath.Join(t.TempDir(), "daemon")
		body := `echo "$* $WS_ENV $$ $(ps -o pgid= -p $$) $PWD" > "$OUT.tmp" && mv "$OUT.tmp" "$OUT"`
		environ := []string{"OUT=" + out, "WS_ENV=fresh", "PATH=/bin:/usr/bin", "HOME=" + home}
		c := New(discardLogger(), fakeClaude(t, body), environ)
		pid, err := c.RunDaemon(t.Context())
		require.NoError(t, err)
		var data []byte
		require.Eventually(t, func() bool {
			data, err = os.ReadFile(out)
			return err == nil
		}, 5*time.Second, 10*time.Millisecond)
		fields := strings.Fields(string(data))
		require.Len(t, fields, 6)
		assert.Equal(t, []string{"daemon", "run", "fresh"}, fields[:3])
		assert.Equal(t, strconv.Itoa(pid), fields[3])
		assert.Equal(t, fields[3], fields[4], "the daemon leads its own process group")
		assert.Equal(t, resolved(home), resolved(fields[5]), "the daemon runs in HOME")
	})
	t.Run("doesn't inherit ws's stdio", func(t *testing.T) {
		t.Parallel()
		out := filepath.Join(t.TempDir(), "daemon")
		body := `if [ -t 0 ] || [ -p /dev/fd/0 ] || [ -p /dev/fd/1 ] || [ -p /dev/fd/2 ]; then r=inherited; else r=detached; fi; ` +
			`echo "$r" > "$OUT.tmp" && mv "$OUT.tmp" "$OUT"`
		c := New(discardLogger(), fakeClaude(t, body), []string{"OUT=" + out, "PATH=/bin:/usr/bin"})
		_, err := c.RunDaemon(t.Context())
		require.NoError(t, err)
		var data []byte
		require.Eventually(t, func() bool {
			data, err = os.ReadFile(out)
			return err == nil
		}, 5*time.Second, 10*time.Millisecond)
		assert.Equal(t, "detached\n", string(data))
	})
	t.Run("checks the version first", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), fakeClaude(t, ""), []string{"FAKE_VERSION=2.0.0"})
		_, err := c.RunDaemon(t.Context())
		assert.ErrorIs(t, err, ErrTooOld)
	})
	t.Run("missing claude", func(t *testing.T) {
		t.Parallel()
		c := New(discardLogger(), filepath.Join(t.TempDir(), "no-claude"), nil)
		_, err := c.RunDaemon(t.Context())
		assert.ErrorIs(t, err, ErrNotInstalled)
	})
	t.Run("claude can't be started", func(t *testing.T) {
		t.Parallel()
		path := fakeClaude(t, "")
		c := New(discardLogger(), path, nil)
		require.NoError(t, c.ensureVersion(t.Context()))
		require.NoError(t, os.Chmod(path, 0o600))
		_, err := c.RunDaemon(t.Context())
		require.ErrorContains(t, err, "starting claude daemon run")
		assert.NotErrorIs(t, err, ErrNotInstalled)
	})
}

func TestParseDaemonStatus(t *testing.T) {
	t.Run("transient daemon", func(t *testing.T) {
		st, err := parseDaemonStatus([]byte(daemonStatusOutput))
		require.NoError(t, err)
		assert.Equal(t, DaemonStatus{
			Running: true,
			PID:     49849,
			Version: "2.1.303",
			Uptime:  56504 * time.Second,
			Origin:  "transient — started on-demand by `claude` (pid 49109) in /Users/me/Dev/ws",
		}, st)
	})
	t.Run("foreground daemon", func(t *testing.T) {
		st, err := parseDaemonStatus([]byte("pid: 77\nuptime: 4s\norigin: foreground\n"))
		require.NoError(t, err)
		assert.Equal(t, DaemonStatus{Running: true, PID: 77, Uptime: 4 * time.Second, Origin: "foreground"}, st)
	})
	t.Run("not running", func(t *testing.T) {
		st, err := parseDaemonStatus([]byte(daemonNotRunning))
		require.NoError(t, err)
		assert.Equal(t, DaemonStatus{}, st)
	})
	t.Run("rejects not running below the first line", func(t *testing.T) {
		_, err := parseDaemonStatus([]byte("warning: x\nnot running\n"))
		assert.ErrorIs(t, err, ErrUnexpectedOutput)
	})
	t.Run("rejects output without a pid", func(t *testing.T) {
		_, err := parseDaemonStatus([]byte("version: 2.1.303\n"))
		assert.ErrorIs(t, err, ErrUnexpectedOutput)
	})
	t.Run("rejects a bad pid", func(t *testing.T) {
		_, err := parseDaemonStatus([]byte("pid: many\n"))
		assert.ErrorIs(t, err, ErrUnexpectedOutput)
	})
	t.Run("rejects a bad uptime", func(t *testing.T) {
		_, err := parseDaemonStatus([]byte("pid: 1\nuptime: a while\n"))
		assert.ErrorIs(t, err, ErrUnexpectedOutput)
	})
}

func TestClient_daemonDir(t *testing.T) {
	t.Run("HOME", func(t *testing.T) {
		assert.Equal(t, "/home/me", New(discardLogger(), "claude", []string{"HOME=/home/me"}).daemonDir())
	})
	t.Run("root without HOME", func(t *testing.T) {
		assert.Equal(t, "/", New(discardLogger(), "claude", nil).daemonDir())
	})
}

func TestReportsNotRunning(t *testing.T) {
	t.Run("first line", func(t *testing.T) {
		assert.True(t, reportsNotRunning([]byte(daemonNotRunning)))
	})
	t.Run("without a newline", func(t *testing.T) {
		assert.True(t, reportsNotRunning([]byte("not running")))
	})
	t.Run("running", func(t *testing.T) {
		assert.False(t, reportsNotRunning([]byte(daemonStatusOutput)))
	})
	t.Run("elsewhere in a line", func(t *testing.T) {
		assert.False(t, reportsNotRunning([]byte("keychain not running\n")))
	})
}

func TestStatusFields(t *testing.T) {
	t.Run("top-level lines only", func(t *testing.T) {
		assert.Equal(t, map[string]string{"pid": "1", "bg sessions": ""},
			statusFields([]byte("pid: 1\n\nbg sessions:\n  pid: 2\n\tversion: 3\n")))
	})
	t.Run("keeps colons in values", func(t *testing.T) {
		assert.Equal(t, map[string]string{"log": "/a:b"}, statusFields([]byte("log: /a:b\n")))
	})
	t.Run("skips lines without a colon", func(t *testing.T) {
		assert.Empty(t, statusFields([]byte("not running\n")))
	})
}
