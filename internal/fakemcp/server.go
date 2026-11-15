package fakemcp

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"slices"
	"sync/atomic"
	"text/template"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewServer builds the SDK server for cfg; record (may be nil) gets one
// JSON line per call: {"time","tool","arguments"}.
func NewServer(cfg Config, record io.Writer, log *slog.Logger) (*mcp.Server, error) {
	return newServer(cfg, record, log, os.Exit) //nolint:forbidigo // the crash fault ends the fake process, as a crashing server would.
}

// RequireHeaders wraps h, answering 401 when a required header is missing or wrong.
func RequireHeaders(cfg Config, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for name, want := range cfg.RequireHeader {
			if r.Header.Get(name) != want {
				http.Error(w, "unauthorized: missing or wrong header "+name, http.StatusUnauthorized)
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}

func newServer(cfg Config, record io.Writer, log *slog.Logger, exit func(int)) (*mcp.Server, error) {
	tools, err := compileTools(append(slices.Clone(cfg.Tools), generatedTools(cfg.Generate)...))
	if err != nil {
		return nil, err
	}
	srv := mcp.NewServer(
		&mcp.Implementation{Name: cfg.Server.Name, Version: cfg.Server.Version},
		&mcp.ServerOptions{Instructions: cfg.Server.Instructions, Logger: log},
	)
	f := &fake{crashAfter: cfg.Faults.CrashAfterCalls, slowCall: cfg.Faults.SlowCall, exit: exit}
	if record != nil {
		f.record = &recorder{w: record}
	}
	for _, t := range tools {
		srv.AddTool(t.tool, f.toolHandler(t))
	}
	if cfg.Faults.SlowInitialize > 0 {
		srv.AddReceivingMiddleware(slowInitialize(cfg.Faults.SlowInitialize))
	}
	return srv, nil
}

// fake holds what every tool handler shares: the call counter, the faults and the recorder.
type fake struct {
	calls      atomic.Int64
	crashAfter int
	slowCall   time.Duration
	exit       func(int)
	record     *recorder
}

type compiledTool struct {
	tool    *mcp.Tool
	replies []compiledReply
}

type compiledReply struct {
	spec  Reply
	match *regexp.Regexp
	text  *template.Template
}

type arguments struct {
	canonical []byte
	values    map[string]any
}

func compileTools(specs []Tool) ([]compiledTool, error) {
	seen := make(map[string]bool, len(specs))
	tools := make([]compiledTool, 0, len(specs))
	for _, spec := range specs {
		if seen[spec.Name] {
			return nil, fmt.Errorf("duplicate tool %q", spec.Name)
		}
		seen[spec.Name] = true
		t, err := compileTool(spec)
		if err != nil {
			return nil, err
		}
		tools = append(tools, t)
	}
	return tools, nil
}

func compileTool(spec Tool) (compiledTool, error) {
	if spec.Name == "" {
		return compiledTool{}, errors.New("tool without a name")
	}
	if !isObjectSchema(spec.InputSchema) {
		return compiledTool{}, fmt.Errorf("tool %s: input_schema type must be object", spec.Name)
	}
	replies := make([]compiledReply, 0, len(spec.Replies))
	for _, r := range spec.Replies {
		c, err := compileReply(r)
		if err != nil {
			return compiledTool{}, fmt.Errorf("tool %s: %w", spec.Name, err)
		}
		replies = append(replies, c)
	}
	tool := &mcp.Tool{
		Name:        spec.Name,
		Description: spec.Description,
		InputSchema: spec.InputSchema,
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    derefBool(spec.Annotations.ReadOnlyHint),
			DestructiveHint: spec.Annotations.DestructiveHint,
		},
	}
	return compiledTool{tool: tool, replies: replies}, nil
}

func compileReply(r Reply) (compiledReply, error) {
	if setFields(r) > 1 {
		return compiledReply{}, errors.New("a reply sets more than one of text, error and protocol_error")
	}
	match, err := regexp.Compile(r.Match)
	if err != nil {
		return compiledReply{}, fmt.Errorf("match %q: %w", r.Match, err)
	}
	text, err := template.New("text").Parse(r.Text)
	if err != nil {
		return compiledReply{}, fmt.Errorf("text template: %w", err)
	}
	return compiledReply{spec: r, match: match, text: text}, nil
}

func setFields(r Reply) int {
	n := 0
	for _, s := range []string{r.Text, r.Error, r.ProtocolError} {
		if s != "" {
			n++
		}
	}
	return n
}

func isObjectSchema(schema map[string]any) bool {
	typ, ok := schema["type"].(string)
	return ok && typ == "object"
}

func derefBool(b *bool) bool {
	return b != nil && *b
}

// generatedTools are the filler tools of Generate: read-only, replying "ok".
func generatedTools(g *Generate) []Tool {
	if g == nil {
		return nil
	}
	prefix := g.Prefix
	if prefix == "" {
		prefix = "generated_"
	}
	readOnly := true
	tools := make([]Tool, 0, g.Count)
	for i := 1; i <= g.Count; i++ {
		name := fmt.Sprintf("%s%03d", prefix, i)
		tools = append(tools, Tool{
			Name:        name,
			Description: fmt.Sprintf("Generated fake tool %s.", name),
			InputSchema: map[string]any{"type": "object"},
			Annotations: Annotations{ReadOnlyHint: &readOnly},
			Replies:     []Reply{{Text: "ok"}},
		})
	}
	return tools
}

func (f *fake) toolHandler(tool compiledTool) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return f.call(ctx, tool, []byte(req.Params.Arguments))
	}
}

func (f *fake) call(ctx context.Context, tool compiledTool, raw []byte) (*mcp.CallToolResult, error) {
	n := f.calls.Add(1)
	args, err := decodeArguments(raw)
	if err != nil {
		return nil, err
	}
	if err := f.record.write(tool.tool.Name, args.canonical); err != nil {
		return nil, err
	}
	if f.crashAfter > 0 && n > int64(f.crashAfter) {
		f.exit(3)
		return nil, &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: "fake-mcp crashed"}
	}
	if err := sleepContext(ctx, f.slowCall); err != nil {
		return nil, err
	}
	reply := matchReply(tool.replies, args.canonical)
	if reply == nil {
		return errorResult("no reply matched"), nil
	}
	return reply.result(args.values)
}

func decodeArguments(raw []byte) (arguments, error) {
	values := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &values); err != nil {
			return arguments{}, fmt.Errorf("decoding arguments: %w", err)
		}
	}
	if values == nil {
		values = map[string]any{}
	}
	canonical, err := json.Marshal(values)
	if err != nil {
		return arguments{}, fmt.Errorf("encoding arguments: %w", err)
	}
	return arguments{canonical: canonical, values: values}, nil
}

func matchReply(replies []compiledReply, canonical []byte) *compiledReply {
	for i := range replies {
		if replies[i].match.Match(canonical) {
			return &replies[i]
		}
	}
	return nil
}

func (r compiledReply) result(args map[string]any) (*mcp.CallToolResult, error) {
	switch {
	case r.spec.ProtocolError != "":
		return nil, &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: r.spec.ProtocolError}
	case r.spec.Error != "":
		return errorResult(r.spec.Error), nil
	}
	var text bytes.Buffer
	if err := r.text.Execute(&text, struct{ Args map[string]any }{args}); err != nil {
		return nil, fmt.Errorf("executing reply text: %w", err)
	}
	return textResult(text.String(), false), nil
}

func textResult(text string, isError bool) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}, IsError: isError}
}

func errorResult(text string) *mcp.CallToolResult {
	return textResult(text, true)
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// slowInitialize delays the handshake: initialize, or server/discover under
// the new protocol, which is what current clients send first.
func slowInitialize(d time.Duration) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "initialize" || method == "server/discover" {
				if err := sleepContext(ctx, d); err != nil {
					return nil, err
				}
			}
			return next(ctx, method, req)
		}
	}
}
