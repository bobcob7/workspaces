package fakeollama

import (
	"cmp"
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
	errRawAndArguments      = errors.New("set at most one of arguments and raw_arguments")
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

// assistantTurns counts the assistant messages, which is the turn the reply
// to req is.
func assistantTurns(req openaiwire.ChatRequest) int {
	n := 0
	for _, msg := range req.Messages {
		if msg.Role == openaiwire.RoleAssistant {
			n++
		}
	}
	return n
}

// reply is the generated assistant turn.
type reply struct {
	reasoning string
	content   string
	toolCalls []openaiwire.ToolCall
}

// rule is a compiled Rule. Exactly one of reply and tool is set.
type rule struct {
	pattern     *regexp.Regexp
	reply       *template.Template
	tool        string
	arguments   *template.Template
	raw         bool
	anyTool     bool
	turn        *int
	matchResult *regexp.Regexp
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
	// ToolResults are the trailing tool results.
	ToolResults []string
	// Turn is the number of assistant messages before the reply.
	Turn int
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
	if err := validateRuleFields(r); err != nil {
		return rule{}, err
	}
	pattern, err := regexp.Compile(r.Match)
	if err != nil {
		return rule{}, err
	}
	c := rule{pattern: pattern, tool: r.Tool, raw: r.RawArguments != "", anyTool: r.AnyTool, turn: r.Turn}
	if r.MatchToolResult != "" {
		if c.matchResult, err = regexp.Compile(r.MatchToolResult); err != nil {
			return rule{}, err
		}
	}
	if c.reply, err = parseTemplate(name, r.Reply); err != nil {
		return rule{}, err
	}
	if c.arguments, err = parseTemplate(name+" arguments", cmp.Or(r.Arguments, r.RawArguments)); err != nil {
		return rule{}, err
	}
	return c, nil
}

func validateRuleFields(r Rule) error {
	switch {
	case (r.Reply == "") == (r.Tool == ""):
		return errRuleAction
	case r.Arguments != "" && r.RawArguments != "":
		return errRawAndArguments
	case r.Arguments != "" && r.Tool == "":
		return errArgumentsWithoutTool
	case (r.RawArguments != "" || r.AnyTool) && r.Tool == "":
		return errArgumentsWithoutTool
	}
	return nil
}

// parseTemplate parses text, returning nil for empty text.
func parseTemplate(name, text string) (*template.Template, error) {
	if text == "" {
		return nil, nil //nolint:nilnil // empty template text means the field has no template.
	}
	return template.New(name).Funcs(templateFuncs()).Option("missingkey=error").Parse(text)
}

func newTemplateData(req openaiwire.ChatRequest, last string, match []string) templateData {
	data := templateData{Model: req.Model, Last: last, Match: match, ToolResults: trailingToolResults(req), Turn: assistantTurns(req)}
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

// answer applies the first scripted rule (one with turn or match_tool_result)
// that applies, then states trailing tool results, then applies the first
// other rule that matches the last user message and can apply, and otherwise
// echoes the message. Tool rules apply only when the request offers the tool
// (or the rule sets any_tool) and the model supports tools, so a model only
// calls tools when a rule asks for it.
func answer(rules []rule, req openaiwire.ChatRequest, m Model) (reply, error) {
	last := lastUserText(req)
	if rep, ok, err := firstApplying(rules, req, m, last, true); ok || err != nil {
		return rep, err
	}
	if results := trailingToolResults(req); len(results) > 0 {
		return reply{content: "The tool returned: " + strings.Join(results, "\n")}, nil
	}
	if rep, ok, err := firstApplying(rules, req, m, last, false); ok || err != nil {
		return rep, err
	}
	return reply{content: last}, nil
}

// firstApplying returns the reply of the first rule that applies to req,
// considering only scripted rules (those with turn or match_tool_result) or
// only the others.
func firstApplying(rules []rule, req openaiwire.ChatRequest, m Model, last string, scripted bool) (reply, bool, error) {
	for _, r := range rules {
		if r.scripted() != scripted {
			continue
		}
		if rep, ok, err := r.apply(req, m, last); ok || err != nil {
			return rep, ok, err
		}
	}
	return reply{}, false, nil
}

func (r rule) scripted() bool {
	return r.turn != nil || r.matchResult != nil
}

// apply returns the reply the rule gives to req, or false if the rule doesn't
// apply to it.
func (r rule) apply(req openaiwire.ChatRequest, m Model, last string) (reply, bool, error) {
	match := r.pattern.FindStringSubmatch(last)
	if match == nil || !r.conditionsHold(req) {
		return reply{}, false, nil
	}
	data := newTemplateData(req, last, match)
	if r.reply != nil {
		content, err := render(r.reply, data)
		return reply{content: content}, true, err
	}
	tool, ok := r.toolFor(req)
	if !ok || !m.has(CapabilityTools) {
		return reply{}, false, nil
	}
	call, err := r.toolCall(tool, data)
	return reply{toolCalls: []openaiwire.ToolCall{call}}, true, err
}

// conditionsHold reports whether the rule's turn and tool result conditions
// hold for req.
func (r rule) conditionsHold(req openaiwire.ChatRequest) bool {
	if r.turn != nil && *r.turn != assistantTurns(req) {
		return false
	}
	if r.matchResult == nil {
		return true
	}
	results := trailingToolResults(req)
	return len(results) > 0 && r.matchResult.MatchString(strings.Join(results, "\n"))
}

func offeredTool(req openaiwire.ChatRequest, name string) (openaiwire.Tool, bool) {
	i := slices.IndexFunc(req.Tools, func(t openaiwire.Tool) bool { return t.Function.Name == name })
	if i < 0 {
		return openaiwire.Tool{}, false
	}
	return req.Tools[i], true
}

// toolFor returns the tool the rule calls: the offered one, or a tool with no
// parameters when the rule sets any_tool.
func (r rule) toolFor(req openaiwire.ChatRequest) (openaiwire.Tool, bool) {
	if r.anyTool {
		return openaiwire.Tool{Type: openaiwire.ToolTypeFunction, Function: openaiwire.FunctionDefinition{Name: r.tool}}, true
	}
	return offeredTool(req, r.tool)
}

// toolCall calls tool with arguments rendered from the rule's template, or
// built from the tool's schema when the rule has none. Raw arguments are sent
// as rendered, valid JSON or not.
func (r rule) toolCall(tool openaiwire.Tool, data templateData) (openaiwire.ToolCall, error) {
	args, err := argumentsFor(tool.Function.Parameters)
	if r.arguments != nil {
		args, err = render(r.arguments, data)
	}
	if err != nil {
		return openaiwire.ToolCall{}, err
	}
	if !r.raw && !jsontext.Value(args).IsValid() {
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
