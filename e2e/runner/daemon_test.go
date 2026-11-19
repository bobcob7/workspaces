//go:build e2e

package runner_test

import (
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The daemon and loop scenarios of ADR 0067's table: the ws daemon supervises
// runners that fake-ollama holds mid-run. A runner is killed by the pid in
// its status.json, as a crash would, and resumed or finalized by the daemon.

const (
	editPrompt = "Add hello.txt and commit it."
	// backoffWait covers the daemon's longest backoff, 2 minutes.
	backoffWait = 3 * time.Minute
	// slowFirstToken delays every reply, so a run stays mid-step while the
	// test acts on it.
	slowFirstToken = `[{"slow_first_token":"3s"}]`
	// hang never answers the run's chat requests, so the run stays at the step
	// it was on. It is scoped to the model, which leaves the probes alone: a
	// resumed runner must pass its preflight. failover.yaml's turn 0 is a run
	// that sleeps 2 s, which leaves time to set the fault before turn 1.
	hang = `[{"hang":true,"model":"qwen/qwen3.5-9b"}]`
)

// statusNum is a numeric field of a status.json; 0 when it is absent.
func statusNum(status map[string]any, key string) int {
	n, _ := status[key].(float64)
	return int(n)
}

// statusExit is the exit reason of a status.json; "" while the run goes on.
func statusExit(status map[string]any) string {
	s, _ := status["exit"].(string)
	return s
}

// statusOrNil reads a runner's status.json, or nil while it can't be read.
func (e *testEnv) statusOrNil(id string) map[string]any {
	data, err := os.ReadFile(filepath.Join(e.runnerDir(id), "status.json"))
	if err != nil {
		return nil
	}
	var status map[string]any
	if json.Unmarshal(data, &status) != nil {
		return nil
	}
	return status
}

// killRunner kills the runner process of workspace id, as a crash would,
// and returns its pid.
func (e *testEnv) killRunner(t *testing.T, id string) int {
	t.Helper()
	pid := statusNum(e.readStatus(t, id), "pid")
	require.NotZero(t, pid, "no pid in status.json")
	proc, err := os.FindProcess(pid)
	require.NoError(t, err)
	require.NoError(t, proc.Kill())
	return pid
}

// processAlive reports whether a process with pid still runs.
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// spawnPlain starts a claude child in the parent workspace, with the fake
// claude, and returns its id.
func spawnPlain(t *testing.T, env *testEnv, prompt string) string {
	t.Helper()
	res := runWS(t, env, "machine", "spawn", "--json", "--", prompt)
	require.Zero(t, res.Code, "spawn: %s %s", res.Stdout, res.Stderr)
	var got child
	require.NoError(t, json.Unmarshal([]byte(res.Stdout), &got))
	require.NotEmpty(t, got.ID)
	return got.ID
}

// waitChildren waits for the named children and returns each one's record.
// `ws machine wait` exits 1 when a child failed, so only its output is read.
func waitChildren(t *testing.T, env *testEnv, timeout string, ids ...string) []waited {
	t.Helper()
	res := runWS(t, env, append([]string{"machine", "wait", "--timeout", timeout}, ids...)...)
	require.NotEmpty(t, res.Stdout, "wait: %s", res.Stderr)
	var got struct {
		Children []waited `json:"children"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.Stdout), &got), res.Stdout)
	return got.Children
}

// startHeldRun spawns a failover run and holds its second request, so the
// runner is alive mid-run when the caller acts on it.
func startHeldRun(t *testing.T, env *testEnv, fake fakeOllama) child {
	t.Helper()
	c := spawnRunner(t, env, "Sleep, then write the file.")
	transcript := env.transcriptOf(c.ID)
	waitEvent(t, transcript, "response")
	setFaults(t, fake.Control, hang)
	waitEventCount(t, transcript, "request", 2)
	return c
}

func testMixedWait(t *testing.T) {
	env, _ := startRunnerEnv(t, "edit-file.yaml")
	claudeID := spawnPlain(t, env, "x")
	// The fake claude exits at once and records nothing, so the child
	// records its own result, as a real claude child does.
	report := filepath.Join(t.TempDir(), "report.md")
	require.NoError(t, os.WriteFile(report, []byte("Done.\n"), 0o600))
	res := runWSIn(t, env, filepath.Join(env.configDir, "workspaces", claudeID), "machine", "result", "PASS", "--file", report)
	require.Zero(t, res.Code, "result: %s %s", res.Stdout, res.Stderr)
	c := spawnRunner(t, env, editPrompt)
	children := waitChildren(t, env, "2m", claudeID, c.ID)

	require.Len(t, children, 2)
	byID := map[string]waited{}
	for _, w := range children {
		byID[w.ID] = w
	}
	claude, ok := byID[claudeID]
	require.True(t, ok, "wait returned no record for the claude child")
	assert.Equal(t, "claude", claude.Runner)
	assert.Nil(t, claude.WSRunner)
	runner, ok := byID[c.ID]
	require.True(t, ok, "wait returned no record for the runner child")
	assert.Equal(t, "ws", runner.Runner)
	assert.Equal(t, "qwen/qwen3.5-9b", runner.Model)
	require.NotNil(t, runner.WSRunner)
	assert.Equal(t, "done", runner.WSRunner.Exit)
}

func testDaemonResume(t *testing.T) {
	env, fake := startRunnerEnv(t, "failover.yaml")
	c := startHeldRun(t, env, fake)

	dead := env.killRunner(t, c.ID)
	setFaults(t, fake.Control, "[]")
	awaitResume(t, env, c.ID, dead, backoffWait)
	w := waitRunner(t, env, c.ID)

	assert.Equal(t, "done", w.WSRunner.Exit)
	assert.Equal(t, 1, statusNum(env.readStatus(t, c.ID), "restarts"))
}

// awaitResume waits within d until the daemon has resumed workspace id's
// runner: its pid has moved on from dead.
func awaitResume(t *testing.T, env *testEnv, id string, dead int, d time.Duration) {
	t.Helper()
	t.Cleanup(func() {
		if t.Failed() {
			env.logFiles(t)
		}
	})
	eventually(t, d, env.runnerDir(id), fmt.Sprintf("the daemon did not resume the runner (dead pid %d)", dead), func() bool {
		pid := statusNum(env.statusOrNil(id), "pid")
		return pid != 0 && pid != dead
	})
}

func testDaemonCrashLimit(t *testing.T) {
	env, fake := startRunnerEnv(t, "failover.yaml")
	setFaults(t, fake.Control, slowFirstToken)
	c := spawnRunner(t, env, "Sleep, then write the file.")
	waitEvent(t, env.transcriptOf(c.ID), "request")

	// The daemon resumes a dead runner after its backoff: 5 s, 30 s, then
	// 2 min. Each resumed runner is killed as soon as it is back, while its
	// first reply is still slow. The fourth death has no backoff left, so it
	// finalizes at once.
	for range 3 {
		awaitResume(t, env, c.ID, env.killRunner(t, c.ID), backoffWait)
	}
	env.killRunner(t, c.ID)
	eventually(t, eventWait, env.runnerDir(c.ID), "the daemon did not finalize the runner", func() bool {
		return statusExit(env.statusOrNil(c.ID)) != ""
	})
	w := waitRunner(t, env, c.ID)

	assert.Equal(t, "crashed", w.WSRunner.Exit)
	require.NotNil(t, w.Result)
	assert.Equal(t, "FAIL", w.Result.Verdict)
}

func testDaemonRestart(t *testing.T) {
	env, fake := startRunnerEnv(t, "edit-file.yaml")
	setFaults(t, fake.Control, slowFirstToken)
	c := spawnRunner(t, env, editPrompt)
	waitEvent(t, env.transcriptOf(c.ID), "request")

	res := runWS(t, env, "daemon", "stop", "--keep-runners")
	require.Zero(t, res.Code, res.Stderr)
	second := spawnRunner(t, env, editPrompt)
	w := waitRunner(t, env, c.ID)
	waitRunner(t, env, second.ID)

	assert.Equal(t, "done", w.WSRunner.Exit)
	assert.Zero(t, statusNum(env.readStatus(t, c.ID), "restarts"), "a kept runner is adopted, not resumed")
}

func testDaemonStartResumes(t *testing.T) {
	env, fake := startRunnerEnv(t, "edit-file.yaml")
	setFaults(t, fake.Control, slowFirstToken)
	c := spawnRunner(t, env, editPrompt)
	waitEvent(t, env.transcriptOf(c.ID), "request")
	pid := statusNum(env.readStatus(t, c.ID), "pid")

	res := runWS(t, env, "daemon", "stop")
	require.Zero(t, res.Code, res.Stderr)
	eventually(t, eventWait, env.runnerDir(c.ID), "the runner outlived the daemon's stop", func() bool {
		return !processAlive(pid)
	})
	setFaults(t, fake.Control, "[]")
	second := spawnRunner(t, env, editPrompt)
	awaitResume(t, env, c.ID, pid, eventWait)
	w := waitRunner(t, env, c.ID)
	waitRunner(t, env, second.ID)

	assert.Equal(t, "done", w.WSRunner.Exit)
	assert.Equal(t, 1, statusNum(env.readStatus(t, c.ID), "restarts"), "a stopped runner resumes when a daemon starts")
}

func testRunnerStop(t *testing.T) {
	env, fake := startRunnerEnv(t, "failover.yaml")
	c := spawnRunner(t, env, "Sleep, then write the file.")
	transcript := env.transcriptOf(c.ID)
	waitEvent(t, transcript, "response")
	setFaults(t, fake.Control, hang)
	waitEventCount(t, transcript, "request", 2)

	res := runWS(t, env, "runner", "stop", c.ID)
	require.Zero(t, res.Code, res.Stderr)
	w := waitRunner(t, env, c.ID)

	assert.Equal(t, "stopped", w.WSRunner.Exit)
	assert.Nil(t, w.Result, "a stopped run records no verdict")
	_, err := os.Stat(c.Path)
	require.NoError(t, err, "the workspace is kept")
	_, err = os.Stat(transcript)
	require.NoError(t, err, "the transcript is kept")

	runWS(t, env, "daemon", "stop")
	setFaults(t, fake.Control, "[]")
	second := spawnRunner(t, env, editPrompt)
	waitRunner(t, env, second.ID)
	assert.Equal(t, "stopped", statusExit(env.readStatus(t, c.ID)), "a later daemon start must not resume a stopped run")
	assert.Zero(t, statusNum(env.readStatus(t, c.ID), "restarts"))
}

func testLoopRunner(t *testing.T) {
	env, _ := startRunnerEnv(t, "edit-file.yaml")
	add := runWS(t, env, "loop", "add", "--json", "--every", "15m", "--prompt", editPrompt,
		"--runner", "ws", "--model", "qwen9b", "--show", "loop-runner", "o/n")
	require.Zero(t, add.Code, add.Stderr)
	run := runWS(t, env, "loop", "run", "--json", "loop-runner")
	require.Zero(t, run.Code, run.Stderr)
	var started struct {
		Workspace string `json:"workspace"`
		Session   string `json:"session"`
		Path      string `json:"path"`
	}
	require.NoError(t, json.Unmarshal([]byte(run.Stdout), &started), run.Stdout)
	require.NotEmpty(t, started.Workspace)
	assert.True(t, strings.HasPrefix(started.Session, "rn-"), "the run's session is %q", started.Session)

	eventually(t, wsTimeout, env.runnerDir(started.Workspace), "the loop run did not exit", func() bool {
		return statusExit(env.statusOrNil(started.Workspace)) != ""
	})
	assert.Equal(t, "done", statusExit(env.readStatus(t, started.Workspace)))
	history := runWS(t, env, "loop", "history", "--json", "loop-runner")
	assert.Contains(t, history.Stdout, started.Session)
	_, err := os.Stat(filepath.Join(env.runnerDir(started.Workspace), "report.md"))
	require.NoError(t, err, "a run without a parent writes runner/report.md")
	assert.Contains(t, gitOut(t, env, filepath.Join(started.Path, "n"), "log", "--oneline", "-1"), "feat: add hello")
}

// logFiles logs every .log and status.json file under the scenario's config dir.
func (e *testEnv) logFiles(t *testing.T) {
	t.Helper()
	_ = filepath.WalkDir(e.configDir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && (filepath.Ext(path) == ".log" || d.Name() == "status.json") {
			data, _ := os.ReadFile(path)
			t.Logf("%s:\n%s", path, data)
		}
		return nil
	})
}
