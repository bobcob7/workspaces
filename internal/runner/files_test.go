package runner

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/agent"
)

// sampleRun is a run with two endpoints in its history.
func sampleRun() Run {
	created := time.Date(2026, 11, 13, 17, 45, 58, 0, time.UTC)
	return Run{
		Version:        1,
		Session:        "rn-3f2a9c1e",
		ModelAlias:     "qwen9b",
		Model:          "qwen/qwen3.5-9b",
		Endpoint:       "gpu",
		URL:            "http://gpu-a.inference.svc:1234",
		API:            "openai",
		ContextTokens:  65536,
		MaxOutput:      4096,
		Temperature:    0.7,
		Reasoning:      "none",
		RequestTimeout: 10 * time.Minute,
		IdleTimeout:    90 * time.Second,
		Role:           "implementer",
		Pass:           []string{"PASS"},
		Fail:           []string{"FAIL"},
		MaxSteps:       80,
		Timeout:        60 * time.Minute,
		MaxTokens:      4000000,
		Branch:         "feat/x-t1",
		MCPServers:     []string{},
		MCPTools:       []string{"*"},
		MCPDeny:        []string{},
		MCPUpfront:     8,
		Prompt:         "Implement T1.\n",
		TraceID:        "4bf92f3577b34da6a3ce929d0e0e4736",
		CreatedAt:      created,
		EndpointHistory: []EndpointEntry{
			{Since: created, Step: 0, Reason: "spawn", Endpoint: "gpu", URL: "http://gpu-a.inference.svc:1234", API: "openai", Model: "qwen/qwen3.5-9b", ContextTokens: 65536},
			{Since: created.Add(time.Minute), Step: 12, Reason: "retry", Endpoint: "gpu-b", URL: "http://gpu-b.inference.svc:1234", API: "openai", Model: "qwen/qwen3.5-9b", ContextTokens: 65536},
		},
	}
}

func TestFilesFor(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	files := FilesFor(state, "ws-1")
	assert.Equal(t, filepath.Join(state, "ws-1", "runner"), files.Dir)
	assert.Equal(t, "ws-1", files.ID)
}

func TestFiles_WriteRun(t *testing.T) {
	t.Parallel()
	t.Run("round trips every key and the endpoint history", func(t *testing.T) {
		files := FilesFor(t.TempDir(), "ws-1")
		want := sampleRun()
		require.NoError(t, files.WriteRun(&want))
		got, err := files.ReadRun()
		require.NoError(t, err)
		assert.Equal(t, want.Session, got.Session)
		assert.Equal(t, want.RequestTimeout, got.RequestTimeout)
		assert.Equal(t, want.Timeout, got.Timeout)
		assert.InEpsilon(t, want.Temperature, got.Temperature, 1e-9)
		assert.Equal(t, want.Pass, got.Pass)
		assert.Equal(t, want.EndpointHistory, got.EndpointHistory)
	})
	t.Run("writes the file mode 0600", func(t *testing.T) {
		files := FilesFor(t.TempDir(), "ws-1")
		want := sampleRun()
		require.NoError(t, files.WriteRun(&want))
		info, err := os.Stat(files.path(runFile))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(fileMode), info.Mode().Perm())
	})
	t.Run("fails when the directory can't be created", func(t *testing.T) {
		parent := t.TempDir()
		blocker := filepath.Join(parent, "blocked")
		require.NoError(t, os.WriteFile(blocker, nil, fileMode))
		files := Files{Dir: filepath.Join(blocker, "runner")}
		want := sampleRun()
		require.Error(t, files.WriteRun(&want))
	})
}

func TestFiles_ReadRun(t *testing.T) {
	t.Parallel()
	t.Run("a missing run is an error", func(t *testing.T) {
		_, err := FilesFor(t.TempDir(), "ws-1").ReadRun()
		require.Error(t, err)
		assert.ErrorIs(t, err, fs.ErrNotExist)
	})
	t.Run("a file that doesn't parse is an error", func(t *testing.T) {
		files := FilesFor(t.TempDir(), "ws-1")
		require.NoError(t, os.MkdirAll(files.Dir, dirMode))
		require.NoError(t, os.WriteFile(files.path(runFile), []byte("not = [toml"), fileMode))
		_, err := files.ReadRun()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "parsing run.toml")
	})
}

func TestFiles_AppendEndpoint(t *testing.T) {
	t.Parallel()
	t.Run("adds an entry and keeps the other keys", func(t *testing.T) {
		files := FilesFor(t.TempDir(), "ws-1")
		want := sampleRun()
		want.EndpointHistory = want.EndpointHistory[:1]
		require.NoError(t, files.WriteRun(&want))
		entry := EndpointEntry{Reason: "resume", Endpoint: "gpu-c", URL: "http://c", API: "openai", Model: "qwen/qwen3.5-9b"}
		require.NoError(t, files.AppendEndpoint(entry))
		got, err := files.ReadRun()
		require.NoError(t, err)
		require.Len(t, got.EndpointHistory, 2)
		assert.Equal(t, "gpu-c", got.Current().Endpoint)
		assert.Equal(t, want.Prompt, got.Prompt)
		assert.Equal(t, want.MaxSteps, got.MaxSteps)
	})
	t.Run("fails without a run", func(t *testing.T) {
		require.Error(t, FilesFor(t.TempDir(), "ws-1").AppendEndpoint(EndpointEntry{}))
	})
}

func TestFiles_ReadStatus(t *testing.T) {
	t.Parallel()
	t.Run("a missing status is fs.ErrNotExist", func(t *testing.T) {
		_, err := FilesFor(t.TempDir(), "ws-1").ReadStatus()
		assert.ErrorIs(t, err, fs.ErrNotExist)
	})
	t.Run("invalid JSON is an error", func(t *testing.T) {
		files := FilesFor(t.TempDir(), "ws-1")
		require.NoError(t, os.MkdirAll(files.Dir, dirMode))
		require.NoError(t, os.WriteFile(files.path(statusFile), []byte("{"), fileMode))
		_, err := files.ReadStatus()
		require.Error(t, err)
		assert.NotErrorIs(t, err, fs.ErrNotExist)
	})
}

func TestFiles_WriteStatus(t *testing.T) {
	t.Parallel()
	t.Run("writes the version and reads back", func(t *testing.T) {
		files := FilesFor(t.TempDir(), "ws-1")
		st := Status{Session: "rn-3f2a9c1e", State: "working", Step: 3, Usage: agent.Usage{Requests: 3}}
		require.NoError(t, files.WriteStatus(&st))
		got, err := files.ReadStatus()
		require.NoError(t, err)
		assert.Equal(t, statusVersion, got.Version)
		assert.Equal(t, 3, got.Step)
		assert.Equal(t, 3, got.Usage.Requests)
		assert.Nil(t, got.Exit)
	})
	t.Run("fails when the directory can't be created", func(t *testing.T) {
		parent := t.TempDir()
		blocker := filepath.Join(parent, "blocked")
		require.NoError(t, os.WriteFile(blocker, nil, fileMode))
		st := Status{}
		require.Error(t, Files{Dir: filepath.Join(blocker, "runner")}.WriteStatus(&st))
	})
}

func TestFiles_RequestStop(t *testing.T) {
	t.Parallel()
	t.Run("creates the stop file that StopRequested sees", func(t *testing.T) {
		files := FilesFor(t.TempDir(), "ws-1")
		assert.False(t, files.StopRequested())
		require.NoError(t, files.RequestStop())
		assert.True(t, files.StopRequested())
	})
	t.Run("fails when the directory can't be created", func(t *testing.T) {
		parent := t.TempDir()
		blocker := filepath.Join(parent, "blocked")
		require.NoError(t, os.WriteFile(blocker, nil, fileMode))
		require.Error(t, Files{Dir: filepath.Join(blocker, "runner")}.RequestStop())
	})
}

func TestFiles_Paths(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	files := FilesFor(state, "ws-1")
	assert.Equal(t, filepath.Join(state, "ws-1", "runner", "transcript.jsonl"), files.Transcript())
	assert.Equal(t, filepath.Join(state, "ws-1", "runner", "runner.log"), files.Log())
}

func TestFiles_UsagePath(t *testing.T) {
	t.Parallel()
	t.Run("without a parent it is in the runner directory", func(t *testing.T) {
		state := t.TempDir()
		path, err := FilesFor(state, "ws-1").UsagePath(&Run{}, &resultStoreMock{})
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(state, "ws-1", "runner", "usage.json"), path)
	})
	t.Run("with a parent it is under the root's machine directory", func(t *testing.T) {
		state := t.TempDir()
		store := &resultStoreMock{
			LineageFunc: func(string) (string, []string, error) { return "root-1", []string{"root-1"}, nil },
			DirFunc: func(root, child string) string {
				return filepath.Join(state, root, "machine", child)
			},
		}
		files := FilesFor(state, "ws-1")
		path, err := files.UsagePath(&Run{Parent: "root-1"}, store)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(state, "root-1", "machine", "ws-1", "usage.json"), path)
	})
	t.Run("a lineage failure is an error", func(t *testing.T) {
		store := &resultStoreMock{LineageFunc: func(string) (string, []string, error) { return "", nil, errors.New("boom") }}
		_, err := FilesFor("/state", "ws-1").UsagePath(&Run{Parent: "root-1"}, store)
		require.Error(t, err)
	})
}

func TestWriteAtomic(t *testing.T) {
	t.Parallel()
	t.Run("replaces an existing file without a temp file left behind", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "f.json")
		require.NoError(t, writeAtomic(path, []byte("one")))
		require.NoError(t, writeAtomic(path, []byte("two")))
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "two", string(data))
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Len(t, entries, 1)
	})
	t.Run("a directory in the way is an error", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "f")
		require.NoError(t, os.Mkdir(path, dirMode))
		require.Error(t, writeAtomic(path, []byte("x")))
	})
}
