//go:build e2e

package runner_test

import (
	"cmp"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRealModel runs two scenarios against a real model: the endpoint at
// E2E_RUNNER_URL, serving E2E_RUNNER_MODEL through E2E_RUNNER_API. It asserts
// only that each run ends done with a verdict, since a real model's words
// and choices vary. It never runs in CI.
func TestRealModel(t *testing.T) {
	url := os.Getenv("E2E_RUNNER_URL")
	if url == "" {
		t.Skip("E2E_RUNNER_URL is not set; the real-model scenarios need a real endpoint")
	}
	model := cmp.Or(os.Getenv("E2E_RUNNER_MODEL"), "qwen/qwen3.5-9b")
	api := cmp.Or(os.Getenv("E2E_RUNNER_API"), "openai")

	t.Run("edit-file", func(t *testing.T) {
		env := realEnv(t, url, model, api)
		c := spawnRunner(t, env, editPrompt)
		w := waitRunner(t, env, c.ID)
		requireVerdict(t, &w)
	})
	t.Run("review-verdict", func(t *testing.T) {
		env := realEnv(t, url, model, api)
		pushFile(t, env, "README.md", "Title\n", "docs: add the readme")
		c := spawnRunner(t, env, "Review the last commit.")
		w := waitRunner(t, env, c.ID)
		requireVerdict(t, &w)
	})
}

// realEnv is a scenario whose only endpoint is the real one, with its
// model as the runner's model.
func realEnv(t *testing.T, url, model, api string) *testEnv {
	t.Helper()
	env := newEnv(t)
	env.writeConfig(t, fmt.Sprintf(`
[runner]
model = "real"

[endpoints.fake]
url = %q
api = %q
probe = "none"

[models.real]
endpoint = "fake"
id = %q
context_tokens = 32768
`, url, api, model))
	return env
}

// requireVerdict asserts that the run ended done and recorded a verdict.
func requireVerdict(t *testing.T, w *waited) {
	t.Helper()
	require.NotNil(t, w.WSRunner)
	assert.Equal(t, "done", w.WSRunner.Exit)
	require.NotNil(t, w.Result)
	assert.NotEmpty(t, w.Result.Verdict)
}
