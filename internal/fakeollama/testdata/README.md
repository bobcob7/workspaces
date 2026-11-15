# Golden Ollama responses

`TestServer_handleChat` and `TestServer_handleModels` send each `*.request.json`
to the fake and compare the response with the matching `*.response.json` (JSON)
or `*.response.txt` (SSE stream).

The comparison is structural, so fixtures captured from a real model still apply
to the fake's different wording: every JSON value is reduced to a skeleton in
which strings, numbers, and booleans become their type, except the values of
`object`, `role`, `type`, `finish_reason`, `system_fingerprint`, and `owned_by`,
which must match exactly. Consecutive identical skeletons in an array or a
stream collapse into one, so the number of models or content chunks doesn't
matter but the order of chunk kinds (role, reasoning, content, tool calls,
finish, usage) does. Streams must end with `data: [DONE]`.

## Provenance

Ollama wasn't available when these were created, so they were written by hand
from Ollama's OpenAI compatibility layer (`openai/openai.go` and
`middleware/openai.go` on `main` in github.com/ollama/ollama, October 2026):
the role is only in the first delta, reasoning deltas carry `"content":""`,
tool calls arrive whole in one delta, `finish_reason` comes in its own chunk
with an empty delta, and usage in a final chunk with empty `choices`.

Ollama also sends a `timings` object alongside streamed usage. The fake omits
it; the router doesn't read it.

## Recapturing from a real Ollama

Use a model without reasoning for the plain fixtures (e.g. `llama3.2:3b`) and a
reasoning model for the `*-reasoning` ones (e.g. `qwen3:8b`); both need tool
support. Then, from this directory:

```sh
OLLAMA=http://localhost:11434
curl -s "$OLLAMA/v1/models" > models.response.json
for f in chat chat-tools stream stream-no-usage stream-tools; do
  jq '.model = "llama3.2:3b"' "$f.request.json" |
    curl -s "$OLLAMA/v1/chat/completions" -H 'Content-Type: application/json' -d @- > "$f.response.tmp"
done
for f in chat-reasoning stream-reasoning; do
  jq '.model = "qwen3:8b"' "$f.request.json" |
    curl -s "$OLLAMA/v1/chat/completions" -H 'Content-Type: application/json' -d @- > "$f.response.tmp"
done
curl -s "$OLLAMA/v1/chat/completions" -d @model-not-found.request.json > model-not-found.response.json
for f in *.response.tmp; do
  case "$f" in
    stream*) mv "$f" "${f%.tmp}.txt" ;;
    *) mv "$f" "${f%.tmp}.json" ;;
  esac
done
```

Check that the tool fixtures contain a tool call (a model may answer in text
instead) and then run `go test ./internal/fakeollama`. A failure means Ollama's
shapes have changed and the fake needs updating.
Copied from llm-router @9789c32; maintained here.
