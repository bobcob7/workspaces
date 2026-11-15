//go:build e2e

package runner_test

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The agent scenarios of ADR 0067's table: a ws child on the runner, driven
// by fake-ollama, plus the default Claude path's regression. Fake configs are
// in testdata/.

const (
	tokenA = "tok-a-e2e-7f3a" // WS_ENDPOINT_TOKEN_FAKE
	tokenB = "tok-b-e2e-9c21" // WS_ENDPOINT_TOKEN_FAKE_B
)

// child is the JSON `ws machine spawn --json` prints.
type child struct {
	ID      string `json:"id"`
	Session string `json:"session"`
	Path    string `json:"path"`
}

// waited is one child in `ws machine wait`'s output.
type waited struct {
	ID       string        `json:"id"`
	Finished bool          `json:"finished"`
	Outcome  string        `json:"outcome"`
	Result   *verdict      `json:"result"`
	Error    *waitError    `json:"error"`
	Runner   string        `json:"runner"`
	Model    string        `json:"model"`
	WSRunner *wsRunnerWait `json:"ws_runner"`
}

type verdict struct {
	Verdict string `json:"verdict"`
	Outcome string `json:"outcome"`
	Report  string `json:"report"`
}

type waitError struct {
	Code string `json:"code"`
}

type wsRunnerWait struct {
	Exit            string `json:"exit"`
	Endpoint        string `json:"endpoint"`
	EndpointChanges int    `json:"endpoint_changes"`
	UsageFile       string `json:"usage_file"`
}

// spawnArgs is `ws machine spawn` for a ws child of the repo o/n.
func spawnArgs(prompt string, args ...string) []string {
	return append([]string{"machine", "spawn", "--runner", "ws", "--json", "--name", "child", "o/n", "--", prompt}, args...)
}

// spawnRunner starts a ws child in the parent workspace and returns it.
func spawnRunner(t *testing.T, env *testEnv, prompt string, args ...string) child {
	t.Helper()
	res := runWS(t, env, spawnArgs(prompt, args...)...)
	require.Zero(t, res.Code, "spawn: %s %s", res.Stdout, res.Stderr)
	var got child
	require.NoError(t, json.Unmarshal([]byte(res.Stdout), &got))
	require.NotEmpty(t, got.ID)
	return got
}

// waitRunner waits for the child and returns its record. `ws machine wait`
// exits 1 when a child failed, so only its output is checked.
func waitRunner(t *testing.T, env *testEnv, id string) waited {
	t.Helper()
	res := runWS(t, env, "machine", "wait", "--timeout", "2m", id)
	t.Cleanup(func() {
		if t.Failed() {
			data, _ := os.ReadFile(env.transcriptOf(id))
			t.Logf("transcript of %s:\n%s", id, data)
		}
	})
	require.NotEmpty(t, res.Stdout, "wait: %s", res.Stderr)
	var got struct {
		Children []waited `json:"children"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.Stdout), &got), res.Stdout)
	require.Len(t, got.Children, 1, res.Stdout)
	return got.Children[0]
}

func (w waited) errorCode() string {
	if w.Error == nil {
		return ""
	}
	return w.Error.Code
}

// errorCode is the code of the JSON error ws printed, or "".
func errorCode(res wsRun) string {
	for _, out := range []string{res.Stdout, res.Stderr} {
		var got struct {
			Code  string `json:"code"`
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(out), &got) == nil {
			return cmp.Or(got.Error.Code, got.Code)
		}
	}
	return ""
}

// startRunnerEnv builds a scenario with one fake-ollama on config, as the
// [endpoints.fake] of the runner's model.
func startRunnerEnv(t *testing.T, config string) (*testEnv, fakeOllama) {
	t.Helper()
	env := newEnv(t)
	fake := startFakeOllama(t, config)
	env.writeConfig(t, runnerConfig(fake.URL))
	return env, fake
}

// aliasBlock is a [models.<alias>] table on the fake endpoint.
func aliasBlock(alias, id string, contextTokens int) string {
	return fmt.Sprintf("\n[models.%s]\nendpoint = \"fake\"\nid = %q\ncontext_tokens = %d\n", alias, id, contextTokens)
}

// endpointBlock is an [endpoints.<name>] table for a fake at url.
func endpointBlock(name, url string) string {
	return fmt.Sprintf("\n[endpoints.%s]\nurl = %q\napi = \"openai\"\nprobe = \"ollama\"\n", name, url)
}

// authOf is what fake-ollama records for a bearer token: "bearer:" and the
// first 8 hex digits of its SHA-256.
func authOf(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "bearer:" + hex.EncodeToString(sum[:])[:8]
}

// chatRequests keeps the chat completions a fake received.
func chatRequests(reqs []fakeRequest) []fakeRequest {
	var out []fakeRequest
	for _, r := range reqs {
		if r.Path == "/v1/chat/completions" {
			out = append(out, r)
		}
	}
	return out
}

// countEvents counts the transcript's events of kind.
func countEvents(transcript, kind string) int {
	n := 0
	for _, ev := range events(transcript) {
		if ev["kind"] == kind {
			n++
		}
	}
	return n
}

// waitEventCount polls until transcript has n events of kind.
func waitEventCount(t *testing.T, transcript, kind string, n int) {
	t.Helper()
	deadline := time.Now().Add(eventWait)
	for countEvents(transcript, kind) < n {
		if time.Now().After(deadline) {
			t.Fatalf("fewer than %d %q events in %s within %s", n, kind, transcript, eventWait)
		}
		time.Sleep(pollInterval)
	}
}

// assertNoSecret checks that neither the child's transcript nor its
// runner.log holds one of the values.
func assertNoSecret(t *testing.T, env *testEnv, id string, values ...string) {
	t.Helper()
	logData, err := os.ReadFile(filepath.Join(env.runnerDir(id), "runner.log"))
	require.NoError(t, err)
	transcript, err := os.ReadFile(env.transcriptOf(id))
	require.NoError(t, err)
	for _, v := range values {
		assert.NotContains(t, string(transcript), v)
		assert.NotContains(t, string(logData), v)
	}
}

// pushFile commits one file to the upstream's main, so a child cloned
// afterwards has it.
func pushFile(t *testing.T, env *testEnv, path, content, msg string) {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "clone")
	gitRun(t, env, "clone", "-q", filepath.Join(env.upstream, "o", "n.git"), clone)
	require.NoError(t, os.WriteFile(filepath.Join(clone, path), []byte(content), 0o600))
	gitRun(t, env, "-C", clone, "add", path)
	gitRun(t, env, "-C", clone, "commit", "-q", "-m", msg)
	gitRun(t, env, "-C", clone, "push", "-q", "origin", "HEAD:refs/heads/main")
}

// transcriptOf is the transcript of a runner child.
func (e *testEnv) transcriptOf(id string) string {
	return filepath.Join(e.runnerDir(id), "transcript.jsonl")
}

// childDirs lists the workspaces ws spawned under the config dir.
func (e *testEnv) childDirs(t *testing.T) []string {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join(e.configDir, "workspaces", "child-*"))
	require.NoError(t, err)
	return dirs
}

// dropEndpointAt waits for the child's first reply, takes endpoint a down,
// and repoints the alias to fake-b at the first retry.
func dropEndpointAt(t *testing.T, env *testEnv, a fakeOllama, transcript string) {
	t.Helper()
	waitEvent(t, transcript, "response")
	setFaults(t, a.Control, `[{"down":true}]`)
	waitEvent(t, transcript, "retry")
	res := runWS(t, env, "config", "set", "models.qwen9b.endpoint", "fake-b")
	require.Zero(t, res.Code, res.Stderr)
}

// startTwoEndpoints starts fake A on failover.yaml as [endpoints.fake] and
// fake B on configB as [endpoints.fake-b], with each endpoint's token.
func startTwoEndpoints(t *testing.T, configB string) (*testEnv, fakeOllama, fakeOllama) {
	t.Helper()
	env := newEnv(t)
	a := startFakeOllama(t, "failover.yaml")
	b := startFakeOllama(t, configB)
	env.writeConfig(t, runnerConfig(a.URL)+endpointBlock("fake-b", b.URL))
	env.writeSecrets(t, map[string]string{"WS_ENDPOINT_TOKEN_FAKE": tokenA, "WS_ENDPOINT_TOKEN_FAKE_B": tokenB})
	return env, a, b
}

// bigText is size bytes of padding for big.txt.
func bigText(size int) string {
	return strings.Repeat("line of big.txt padding for the context check\n", size/46+1)[:size]
}

// repoOf is the child's clone of o/n, under its workspace.
func repoOf(c child) string {
	return filepath.Join(c.Path, "n")
}

// gitOut runs git in dir and returns its trimmed output.
func gitOut(t *testing.T, env *testEnv, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = env.environ()
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

// readRunToml is the child's run.toml.
func readRunToml(t *testing.T, env *testEnv, id string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(env.runnerDir(id), "run.toml"))
	require.NoError(t, err)
	return string(data)
}

func TestScenarios(t *testing.T) {
	scenarios := []struct {
		name string
		run  func(*testing.T)
	}{
		{"edit-file", testEditFile},
		{"review-verdict", testReviewVerdict},
		{"endpoint-down", testEndpointDown},
		{"endpoint-down-mid-run", testEndpointDownMidRun},
		{"endpoint-failover", testEndpointFailover},
		{"endpoint-repoint-no-tools", testEndpointRepointNoTools},
		{"config-invalid-mid-run", testConfigInvalidMidRun},
		{"malformed-tool-call", testMalformedToolCall},
		{"unknown-tool", testUnknownTool},
		{"mid-stream-drop", testMidStreamDrop},
		{"context-overflow", testContextOverflow},
		{"no-tools-model", testNoToolsModel},
		{"model-not-served", testModelNotServed},
		{"token-redacted", testTokenRedacted},
		{"claude-default-untouched", testClaudeDefaultUntouched},
	}
	for _, s := range scenarios {
		t.Run(s.name, s.run)
	}
}

func testEditFile(t *testing.T) {
	env, fake := startRunnerEnv(t, "edit-file.yaml")
	c := spawnRunner(t, env, "Add hello.txt and commit it.")
	w := waitRunner(t, env, c.ID)

	assert.Equal(t, "ws", w.Runner)
	assert.Equal(t, "done", w.WSRunner.Exit)
	assert.Equal(t, "qwen/qwen3.5-9b", w.Model)
	require.NotNil(t, w.Result)
	assert.Equal(t, "PASS", w.Result.Verdict)

	data, err := os.ReadFile(filepath.Join(repoOf(c), "hello.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello from the fake model\n", string(data))
	assert.Contains(t, gitOut(t, env, repoOf(c), "log", "--oneline", "-1"), "feat: add hello")
	_, err = os.Stat(w.WSRunner.UsageFile)
	assert.NoError(t, err)
	assert.NotEmpty(t, requests(t, fake.Control))
	assert.Zero(t, countEvents(env.transcriptOf(c.ID), "truncated"), "a three-turn run must not trip the truncated check")
}

func testReviewVerdict(t *testing.T) {
	env, _ := startRunnerEnv(t, "review-verdict.yaml")
	pushFile(t, env, "README.md", "Title\n", "docs: add the readme")
	c := spawnRunner(t, env, "Review the last commit.")
	w := waitRunner(t, env, c.ID)

	assert.Equal(t, "done", w.WSRunner.Exit)
	assert.Equal(t, "fail", w.Outcome)
	assert.Equal(t, "child_failed", w.errorCode())
	require.NotNil(t, w.Result)
	assert.Equal(t, "CHANGES_REQUESTED", w.Result.Verdict)
	report, err := os.ReadFile(w.Result.Report)
	require.NoError(t, err)
	assert.Contains(t, string(report), "README.md:1")
}

func testEndpointDown(t *testing.T) {
	env := newEnv(t)
	fake := startFakeOllama(t, "edit-file.yaml")
	env.writeConfig(t, runnerConfig(fake.URL))
	setFaults(t, fake.Control, `[{"down":true}]`)

	res := runWS(t, env, spawnArgs("Add hello.txt.")...)
	assert.Equal(t, 1, res.Code)
	assert.Equal(t, "endpoint_unreachable", errorCode(res))
	assert.Empty(t, env.childDirs(t), "a failed spawn leaves no workspace")
}

func testEndpointDownMidRun(t *testing.T) {
	env, fake := startRunnerEnv(t, "endpoint-down-mid-run.yaml")
	c := spawnRunner(t, env, "Add hello.txt and commit it.")
	transcript := env.transcriptOf(c.ID)

	// Turn 1's run sleeps 2 s, so the fault is in place before turn 2.
	waitEventCount(t, transcript, "response", 2)
	setFaults(t, fake.Control, `[{"status":503,"retry_after":1,"count":4}]`)
	w := waitRunner(t, env, c.ID)

	assert.Equal(t, "endpoint_down", w.WSRunner.Exit)
	assert.Equal(t, 3, countEvents(transcript, "retry"))
	assert.Equal(t, "fail", w.Outcome)
	require.NotNil(t, w.Result)
	assert.Equal(t, "FAIL", w.Result.Verdict)
	report, err := os.ReadFile(w.Result.Report)
	require.NoError(t, err)
	assert.Contains(t, string(report), "ws runner: endpoint_down")
}

func testEndpointFailover(t *testing.T) {
	env, a, b := startTwoEndpoints(t, "failover.yaml")
	c := spawnRunner(t, env, "Sleep, then write failover.txt.")
	transcript := env.transcriptOf(c.ID)
	dropEndpointAt(t, env, a, transcript)
	w := waitRunner(t, env, c.ID)

	assert.Equal(t, "done", w.WSRunner.Exit)
	assert.Equal(t, "fake-b", w.WSRunner.Endpoint)
	assert.Equal(t, 1, w.WSRunner.EndpointChanges)
	require.NotNil(t, w.Result)
	assert.Equal(t, "PASS", w.Result.Verdict)

	changed := findEvent(transcript, "endpoint_changed")
	require.NotNil(t, changed)
	assert.Equal(t, "fake", changed["from_endpoint"])
	assert.Equal(t, "fake-b", changed["to_endpoint"])
	assert.Equal(t, "retry", changed["reason"])

	chats := chatRequests(requests(t, b.Control))
	require.GreaterOrEqual(t, len(chats), 2)
	for _, r := range chats {
		assert.Equal(t, authOf(tokenB), r.Auth)
	}
	assert.Equal(t, 2, strings.Count(readRunToml(t, env, c.ID), "[[endpoint_history]]"))
	assertNoSecret(t, env, c.ID, tokenA, tokenB)
}

func testEndpointRepointNoTools(t *testing.T) {
	env, a, b := startTwoEndpoints(t, "failover-notools.yaml")
	c := spawnRunner(t, env, "Sleep, then write failover.txt.")
	transcript := env.transcriptOf(c.ID)
	dropEndpointAt(t, env, a, transcript)
	w := waitRunner(t, env, c.ID)

	assert.Equal(t, "endpoint_rejected", w.WSRunner.Exit)
	require.NotNil(t, w.Result)
	assert.Equal(t, "FAIL", w.Result.Verdict)
	report, err := os.ReadFile(w.Result.Report)
	require.NoError(t, err)
	assert.Contains(t, string(report), "fake-b")
	assert.Contains(t, string(report), "model_without_tools")

	rejected := findEvent(transcript, "endpoint_rejected")
	require.NotNil(t, rejected)
	assert.Equal(t, "model_without_tools", rejected["code"])
	assert.Empty(t, chatRequests(requests(t, b.Control)), "a rejected endpoint gets no chat request")
}

func testConfigInvalidMidRun(t *testing.T) {
	env, fake := startRunnerEnv(t, "failover.yaml")
	c := spawnRunner(t, env, "Sleep, then write failover.txt.")
	transcript := env.transcriptOf(c.ID)
	waitEvent(t, transcript, "response")
	setFaults(t, fake.Control, `[{"status":503,"count":1}]`)
	breakConfig(t, env)
	w := waitRunner(t, env, c.ID)

	assert.Equal(t, "done", w.WSRunner.Exit)
	require.NotNil(t, w.Result)
	assert.Equal(t, "PASS", w.Result.Verdict)
	warning := findEvent(transcript, "config_warning")
	require.NotNil(t, warning)
	assert.Equal(t, "config_invalid", warning["code"])
	retried := findEvent(transcript, "retry")
	require.NotNil(t, retried)
	assert.Equal(t, "fake", retried["endpoint"], "the retry stays on the same endpoint")
}

func testMalformedToolCall(t *testing.T) {
	t.Run("recovers after one invalid call", func(t *testing.T) {
		env, _ := startRunnerEnv(t, "malformed-tool-call.yaml")
		c := spawnRunner(t, env, "MALFORMED_ONCE: read README.md.")
		w := waitRunner(t, env, c.ID)

		assert.Equal(t, "done", w.WSRunner.Exit)
		require.NotNil(t, w.Result)
		assert.Equal(t, "PASS", w.Result.Verdict)
		assert.Equal(t, 1, countEvents(env.transcriptOf(c.ID), "invalid_call"))
	})
	t.Run("fails after three in a row", func(t *testing.T) {
		env, _ := startRunnerEnv(t, "malformed-tool-call.yaml")
		c := spawnRunner(t, env, "MALFORMED_ALWAYS: read README.md.")
		w := waitRunner(t, env, c.ID)

		assert.Equal(t, "malformed_tool_calls", w.WSRunner.Exit)
		require.NotNil(t, w.Result)
		assert.Equal(t, "FAIL", w.Result.Verdict)
		assert.Equal(t, 3, countEvents(env.transcriptOf(c.ID), "invalid_call"))
	})
}

func testUnknownTool(t *testing.T) {
	env, _ := startRunnerEnv(t, "malformed-tool-call.yaml")
	c := spawnRunner(t, env, "UNKNOWN_TOOL: do it.")
	w := waitRunner(t, env, c.ID)

	assert.Equal(t, "done", w.WSRunner.Exit)
	require.NotNil(t, w.Result)
	assert.Equal(t, "PASS", w.Result.Verdict)
	invalid := findEvent(env.transcriptOf(c.ID), "invalid_call")
	require.NotNil(t, invalid)
	assert.Equal(t, "delete_repo", invalid["name"])
}

func testMidStreamDrop(t *testing.T) {
	env, fake := startRunnerEnv(t, "edit-file.yaml")
	setFaults(t, fake.Control, `[{"drop_after_chunks":2,"count":1}]`)
	c := spawnRunner(t, env, "Add hello.txt and commit it.")
	w := waitRunner(t, env, c.ID)

	assert.Equal(t, "done", w.WSRunner.Exit)
	require.NotNil(t, w.Result)
	assert.Equal(t, "PASS", w.Result.Verdict)
	assert.Equal(t, 1, countEvents(env.transcriptOf(c.ID), "retry"))
}

// contextOverflowEnv is a probe-less endpoint, so the alias's 16384 is the
// window the runner uses while fake-small truncates at 4096 words.
func contextOverflowEnv(t *testing.T) *testEnv {
	t.Helper()
	env := newEnv(t)
	fake := startFakeOllama(t, "context-overflow.yaml")
	body := strings.Replace(runnerConfig(fake.URL), `probe = "ollama"`, `probe = "none"`, 1)
	env.writeConfig(t, body+aliasBlock("small", "fake-small", 16384))
	pushFile(t, env, "big.txt", bigText(40*1024), "test: add big.txt")
	return env
}

func testContextOverflow(t *testing.T) {
	env := contextOverflowEnv(t)
	task := "Read big.txt in 400-line chunks. " + strings.Repeat("Keep the reads in order. ", 30*1024/25)
	c := spawnRunner(t, env, task, "--model", "small")
	w := waitRunner(t, env, c.ID)

	assert.Equal(t, "context_overflow", w.WSRunner.Exit)
	assert.NotNil(t, findEvent(env.transcriptOf(c.ID), "compaction"))
}

func testNoToolsModel(t *testing.T) {
	t.Run("probe ollama refuses the spawn", func(t *testing.T) {
		env := newEnv(t)
		fake := startFakeOllama(t, "no-tools-model.yaml")
		env.writeConfig(t, runnerConfig(fake.URL)+aliasBlock("notools", "fake-notools", 32768))
		res := runWS(t, env, spawnArgs("Add hello.txt.", "--model", "notools")...)
		assert.Equal(t, 1, res.Code)
		assert.Equal(t, "model_without_tools", errorCode(res))
	})
	t.Run("probe none starts and exits model_no_tools", func(t *testing.T) {
		env := newEnv(t)
		fake := startFakeOllama(t, "no-tools-model.yaml")
		body := strings.Replace(runnerConfig(fake.URL), `probe = "ollama"`, `probe = "none"`, 1)
		env.writeConfig(t, body+aliasBlock("notools", "fake-notools", 32768))
		c := spawnRunner(t, env, "Add hello.txt.", "--model", "notools")
		w := waitRunner(t, env, c.ID)
		assert.Equal(t, "model_no_tools", w.WSRunner.Exit)
		require.NotNil(t, w.Result)
		assert.Equal(t, "FAIL", w.Result.Verdict)
	})
}

func testModelNotServed(t *testing.T) {
	env := newEnv(t)
	fake := startFakeOllama(t, "no-tools-model.yaml")
	env.writeConfig(t, runnerConfig(fake.URL)+aliasBlock("ghost", "qwen/absent", 32768))
	res := runWS(t, env, spawnArgs("Add hello.txt.", "--model", "ghost")...)
	assert.Equal(t, 3, res.Code)
	assert.Equal(t, "model_not_served", errorCode(res))
	assert.Contains(t, res.Stdout+res.Stderr, "qwen/qwen3.5-9b")
}

func testTokenRedacted(t *testing.T) {
	env, fake := startRunnerEnv(t, "token-redacted.yaml")
	env.writeSecrets(t, map[string]string{"WS_ENDPOINT_TOKEN_FAKE": tokenA})
	c := spawnRunner(t, env, "Print the environment.")
	w := waitRunner(t, env, c.ID)

	assert.Equal(t, "done", w.WSRunner.Exit)
	chats := chatRequests(requests(t, fake.Control))
	require.NotEmpty(t, chats)
	assert.Equal(t, authOf(tokenA), chats[0].Auth)
	assertNoSecret(t, env, c.ID, tokenA)
}

// runClaudeCase runs one default-runner ws command in dir and appends the
// claude calls it made to out, under label.
func runClaudeCase(t *testing.T, env *testEnv, out, stdout *strings.Builder, label, dir string, args ...string) wsRun {
	t.Helper()
	before := len(env.claudeCalls(t))
	res := runWSIn(t, env, dir, args...)
	require.Zero(t, res.Code, "%s: %s", label, res.Stderr)
	require.Empty(t, errorCode(res), "%s printed an error", label)
	fmt.Fprintf(out, "## %s\n", label)
	for _, call := range env.claudeCalls(t)[before:] {
		data, err := json.Marshal(call)
		require.NoError(t, err)
		out.Write(data)
		out.WriteString("\n")
	}
	stdout.WriteString(res.Stdout)
	return res
}

// claudeGolden runs the default-runner commands and returns their normalized
// claude arguments, as testdata/claude-args.golden holds them.
func claudeGolden(t *testing.T, env *testEnv) string {
	t.Helper()
	var out, stdout strings.Builder
	first := runClaudeCase(t, env, &out, &stdout, "ws claude -- x", env.home, "claude", "--json", "--name", "parent", "--", "x")
	var parent struct {
		Path string `json:"path"`
	}
	require.NoError(t, json.Unmarshal([]byte(first.Stdout), &parent))
	runClaudeCase(t, env, &out, &stdout, "ws machine spawn -- x", parent.Path, "machine", "spawn", "--json", "--", "x")
	runClaudeCase(t, env, &out, &stdout, "ws machine spawn o/n --name fix -- x", parent.Path, "machine", "spawn", "--json", "--name", "fix", "o/n", "--", "x")
	runClaudeCase(t, env, &out, &stdout, "ws machine spawn --role verifier", parent.Path, "machine", "spawn", "--json", "--role", "verifier", "--var", "BRANCH=feat/x", "--var", "CHECKS=make test", "--")
	runClaudeCase(t, env, &out, &stdout, "ws claude o/n -- --model opus x", env.home, "claude", "--json", "o/n", "--", "--model", "opus", "x")
	runClaudeCase(t, env, &out, &stdout, "ws loop add claude-loop", env.home, "loop", "add", "--json", "--every", "15m", "--prompt", "do the loop", "--model", "sonnet", "--permission-mode", "plan", "claude-loop")
	runClaudeCase(t, env, &out, &stdout, "ws loop run claude-loop", env.home, "loop", "run", "--json", "--force", "claude-loop")
	return normalizeClaude(env, out.String(), stdout.String())
}

var (
	stdoutIDRe = regexp.MustCompile(`"id":"([^"]*)"`)
	loopIDRe   = regexp.MustCompile(`loop-[a-z0-9-]+-[0-9a-f]{8}`)
	timeRe     = regexp.MustCompile(`[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:.]+Z?`)
	sessionRe  = regexp.MustCompile(`\b0a1b2c3d[0-9]+\b`)
)

// normalizeClaude replaces the scenario's temp dir, workspace ids, times and
// fake session ids, as capture-claude-args.sh does.
func normalizeClaude(env *testEnv, args, stdout string) string {
	seen := map[string]bool{}
	out := strings.ReplaceAll(args, env.root, "<DIR>")
	for _, m := range stdoutIDRe.FindAllStringSubmatch(stdout, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = strings.ReplaceAll(out, m[1], "<WS_ID>")
		}
	}
	out = loopIDRe.ReplaceAllString(out, "<WS_ID>")
	out = timeRe.ReplaceAllString(out, "<TIME>")
	return sessionRe.ReplaceAllString(out, "<SESSION>")
}

// socketFiles lists the daemon sockets under the scenario's runtime dir.
func socketFiles(t *testing.T, env *testEnv) []string {
	t.Helper()
	var socks []string
	err := filepath.WalkDir(env.runtime, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if filepath.Ext(path) == ".sock" {
			socks = append(socks, path)
		}
		return nil
	})
	require.NoError(t, err)
	return socks
}

func testClaudeDefaultUntouched(t *testing.T) {
	golden, err := os.ReadFile(testdata(t, "claude-args.golden"))
	require.NoError(t, err)
	variants := map[string]string{
		"no runner tables": "",
		"invalid models":   "\n[models.qwen9b]\ncontext_tokens = 4096\n",
	}
	for name, extra := range variants {
		t.Run(name, func(t *testing.T) {
			env := newBareEnv(t)
			env.extra = []string{"WS_DAEMON_AUTOSTART=0"}
			env.writeConfig(t, extra)
			got := claudeGolden(t, env)
			assert.Equal(t, string(golden), got)
			assert.Empty(t, socketFiles(t, env), "the Claude path starts no daemon")
		})
	}
}
