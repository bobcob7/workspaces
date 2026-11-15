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

## Rules and endpoints added in workspaces

These are in the fake from this repo on, beyond the llm-router copy. The
design's configs in `docs/design/ollama-agent/fake-ollama/` use them.

- Rules: `turn` (applies when the request has that many assistant messages),
  `match_tool_result` (a regular expression over the trailing tool results,
  so it applies only to requests that end with them), `raw_arguments` (sent
  verbatim, even if not JSON; exclusive with `arguments`) and `any_tool`
  (calls the tool even when the request doesn't offer it). Rules with `turn`
  or `match_tool_result` are tried before the others.
- Models: `context_length` (in words, the fake's tokens; the conversation is
  truncated to fit, oldest messages first, system messages and the last
  message kept) and `family` (default `fake`, used by `/api/tags` and
  `/api/show`).
- `strict_capabilities`: a chat request that offers tools to a model without
  the `tools` capability gets Ollama's native `400`.
- Endpoints: `GET /api/version`, `GET /api/tags` and `POST /api/show`, with
  Ollama's shapes. They are recorded and faulted like the other endpoints.
- Faults: `retry_after` (seconds) on a `status` fault sends `Retry-After`.
- Recorded requests: `auth` is `bearer:` plus the first 8 hex digits of the
  SHA-256 of the bearer token, and empty without an `Authorization` header.
  The token itself is never recorded.

Copied from llm-router @9789c32; maintained here.
