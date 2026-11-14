# 0069. The runner uses the MCP servers Claude Code is configured with, filtered per role, and matches Claude Code where a small model can afford it

- Status: Proposed
- Date: 2026-11-14

## Context

### Problem

The review asked whether the runner's seven tools are enough to use the
MCP servers the owner already uses with Claude Code, such as `outline` and
`grafana`, and asked for the runner to be as close to Claude Code parity as
possible. How does the runner find, start, and call MCP servers? How does
it keep a small model from drowning in tool definitions? And, feature by
feature, how close to Claude Code can and should it get?

### What a solution needs

1. **Same servers, no second config**: a server that works in Claude Code
   works in the runner from the same definition.
2. **Both transports in use**: stdio (`mcp-grafana`) and HTTP (`outline`).
3. **Secrets stay secret**: tokens in server definitions and environments
   never reach a transcript, a log, `status.json` or the model.
4. **Small models stay usable**: the number of tool definitions a request
   carries stays small, per role.
5. **Honest parity**: every Claude Code feature is in, later, or never,
   with a reason; where parity would hurt small models, say so.

### Relevant facts

- Claude Code reads MCP servers from three scopes: **local** (in
  `~/.claude.json`, for one project path), **project** (`.mcp.json` in the
  project root) and **user** (`~/.claude.json`, all projects)[^cc-scopes].
  Project servers need approval in interactive sessions;
  `enableAllProjectMcpServers` or `enabledMcpjsonServers` committed to a
  project is ignored in an untrusted folder; non-interactive runs load
  them without asking[^cc-approval].
- Definitions may use `${VAR}` and `${VAR:-default}` in `command`, `args`,
  `env`, `url` and `headers`[^cc-expand]. Transports: stdio, HTTP
  (recommended), SSE ("deprecated") and WebSocket[^cc-transports]. A
  plugin's tools are named `mcp__plugin_<plugin>_<server>__<tool>`, any
  character outside `A-Za-z0-9_-` replaced with `_`[^cc-naming]; other
  servers' tools are `mcp__<server>__<tool>`.
- Claude Code defers MCP tool definitions by default ("Only tool names and
  server instructions load at session start") and loads them through a
  search tool; in `auto` mode it loads them up front while they total
  under 10% of the context window[^cc-toolsearch]. It caps a tool result
  (`MAX_MCP_OUTPUT_TOKENS`) and saves text results over 50 000 characters
  to a file[^cc-output].
- This user's config: `mcp-grafana` (stdio, `GRAFANA_URL` in `env`, its
  token in the process environment) with 80 tools, and `outline` (HTTP, an
  `Authorization` header written into `~/.claude.json`) with 20; no
  project-scope servers[^mcp-shape].
- Workspaces are new directories, so a local-scope server, keyed by a
  project path, never matches a workspace's session directory.
- On BFCL v4, multi-turn tool accuracy for 8–32B open models is 30–48%,
  against 68% for a frontier model[^bfcl], and quantization raises
  tool-name hallucination up to 2.5×[^quant]. Each tool definition costs
  prompt tokens on every request.
- The official MCP Go SDK (`github.com/modelcontextprotocol/go-sdk`) is at
  v1 with a compatibility promise, with stdio and streamable HTTP
  transports (SSE client support unverified; the task checks); v1.8.0 is the newest found on 2026-11-14[^gosdk].
- The user's global `~/.claude/CLAUDE.md` tells every agent to read an
  Outline-hosted handbook, through the `outline` MCP server, before every
  task.

## Decision

### MCP client

A new package, `internal/mcp`, on the official Go SDK.

**Where servers come from.** The runner reads the same definitions Claude
Code does, in Claude Code's precedence (a name in a higher row wins):

1. **project**: `.mcp.json` at the root of the workspace's session repo;
2. **user**: `mcpServers` in `~/.claude.json` (`$CLAUDE_CONFIG_DIR` when
   set, as [0024](0024-claude-code-client.md) item 7).

Local scope never applies (see the facts) and isn't read. A project server
is used only when the role's `mcp_servers` names it, since nobody can
approve it interactively and a repo mustn't enable its own servers. Plugin
and claude.ai connector servers aren't read (later). `ws` never writes any
of these files.

**Which servers a run gets.** None, unless the role's config says so
(`[runner.roles.<role>]`, all optional):

| Key | Type | Default | Meaning |
| --- | ---- | ------- | ------- |
| `mcp_servers` | strings | `[]` | server names to start for this role |
| `mcp_tools` | strings | `["*"]` | tool allow-list: full names (`mcp__mcp-grafana__query_loki_logs`) or globs (`mcp__outline__list_*`) |
| `mcp_deny` | strings | `[]` | globs removed after the allow-list |
| `mcp_upfront` | int | 8 | at most this many MCP tool definitions are sent up front; the rest are deferred |

A tool whose annotations don't say `readOnlyHint: true`, or say
`destructiveHint: true`, is offered only when `mcp_tools` names it exactly,
never through a glob ([0066](0066-runner-security-boundaries.md)).

**Starting and calling.**

- Servers start in parallel when the run starts (and on `--resume`), each
  with a 30 s limit to initialize and list tools. One that fails or times
  out is left out: a `mcp_error` event in the transcript, its state in
  `status.json`'s `mcp`, and a line in the preamble ("mcp-grafana is
  unavailable"). The run goes on.
- **stdio**: `command` and `args` after expansion, in its own process
  group, working directory the session directory, environment the runner's
  unscrubbed environment ([0068](0068-ws-daemon.md)) plus the expanded
  `env` block, stderr to `runner.log`. It's killed with the runner.
- **http**: streamable HTTP to the expanded `url` with the expanded
  `headers`. **sse**: the SDK's SSE client, same inputs, for legacy
  servers; if the SDK version in use has no SSE client, `sse` servers are
  skipped with a warning (later). **ws** (WebSocket) and servers that need OAuth (`claude mcp
  login`, whose tokens are in Claude Code's own credential store) are
  skipped with a warning: later.
- **Expansion** is Claude Code's: `${VAR}` and `${VAR:-default}`, from the
  runner's environment; an unset `${VAR}` without a default makes that
  server fail to start, naming the variable, never its value. Every
  expanded value, and every literal `headers` value, is registered with
  the transcript redactor.
- A call has a 120 s limit. Its result's text content is joined; image and
  audio content become `[image omitted: this runner has no vision]`;
  resource links become their URI. The text then gets the same 16 KiB cap
  and `<tool_result>` framing as a built-in tool
  ([0065](0065-runner-agent-loop-tools-and-budgets.md)). An `isError`
  result is shown to the model as `error: …`; a protocol error, or a
  server that died, is the same plus a `mcp_error` event, and the server
  isn't restarted within the run.

**Naming.** A tool is offered as `mcp__<server>__<tool>`, with every
character outside `A-Za-z0-9_-` replaced by `_`, as Claude Code names
them. A name over 64 characters (the OpenAI function-name limit) is cut to
55 and suffixed with `_` and 8 hex digits of its SHA-256; the runner maps
names back. The transcript records the offered name and the server and
tool it maps to.

**Deferral (tool search).** When a role allows more MCP tools than
`mcp_upfront`, the request carries the seven built-in tools, the first
`mcp_upfront` allowed MCP tools in config order, and an eighth built-in:

```
tool_search {query: string}
  → up to 5 allowed tools whose name or description matches the query's
    words, each as "name: first line of description"; they are added to
    every later request of this run
```

The preamble lists every deferred tool's name, grouped by server, one line
per server, cut at 2 KiB. Loaded tools stay loaded; past 24 MCP tools in a
request, the least recently called loaded tool is dropped (it can be found
again). Matching is case-insensitive word overlap with the name, then the
description; there are no embeddings.

**Later**: MCP resources and prompts (as tools `mcp_read_resource` and
role-prompt includes), `list_changed` notifications, sampling and
elicitation (never: the runner has no one to ask), OAuth, WebSocket,
plugin and connector servers, restarting a dead server.

### Parity with Claude Code

The table is also in the design's README. "v1" means this design's tasks.

| Claude Code feature | Runner | Why |
| ------------------- | ------ | --- |
| File read, write, edit; glob and grep; Bash | v1 | The seven built-in tools ([0065](0065-runner-agent-loop-tools-and-budgets.md)); fewer, flatter tools than Claude Code's |
| MCP tools, stdio and HTTP (SSE where the SDK has a client) | v1 | This ADR; same definitions, filtered per role |
| MCP tool search / deferred tools | v1 | `tool_search`, simpler than Claude Code's |
| MCP resources, prompts, OAuth, WebSocket, plugin servers | later | Not needed by the servers in use; OAuth needs Claude Code's credential store |
| `CLAUDE.md` project instructions | v1 | `CLAUDE.md` and `AGENTS.md` at the repo root and, if present, the workspace root, in that order, after the preamble; 8 KiB in total, cut with a marker. `@path` imports aren't followed (later) |
| User `~/.claude/CLAUDE.md` | off by default, `runner.user_instructions = true` | It's written for Claude: this user's tells every agent to read a handbook through `outline` first, which would spend a small model's context and steps before the task. Opt in per machine |
| Skills | later | They load instructions and scripts on demand; a `skill` tool like `tool_search` could do it, after the core works |
| Subagents (the Task tool) | later, as `ws machine spawn` | A small model can't manage children well; when it comes, it's a `ws` child, not an in-process agent |
| Hooks (`PreToolUse`, `PostToolUse`, `Stop`, …) | later | The user's hooks could run with Claude Code's JSON contract around the runner's tools; needs its own design for which events map |
| Permission rules (`permissions.allow` / `deny`) | later | `deny` rules could extend the command guard ([0066](0066-runner-security-boundaries.md)) |
| Permission prompts, auto mode | never | The runner is always unattended, equivalent to auto mode with guard rails |
| Plan mode | never | No person approves a plan; roles that plan stay on Claude |
| WebFetch | later | A capped, text-only fetch tool is easy; until then, through an MCP fetch server |
| WebSearch | never built in | Needs a search API; use an MCP server |
| NotebookEdit | never | `edit_file` on notebook JSON is enough for the roles that run here |
| Image input (screenshots, PDFs) | later, for models whose probe says `vision` | Needs a provider field for images; the screenshot role stays on Claude |
| Slash commands and custom commands | never | Interactive; role prompts do their job |
| Memory files, auto-memory | never | Roles and loop memory ([0056](0056-loop-history-and-memory.md)) cover it |
| Background shells, `TodoWrite` | later | Useful for long tasks; not for the first roles |
| Resume after a crash | v1 | `--resume`, by the daemon ([0068](0068-ws-daemon.md)) |
| Attach, send a message, continue | never in v1 | Unattended children; reconsider with the desktop |
| Status line, output styles, themes | never | Interactive UI |

**Where the review's ask conflicts with small models.** Full parity means
Claude Code's system prompt and built-in tools, every configured MCP tool,
hooks and skills on every request. That's the configuration the
tool-calling evidence says small models handle worst: each tool adds
tokens to every request and another name to confuse. So parity here is
parity of **reach**: the same MCP servers, the same project instructions,
the same roles, from the same files, with what a role actually sees kept
small by default and widened in config. A role given all 80 grafana tools
up front will work worse, not better; `tool_search` is the way to reach
them. We don't claim the runner can do every job a Claude child can.

### Why this option

1. **Same servers**: read from Claude Code's own files with its precedence
   and expansion rules; nothing to configure twice.
2. **Both transports** through the official SDK.
3. **Secrets**: expanded values and literal headers are redacted, server
   definitions never logged, environments never shown to the model.
4. **Small models**: no MCP by default, per-role allow-lists, a cap on
   what's sent up front, and search for the rest.
5. **Honest**: a row per feature, and the conflict stated.

Where it's weak: allow-lists are one more thing to configure per role.
Word-overlap search is crude, and a small model may not think to search.
Annotations are the server's claim. Reading `~/.claude.json` ties the
runner to a Claude Code file format that isn't documented as stable, as
`ws` already is for transcripts.

## Alternatives

### A separate `[mcp.servers]` in `config.toml`

**Pros**

- `ws` owns the format.

**Cons**

- Every server configured twice, and tokens in a second place; fails
  criterion 1. Can be added later as a third, lower-precedence source.

### Offer every allowed tool up front, no search

**Pros**

- Simplest; no extra round trip.

**Cons**

- 80 grafana tools on every request of a 32k-context model; fails
  criterion 4.

### Run MCP calls through a `run` command (`npx` clients)

**Pros**

- No SDK in `ws`.

**Cons**

- Secrets on command lines and in the model's view; no schemas for
  validation.

### Ask Claude Code to proxy MCP calls

**Pros**

- OAuth and plugins for free.

**Cons**

- Needs a Claude session per runner, which defeats the purpose
  ([0063](0063-runner-children-are-supervised-sessions.md)).

## Consequences

- New: `internal/mcp` (config reading, expansion, client, naming, filter,
  search), `tool_search` in `internal/agent`, role keys in
  `internal/config`, and project-instruction loading in `internal/agent`.
- `go.mod` gains `github.com/modelcontextprotocol/go-sdk` (the newest v1
  when the task runs; v1.8.0 as of 2026-11-14).
- `status.json` gains `mcp`; the transcript gains `mcp_start`, `mcp_error`
  and `tool_search` events; `ws list` and `wait` show MCP state under
  `ws_runner`.
- How we'll know: the `mcp-*` e2e scenarios in
  [0067](0067-runner-tests-use-vendored-fakes.md), and the opt-in smoke
  against the user's real `outline` and `mcp-grafana`.
- Unverified: how well any small model uses `tool_search`; that's a
  measurement for the first real runs.

## References

[^cc-scopes]: Anthropic, "Connect Claude Code to tools via MCP." <https://code.claude.com/docs/en/mcp> (accessed 2026-11-14). Scope table: Local "`~/.claude.json`", Project "`.mcp.json` in project root", User "`~/.claude.json`".
[^cc-approval]: Same page. "Claude Code prompts for approval in interactive sessions before using project-scoped servers from `.mcp.json` files"; "`enableAllProjectMcpServers` or `enabledMcpjsonServers` committed to the project's `.claude/settings.json` is ignored in an untrusted folder"; in `claude -p` runs "Claude Code loads project-scoped servers without asking".
[^cc-expand]: Same page. "You can reference an environment variable in a server's `command`, `args`, `env`, `url`, or `headers` value … `${VAR}` … `${VAR:-default}`".
[^cc-transports]: Same page. "HTTP servers are the recommended option"; "The SSE (Server-Sent Events) transport is deprecated"; stdio; WebSocket.
[^cc-naming]: Same page. "The full form is `mcp__plugin_<plugin-name>_<server-name>__<tool-name>`, where any character outside `A-Z`, `a-z`, `0-9`, `_`, and `-` is replaced with `_`"; a hook example matches `mcp__database-tools__.*`.
[^cc-toolsearch]: Same page, "Scale with MCP tool search." "Only tool names and server instructions load at session start"; `auto`: "Tools that would otherwise be deferred load upfront while their definitions total under 10% of the context window."
[^cc-output]: Same page. "`MAX_MCP_OUTPUT_TOKENS`"; "Claude Code saves a successful result with no image content to a file once it's longer than 50,000 characters".
[^mcp-shape]: See [0067](0067-runner-tests-use-vendored-fakes.md) (key names only, read 2026-11-14; no values copied), and the tool counts there.
[^bfcl]: Berkeley Function Calling Leaderboard v4, updated 2026-04-12, <https://gorilla.cs.berkeley.edu/leaderboard.html> (accessed 2026-11-13); figures in [0065](0065-runner-agent-loop-tools-and-budgets.md).
[^quant]: "Flat Score, Amplified Failures," arXiv 2607.27275, <https://arxiv.org/abs/2607.27275> (accessed 2026-11-13).
[^gosdk]: modelcontextprotocol/go-sdk releases, <https://github.com/modelcontextprotocol/go-sdk/releases>, and <https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk@v1.7.0> (search results, 2026-11-14; release pages not opened). v1.0.0 "formalizes a compatibility guarantee"; v1.7.0 added protocol revision 2026-07-28; v1.8.0 the newest listed. The task re-checks the version and client API.
