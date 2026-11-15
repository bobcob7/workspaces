package fakeollama

import (
	"strings"
	"time"

	"github.com/bobcob7/workspaces/internal/fakeollama/internal/openaiwire"
)

// systemFingerprint is the fingerprint Ollama's OpenAI compatibility layer
// reports (github.com/ollama/ollama, openai/openai.go).
const systemFingerprint = "fp_ollama"

// ownedBy returns the namespace of a model name, as Ollama reports it.
func ownedBy(name string) string {
	parts := strings.Split(name, "/")
	if len(parts) < 2 {
		return "library"
	}
	return parts[len(parts)-2]
}

func (r reply) finishReason() *string {
	if len(r.toolCalls) > 0 {
		return new(openaiwire.FinishReasonToolCalls)
	}
	return new(openaiwire.FinishReasonStop)
}

// usageFor counts words as tokens.
func usageFor(req openaiwire.ChatRequest, rep reply) openaiwire.Usage {
	var u openaiwire.Usage
	for _, m := range req.Messages {
		u.PromptTokens += len(strings.Fields(m.Text()))
	}
	u.CompletionTokens = len(strings.Fields(rep.reasoning)) + len(strings.Fields(rep.content))
	for _, tc := range rep.toolCalls {
		u.CompletionTokens += 1 + len(strings.Fields(tc.Function.Arguments))
	}
	u.TotalTokens = u.PromptTokens + u.CompletionTokens
	return u
}

func completionFor(req openaiwire.ChatRequest, rep reply) openaiwire.ChatCompletion {
	return openaiwire.ChatCompletion{
		ID:                newCompletionID(),
		Object:            openaiwire.ObjectChatCompletion,
		Created:           time.Now().Unix(),
		Model:             req.Model,
		SystemFingerprint: systemFingerprint,
		Choices: []openaiwire.Choice{{
			Message:      openaiwire.ResponseMessage{Role: openaiwire.RoleAssistant, Content: rep.content, Reasoning: rep.reasoning, ToolCalls: rep.toolCalls},
			FinishReason: rep.finishReason(),
		}},
		Usage: usageFor(req, rep),
	}
}

// pieces splits text into chunks of n words, keeping the spaces.
func pieces(text string, n int) []string {
	words := strings.SplitAfter(text, " ")
	var out []string
	for len(words) > 0 && text != "" {
		k := min(n, len(words))
		out = append(out, strings.Join(words[:k], ""))
		words = words[k:]
	}
	return out
}

// deltas splits the reply into stream deltas as Ollama does: reasoning,
// then content, then all tool calls in one delta. The first carries the role.
func deltas(rep reply, wordsPerChunk int) []openaiwire.Delta {
	var out []openaiwire.Delta
	for _, p := range pieces(rep.reasoning, wordsPerChunk) {
		out = append(out, openaiwire.Delta{Content: new(""), Reasoning: p})
	}
	for _, p := range pieces(rep.content, wordsPerChunk) {
		out = append(out, openaiwire.Delta{Content: new(p)})
	}
	if len(rep.toolCalls) > 0 {
		out = append(out, openaiwire.Delta{Content: new(""), ToolCalls: rep.toolCalls})
	}
	if len(out) == 0 {
		out = append(out, openaiwire.Delta{Content: new("")})
	}
	out[0].Role = openaiwire.RoleAssistant
	return out
}

// streamChunks returns every chunk of a streamed reply: the deltas, a finish
// chunk with an empty delta, and a usage chunk if requested.
func streamChunks(req openaiwire.ChatRequest, rep reply, wordsPerChunk int) []openaiwire.Chunk {
	base := openaiwire.Chunk{ID: newCompletionID(), Object: openaiwire.ObjectChatCompletionChunk, Created: time.Now().Unix(), Model: req.Model, SystemFingerprint: systemFingerprint}
	ds := deltas(rep, wordsPerChunk)
	chunks := make([]openaiwire.Chunk, 0, len(ds)+2)
	for _, d := range ds {
		c := base
		c.Choices = []openaiwire.ChunkChoice{{Delta: d}}
		chunks = append(chunks, c)
	}
	finish := base
	finish.Choices = []openaiwire.ChunkChoice{{FinishReason: rep.finishReason()}}
	chunks = append(chunks, finish)
	if req.IncludeUsage() {
		u := usageFor(req, rep)
		final := base
		final.Choices = []openaiwire.ChunkChoice{}
		final.Usage = &u
		chunks = append(chunks, final)
	}
	return chunks
}
