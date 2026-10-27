# 0038. A review stays one agent per PR head, which runs one Claude Code subagent per loam-refinery profile and checks that each one submitted

- Status: Proposed
- Date: 2026-10-26

## Context

### Problem

loam-refinery reviewers are focused by **profiles** (`architecture`,
`claims`, `compatibility`, `efficiency`, `security`, `tests`, `go`,
`typescript`), and `collect-reviews` combines one review per profile into
one report ([0037](0037-loam-refinery-as-external-binary.md)). A review
today is one `ws claude` agent at the PR head
([0032](0032-review-agent-per-pr-head.md)). How do several profiles review
one commit: several agents, or one?

### What a solution needs

1. **Keeps 0032's model**: the app finds a review and its status from
   `ws list`, and starting it twice is harmless.
2. **Each profile reviews independently**, so one reviewer's framing
   doesn't flatten the others.
3. **Complete or visibly incomplete**: a report missing a requested
   profile must say so.
4. **Cost** in clones, sessions, and the auto-review cap stays bounded.

### Relevant facts

- Claude Code subagents: "Each subagent runs in its own context window with
  a custom system prompt, specific tool access, and independent
  permissions"; "Background subagents run concurrently"; "A subagent starts
  in the main conversation's current working directory"; spawning beyond
  20 concurrent ones fails[^cc-subagents].
- `loam-refinery prime --profile=<p>` appends the profile's guidance to
  the workflow; `prime --list` lists profiles as JSON[^lr-prime]. A
  submission's `profile` is caller-authored, and resubmitting under the
  same profile supersedes the earlier one in `collect-reviews`[^lr-collect].
- `collect-reviews` lists `submissions[]` with `profile`, `verdict`, and a
  `severity` summary, and qualifies each comment as `<profile>:<id>`
  (`#<ordinal>:<id>` for unprofiled or superseded ones)[^lr-collect].
- Verification needs the working tree to match `ref` for anchored files;
  an untracked file elsewhere in the repo didn't stop it
  ([0037](0037-loam-refinery-as-external-binary.md), `bench-lr`).
- Each workspace is a hard-linked clone plus a Claude Code session
  ([0017](0017-clones-not-worktrees.md)).

## Decision

We will keep one review agent per PR head and make its task an
orchestration prompt that `ws` embeds (`internal/review/prompt.md`), filled
with the repo, commit, PR, and profiles:

1. **Profiles.** The required set comes from `--profile` (repeatable),
   else `[review] profiles` in `config.toml`, else `architecture`,
   `claims`, `security`, `tests`. `ws` checks each against `prime --list`
   before starting (`profile_unknown`, exit 2). The agent may **add**
   language profiles from `prime --list` that match the changed files
   (`go` for `.go`, `typescript` for `.ts`/`.tsx`); it may not drop one.
2. **Fan-out.** The agent starts one background subagent per profile. Each
   runs `loam-refinery prime --profile=<p>`, reviews the diff against the
   base (or the commit alone without a PR), sets `profile` and `ref` in its
   document, and submits with `loam-refinery submit-review -` until exit 0.
   Exit 2, 3, or 101 stops that subagent and is reported.
3. **No writes.** Review documents go on stdin or under `$CLAUDE_JOB_DIR`,
   never in the repo; the agent never commits, pushes, or comments on
   GitHub.
4. **Completion check.** The agent runs `loam-refinery collect-reviews
   --ref=<sha>` and re-runs any required profile with no current
   submission, once. Its final message (the job result the app shows) is
   one line per profile: verdict and the count of priority 7+ findings,
   then any profile that failed and why.
5. **Extra instructions** from `ws review start -- … <text>`, or the
   app's template setting, are appended under "From the requester".

`ws review report` compares the record's profiles with the current
submissions and lists the difference as `missing_profiles`
([0039](0039-ws-review-command.md)), so completeness doesn't rest on the
agent's word.

### Why this option

1. Workspace, name, and status are exactly 0032's; the app's matching,
   the dedupe, and the auto-review cap don't change.
2. Subagents have their own context windows, so each profile reviews from
   scratch. They share the checkout, which is safe because none of them
   writes to it.
3. Completeness is checked twice: by the agent, which can retry, and by
   `ws review report`, which reads the store.
4. One clone and one session per review, as now; subagents cost tokens,
   not disk.

It's weak where the agent has latitude: whether it really started one
subagent per profile, or reviewed some itself, isn't observable; only the
submissions are. And per-profile progress isn't visible in `ws list`, only
"working" or "done" for the whole review.

## Alternatives

### One workspace and agent per profile

`review-<repo>-<n>-<sha7>-<profile>`, each a `ws claude` agent.

**Pros:** independence enforced by process boundaries; per-profile status
in `ws list` and the app; a different model per profile; one profile can
be retried alone.
**Cons:** four or more clones and sessions per PR head; 0032's name format
and matching change; the auto-review cap counts profiles rather than PRs;
and something still has to notice when all of them are done and combine
them. Could complement this later for profiles that need their own model.

### One agent reviews under every profile in turn

**Pros:** simplest prompt; no subagents.
**Cons:** one context window carries every earlier profile's findings into
the next, which is the framing problem profiles exist to avoid; and the
reviews run one after another.

### `ws` drives subagents itself

`ws` would run `claude -p` once per profile and submit for it.

**Pros:** `ws` knows exactly which profile ran.
**Cons:** a second way of running Claude Code beside background sessions,
outside `internal/claude`'s contract ([0024](0024-claude-code-client.md));
the runs wouldn't be sessions the app can attach to.

## Consequences

- The review task changes from a free-form template to `ws`'s prompt plus
  extra instructions; the app's "Review task" setting is relabelled
  accordingly ([0039](0039-ws-review-command.md)).
- A review costs roughly one subagent per profile in tokens; four by
  default. The Settings and `--profile` are the lever.
- Conformance: a unit test fills the prompt and checks it names every
  profile, the commit, `submit-review -`, and the no-writes rule; `ws
  review report`'s `missing_profiles` is tested against captured
  `collect-reviews` output.
- **Unverified:** that subagents started from a `claude --bg` session run
  concurrently there as they do interactively. Check with a four-profile
  review and the session's transcript before accepting.

## References

[^cc-subagents]: Claude Code docs, "Create custom subagents." <https://code.claude.com/docs/en/sub-agents> (accessed 2026-10-26). Quoted as above; also "By default, when 20 subagents are running in a session, spawning another with the Agent tool fails with `Concurrent subagent limit reached`."
[^lr-prime]: `loam-refinery prime --list`, built from loam-refinery f7b52c0, run 2026-10-26: a JSON object with `profiles`, each `{name, description}`, listing architecture, claims, compatibility, efficiency, go, security, tests, typescript.
[^lr-collect]: loam-refinery `describe --lens=collect-reviews`, f7b52c0. "Each comment's id is qualified to stay unique: `profile:origin_id` when its submission is current for that profile, `#ordinal:origin_id` otherwise — no profile claimed, or superseded." "Resubmitting under the same profile supersedes (`topic:store`): the earlier submission gets `superseded_by`".
