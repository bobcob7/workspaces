package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failOnceWriter fails its first write and records the rest.
type failOnceWriter struct {
	failed bool
	buf    bytes.Buffer
}

func (w *failOnceWriter) Write(p []byte) (int, error) {
	if !w.failed {
		w.failed = true
		return 0, errTest
	}
	return w.buf.Write(p)
}

func TestApp_writeError(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		var stderr bytes.Buffer
		app := &App{stderr: &stderr}
		app.writeError(newError(codeRepoNotFound, errTest))
		assert.Equal(t, "ws: boom\n", stderr.String())
	})
	t.Run("json is one line", func(t *testing.T) {
		var stderr bytes.Buffer
		app := &App{stderr: &stderr, global: globalOptions{json: true}}
		app.writeError(newError(codeRepoNotFound, errTest))
		assert.JSONEq(t, `{"error":{"code":"repo_not_found","message":"boom","details":null}}`, stderr.String())
		assert.Equal(t, 1, strings.Count(stderr.String(), "\n"))
	})
	t.Run("falls back to text when the JSON write fails", func(t *testing.T) {
		w := &failOnceWriter{}
		app := &App{stderr: w, global: globalOptions{json: true}}
		app.writeError(newError(codeRepoNotFound, errTest))
		assert.Equal(t, "ws: boom\n", w.buf.String())
	})
	t.Run("json includes details", func(t *testing.T) {
		var stderr bytes.Buffer
		app := &App{stderr: &stderr, global: globalOptions{json: true}}
		err := newError(codeRepoAmbiguous, errTest)
		err.details = map[string]any{"candidates": []string{"a", "b"}}
		app.writeError(err)
		var body struct {
			Error struct {
				Details map[string][]string `json:"details"`
			} `json:"error"`
		}
		require.NoError(t, json.Unmarshal(stderr.Bytes(), &body))
		assert.Equal(t, []string{"a", "b"}, body.Error.Details["candidates"])
	})
}

func TestApp_writeJSON(t *testing.T) {
	t.Run("compact otherwise", func(t *testing.T) {
		var stdout bytes.Buffer
		app := &App{stdout: &stdout}
		require.NoError(t, app.writeJSON(map[string]any{"workspaces": []string{}, "warnings": []string{}}))
		assert.JSONEq(t, `{"warnings":[],"workspaces":[]}`, stdout.String())
		assert.Equal(t, 1, strings.Count(stdout.String(), "\n"))
		assert.NotContains(t, stdout.String(), " ")
	})
	t.Run("indented on a terminal", func(t *testing.T) {
		var stdout bytes.Buffer
		app := &App{stdout: &stdout, indentJSON: true}
		require.NoError(t, app.writeJSON(map[string]int{"a": 1}))
		assert.Equal(t, "{\n  \"a\": 1\n}\n", stdout.String())
	})
	t.Run("unencodable value is an error", func(t *testing.T) {
		app := &App{stdout: &bytes.Buffer{}}
		assert.Error(t, app.writeJSON(make(chan int)))
	})
}

func TestIsTerminal(t *testing.T) {
	t.Parallel()
	t.Run("buffer is not a terminal", func(t *testing.T) {
		t.Parallel()
		assert.False(t, isTerminal(&bytes.Buffer{}))
	})
	t.Run("regular file is not a terminal", func(t *testing.T) {
		t.Parallel()
		f, err := os.Create(filepath.Join(t.TempDir(), "out"))
		require.NoError(t, err)
		t.Cleanup(func() { _ = f.Close() })
		assert.False(t, isTerminal(f))
	})
	t.Run("character device counts as a terminal", func(t *testing.T) {
		t.Parallel()
		f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		require.NoError(t, err)
		t.Cleanup(func() { _ = f.Close() })
		assert.True(t, isTerminal(f))
	})
}
