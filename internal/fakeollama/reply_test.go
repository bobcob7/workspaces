package fakeollama

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/fakeollama/internal/openaiwire"
)

func msg(role, content string) openaiwire.Message {
	return openaiwire.TextMessage(role, content)
}

func orderTool() openaiwire.Tool {
	return openaiwire.Tool{Type: openaiwire.ToolTypeFunction, Function: openaiwire.FunctionDefinition{
		Name: "lookup_order_status",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"order_id": map[string]any{"type": "string"}},
			"required":   []any{"order_id"},
		},
	}}
}

func orderRule() Rule {
	return Rule{Match: `(?i)status of order ([A-Z]-\d+)`, Tool: "lookup_order_status", Arguments: `{"order_id":"{{ index .Match 1 }}"}`}
}

func mustRules(t *testing.T, rules ...Rule) []rule {
	t.Helper()
	compiled, err := compileRules(rules)
	require.NoError(t, err)
	return compiled
}

func chatModel() Model {
	return Model{Name: "fake-chat", Capabilities: []Capability{CapabilityTools}}
}

func reasoningModel() Model {
	return Model{Name: "fake-reasoning", Capabilities: []Capability{CapabilityTools, CapabilityReasoning}}
}

func TestLastUserText(t *testing.T) {
	t.Run("returns the last user message", func(t *testing.T) {
		req := openaiwire.ChatRequest{Messages: []openaiwire.Message{msg("user", "first"), msg("assistant", "ok"), msg("user", "second"), msg("tool", "{}")}}
		assert.Equal(t, "second", lastUserText(req))
	})
	t.Run("returns empty without user messages", func(t *testing.T) {
		assert.Empty(t, lastUserText(openaiwire.ChatRequest{Messages: []openaiwire.Message{msg("system", "be nice")}}))
	})
}

func TestTrailingToolResults(t *testing.T) {
	t.Run("returns consecutive tool results in order", func(t *testing.T) {
		req := openaiwire.ChatRequest{Messages: []openaiwire.Message{msg("tool", "old"), msg("user", "hi"), msg("tool", "a"), msg("tool", "b")}}
		assert.Equal(t, []string{"a", "b"}, trailingToolResults(req))
	})
	t.Run("returns nothing when the last message isn't a tool result", func(t *testing.T) {
		req := openaiwire.ChatRequest{Messages: []openaiwire.Message{msg("tool", "a"), msg("user", "hi")}}
		assert.Empty(t, trailingToolResults(req))
	})
}

func TestFind(t *testing.T) {
	t.Run("returns the first group", func(t *testing.T) {
		got, err := find(`code: (\w+)`, "the code: abc here")
		require.NoError(t, err)
		assert.Equal(t, "abc", got)
	})
	t.Run("returns the whole match without groups", func(t *testing.T) {
		got, err := find(`\d+`, "order 42")
		require.NoError(t, err)
		assert.Equal(t, "42", got)
	})
	t.Run("returns empty without a match", func(t *testing.T) {
		got, err := find(`\d+`, "none")
		require.NoError(t, err)
		assert.Empty(t, got)
	})
	t.Run("rejects invalid patterns", func(t *testing.T) {
		_, err := find(`(`, "x")
		assert.Error(t, err)
	})
}

func TestCompileRules(t *testing.T) {
	t.Run("rejects invalid patterns", func(t *testing.T) {
		_, err := compileRules([]Rule{{Match: "(", Reply: "x"}})
		require.ErrorIs(t, err, errInvalidConfig)
		assert.ErrorContains(t, err, "rule 1")
	})
	t.Run("rejects rules with both a reply and a tool", func(t *testing.T) {
		_, err := compileRules([]Rule{{Match: "a", Reply: "x", Tool: "t"}})
		require.ErrorIs(t, err, errRuleAction)
		assert.ErrorIs(t, err, errInvalidConfig)
	})
	t.Run("rejects rules with neither a reply nor a tool", func(t *testing.T) {
		_, err := compileRules([]Rule{{Match: "a"}})
		assert.ErrorIs(t, err, errRuleAction)
	})
	t.Run("rejects arguments without a tool", func(t *testing.T) {
		_, err := compileRules([]Rule{{Match: "a", Reply: "x", Arguments: "{}"}})
		assert.ErrorIs(t, err, errArgumentsWithoutTool)
	})
	t.Run("rejects invalid arguments templates", func(t *testing.T) {
		_, err := compileRules([]Rule{{Match: "a", Tool: "t", Arguments: "{{ .Match"}})
		require.ErrorIs(t, err, errInvalidConfig)
		assert.ErrorContains(t, err, "rule 1")
	})
	t.Run("rejects invalid templates", func(t *testing.T) {
		_, err := compileRules([]Rule{{Match: "a", Reply: "ok"}, {Match: "b", Reply: "{{ .Last"}})
		require.ErrorIs(t, err, errInvalidConfig)
		assert.ErrorContains(t, err, "rule 2")
	})
}

func TestRespond(t *testing.T) {
	t.Run("states a tool result", func(t *testing.T) {
		req := openaiwire.ChatRequest{Messages: []openaiwire.Message{msg("user", "status?"), msg("assistant", ""), msg("tool", `{"tracking_code":"TRK-1"}`)}, Tools: []openaiwire.Tool{orderTool()}}
		rep, err := respond(nil, req, chatModel())
		require.NoError(t, err)
		assert.Equal(t, `The tool returned: {"tracking_code":"TRK-1"}`, rep.content)
	})
	t.Run("calls the tool a matching rule names, with templated arguments", func(t *testing.T) {
		other := orderTool()
		other.Function.Name = "other"
		req := openaiwire.ChatRequest{Messages: []openaiwire.Message{msg("user", "status of order A-7?")}, Tools: []openaiwire.Tool{other, orderTool()}}
		rep, err := respond(mustRules(t, orderRule()), req, chatModel())
		require.NoError(t, err)
		require.Len(t, rep.toolCalls, 1)
		assert.Empty(t, rep.content)
		assert.Equal(t, openaiwire.ToolTypeFunction, rep.toolCalls[0].Type)
		assert.Equal(t, openaiwire.FunctionCall{Name: "lookup_order_status", Arguments: `{"order_id":"A-7"}`}, rep.toolCalls[0].Function)
	})
	t.Run("fills arguments from the tool's schema without a template", func(t *testing.T) {
		req := openaiwire.ChatRequest{Messages: []openaiwire.Message{msg("user", "status?")}, Tools: []openaiwire.Tool{orderTool()}}
		rep, err := respond(mustRules(t, Rule{Match: "status", Tool: "lookup_order_status"}), req, chatModel())
		require.NoError(t, err)
		require.Len(t, rep.toolCalls, 1)
		assert.JSONEq(t, `{"order_id":"example"}`, rep.toolCalls[0].Function.Arguments)
	})
	t.Run("replies with text when tools are offered but no tool rule matches", func(t *testing.T) {
		req := openaiwire.ChatRequest{Messages: []openaiwire.Message{msg("user", "hello")}, Tools: []openaiwire.Tool{orderTool()}}
		rep, err := respond(mustRules(t, orderRule()), req, chatModel())
		require.NoError(t, err)
		assert.Empty(t, rep.toolCalls)
		assert.Equal(t, "hello", rep.content)
	})
	t.Run("skips a tool rule whose tool isn't offered", func(t *testing.T) {
		req := openaiwire.ChatRequest{Messages: []openaiwire.Message{msg("user", "status of order A-7?")}}
		rep, err := respond(mustRules(t, orderRule(), Rule{Match: "status", Reply: "no tools here"}), req, chatModel())
		require.NoError(t, err)
		assert.Empty(t, rep.toolCalls)
		assert.Equal(t, "no tools here", rep.content)
	})
	t.Run("ignores tools when the model lacks tool support", func(t *testing.T) {
		req := openaiwire.ChatRequest{Messages: []openaiwire.Message{msg("user", "status of order A-7?")}, Tools: []openaiwire.Tool{orderTool()}}
		rep, err := respond(mustRules(t, orderRule()), req, Model{Name: "plain"})
		require.NoError(t, err)
		assert.Empty(t, rep.toolCalls)
		assert.Equal(t, "status of order A-7?", rep.content)
	})
	t.Run("fails when templated arguments aren't JSON", func(t *testing.T) {
		req := openaiwire.ChatRequest{Messages: []openaiwire.Message{msg("user", "status?")}, Tools: []openaiwire.Tool{orderTool()}}
		_, err := respond(mustRules(t, Rule{Match: "status", Tool: "lookup_order_status", Arguments: "{not json"}), req, chatModel())
		assert.ErrorIs(t, err, errInvalidArguments)
	})
	t.Run("fails when an arguments template fails", func(t *testing.T) {
		req := openaiwire.ChatRequest{Messages: []openaiwire.Message{msg("user", "status?")}, Tools: []openaiwire.Tool{orderTool()}}
		_, err := respond(mustRules(t, Rule{Match: "status", Tool: "lookup_order_status", Arguments: "{{ .Missing }}"}), req, chatModel())
		assert.ErrorContains(t, err, "rendering rule 1 arguments")
	})
	t.Run("uses the first matching rule", func(t *testing.T) {
		rules := mustRules(t, Rule{Match: "nope", Reply: "a"}, Rule{Match: "(?i)hello", Reply: "b"}, Rule{Match: ".", Reply: "c"})
		rep, err := respond(rules, openaiwire.ChatRequest{Messages: []openaiwire.Message{msg("user", "Hello")}}, chatModel())
		require.NoError(t, err)
		assert.Equal(t, "b", rep.content)
	})
	t.Run("rule templates can reference earlier messages", func(t *testing.T) {
		rules := mustRules(t, Rule{Match: "what was it", Reply: `{{ .Transcript | find "word: (\\w+)" }} ({{ len .Messages }} messages, {{ (index .Messages 1).Role }})`})
		req := openaiwire.ChatRequest{Messages: []openaiwire.Message{msg("user", "remember word: falcon"), msg("assistant", "OK"), msg("user", "what was it?")}}
		rep, err := respond(rules, req, chatModel())
		require.NoError(t, err)
		assert.Equal(t, "falcon (3 messages, assistant)", rep.content)
	})
	t.Run("rule templates can use submatches and the model", func(t *testing.T) {
		rules := mustRules(t, Rule{Match: `(\d+) \+ (\d+)`, Reply: `{{ .Model }}: {{ index .Match 1 }} and {{ index .Match 2 }} in "{{ .Last }}"`})
		rep, err := respond(rules, openaiwire.ChatRequest{Model: "m", Messages: []openaiwire.Message{msg("user", "1 + 2")}}, chatModel())
		require.NoError(t, err)
		assert.Equal(t, `m: 1 and 2 in "1 + 2"`, rep.content)
	})
	t.Run("fails when a template fails", func(t *testing.T) {
		rules := mustRules(t, Rule{Match: ".", Reply: `{{ .Missing }}`})
		_, err := respond(rules, openaiwire.ChatRequest{Messages: []openaiwire.Message{msg("user", "hi")}}, chatModel())
		assert.ErrorContains(t, err, "rendering rule 1")
	})
	t.Run("echoes the last user message", func(t *testing.T) {
		rep, err := respond(mustRules(t, Rule{Match: "nope", Reply: "x"}), openaiwire.ChatRequest{Messages: []openaiwire.Message{msg("user", "echo me")}}, chatModel())
		require.NoError(t, err)
		assert.Equal(t, reply{content: "echo me"}, rep)
	})
	t.Run("reasoning models reason before replying", func(t *testing.T) {
		rep, err := respond(nil, openaiwire.ChatRequest{Messages: []openaiwire.Message{msg("user", "hi")}}, reasoningModel())
		require.NoError(t, err)
		assert.Equal(t, "Working out a reply to: hi", rep.reasoning)
	})
	t.Run("reasoning models reason before tool calls", func(t *testing.T) {
		req := openaiwire.ChatRequest{Messages: []openaiwire.Message{msg("user", "status of order A-7?")}, Tools: []openaiwire.Tool{orderTool()}}
		rep, err := respond(mustRules(t, orderRule()), req, reasoningModel())
		require.NoError(t, err)
		assert.NotEmpty(t, rep.reasoning)
		assert.Len(t, rep.toolCalls, 1)
	})
	t.Run("reasoning effort none disables reasoning", func(t *testing.T) {
		rep, err := respond(nil, openaiwire.ChatRequest{ReasoningEffort: "none", Messages: []openaiwire.Message{msg("user", "hi")}}, reasoningModel())
		require.NoError(t, err)
		assert.Empty(t, rep.reasoning)
	})
}

func TestNewToolCallID(t *testing.T) {
	assert.Regexp(t, `^call_[a-z0-9]{8}$`, newToolCallID())
}

func TestNewCompletionID(t *testing.T) {
	assert.Regexp(t, `^chatcmpl-\d{1,3}$`, newCompletionID())
}
