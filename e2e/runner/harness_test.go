//go:build e2e

// Package runner holds the runner's end-to-end scenarios (ADR 0067). They run
// the built ws binary against fake-ollama and fake-mcp processes on loopback,
// in temporary directories, and never reach a real model or network.
package runner_test

import (
	"bufio"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	repoRoot     = "../.."
	pollInterval = 100 * time.Millisecond
	eventWait    = 30 * time.Second
	startWait    = 10 * time.Second
	wsTimeout    = 3 * time.Minute
)

// binary returns bin/<name> under the repo root, or env's value when it is set.
func binary(t *testing.T, env, name, build string) string {
	t.Helper()
	if path := os.Getenv(env); path != "" {
		return path
	}
	path, err := filepath.Abs(filepath.Join(repoRoot, "bin", name))
	require.NoError(t, err)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s not found; run %s", path, build)
	}
	return path
}

func wsBinary(t *testing.T) string {
	t.Helper()
	return binary(t, "", "ws", "make build")
}

// testdata returns the path of a file in testdata/.
func testdata(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("testdata", name))
	require.NoError(t, err)
	_, err = os.Stat(path)
	require.NoError(t, err)
	return path
}

// process is a child the test started. Its stdout lines arrive on lines.
type process struct {
	cmd   *exec.Cmd
	lines chan string
}

func startProcess(t *testing.T, bin string, args ...string) *process {
	t.Helper()
	log, err := os.Create(filepath.Join(t.TempDir(), filepath.Base(bin)+".log"))
	require.NoError(t, err)
	cmd := exec.CommandContext(t.Context(), bin, args...)
	cmd.Stderr = log
	out, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	p := &process{cmd: cmd, lines: make(chan string, 64)}
	go p.forward(out)
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = log.Close()
	})
	return p
}

func (p *process) forward(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		select {
		case p.lines <- scanner.Text():
		default:
		}
	}
	close(p.lines)
}

// expectLine waits for a stdout line with prefix and returns what follows it.
func (p *process) expectLine(t *testing.T, prefix string) string {
	t.Helper()
	timeout := time.After(startWait)
	for {
		select {
		case line, ok := <-p.lines:
			require.True(t, ok, "exited before printing %q", prefix)
			if rest, found := strings.CutPrefix(line, prefix); found {
				return rest
			}
		case <-timeout:
			t.Fatalf("no %q line within %s", prefix, startWait)
		}
	}
}

// fakeOllama is a running fake-ollama: its API and its control API.
type fakeOllama struct {
	URL     string
	Control string
}

// startFakeOllama runs fake-ollama on loopback with testdata/<config>.
func startFakeOllama(t *testing.T, config string) fakeOllama {
	t.Helper()
	return startFakeOllamaAt(t, testdata(t, config))
}

// startFakeOllamaAt runs fake-ollama on loopback with the script at path.
func startFakeOllamaAt(t *testing.T, path string) fakeOllama {
	t.Helper()
	bin := binary(t, "WS_FAKE_OLLAMA", "fake-ollama", "make fakes")
	p := startProcess(t, bin, "--config", path,
		"--listen", "127.0.0.1:0", "--control-listen", "127.0.0.1:0")
	return fakeOllama{
		URL:     p.expectLine(t, "API listening on "),
		Control: p.expectLine(t, "control API listening on "),
	}
}

// startFakeMCPHTTP runs fake-mcp over streamable HTTP and returns its URL.
func startFakeMCPHTTP(t *testing.T, config string, extra ...string) string {
	t.Helper()
	bin := binary(t, "WS_FAKE_MCP", "fake-mcp", "make fakes")
	args := append([]string{"--config", testdata(t, config), "--http", "127.0.0.1:0"}, extra...)
	return startProcess(t, bin, args...).expectLine(t, "MCP listening on ")
}

// fakeMCPStdio is the command line that starts fake-mcp over stdio with the
// script at config, an absolute path, and records its calls to record.
func fakeMCPStdio(t *testing.T, config, record string) (string, []string) {
	t.Helper()
	bin := binary(t, "WS_FAKE_MCP", "fake-mcp", "make fakes")
	return bin, []string{"--config", config, "--record", record}
}

// setFaults replaces fake-ollama's faults with body, a JSON array of faults.
func setFaults(t *testing.T, control, body string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, control+"/faults", strings.NewReader(body))
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Less(t, resp.StatusCode, 300)
}

// fakeRequest is one request fake-ollama received.
type fakeRequest struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Model       string `json:"model"`
	Stream      bool   `json:"stream"`
	Auth        string `json:"auth"`
	Traceparent string `json:"traceparent"`
	Body        any    `json:"body"`
}

// requests returns what fake-ollama has received since it started.
func requests(t *testing.T, control string) []fakeRequest {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, control+"/requests", http.NoBody)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var got []fakeRequest
	require.NoError(t, json.UnmarshalRead(resp.Body, &got))
	return got
}

// recorded returns the JSON lines fake-mcp wrote with --record.
func recorded(t *testing.T, file string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	require.NoError(t, err)
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil
	}
	var calls []map[string]any
	for line := range strings.SplitSeq(trimmed, "\n") {
		var call map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &call))
		calls = append(calls, call)
	}
	return calls
}

// testEnv is one scenario's world: a config dir, a home, the fake claude, an
// upstream repo and the parent workspace that ws commands act for.
type testEnv struct {
	configDir string
	home      string
	runtime   string
	bin       string
	gitConf   string
	upstream  string
	claudeLog string
	claudeCnt string
	parent    string
	root      string
	extra     []string
}

// newEnv is newBareEnv with the parent workspace a person's `ws claude`
// would have made.
func newEnv(t *testing.T) *testEnv {
	t.Helper()
	env := newBareEnv(t)
	env.parent = env.makeParent(t)
	return env
}

// newBareEnv builds a scenario's world with a config.toml holding only
// [defaults], a fake claude, and a bare upstream o/n, and no parent
// workspace. A daemon the scenario starts is stopped in t.Cleanup.
func newBareEnv(t *testing.T) *testEnv {
	t.Helper()
	root, err := os.MkdirTemp("", "wse2e") //nolint:usetesting // short: unix socket paths are limited.
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	env := &testEnv{
		configDir: filepath.Join(root, "cfg"),
		home:      filepath.Join(root, "home"),
		runtime:   filepath.Join(root, "run"),
		bin:       filepath.Join(root, "bin"),
		gitConf:   filepath.Join(root, "gitconfig"),
		upstream:  filepath.Join(root, "up"),
		claudeLog: filepath.Join(root, "claude.log"),
		claudeCnt: filepath.Join(root, "claude.n"),
	}
	for _, dir := range []string{env.configDir, env.home, env.bin, env.upstream} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}
	require.NoError(t, os.Mkdir(env.runtime, 0o700))
	env.writeGitConfig(t)
	env.makeUpstream(t)
	env.writeFakeClaude(t)
	env.writeConfig(t, "")
	env.writeSecrets(t, nil)
	env.root = root
	t.Cleanup(func() { runWS(t, env, "daemon", "stop") })
	return env
}

func (e *testEnv) writeGitConfig(t *testing.T) {
	t.Helper()
	body := fmt.Sprintf("[url %q]\n\tinsteadOf = https://example.test/\n[user]\n\tname = t\n\temail = t@example.test\n[init]\n\tdefaultBranch = main\n", e.upstream+"/")
	require.NoError(t, os.WriteFile(e.gitConf, []byte(body), 0o600))
}

func (e *testEnv) makeUpstream(t *testing.T) {
	t.Helper()
	repo := filepath.Join(e.upstream, "o", "n.git")
	seed := filepath.Join(e.upstream, "seed")
	gitRun(t, e, "init", "-q", "--bare", repo)
	gitRun(t, e, "init", "-q", seed)
	gitRun(t, e, "-C", seed, "commit", "-q", "--allow-empty", "-m", "init")
	gitRun(t, e, "-C", seed, "push", "-q", repo, "HEAD:refs/heads/main")
}

// gitRun runs git with the scenario's global config, failing the test on error.
func gitRun(t *testing.T, e *testEnv, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Env = e.environ()
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

// writeFakeClaude installs a claude that answers --version, records every
// other call's arguments NUL-separated (a call ends with an empty field), and
// prints the line `claude --bg` prints. `claude agents --json` lists each
// session it backgrounded as idle with a pid, as a finished turn looks, so
// ws sees the session as quiet.
func (e *testEnv) writeFakeClaude(t *testing.T) {
	t.Helper()
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = --version ]; then echo "2.1.303 (Claude Code)"; exit 0; fi
if [ "$1" = agents ]; then
  n=$(cat %[2]q 2>/dev/null || echo 0); sep=
  printf '['
  i=1
  while [ $i -le $n ]; do
    printf '%%s{"id":"0a1b2c3d%%s","sessionId":"s-%%s","name":"fake","cwd":"/","kind":"background","state":"idle","status":"idle","pid":1,"startedAt":1}' "$sep" $i $i
    sep=,; i=$((i + 1))
  done
  printf ']\n'
  exit 0
fi
for a in "$@"; do printf '%%s\0' "$a"; done >> %[1]q
printf '\0' >> %[1]q
if [ "$1" = --bg ]; then
  name=
  [ "$2" = -n ] && name=$3
  n=$(cat %[2]q 2>/dev/null || echo 0); n=$((n + 1)); echo $n > %[2]q
  echo "backgrounded · 0a1b2c3d$n · $name"
fi
exit 0
`, e.claudeLog, e.claudeCnt)
	require.NoError(t, os.WriteFile(filepath.Join(e.bin, "claude"), []byte(script), 0o700)) //nolint:gosec // G306: a fake executable.
}

// claudeCalls returns the arguments of each call the fake claude recorded.
func (e *testEnv) claudeCalls(t *testing.T) [][]string {
	t.Helper()
	data, err := os.ReadFile(e.claudeLog)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	require.NoError(t, err)
	var calls [][]string
	var cur []string
	for tok := range strings.SplitSeq(string(data), "\x00") {
		if tok != "" {
			cur = append(cur, tok)
			continue
		}
		if len(cur) > 0 {
			calls = append(calls, cur)
		}
		cur = nil
	}
	return calls
}

// writeConfig writes config.toml: the [defaults] every scenario needs, then body.
func (e *testEnv) writeConfig(t *testing.T, body string) {
	t.Helper()
	const defaults = "[defaults]\nowners = [\"o\"]\nhost = \"example.test\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(e.configDir, "config.toml"), []byte(defaults+body), 0o600))
}

// writeSecrets writes secrets.env with the given NAME=value pairs.
func (e *testEnv) writeSecrets(t *testing.T, secrets map[string]string) {
	t.Helper()
	var b strings.Builder
	for name, value := range secrets {
		fmt.Fprintf(&b, "%s=%s\n", name, value)
	}
	require.NoError(t, os.WriteFile(filepath.Join(e.configDir, "secrets.env"), []byte(b.String()), 0o600))
}

// runnerConfig is the config.toml of a scenario with the runner on endpoint
// fake at url and the model alias qwen9b.
func runnerConfig(url string) string {
	return fmt.Sprintf(`
[runner]
model = "qwen9b"

[endpoints.fake]
url = %q
api = "openai"
probe = "ollama"

[models.qwen9b]
endpoint = "fake"
id = "qwen/qwen3.5-9b"
context_tokens = 32768
`, url)
}

// makeParent creates the parent workspace with the fake claude, as a person's
// `ws claude` would, and returns its path.
func (e *testEnv) makeParent(t *testing.T) string {
	t.Helper()
	res := runWSIn(t, e, e.home, "claude", "--json", "--name", "parent", "--", "x")
	require.Zero(t, res.Code, res.Stderr)
	var got struct {
		Path string `json:"path"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.Stdout), &got))
	require.NotEmpty(t, got.Path)
	return got.Path
}

// environ is the environment ws runs in: fake claude first on PATH, every
// state under the scenario's own directories, and no real git config.
func (e *testEnv) environ() []string {
	return append([]string{
		"PATH=" + e.bin + string(os.PathListSeparator) + gitDir() + ":/usr/bin:/bin",
		"HOME=" + e.home,
		"XDG_RUNTIME_DIR=" + e.runtime,
		"WS_CONFIG_DIR=" + e.configDir,
		"CLAUDE_CONFIG_DIR=" + e.home,
		"GIT_CONFIG_GLOBAL=" + e.gitConf,
		"GIT_CONFIG_NOSYSTEM=1",
	}, e.extra...)
}

// gitDir is the directory git lives in, so the scenario's PATH can run it.
func gitDir() string {
	path, err := exec.LookPath("git")
	if err != nil {
		return "/usr/bin"
	}
	return filepath.Dir(path)
}

// wsRun is the outcome of one ws invocation.
type wsRun struct {
	Stdout string
	Stderr string
	Code   int
}

// runWS runs ws in the parent workspace, as the scenario's agent would.
func runWS(t *testing.T, env *testEnv, args ...string) wsRun {
	t.Helper()
	dir := env.parent
	if dir == "" {
		dir = env.home
	}
	return runWSIn(t, env, dir, args...)
}

// runWSIn runs ws in dir and returns its output and exit code. Output goes to
// files, so a daemon the command starts can't hold the call open.
func runWSIn(t *testing.T, env *testEnv, dir string, args ...string) wsRun {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), wsTimeout)
	defer cancel()
	stdout, stderr := scratch(t), scratch(t)
	cmd := exec.CommandContext(ctx, wsBinary(t), args...)
	cmd.Dir = dir
	cmd.Env = env.environ()
	cmd.Stdin = nil
	cmd.Stdout, cmd.Stderr = stdout, stderr
	code := 0
	var exitErr *exec.ExitError
	if err := cmd.Run(); errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else {
		require.NoError(t, err)
	}
	return wsRun{Stdout: readFile(t, stdout), Stderr: readFile(t, stderr), Code: code}
}

func scratch(t *testing.T) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "out")
	require.NoError(t, err)
	return f
}

func readFile(t *testing.T, f *os.File) string {
	t.Helper()
	require.NoError(t, f.Close())
	data, err := os.ReadFile(f.Name())
	require.NoError(t, err)
	return string(data)
}

// runnerDir is state/<id>/runner under the scenario's config dir.
func (e *testEnv) runnerDir(id string) string {
	return filepath.Join(e.configDir, "state", id, "runner")
}

// readStatus reads the runner's status.json as a generic object.
func (e *testEnv) readStatus(t *testing.T, id string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(e.runnerDir(id), "status.json"))
	require.NoError(t, err)
	var status map[string]any
	require.NoError(t, json.Unmarshal(data, &status))
	return status
}

// waitEvent polls transcript until an event of kind appears and returns it.
func waitEvent(t *testing.T, transcript, kind string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(eventWait)
	for time.Now().Before(deadline) {
		if ev := findEvent(transcript, kind); ev != nil {
			return ev
		}
		time.Sleep(pollInterval)
	}
	t.Fatalf("no %q event in %s within %s", kind, transcript, eventWait)
	return nil
}

// findEvent returns the first event of kind in a transcript.jsonl, or nil.
func findEvent(transcript, kind string) map[string]any {
	for _, ev := range events(transcript) {
		if ev["kind"] == kind {
			return ev
		}
	}
	return nil
}

// events parses transcript.jsonl; a line still being written is skipped.
func events(transcript string) []map[string]any {
	data, err := os.ReadFile(transcript)
	if err != nil {
		return nil
	}
	var out []map[string]any
	for line := range strings.SplitSeq(string(data), "\n") {
		var ev map[string]any
		if json.Unmarshal([]byte(line), &ev) == nil && ev != nil {
			out = append(out, ev)
		}
	}
	return out
}

// breakConfig writes an invalid config.toml, and restores the previous one in
// t.Cleanup, which runs before the daemon is stopped.
func breakConfig(t *testing.T, env *testEnv) {
	t.Helper()
	path := filepath.Join(env.configDir, "config.toml")
	saved, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("[runner"), 0o600))
	t.Cleanup(func() { _ = os.WriteFile(path, saved, 0o600) })
}
