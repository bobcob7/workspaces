package review

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testPrompt() *Prompt {
	return &Prompt{
		Repo:      "github.com/bobcob7/buff-bear",
		Ref:       "3f2a9c1e4b5d6f7a8b9c0d1e2f3a4b5c6d7e8f90",
		PR:        &PromptPR{Number: 42, Title: "Fix login", URL: "https://github.com/bobcob7/buff-bear/pull/42", Base: "main", HeadBranch: "feat/login"},
		Subagents: []string{"review-security", "review-go"},
	}
}

func TestAgentPrompt(t *testing.T) {
	t.Run("names every subagent and the pull request", func(t *testing.T) {
		text, err := AgentPrompt(testPrompt())
		require.NoError(t, err)
		assert.Contains(t, text, "- review-security\n- review-go\n")
		assert.Contains(t, text, `pull request #42 "Fix login" (https://github.com/bobcob7/buff-bear/pull/42), feat/login into main`)
		assert.Contains(t, text, "ws review report --json --repo github.com/bobcob7/buff-bear --ref 3f2a9c1e4b5d6f7a8b9c0d1e2f3a4b5c6d7e8f90")
		assert.Contains(t, text, "Don't pass a `model` parameter")
		assert.NotContains(t, text, "From the requester")
	})
	t.Run("adds the focus under From the requester", func(t *testing.T) {
		p := testPrompt()
		p.Focus = "Look hard at the retry loop."
		text, err := AgentPrompt(p)
		require.NoError(t, err)
		assert.True(t, strings.HasSuffix(text, "## From the requester\n\nLook hard at the retry loop.\n"), text)
	})
	t.Run("a commit without a pull request", func(t *testing.T) {
		p := testPrompt()
		p.PR = nil
		text, err := AgentPrompt(p)
		require.NoError(t, err)
		assert.Contains(t, text, "at commit 3f2a9c1e4b5d6f7a8b9c0d1e2f3a4b5c6d7e8f90. ws defined")
	})
}

func TestSubagentTask(t *testing.T) {
	t.Run("a pull request's diff is against its base", func(t *testing.T) {
		text, err := SubagentTask(testPrompt(), "go")
		require.NoError(t, err)
		assert.Contains(t, text, "as the go profile")
		assert.Contains(t, text, "`git diff origin/main...HEAD`")
		assert.Contains(t, text, `setting "profile" to "go"`)
	})
	t.Run("a commit's diff is the commit", func(t *testing.T) {
		p := testPrompt()
		p.PR = nil
		text, err := SubagentTask(p, "go")
		require.NoError(t, err)
		assert.Contains(t, text, "`git show HEAD`")
	})
	t.Run("carries the focus", func(t *testing.T) {
		p := testPrompt()
		p.Focus = "retries"
		text, err := SubagentTask(p, "go")
		require.NoError(t, err)
		assert.Contains(t, text, "From the requester: retries\n")
	})
}
