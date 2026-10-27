# 0038. A review stays one agent per PR head; `ws` chooses its profiles and defines one Claude Code subagent per profile, with that profile's model

- Status: Proposed
- Date: 2026-10-26

## Context

### Problem

Reviewers are focused by **profiles** (`architecture`, `claims`,
`compatibility`, `efficiency`, `security`, `tests`, `go`, `typescript`),
now `ws`'s own ([0037](0037-review-engine-forked-from-loam-refinery.md)),
and a report combines one review per profile. A review today is one `ws
claude` agent at the PR head ([0032](0032-review-agent-per-pr-head.md)).
How do several profiles review one commit, which profiles run, and how
does each get the model chosen for it?

### What a solution needs

1. **Keeps 0032's model**: the app finds a review and its status from
   `ws list`, and starting it twice is harmless.
2. **Each profile reviews independently**, so one reviewer's framing
   doesn't flatten the others.
3. **Profiles and models are chosen by the person**, per profile, and
   applied as chosen, not left to the review agent's judgment.
4. **Complete or visibly incomplete**.
5. **Cost** in clones, sessions, and the auto-review cap stays bounded.

### Relevant facts

- Claude Code subagents: "Each subagent runs in its own context window
  with a custom system prompt, specific tool access, and independent
  permissions"; "Background subagents run concurrently"; "A subagent
  starts in the main conversation's current working directory"; more than
  20 at once fails[^cc-subagents].
- Subagents can be defined for a session with `claude --agents <json>`,
  "a JSON object defining custom agents", whose entries take `description`,
  `prompt`, `tools`, and `model`[^cc-help][^cc-subagents]. `model` is
  "`sonnet`, `opus`, `haiku`, `fable`, a full model ID such as
  `claude-opus-5-5`, or `inherit`"; a per-invocation `model` parameter
  takes precedence over it[^cc-subagents].
- `ws claude` passes everything after `--` to `claude --bg`, except
  `--settings` ([`ws claude`](../tools/ws-claude.md)).
- In a `ws claude` session started with `--agents` defining two
  subagents (one `haiku`, one `sonnet`) and a `sonnet` main model, the
  agent started both in the background with no `model` parameter; their
  transcripts show `claude-haiku-5-5` and `claude-sonnet-5-5`, and their
  runs overlapped (one 25 s command each, started 3 s apart)[^bench-agents].
- After `claude stop` and `ws restore`, the session's job state
  (`~/.claude/jobs/<id>/state.json`, undocumented) listed `respawnFlags`
  including `--agents` and its full JSON[^bench-agents].
- loam-refinery's profile files are Markdown with a `description` in
  front matter[^lr-profile].
- Each workspace is a hard-linked clone plus a Claude Code session
  ([0017](0017-clones-not-worktrees.md)).

## Decision

We will keep one review agent per PR head, and have `ws review start`
choose the profiles and define a subagent for each.

**Profiles.** A profile file's front matter gains two keys:

```markdown
---
description: Go: concurrency, error wrapping, context handling, hot-path cost, and tests
when: auto          # always | auto | off
match: ["*.go", "go.mod"]
---
```

Built in: `architecture`, `claims`, `security`, `tests` are `always`;
`go` (`*.go`, `go.mod`) and `typescript` (`*.ts`, `*.tsx`) are `auto`;
`compatibility` and `efficiency` are `off`. `config.toml` can override
`when` and set a `model` per profile under `[review.profiles.<name>]`.

**Choosing.** `ws review start` takes `--profile NAME[:MODEL]` and `--auto
NAME[:MODEL]`, both repeatable; given either, they replace the
configured set. `ws` lists the files the review covers (`git diff
--name-only <base>...<head>` in the cache with `--pr`, else the commit
against its first parent) and adds each `auto` profile with a matching
file. The record ([0039](0039-ws-review-command.md)) holds the final
list, each with its model (`""` for the session's).

**Defining.** The session is started with `--agents`, one entry per
profile:

```json
{"review-security": {
  "description": "Reviews this commit as the security profile and submits with ws review submit",
  "prompt": "<ws review prime --profile=security>\n\n<the task: diff, commit, PR, no writes>",
  "tools": ["Read", "Grep", "Glob", "Bash"],
  "model": "opus"}}
```

`model` is omitted when the profile has none.

**The review agent's prompt** (embedded in `ws`) tells it to start every
`review-*` subagent in the background without a `model` parameter, so the
definition's model applies; to wait for them; to run `ws review report`
and start each profile with no current submission once more; and to end
with one line per profile (verdict, priority 7+ count) and any profile
that failed. Documents go on stdin; nothing in the repo is written,
committed, pushed, or posted.

### Why this option

1. Workspace, name, and status are exactly 0032's.
2. Subagents have their own context windows; they share the checkout,
   which is safe because none of them writes to it.
3. Profiles are chosen by `ws` from settings and the diff, and each
   model is pinned in the subagent's definition, so neither depends on the
   review agent following instructions.
4. Completeness is checked by the agent, which can retry, and by `ws
   review report`, which compares the record with the store.
5. One clone and one session per review; subagents cost tokens, not disk.

It's weak in two ways. Whether the agent started every subagent isn't
observable, only the submissions are. And per-profile progress is only
visible as submissions arrive, not while a subagent works.

## Alternatives

### One workspace and agent per profile

**Pros:** independence enforced by process boundaries; per-profile status
in `ws list`; one profile can be retried alone.
**Cons:** a clone and session per profile; 0032's names and matching
change; the auto-review cap counts profiles rather than PRs; and
something still has to notice when all are done. Models per profile no
longer need it, since subagent definitions carry them.

### Tell the review agent which models to use

List profiles and models in the prompt and let the agent pass `model` per
invocation.

**Pros:** no `--agents`; the prompt alone carries everything.
**Cons:** the model is applied only if the agent remembers to; the
definitions make it the default instead of a request.

### The review agent picks language profiles itself

**Pros:** it can judge relevance from the diff's content, not file names.
**Cons:** the record no longer says in advance which profiles to expect,
so a missing one can't be told from one never started.

## Consequences

- The review task is `ws`'s prompt; the app's "Review task" setting is
  removed ([0039](0039-ws-review-command.md)).
- A review costs about one subagent per profile in tokens: four by
  default, plus the language profiles that match.
- Conformance: unit tests for profile front matter and overrides, the
  `--profile`/`--auto` rules, matching against a file list, the
  `--agents` JSON (one entry per profile, model present only when set),
  and the prompt naming every subagent.
- `--agents`, pinned models, and concurrent background subagents in a
  `claude --bg` session were checked[^bench-agents].
- **Partly unverified:** that a respawned session *uses* the definitions.
  Its recorded `respawnFlags` include them, but a subagent wasn't started
  after the restore (typing into `claude attach` from `expect` didn't
  submit). The implementing PR checks it from the desktop app's reply box:
  restore a review mid-run and confirm the next subagent's model in its
  transcript.

## References

[^cc-subagents]: Claude Code docs, "Create custom subagents." <https://code.claude.com/docs/en/sub-agents> (accessed 2026-10-27). Quoted as above; on precedence: "When Claude invokes a subagent, it can also pass a `model` parameter for that specific invocation. Claude Code resolves the subagent's model in this order: 1. The per-invocation `model` parameter 2. The subagent definition's `model` frontmatter …"; on `--agents`: "CLI-defined subagents are passed as JSON when launching Claude Code. They exist only for that session and aren't saved to disk."
[^cc-help]: `claude --help`, Claude Code 2.1.311, run 2026-10-27. "--agents <json-or-file>  JSON object defining custom agents, or with --print the path to a file that holds one".
[^bench-agents]: Measured 2026-10-27 on an Apple M4, macOS 26.6.2, Claude Code 2.1.311, `ws claude --name probe-agents -- --agents '<json>' --allowedTools Bash --model sonnet '<prompt>'` in an empty workspace. Subagents `probe-a` (`model: haiku`) and `probe-b` (`model: sonnet`), each told to run `date +%s > <dir>/<name>.start; sleep 25; date +%s > <dir>/<name>.end`. The main agent's two `Agent` calls had `subagent_type` and `run_in_background` and no `model`. Timestamps: probe-b 1793110377–1793110402, probe-a 1793110380–1793110405. Models read from `message.model` in each `subagents/agent-*.jsonl`. Then `claude stop 7e1f7c9c`, `ws restore --json probe-agents` (`restored: true`), and `jq` over `~/.claude/jobs/7e1f7c9c/state.json`: `respawnFlags` = `-n probe-agents --settings … --agents {…} --allowedTools Bash --model sonnet`. One run.
[^lr-profile]: loam-refinery `profiles/go.md`, at f7b52c0: front matter `description: Go: concurrency, error wrapping, context handling, hot-path cost, and tests`.
