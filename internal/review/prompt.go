package review

import (
	_ "embed"
	"fmt"
	"strings"
	"text/template"
)

// agentPromptText is the review agent's prompt (ADR 0038).
//
//go:embed prompt.md
var agentPromptText string

// subagentTaskText is the task each review-<profile> subagent's prompt
// ends with, after ws review prime --profile's guide (ADR 0038).
//
//go:embed task.md
var subagentTaskText string

var (
	agentPrompt  = template.Must(template.New("prompt.md").Parse(agentPromptText))
	subagentTask = template.Must(template.New("task.md").Parse(subagentTaskText))
)

// PromptPR is the pull request a review covers, as the prompts name it.
type PromptPR struct {
	Number     int
	Title      string
	URL        string
	Base       string
	HeadBranch string
}

// Prompt is what the review agent's and subagents' prompts are made from.
type Prompt struct {
	Repo string
	Ref  string
	// PR is nil for a review of a commit.
	PR *PromptPR
	// Subagents are the review-<profile> subagents' names.
	Subagents []string
	// Profiles are the review's profiles, so each subagent can be told
	// what the others cover.
	Profiles []PromptProfile
	// Focus is the requester's text, or empty.
	Focus string
}

// PromptProfile is one of a review's profiles, as another reviewer sees it.
type PromptProfile struct {
	Name        string
	Description string
}

// AgentPrompt is the review agent's prompt: start every subagent, check
// the report, retry a missing profile once, and summarize. The requester's
// focus, if any, follows under "From the requester".
func AgentPrompt(p *Prompt) (string, error) {
	return execute(agentPrompt, p)
}

// SubagentTask is the task that ends profile's subagent prompt: what to
// review, how to see the changes, what the review's other profiles cover,
// and how to submit.
func SubagentTask(p *Prompt, profile string) (string, error) {
	others := make([]PromptProfile, 0, len(p.Profiles))
	for _, other := range p.Profiles {
		if other.Name != profile {
			others = append(others, other)
		}
	}
	data := struct {
		*Prompt

		Profile string
		Others  []PromptProfile
	}{p, profile, others}
	return execute(subagentTask, data)
}

func execute(t *template.Template, data any) (string, error) {
	var b strings.Builder
	if err := t.Execute(&b, data); err != nil {
		return "", fmt.Errorf("writing the review prompt: %w", err)
	}
	return strings.TrimSpace(b.String()) + "\n", nil
}
