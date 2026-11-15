// Package tools is the runner's built-in tool set (ADR 0065): file tools
// confined to one directory, a shell through internal/gate, the verdict,
// and the scrub and redaction rules of ADR 0066.
package tools

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// MaxResult is the most text a tool result keeps.
const MaxResult = 16 << 10

// Tool is one tool: its schema for the model and its implementation.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any // JSON schema
	// Call runs the tool with the model's raw JSON arguments. A returned
	// error is shown to the model as "error: <err>"; Result.Done ends the run.
	Call func(ctx context.Context, arguments string) (Result, error)
}

// Result is what a tool returns to the model.
type Result struct {
	Text      string
	Truncated bool
	Done      bool   // record_result succeeded
	Verdict   string // with Done
}

// Config is what the tools act on.
type Config struct {
	Dir      string   // the session directory; paths are relative to it
	Root     string   // the workspace directory; paths must stay inside it
	Branch   string   // the role's BRANCH, for the push guard; "" for none
	Verdicts []string // allowed verdicts, pass then fail
	Recorder recorder
	Shell    shellRunner // a gate.Runner built with Environ's output
}

// New returns read_file, list_files, search, write_file, edit_file, run,
// record_result, in that order.
//
//nolint:gocritic // the signature is the spec; the config is copied once here
func New(cfg Config) []Tool {
	tools := []Tool{
		readFileTool(&cfg),
		listFilesTool(&cfg),
		searchTool(&cfg),
		writeFileTool(&cfg),
		editFileTool(&cfg),
		runTool(&cfg),
		recordResultTool(&cfg),
	}
	for i := range tools {
		tools[i].Call = capped(tools[i].Call)
	}
	return tools
}

// Validate checks arguments against a tool's schema: valid JSON, every
// required field present, and the top-level types string, integer and
// boolean right. Other schema keywords are not checked.
func Validate(schema map[string]any, arguments string) error {
	var args map[string]jsontext.Value
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return fmt.Errorf("arguments are not a JSON object: %w", err)
	}
	required, _ := schema["required"].([]string)
	for _, field := range required {
		if _, ok := args[field]; !ok {
			return fmt.Errorf("missing %s", field)
		}
	}
	properties, _ := schema["properties"].(map[string]map[string]any)
	for _, name := range slices.Sorted(maps.Keys(properties)) {
		value, ok := args[name]
		kind, _ := properties[name]["type"].(string)
		if ok && !matchesType(value, kind) {
			return fmt.Errorf("%s must be of type %s", name, kind)
		}
	}
	return nil
}

// matchesType reports whether a raw JSON value has the schema type kind.
// Types other than string, integer and boolean always match.
func matchesType(raw jsontext.Value, kind string) bool {
	switch kind {
	case "string":
		var s string
		return json.Unmarshal(raw, &s) == nil
	case "integer":
		var n int64
		return json.Unmarshal(raw, &n) == nil
	case "boolean":
		var b bool
		return json.Unmarshal(raw, &b) == nil
	}
	return true
}

// capped cuts a tool's text to MaxResult bytes.
func capped(call func(context.Context, string) (Result, error)) func(context.Context, string) (Result, error) {
	return func(ctx context.Context, arguments string) (Result, error) {
		res, err := call(ctx, arguments)
		if err != nil {
			return res, err
		}
		text, cut := capText(res.Text)
		res.Text = text
		res.Truncated = res.Truncated || cut
		return res, nil
	}
}

func capText(s string) (string, bool) {
	if len(s) <= MaxResult {
		return s, false
	}
	kept := strings.ToValidUTF8(s[:MaxResult], "")
	return fmt.Sprintf("%s\n[truncated: %d more bytes]", kept, len(s)-MaxResult), true
}

// typed decodes a tool's arguments into A and runs it.
func typed[A any](run func(context.Context, A) (Result, error)) func(context.Context, string) (Result, error) {
	return func(ctx context.Context, arguments string) (Result, error) {
		var args A
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			return Result{}, fmt.Errorf("arguments: %w", err)
		}
		return run(ctx, args)
	}
}

// objectSchema is the JSON schema of a tool's arguments.
func objectSchema(properties map[string]map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": properties, "required": required}
}

// field is one property of objectSchema.
func field(kind, description string) map[string]any {
	return map[string]any{"type": kind, "description": description}
}
