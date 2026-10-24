# 0030. The desktop app shows an agent's conversation by following its transcript file

- Status: Proposed
- Date: 2026-10-23

## Context

### Problem

The desktop app ([0029](0029-desktop-app.md)) shows an agent as `claude
attach` in xterm.js. That terminal is a full-screen UI: it scrolls in
the terminal's own way, not like a page, text selection follows the grid,
and long replies are hard to read back through. The maintainer wants the
conversation shown as a GUI that scrolls natively, and suggested Claude
Code's OpenTelemetry (OTEL) logs as a source.

Where should the app get an agent's conversation, and what happens to the
terminal?

### What a solution needs

1. **The whole conversation**: the user's messages, the agent's replies, and
   its tool calls with their results, including what happened before the
   app was opened.
2. **Live**: new messages show within a couple of seconds while the agent
   works.
3. **No setup per session**, and no new long-running process.
4. **Nothing lost**: permission prompts and slash commands, which the
   conversation can't answer, still work.

### Relevant facts

- Claude Code appends each background session's conversation to
  `<config>/projects/<dir>/<session uuid>.jsonl` as it works, one JSON
  object per line. `user` and `assistant` lines carry the Messages API
  `content` blocks (`text`, `thinking`, `tool_use`, `tool_result`); other
  lines are bookkeeping (`file-history-snapshot`, `system` with
  `turn_duration`, and so on). The format is undocumented[^survey].
- A job's `state.json` records the transcript's path as `linkScanPath`,
  with `sessionId` and `cwd`[^survey].
- OTEL log events redact prompt and reply text unless
  `OTEL_LOG_USER_PROMPTS` or `OTEL_LOG_ASSISTANT_RESPONSES` is set, and
  carry tool input only with `OTEL_LOG_TOOL_DETAILS`, truncated to about
  4 K characters. No log event carries tool output; that needs tracing and
  `OTEL_LOG_TOOL_CONTENT`. Events are exported as they happen, to an OTLP
  endpoint the session was started with[^otel].
- Reading a 2.4 MB transcript from the start gave 234 items in 0.08 s, with
  debug build and test harness included[^bench].

## Decision

We will read each agent's conversation from its transcript, in the Rust
backend, and show it as a page; the terminal stays one click away.

- **Finding it.** `read_transcript(session, offset)` reads the job's
  `state.json`, and uses `linkScanPath` if it's a file under
  `<config>/projects`; otherwise `projects/<cwd with every character
  but [A-Za-z0-9] as "-">/<sessionId>.jsonl`.
- **Following it.** The command returns the items in the whole lines
  written since `offset` and the offset to ask from next. While the
  conversation is shown, the frontend asks every second. A file shorter
  than the offset is read from the start and replaces what was shown.
- **What shows.** User text (without `<system-reminder>` blocks), the
  agent's text as Markdown, non-empty thinking (collapsed), and each tool
  call as a collapsed card with its result, matched by `tool_use_id`. Slash
  commands, their output, finished background tasks, compaction, and away
  summaries show as notices. Subagent (`isSidechain`) and `isMeta` lines,
  and all other line types, are skipped. A tool input or result over 4000
  characters is cut; at most 2000 items are kept.
- **The terminal.** An agent's pane has a Conversation / Terminal toggle,
  Conversation by default, remembered across agents. `claude attach` still
  runs behind the conversation, so the message box types into it as before.
  An agent that needs input shows a banner pointing to the terminal,
  since a permission prompt isn't in the transcript until it's answered.
  A stopped agent's conversation can be read without resuming it.

### Why this option

1. The transcript has everything, from the session's start, without
   truncation beyond what the app chooses.
2. A one-second read of the lines since the last offset keeps up while the
   agent works; reading only whole lines means a half-written one waits for
   the next read.
3. It needs no settings and no receiver: every session already writes one.
4. The terminal is unchanged and still attached, so nothing it does is lost.

Weak spot: the format is undocumented. Unknown lines and fields are
skipped, so a change shows less rather than failing, and the terminal still
shows everything.

## Alternatives

### OTEL log events

The app runs an OTLP receiver, and sessions export to it.

**Pros**

- A documented, versioned format.

**Cons**

- Every session must be started with the endpoint and the content flags,
  which means changing how `ws claude` and Claude Code's background service
  start them; sessions started before that show nothing.
- Only what happens while the app is listening: no history.
- Tool output isn't in any log event, and tool input is truncated to
  about 4 K characters.
- The app becomes a server, and the content flags send prompts and replies
  to whatever the endpoint is.

### Render the terminal's screen as HTML

Parse the `claude attach` output into a page.

**Pros**

- No second source.

**Cons**

- The output is screen updates for a full-screen UI, not a conversation;
  turning it back into messages means parsing Claude Code's layout, which
  changes more often than its transcript.

### Watch the file instead of polling

**Pros**

- New lines show at once.

**Cons**

- A file watcher dependency and a channel per conversation, for at most
  a second saved. Polling reads one `stat` and nothing else when the
  file hasn't grown.

## Consequences

- The desktop app depends on a second undocumented Claude Code file, beside
  the job state ([0029](0029-desktop-app.md)). Tests in `transcript.rs` pin
  the lines it reads; `cargo test conversation_live -- --ignored` prints a
  real session's conversation.
- A permission prompt or question asked in the terminal shows in the
  conversation only once answered; the needs-input banner points there.
- Subagents' own messages aren't shown; their calls and results are.
- `react-markdown` and `remark-gfm` join the frontend's dependencies.

## References

[^survey]: Surveyed 2026-10-23 on Claude Code 2.1.310: line `type`s and content block types across the 25 largest transcripts under `~/.claude/projects` changed in the last 10 days, with `jq`; and `~/.claude/jobs/*/state.json` for 3 background sessions, whose `linkScanPath` each named `<sessionId>.jsonl`.
[^otel]: Claude Code docs, "Monitoring," <https://code.claude.com/docs/en/monitoring-usage> (accessed 2026-10-23). `assistant_response`: "Redacted to `<REDACTED>` by default. Set `OTEL_LOG_ASSISTANT_RESPONSES=1` to include it." `tool_input`: "Individual values over 512 characters are truncated, and the full payload is bounded to ~4 K characters." `tool_result` records input and result sizes; tool content appears only in the `tool.output` span event, which "Requires tracing."
[^bench]: Measured 2026-10-23 on an Apple M4: `WS_DESKTOP_ATTACH=3dd81c7a cargo test conversation_live -- --ignored`, debug build; the test harness reported 0.08 s. One run.
