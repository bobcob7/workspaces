package agent

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"strconv"
	"strings"

	"github.com/bobcob7/workspaces/internal/agent/tools"
	"github.com/bobcob7/workspaces/internal/llm"
)

// Nudge reasons (reference.md, "nudge").
const (
	nudgeNoToolCall   = "no_tool_call"
	nudgeTextToolCall = "text_tool_call"
	nudgeRepeat       = "repeat"
)

const (
	maxNoCall     = 3
	maxMalformed  = 3
	repeatNudgeAt = 3
	maxRepeats    = 5
)

// dispatch runs the calls of one reply, in order, until one ends the run.
func (r *run) dispatch(ctx context.Context, calls []llm.ToolCall) *exitInfo {
	for _, call := range calls {
		if ex := r.handleCall(ctx, call); ex != nil {
			return ex
		}
	}
	return nil
}

func (r *run) handleCall(ctx context.Context, call llm.ToolCall) *exitInfo {
	if err := r.check(call); err != nil {
		return r.invalid(call, err)
	}
	r.malformed = 0
	if ex := r.countRepeat(call); ex != nil {
		return ex
	}
	res, err := r.agent.cfg.Tools.Call(ctx, call.Name, call.Arguments)
	return r.answer(call, res, err)
}

// check reports why a call can't run: an unknown name, or arguments that
// don't match the tool's schema.
func (r *run) check(call llm.ToolCall) error {
	if !r.agent.cfg.Tools.Known(call.Name) {
		return fmt.Errorf("unknown tool %q", call.Name)
	}
	schema := r.schemaFor(call.Name)
	if schema == nil {
		return nil
	}
	return tools.Validate(schema, call.Arguments)
}

// invalid answers a refused call with the error, and ends the run after
// three in a row.
func (r *run) invalid(call llm.ToolCall, cause error) *exitInfo {
	text := "error: " + cause.Error()
	if schema := r.schemaFor(call.Name); schema != nil {
		text += "; the arguments must match: " + compactJSON(schema)
	}
	r.record(kindInvalidCall, InvalidCallEvent{ID: call.ID, Name: call.Name, Error: text})
	r.history = append(r.history, toolMessage(call, text))
	r.malformed++
	if r.malformed >= maxMalformed {
		return &exitInfo{reason: ExitMalformed, message: cause.Error()}
	}
	return nil
}

// countRepeat counts calls with the same name and arguments in a row. The
// third gets a nudge after its result; the fifth ends the run.
func (r *run) countRepeat(call llm.ToolCall) *exitInfo {
	signature := call.Name + "\x00" + call.Arguments
	if signature == r.repeatSig {
		r.repeats++
	} else {
		r.repeatSig, r.repeats = signature, 1
	}
	if r.repeats >= maxRepeats {
		return &exitInfo{reason: ExitLooping, message: fmt.Sprintf("%s was called %d times in a row with the same arguments", call.Name, r.repeats)}
	}
	if r.repeats >= repeatNudgeAt {
		r.pending = append(r.pending, nudgeRepeat)
	}
	return nil
}

// answer records a tool's result and adds it to the history. A record_result
// that succeeded ends the run.
func (r *run) answer(call llm.ToolCall, res tools.Result, err error) *exitInfo {
	text := res.Text
	if err != nil {
		text = "error: " + err.Error()
	} else {
		r.noteProgress(call, res.Text)
	}
	framed := frameResult(call.Name, res.Server, text)
	r.record(kindToolResult, ToolResultEvent{ID: call.ID, Name: call.Name, OK: err == nil, Bytes: len(text), Truncated: res.Truncated, Text: framed})
	r.history = append(r.history, toolMessage(call, framed))
	if err == nil && res.Done {
		return &exitInfo{reason: ExitDone, verdict: res.Verdict}
	}
	return nil
}

// progressArgs is the part of a call's arguments the progress note keeps.
type progressArgs struct {
	Path    string `json:"path"`
	Command string `json:"command"`
}

// noteProgress keeps what the progress note lists: the files a successful
// write or edit changed, and the commands a run executed with their exit codes.
// Reads and searches leave nothing in the note.
func (r *run) noteProgress(call llm.ToolCall, text string) {
	var args progressArgs
	// check already decoded these arguments, so the error can't happen here.
	_ = json.Unmarshal([]byte(call.Arguments), &args)
	switch call.Name {
	case "write_file", "edit_file":
		r.done = append(r.done, ToolRecord{Name: call.Name, Summary: args.Path})
	case "run":
		r.done = append(r.done, ToolRecord{Name: call.Name, Summary: firstBytes(args.Command, elideArgsBytes), ExitCode: exitCodeOf(text)})
	}
}

// exitCodeOf reads the code from a run result's first line, "exit N"; nil when
// the command timed out and printed none.
func exitCodeOf(text string) *int {
	line, _, _ := strings.Cut(text, "\n")
	code, ok := strings.CutPrefix(line, "exit ")
	if !ok {
		return nil
	}
	n, err := strconv.Atoi(code)
	if err != nil {
		return nil
	}
	return &n
}

// noToolCall nudges a reply that called no tool; the third in a row ends the run.
func (r *run) noToolCall(content string) *exitInfo {
	r.noCall++
	if r.noCall >= maxNoCall {
		return &exitInfo{reason: ExitNoResult, message: fmt.Sprintf("%d replies in a row had no tool call", r.noCall)}
	}
	reason := nudgeNoToolCall
	if strings.Contains(content, "<tool_call>") {
		reason = nudgeTextToolCall
	}
	r.nudge(reason)
	return nil
}

func (r *run) nudge(reason string) {
	r.record(kindNudge, NudgeEvent{Reason: reason})
	r.history = append(r.history, llm.Message{Role: llm.RoleUser, Content: nudgeMessage(reason)})
}

// flushNudges adds the nudges that wait for a reply's results to be in.
func (r *run) flushNudges() {
	for _, reason := range r.pending {
		r.nudge(reason)
	}
	r.pending = nil
}

func nudgeMessage(reason string) string {
	switch reason {
	case nudgeTextToolCall:
		return "Your tool call came back as text. Call the tool through the tools API."
	case nudgeRepeat:
		return "You already did this; its result is above. Do something else."
	}
	return "Call a tool. When the task is done, call record_result."
}

func (r *run) schemaFor(name string) map[string]any {
	for _, def := range r.agent.cfg.Tools.Definitions() {
		if def.Name == name {
			return def.Parameters
		}
	}
	return nil
}

func frameResult(name, server, text string) string {
	if server == "" {
		return "<tool_result tool=\"" + name + "\">" + text + "</tool_result>"
	}
	return "<tool_result tool=\"" + name + "\" server=\"" + server + "\">" + text + "</tool_result>"
}

func toolMessage(call llm.ToolCall, content string) llm.Message {
	return llm.Message{Role: llm.RoleTool, Content: content, ToolCallID: call.ID, ToolName: call.Name}
}

func compactJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(raw)
}
