# 0071. Claude Code stays the default and the supported path; `ws`'s runner is an opt-in peer; the long-term goal is that `ws` depends on no particular model, provider or agent harness

- Status: Proposed
- Date: 2026-11-14

## Context

### Problem

ADRs [0062](0062-agent-runner-built-into-ws.md)–[0070](0070-runner-agents-for-people-and-loops.md)
add a second way to run agents beside Claude Code. The owner wants
"anything can use a runner" eventually, but "Claude is the most powerful
I have right now and works effectively; I don't want to disrupt that
functionality"[^owner-fallback], and asked for a decision that "keeps
alignment" towards "complete independence of any LLM model and
provider"[^owner-adr]. How do the two runners relate now, and which rules
keep each later change pointed the same way?

### What a solution needs

1. **Nothing that works today breaks or slows down.** The Claude path
   gains no new dependency.
2. **Each step is reversible and measured**, not taken on faith.
3. **The direction is checkable in review**, not only stated.

### Relevant facts

- Today every agent `ws` starts is a Claude Code background session
  (`claude --bg`); `internal/claude` is the only place `claude` runs, and
  session listing, `ws restart`, `ws continue`, attach and transcripts are
  built on it ([0024](0024-claude-code-client.md),
  [0018](0018-restart-claude-daemon.md),
  [0047](0047-ws-continue-after-usage-limit.md),
  [0030](0030-desktop-conversation-from-transcript.md)).
- The runner already has one seam to models, `internal/llm.Provider`, and
  reserves `anthropic` and `openai-responses` beside `openai`
  ([0062](0062-agent-runner-built-into-ws.md)).
- Nothing about the runner on `qwen/qwen3.5-9b` has been measured yet
  ([design README](../design/ollama-agent/README.md#which-roles-suit-a-9b-model)).

## Decision

1. **Claude Code is the default** runner for every role, loop and
   command, and the supported path for everything that works today. A
   start with the default runner behaves exactly as before the runner
   existed: no daemon, no preflight, no reading of `[runner]`,
   `[endpoints]` or `[models]`, no dependency on an endpoint.
2. **The runner is a peer that anything may opt into**: any role, loop or
   command can choose `--runner ws` (or config routing) per use. The
   direction is "anything can use a runner"; Claude Code is one runner
   among several in the design, not a special case of the data model.
3. **No automatic fallback between runners, ever.** A failed start fails
   with its code; a failed run records its exit reason. A person or
   orchestrator chooses what to do next.
4. **Disturbing the Claude path needs evidence and the owner.** Any change
   that alters how a Claude agent starts, runs or is listed, or moves a
   default from Claude to the runner, needs measured runs (success rate
   against Claude on the same tasks) and an explicit owner decision,
   recorded in an ADR.
5. **Long-term goal**: `ws` depends on no particular model, provider or
   agent harness, Claude Code and Anthropic included. Claude Code becomes
   optional, not removed. The phases and their exit criteria are in the
   [roadmap](../roadmap.md).

**Design rules that keep alignment**

- The `internal/llm.Provider` seam is the only place a wire protocol is
  known; no package above it names a provider.
- No new feature is Claude-only without a runner story: the parity matrix
  ([0069](0069-runner-mcp-tools-and-claude-code-parity.md),
  [design README](../design/ollama-agent/README.md#parity-with-claude-code))
  gains a row for it, at least `later` or `never` with a reason.
- Config, records and JSON stay harness-neutral: `runner` and `model`
  fields, never new Claude-specific names for things every agent has.
- `ws`'s own session model doesn't newly deepen its dependence on
  `claude --bg`: a new session feature goes through the runner/daemon
  seam, or says in its ADR why it can't yet.

### Why this option

1. **Nothing breaks**: rule 1 and its regression check
   ([0067](0067-runner-tests-use-vendored-fakes.md),
   `claude-default-untouched`) keep the Claude path byte-for-byte as it is.
2. **Measured**: rule 4 and the roadmap's exit criteria gate every widening.
3. **Checkable**: the four design rules are review checklist items (below).

Where it's weak: two runners cost more to maintain than one, and parity
rows marked `later` can pile up; the roadmap's phase 2 revisits them.

## Alternatives

### Replace Claude Code with the runner now

**Pros**

- One runner to maintain; independence sooner.

**Cons**

- Disrupts what works, on a 9B model with no measured runs. The owner
  ruled it out.

### Claude-only forever (drop the runner)

**Pros**

- No second path.

**Cons**

- `ws` stays tied to one harness and one provider's usage limits; the
  owner asked for the opposite direction.

### Automatic fallback between runners

**Pros**

- An orchestration never stalls on a down endpoint.

**Cons**

- Spends usage nobody chose, hides which runner did the work, and couples
  the Claude path to the runner's preflight. Removed by the owner
  ([0064](0064-choosing-the-runner-model-and-endpoint.md)).

## Consequences

- Easier: moving one role at a time, with a measured record, and backing
  it out by config.
- Harder: every new feature needs a runner row in the parity matrix.
- How we'll know, as review checklist items for any change to `ws`:
  - Does a default (Claude) start now touch the daemon, the runner's
    tables or an endpoint? It mustn't; `claude-default-untouched` passes.
  - Does any code outside `internal/llm/<provider>` know a wire protocol?
  - Does a new feature lack a parity row?
  - Does a new JSON field, config key or record name say `claude` for
    something every agent has?
  - Does a new session feature call `internal/claude` where the runner or
    daemon seam would do?
  - Does anything start one runner because another failed?
- Unverified: how far the runner on small models gets; the roadmap's
  phases are gated on it.

## References

[^owner-fallback]: Owner's review of PR #71 (bobcob7/workspaces), fourth round, 2026-11-14: "Remove the fallback behavior. Ideally anything can use a runner, but Claude is the most powerful I have right now and works effectively; I don't want to disrupt that functionality."
[^owner-adr]: Same review: "Add a short ADR that keeps alignment. Maybe a roadmap document with the long-term goal of complete independence of any LLM model and provider."
