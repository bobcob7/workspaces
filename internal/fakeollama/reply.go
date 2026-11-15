package fakeollama

import (
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"math/rand/v2"
	"regexp"
	"slices"
	"strings"
	"text/template"

	"github.com/bobcob7/workspaces/internal/fakeollama/internal/openaiwire"
)

const toolCallIDLetters = "abcdefghijklmnopqrstuvwxyz0123456789"

var (
	errRuleAction           = errors.New("set exactly one of reply and tool")
	errArgumentsWithoutTool = errors.New("arguments need a tool")
	errInvalidArguments     = errors.New("tool arguments are not valid JSON")
)

// lastUserText returns the text of the last user message.
func lastUserText(req openaiwire.ChatRequest) string {
	for _, msg := range slices.Backward(req.Messages) {
		if msg.Role == openaiwire.RoleUser {
			return msg.Text()
		}
	}
	return ""
}

// trailingToolResults returns the contents of the tool messages that end the
// conversation.
func trailingToolResults(req openaiwire.ChatRequest) []string {
	var results []string
	for i := len(req.Messages) - 1; i >= 0 && req.Messages[i].Role == openaiwire.RoleTool; i-- {
		results = append([]string{req.Messages[i].Text()}, results...)
	}
	return results
}

// reply is the generated assistant turn.
type reply struct {
	reasoning string
	content   string
	toolCalls []openaiwire.ToolCall
}

// rule is a compiled Rule. Exactly one of reply and tool is set.
type rule struct {
	pattern   *regexp.Regexp
	reply     *template.Template
	tool      string
	arguments *template.Template
}

// templateData is what rule reply and arguments templates can reference.
type templateData struct {
	// Model is the requested model.
	Model string
	// Messages is the whole conversation.
	Messages []templateMessage
	// Last is the last user message.
	Last string
	// Match holds the rule's submatches in Last; Match[0] is the whole match.
	Match []string
	// Transcript is every message's content, one per line.
	Transcript string
}

type templateMessage struct {
	Role    string
	Content string
}

func templateFuncs() template.FuncMap {
	return template.FuncMap{"find": find}
}

// find returns the first submatch of pattern in text, or the whole match if
// the pattern has no groups, or "" if it doesn't match. Its argument order
// lets it be used in pipelines: {{ .Transcript | find "code: (\\w+)" }}.
func find(pattern, text string) (string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("compiling %q: %w", pattern, err)
	}
	m := re.FindStringSubmatch(text)
	switch {
	case m == nil:
		return "", nil
	case len(m) > 1:
		return m[1], nil
	default:
		return m[0], nil
	}
}

func compileRules(rules []Rule) ([]rule, error) {
	compiled := make([]rule, 0, len(rules))
	for i, r := range rules {
		c, err := compileRule(fmt.Sprintf("rule %d", i+1), r)
		if err != nil {
			return nil, fmt.Errorf("%w: rule %d: %w", errInvalidConfig, i+1, err)
		}
		compiled = append(compiled, c)
	}
	return compiled, nil
}

func compileRule(name string, r Rule) (rule, error) {
	switch {
	case (r.Reply == "") == (r.Tool == ""):
		return rule{}, errRuleAction
	case r.Arguments != "" && r.Tool == "":
		return rule{}, errArgumentsWithoutTool
	}
	pattern, err := regexp.Compile(r.Match)
	if err != nil {
		return rule{}, err
	}
	c := rule{pattern: pattern, tool: r.Tool}
	if c.reply, err = parseTemplate(name, r.Reply); err != nil {
		return rule{}, err
	}
	if c.arguments, err = parseTemplate(name+" arguments", r.Arguments); err != nil {
		return rule{}, err
	}
	return c, nil
}

// parseTemplate parses text, returning nil for empty text.
func parseTemplate(name, text string) (*template.Template, error) {
	if text == "" {
		return nil, nil //nolint:nilnil // empty template text means the field has no template.
	}
	return template.New(name).Funcs(templateFuncs()).Option("missingkey=error").Parse(text)
}

func newTemplateData(req openaiwire.ChatRequest, last string, match []string) templateData {
	data := templateData{Model: req.Model, Last: last, Match: match}
	lines := make([]string, 0, len(req.Messages))
	for _, m := range req.Messages {
		data.Messages = append(data.Messages, templateMessage{Role: m.Role, Content: m.Text()})
		lines = append(lines, m.Text())
	}
	data.Transcript = strings.Join(lines, "\n")
	return data
}

func render(tmpl *template.Template, data templateData) (string, error) {
	var out strings.Builder
	if err := tmpl.Execute(&out, data); err != nil {
		return "", fmt.Errorf("rendering %s: %w", tmpl.Name(), err)
	}
	return out.String(), nil
}

// respond computes the deterministic reply to req from model m.
func respond(rules []rule, req openaiwire.ChatRequest, m Model) (reply, error) {
	rep, err := answer(rules, req, m)
	if err != nil {
		return reply{}, err
	}
	if m.has(CapabilityReasoning) && req.ReasoningEffort != "none" {
		rep.reasoning = "Working out a reply to: " + lastUserText(req)
	}
	return rep, nil
}

// answer states trailing tool results, or applies the first rule that
// matches the last user message and can apply, or echoes the message. Tool
// rules apply only when the request offers the tool and the model supports
// tools, so a model only calls tools when a rule asks for it.
func answer(rules []rule, req openaiwire.ChatRequest, m Model) (reply, error) {
	if results := trailingToolResults(req); len(results) > 0 {
		return reply{content: "The tool returned: " + strings.Join(results, "\n")}, nil
	}
	last := lastUserText(req)
	for _, r := range rules {
		match := r.pattern.FindStringSubmatch(last)
		if match == nil {
			continue
		}
		data := newTemplateData(req, last, match)
		if r.reply != nil {
			content, err := render(r.reply, data)
			return reply{content: content}, err
		}
		tool, ok := offeredTool(req, r.tool)
		if !ok || !m.has(CapabilityTools) {
			continue
		}
		call, err := r.toolCall(tool, data)
		return reply{toolCalls: []openaiwire.ToolCall{call}}, err
	}
	return reply{content: last}, nil
}

func offeredTool(req openaiwire.ChatRequest, name string) (openaiwire.Tool, bool) {
	i := slices.IndexFunc(req.Tools, func(t openaiwire.Tool) bool { return t.Function.Name == name })
	if i < 0 {
		return openaiwire.Tool{}, false
	}
	return req.Tools[i], true
}

// toolCall calls tool with arguments rendered from the rule's template, or
// built from the tool's schema when the rule has none.
func (r rule) toolCall(tool openaiwire.Tool, data templateData) (openaiwire.ToolCall, error) {
	args, err := argumentsFor(tool.Function.Parameters)
	if r.arguments != nil {
		args, err = render(r.arguments, data)
	}
	if err != nil {
		return openaiwire.ToolCall{}, err
	}
	if !jsontext.Value(args).IsValid() {
		return openaiwire.ToolCall{}, fmt.Errorf("%w: %s", errInvalidArguments, args)
	}
	call := openaiwire.ToolCall{ID: newToolCallID(), Type: openaiwire.ToolTypeFunction, Function: openaiwire.FunctionCall{Name: tool.Function.Name, Arguments: args}}
	return call, nil
}

// newToolCallID returns an ID in the format Ollama uses.
func newToolCallID() string {
	b := make([]byte, 8)
	for i := range b {
		b[i] = toolCallIDLetters[rand.IntN(len(toolCallIDLetters))] //nolint:gosec // G404: tool call IDs need variety, not secrecy.
	}
	return "call_" + string(b)
}

func newCompletionID() string {
	return fmt.Sprintf("chatcmpl-%d", rand.IntN(999)) //nolint:gosec // G404: completion IDs need variety, not secrecy.
}
