package runner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/agent"
	"github.com/bobcob7/workspaces/internal/machine"
)

// finalizeInput is what a run that ended with endpoint_down leaves.
func finalizeInput() *FinalizeInput {
	start := time.Date(2026, 11, 13, 17, 45, 58, 0, time.UTC)
	return &FinalizeInput{
		Steps:     3,
		Usage:     agent.Usage{Requests: 4, PromptTokens: 100, CompletionTokens: 20},
		Endpoints: []agent.EndpointUsage{{Endpoint: "gpu", Model: "qwen/qwen3.5-9b", Usage: agent.Usage{Requests: 4}}},
		MCPCalls:  map[string]int{"metrics": 2},
		StartedAt: start,
		EndedAt:   start.Add(90 * time.Second),
	}
}

func TestFinalize(t *testing.T) {
	t.Parallel()
	t.Run("without a verdict records the first fail and a report", func(t *testing.T) {
		files := FilesFor(t.TempDir(), "ws-1")
		run := sampleRun()
		run.Parent = ""
		run.Fail = []string{"FAILED", "OTHER"}
		require.NoError(t, Finalize(files, &run, "endpoint_down", "gone for good", finalizeInput(), &resultStoreMock{}))
		report, err := os.ReadFile(files.path(reportFile))
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(string(report), "ws runner: endpoint_down\n\ngone for good\n\n## Last events\n"))
		st, err := files.ReadStatus()
		require.NoError(t, err)
		require.NotNil(t, st.Verdict)
		assert.Equal(t, "FAILED", *st.Verdict)
		require.NotNil(t, st.Exit)
		assert.Equal(t, "endpoint_down", *st.Exit)
		assert.Equal(t, "exited", st.State)
	})
	t.Run("with a verdict recorded it keeps that verdict and writes no report", func(t *testing.T) {
		files := FilesFor(t.TempDir(), "ws-1")
		run := sampleRun()
		run.Parent = ""
		in := finalizeInput()
		in.Verdict = "PASS"
		require.NoError(t, Finalize(files, &run, "done", "", in, &resultStoreMock{}))
		_, err := os.Stat(files.path(reportFile))
		assert.True(t, os.IsNotExist(err))
		st, err := files.ReadStatus()
		require.NoError(t, err)
		require.NotNil(t, st.Verdict)
		assert.Equal(t, "PASS", *st.Verdict)
	})
	t.Run("stopped records no verdict", func(t *testing.T) {
		files := FilesFor(t.TempDir(), "ws-1")
		run := sampleRun()
		run.Parent = ""
		require.NoError(t, Finalize(files, &run, "stopped", "", finalizeInput(), &resultStoreMock{}))
		st, err := files.ReadStatus()
		require.NoError(t, err)
		assert.Nil(t, st.Verdict)
		assert.Equal(t, "stopped", *st.Exit)
	})
	t.Run("with a parent records through the store", func(t *testing.T) {
		var recorded machine.Result
		state := t.TempDir()
		store := &resultStoreMock{
			LineageFunc: func(string) (string, []string, error) { return "root-1", []string{"root-1"}, nil },
			DirFunc:     func(root, child string) string { return filepath.Join(state, root, "machine", child) },
			WriteResultFunc: func(root, child string, r *machine.Result, report []byte) error {
				recorded = *r
				assert.Equal(t, "root-1", root)
				assert.Equal(t, "ws-1", child)
				assert.Contains(t, string(report), "ws runner: timeout")
				return nil
			},
		}
		files := FilesFor(t.TempDir(), "ws-1")
		run := sampleRun()
		run.Parent = "root-1"
		run.Fail = []string{"FAIL"}
		require.NoError(t, Finalize(files, &run, "timeout", "", finalizeInput(), store))
		assert.Equal(t, "FAIL", recorded.Verdict)
		assert.Equal(t, []string{"root-1"}, recorded.Ancestors)
		_, err := os.Stat(filepath.Join(state, "root-1", "machine", "ws-1", usageFile))
		assert.NoError(t, err)
	})
	t.Run("a failed usage write is an error", func(t *testing.T) {
		parent := t.TempDir()
		blocker := filepath.Join(parent, "blocked")
		require.NoError(t, os.WriteFile(blocker, nil, fileMode))
		files := Files{Dir: filepath.Join(blocker, "runner"), ID: "ws-1"}
		run := sampleRun()
		run.Parent = ""
		in := finalizeInput()
		in.Verdict = "PASS"
		require.Error(t, Finalize(files, &run, "done", "", in, &resultStoreMock{}))
	})
}

func TestFiles_Record(t *testing.T) {
	t.Parallel()
	t.Run("without a parent writes the report and the verdict", func(t *testing.T) {
		files := FilesFor(t.TempDir(), "ws-1")
		run := sampleRun()
		run.Parent = ""
		require.NoError(t, files.Record(&run, &resultStoreMock{}, "PASS", []byte("all good")))
		report, err := os.ReadFile(files.path(reportFile))
		require.NoError(t, err)
		assert.Equal(t, "all good", string(report))
		verdict, err := files.recordedVerdict(&run, &resultStoreMock{})
		require.NoError(t, err)
		assert.Equal(t, "PASS", verdict)
	})
	t.Run("a lineage failure is an error", func(t *testing.T) {
		store := &resultStoreMock{LineageFunc: func(string) (string, []string, error) { return "", nil, errors.New("boom") }}
		files := FilesFor(t.TempDir(), "ws-1")
		run := sampleRun()
		run.Parent = "root-1"
		require.Error(t, files.Record(&run, store, "PASS", []byte("")))
	})
	t.Run("a store write failure is an error", func(t *testing.T) {
		store := &resultStoreMock{
			LineageFunc:     func(string) (string, []string, error) { return "root-1", nil, nil },
			WriteResultFunc: func(string, string, *machine.Result, []byte) error { return errors.New("disk full") },
		}
		files := FilesFor(t.TempDir(), "ws-1")
		run := sampleRun()
		run.Parent = "root-1"
		require.Error(t, files.Record(&run, store, "PASS", []byte("")))
	})
}

func TestFiles_recordedVerdict(t *testing.T) {
	t.Parallel()
	t.Run("without a parent and no status there is none", func(t *testing.T) {
		run := sampleRun()
		run.Parent = ""
		verdict, err := FilesFor(t.TempDir(), "ws-1").recordedVerdict(&run, &resultStoreMock{})
		require.NoError(t, err)
		assert.Empty(t, verdict)
	})
	t.Run("with a parent reads the result", func(t *testing.T) {
		store := &resultStoreMock{
			LineageFunc: func(string) (string, []string, error) { return "root-1", nil, nil },
			ReadFunc: func(string, string) (machine.Record, error) {
				return machine.Record{Result: &machine.Result{Verdict: "PASS"}}, nil
			},
		}
		run := sampleRun()
		run.Parent = "root-1"
		verdict, err := FilesFor(t.TempDir(), "ws-1").recordedVerdict(&run, store)
		require.NoError(t, err)
		assert.Equal(t, "PASS", verdict)
	})
	t.Run("with a parent and no result there is none", func(t *testing.T) {
		store := &resultStoreMock{
			LineageFunc: func(string) (string, []string, error) { return "root-1", nil, nil },
			ReadFunc:    func(string, string) (machine.Record, error) { return machine.Record{}, nil },
		}
		run := sampleRun()
		run.Parent = "root-1"
		verdict, err := FilesFor(t.TempDir(), "ws-1").recordedVerdict(&run, store)
		require.NoError(t, err)
		assert.Empty(t, verdict)
	})
	t.Run("a read failure is an error", func(t *testing.T) {
		store := &resultStoreMock{
			LineageFunc: func(string) (string, []string, error) { return "root-1", nil, nil },
			ReadFunc:    func(string, string) (machine.Record, error) { return machine.Record{}, errors.New("bad toml") },
		}
		run := sampleRun()
		run.Parent = "root-1"
		_, err := FilesFor(t.TempDir(), "ws-1").recordedVerdict(&run, store)
		require.Error(t, err)
	})
}

func TestUsageOf(t *testing.T) {
	t.Parallel()
	t.Run("takes the endpoint fields from the last history entry", func(t *testing.T) {
		run := sampleRun()
		usage := usageOf(&run, "done", &FinalizeInput{})
		assert.Equal(t, "gpu-b", usage.Endpoint)
		assert.Equal(t, "http://gpu-b.inference.svc:1234", usage.URL)
		assert.Equal(t, "done", usage.Exit)
		assert.Empty(t, usage.Endpoints)
		assert.NotNil(t, usage.MCPCalls)
	})
	t.Run("pairs each endpoint usage with its history entry", func(t *testing.T) {
		run := sampleRun()
		in := finalizeInput()
		in.Endpoints = []agent.EndpointUsage{{Endpoint: "gpu"}, {Endpoint: "gpu-b"}}
		entries := usageEndpoints(&run, in.Endpoints)
		require.Len(t, entries, 2)
		assert.Equal(t, "http://gpu-a.inference.svc:1234", entries[0].URL)
		assert.Equal(t, "gpu-b", entries[1].Endpoint)
		assert.Equal(t, "http://gpu-b.inference.svc:1234", entries[1].URL)
	})
	t.Run("an endpoint without a history entry has no url", func(t *testing.T) {
		run := sampleRun()
		entries := usageEndpoints(&run, []agent.EndpointUsage{{Endpoint: "a"}, {Endpoint: "b"}, {Endpoint: "c"}})
		assert.Empty(t, entries[2].URL)
	})
}

func TestFailVerdict(t *testing.T) {
	t.Parallel()
	t.Run("the role's first fail verdict", func(t *testing.T) {
		run := sampleRun()
		run.Fail = []string{"REJECT", "FAIL"}
		assert.Equal(t, "REJECT", failVerdict(&run))
	})
	t.Run("FAIL without a fail list", func(t *testing.T) {
		run := sampleRun()
		run.Fail = nil
		assert.Equal(t, "FAIL", failVerdict(&run))
	})
}

func TestFiles_lastEvents(t *testing.T) {
	t.Parallel()
	t.Run("keeps the last events", func(t *testing.T) {
		files := FilesFor(t.TempDir(), "ws-1")
		var lines []string
		for i := range 25 {
			lines = append(lines, fmt.Sprintf(`{"step":%d}`, i))
		}
		require.NoError(t, writeAtomic(files.Transcript(), []byte(strings.Join(lines, "\n")+"\n")))
		got, err := files.lastEvents()
		require.NoError(t, err)
		require.Len(t, got, 20)
		assert.Equal(t, lines[24], got[19])
	})
	t.Run("a missing transcript has none", func(t *testing.T) {
		got, err := FilesFor(t.TempDir(), "ws-1").lastEvents()
		require.NoError(t, err)
		assert.Empty(t, got)
	})
	t.Run("an unreadable transcript is an error", func(t *testing.T) {
		files := FilesFor(t.TempDir(), "ws-1")
		require.NoError(t, os.MkdirAll(files.Transcript(), dirMode))
		_, err := files.lastEvents()
		require.Error(t, err)
	})
}
