package gate

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun(t *testing.T) {
	t.Parallel()

	run := func(req Request) (Result, error) {
		if req.Timeout == 0 {
			req.Timeout = 10 * time.Second
		}
		if req.Dir == "" {
			req.Dir = t.TempDir()
		}
		return New(os.Environ()).Run(t.Context(), req)
	}

	t.Run("trims trailing newlines from stdout", func(t *testing.T) {
		t.Parallel()
		res, err := run(Request{Script: `printf 'a\nb\n\n'`})
		require.NoError(t, err)
		assert.Equal(t, "a\nb", res.Stdout)
		assert.Equal(t, 0, res.ExitCode)
	})

	t.Run("reports a non-zero exit and stderr as a result", func(t *testing.T) {
		t.Parallel()
		res, err := run(Request{Script: `echo oops >&2; exit 3`})
		require.NoError(t, err)
		assert.Equal(t, 3, res.ExitCode)
		assert.Equal(t, "oops", res.Stderr)
	})

	t.Run("reports a signal as exit code -1", func(t *testing.T) {
		t.Parallel()
		res, err := run(Request{Script: `kill -9 $$`})
		require.NoError(t, err)
		assert.Equal(t, -1, res.ExitCode)
	})

	t.Run("passes the environment with request values replacing runner values", func(t *testing.T) {
		t.Parallel()
		runner := New([]string{"FOO=1", "PATH=" + os.Getenv("PATH")})
		res, err := runner.Run(t.Context(), Request{
			Script:  `printf %s "$WS_LOOP_NAME/$FOO"`,
			Dir:     t.TempDir(),
			Env:     []string{"WS_LOOP_NAME=deps", "FOO=2"},
			Timeout: 10 * time.Second,
		})
		require.NoError(t, err)
		assert.Equal(t, "deps/2", res.Stdout)
	})

	t.Run("runs in the request directory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		res, err := run(Request{Script: `pwd -P`, Dir: dir})
		require.NoError(t, err)
		want, err := filepath.EvalSymlinks(dir)
		require.NoError(t, err)
		assert.Equal(t, want, res.Stdout)
	})

	t.Run("returns at once with empty stdin", func(t *testing.T) {
		t.Parallel()
		res, err := run(Request{Script: `cat`})
		require.NoError(t, err)
		assert.Empty(t, res.Stdout)
	})

	t.Run("caps stdout at MaxOutput and marks it truncated", func(t *testing.T) {
		t.Parallel()
		res, err := run(Request{Script: `head -c 70000 /dev/zero | tr '\0' a`})
		require.NoError(t, err)
		assert.Len(t, res.Stdout, MaxOutput)
		assert.True(t, res.StdoutTruncated)
		assert.False(t, res.StderrTruncated)
	})

	t.Run("does not mark a short output truncated", func(t *testing.T) {
		t.Parallel()
		res, err := run(Request{Script: `echo short`})
		require.NoError(t, err)
		assert.False(t, res.StdoutTruncated)
	})

	t.Run("replaces invalid UTF-8 with U+FFFD", func(t *testing.T) {
		t.Parallel()
		res, err := run(Request{Script: `printf '\377ok'`})
		require.NoError(t, err)
		assert.Equal(t, "�ok", res.Stdout)
	})

	t.Run("kills the process group at the timeout", func(t *testing.T) {
		t.Parallel()
		start := time.Now()
		_, err := run(Request{Script: `sleep 60 & sleep 60`, Timeout: time.Second})
		require.ErrorIs(t, err, ErrTimedOut)
		assert.Less(t, time.Since(start), 8*time.Second)
	})

	t.Run("returns the context error when the context ends first", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		timer := time.AfterFunc(100*time.Millisecond, cancel)
		defer timer.Stop()
		_, err := New(os.Environ()).Run(ctx, Request{
			Script:  `sleep 60`,
			Dir:     t.TempDir(),
			Timeout: 10 * time.Second,
		})
		require.ErrorIs(t, err, context.Canceled)
		assert.NotErrorIs(t, err, ErrTimedOut)
	})

	t.Run("fails to run in a missing directory", func(t *testing.T) {
		t.Parallel()
		_, err := run(Request{Script: `true`, Dir: filepath.Join(t.TempDir(), "missing")})
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrTimedOut)
	})
}

func TestCapWriter(t *testing.T) {
	t.Parallel()

	t.Run("keeps up to the limit and reports the rest as dropped", func(t *testing.T) {
		t.Parallel()
		w := &capWriter{limit: 4}
		n, err := w.Write([]byte("abc"))
		require.NoError(t, err)
		assert.Equal(t, 3, n)
		n, err = w.Write([]byte("defg"))
		require.NoError(t, err)
		assert.Equal(t, 4, n)
		assert.Equal(t, "abcd", w.buf.String())
		assert.True(t, w.truncated)
	})
}

func TestClean(t *testing.T) {
	t.Parallel()

	t.Run("trims trailing carriage returns and newlines only", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, " a \n b", clean([]byte(" a \n b\r\n\r\n")))
	})

	t.Run("keeps leading whitespace", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "\n x", clean([]byte("\n x")))
	})
}

func TestMergeEnv(t *testing.T) {
	t.Parallel()

	t.Run("replaces base variables of the same name", func(t *testing.T) {
		t.Parallel()
		got := mergeEnv([]string{"A=1", "B=2", "C=3"}, []string{"B=x", "D=4"})
		assert.Equal(t, []string{"A=1", "C=3", "B=x", "D=4"}, got)
	})

	t.Run("keeps values containing '='", func(t *testing.T) {
		t.Parallel()
		got := mergeEnv([]string{"A=b=c"}, nil)
		assert.Equal(t, []string{"A=b=c"}, got)
	})

	t.Run("does not match a name that only starts the same", func(t *testing.T) {
		t.Parallel()
		got := mergeEnv([]string{"AB=1"}, []string{"A=2"})
		assert.Equal(t, []string{"AB=1", "A=2"}, got)
	})
}
