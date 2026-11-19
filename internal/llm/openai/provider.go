// Package openai is the runner's provider for OpenAI-compatible chat
// completions (ADR 0062): Ollama's /v1, llm-router, LM Studio, vLLM. It is
// the only package that requests chat completions. The stream accumulator
// and reasoning handling are copied from llm-router internal/agent @9789c32.
package openai

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"

	"github.com/bobcob7/workspaces/internal/llm"
)

// Provider speaks the OpenAI-compatible API to one endpoint.
type Provider struct {
	endpoint    llm.Endpoint
	completions chatCompleter
	httpClient  *http.Client
	auto        autoProbe
}

// New returns the provider for ep. The endpoint's timeouts apply per
// request; httpClient supplies the transport.
func New(ep llm.Endpoint, httpClient *http.Client, log *slog.Logger) *Provider {
	warnPlainHTTP(ep, log)
	client := openai.NewClient(
		option.WithBaseURL(ep.URL+"/v1"),
		option.WithAPIKey(apiKey(ep.Token)),
		option.WithHTTPClient(httpClient),
		option.WithMaxRetries(0),
	)
	return &Provider{endpoint: ep, completions: &client.Chat.Completions, httpClient: httpClient}
}

// Chat sends req and returns the whole answer. The request is streamed, and
// the endpoint's request_timeout and idle_timeout bound it.
func (p *Provider) Chat(ctx context.Context, req llm.Request) (llm.Response, error) {
	reqCtx, cancel := p.requestContext(ctx)
	defer cancel(nil)
	start := time.Now()
	res, err := p.stream(reqCtx, cancel, req)
	if err != nil {
		return llm.Response{}, classify(ctx, reqCtx, err)
	}
	res.Duration = time.Since(start)
	return res, nil
}

// Models lists the model ids the endpoint serves (GET /v1/models).
func (p *Provider) Models(ctx context.Context) ([]string, error) {
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := p.getJSON(ctx, "/v1/models", &list); err != nil {
		return nil, fmt.Errorf("listing models: %w", err)
	}
	ids := make([]string, 0, len(list.Data))
	for _, m := range list.Data {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

// requestContext bounds one request by the endpoint's request_timeout. The
// deadline is a cancel cause, so the caller's own context stays distinct.
func (p *Provider) requestContext(ctx context.Context) (context.Context, context.CancelCauseFunc) {
	reqCtx, cancel := context.WithCancelCause(ctx)
	if p.endpoint.RequestTimeout <= 0 {
		return reqCtx, cancel
	}
	timer := time.AfterFunc(p.endpoint.RequestTimeout, func() { cancel(errRequestTimeout) })
	return reqCtx, func(cause error) {
		timer.Stop()
		cancel(cause)
	}
}

func requestOptions(req llm.Request, guard *streamGuard) []option.RequestOption {
	opts := []option.RequestOption{option.WithMiddleware(guard.middleware)}
	if req.TraceParent != "" {
		opts = append(opts, option.WithHeader("traceparent", req.TraceParent))
	}
	return opts
}

// chatParams builds the request body. The model id is sent as given.
func chatParams(req llm.Request) openai.ChatCompletionNewParams {
	params := openai.ChatCompletionNewParams{
		Model:         req.Model,
		Messages:      chatMessages(req),
		Tools:         chatTools(req.Tools),
		Temperature:   openai.Float(req.Temperature),
		StreamOptions: openai.ChatCompletionStreamOptionsParam{IncludeUsage: openai.Bool(true)},
	}
	if req.MaxOutput > 0 {
		params.MaxTokens = openai.Int(int64(req.MaxOutput))
	}
	if req.Reasoning != "" {
		params.ReasoningEffort = shared.ReasoningEffort(req.Reasoning)
	}
	return params
}

func chatMessages(req llm.Request) []openai.ChatCompletionMessageParamUnion {
	msgs := make([]openai.ChatCompletionMessageParamUnion, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, openai.SystemMessage(req.System))
	}
	for _, m := range req.Messages {
		msgs = append(msgs, chatMessage(m))
	}
	return msgs
}

func chatMessage(m llm.Message) openai.ChatCompletionMessageParamUnion {
	switch m.Role {
	case llm.RoleAssistant:
		return assistantMessage(m)
	case llm.RoleTool:
		return openai.ToolMessage(m.Content, m.ToolCallID)
	default:
		return openai.UserMessage(m.Content)
	}
}

// assistantMessage rebuilds the assistant turn with its tool calls. Its
// reasoning is not sent back.
func assistantMessage(m llm.Message) openai.ChatCompletionMessageParamUnion {
	msg := openai.ChatCompletionAssistantMessageParam{}
	if m.Content != "" {
		msg.Content.OfString = openai.String(m.Content)
	}
	for _, tc := range m.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, openai.ChatCompletionMessageToolCallUnionParam{
			OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
				ID:       tc.ID,
				Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{Name: tc.Name, Arguments: tc.Arguments},
			},
		})
	}
	return openai.ChatCompletionMessageParamUnion{OfAssistant: &msg}
}

// chatTools returns nil without tools, so the field is left out.
func chatTools(tools []llm.Tool) []openai.ChatCompletionToolUnionParam {
	var out []openai.ChatCompletionToolUnionParam
	for _, t := range tools {
		out = append(out, openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
			Name:        t.Name,
			Description: openai.String(t.Description),
			Parameters:  t.Parameters,
		}))
	}
	return out
}

// apiKey returns the bearer key. Servers such as Ollama ignore it, so a
// missing token sends a placeholder.
func apiKey(token string) string {
	if token == "" {
		return "ws"
	}
	return token
}

// warnPlainHTTP logs once, at New, when a token would travel over plain http
// to a host other than this machine (ADR 0066).
func warnPlainHTTP(ep llm.Endpoint, log *slog.Logger) {
	u, err := url.Parse(ep.URL)
	if err != nil || u.Scheme != "http" || ep.Token == "" || isLoopback(u.Hostname()) {
		return
	}
	log.Warn("sending a token over plain http", "endpoint", ep.Name, "host", u.Hostname())
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// send makes one request to the endpoint's root, with the bearer token when
// there is one. A transport failure is an llm.UnreachableError.
func (p *Provider) send(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, p.endpoint.URL+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("building %s %s: %w", method, path, err)
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if p.endpoint.Token != "" {
		req.Header.Set("Authorization", "Bearer "+p.endpoint.Token)
	}
	res, err := p.httpClient.Do(req)
	if err != nil {
		return 0, nil, &llm.UnreachableError{URL: p.endpoint.URL, Cause: err}
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return res.StatusCode, nil, fmt.Errorf("reading %s %s: %w", method, path, err)
	}
	return res.StatusCode, data, nil
}

// getJSON decodes a 2xx answer to GET path into out.
func (p *Provider) getJSON(ctx context.Context, path string, out any) error {
	status, data, err := p.send(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if err := checkStatus(path, status, data); err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

// checkStatus is nil for a 2xx status, and an error naming op otherwise.
func checkStatus(op string, status int, data []byte) error {
	if status >= http.StatusOK && status < http.StatusMultipleChoices {
		return nil
	}
	return fmt.Errorf("%s: %w", op, httpFailure{status: status, body: string(data)}.err())
}
