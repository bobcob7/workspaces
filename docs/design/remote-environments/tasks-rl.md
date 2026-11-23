# Tasks: PR 4, part RL, "Reviews and loops on remote environments"

An addition to PR 4 of [ADR 0076](../../adrs/0076-environment-platforms-and-phasing.md)'s
stack. The owner decided on 2026-11-23 that PR 4 also makes **reviews** and
**loops** work on remote environments. The earlier design had them as local
only, disabled in the UI with a reason ([tasks-ui.md](tasks-ui.md) D3, the
`reviews_*` and `loops_*` rows). Plain `ws list -q` stays local only
(tasks-ui.md D9). Everything else in tasks-ui.md stands. This file adds tasks
**RL1-RL16** and decisions **R1-R7**. The orchestrator merges them with
tasks-ui.md, whose T-tasks are referred to here by id.

**In this part:**

- the `review.*` and `loop.*` operations of [ADR 0073](../../adrs/0073-environment-agent-is-ws.md)'s
  table (its row "`cache.*`, `review.*`, `loop.*`: later, one ADR each");
- the agent ticking its own loops;
- the snapshot's `loops`;
- `--env` on `ws review` and `ws loop`;
- token redaction in results;
- the desktop's and Workspace Online's Reviews and Loops screens per
  environment;
- one new ADR, 0078;
- the docs.

**Not in this part:**

- `cache.*`;
- editing review profiles on a remote (read-only here, R1);
- "Fix" and "Ask" agents on a remote (they stay local, R1);
- `ws loop tick --env` (the agent ticks, R2);
- a remote review's or loop run's conversation view (tasks-ui.md D3, later);
- Windows.

**Read first:**

- [constraints-rl.md](constraints-rl.md): the pinned values this part moves
  that [constraints-ui.md](constraints-ui.md) doesn't list.
- constraints-ui.md: toolchains, the desktop commands, what the pod can't
  run, lint and test rules. All of it holds here.
- The "Rules for every task" of [tasks-ui.md](tasks-ui.md): Go, TypeScript
  and Rust style, no new dependency, verify one command at a time, no
  `Co-Authored-By` trailer, a failing test first, and Node 22.23.5 from the
  official tarball into `$CLAUDE_JOB_DIR/tmp/node` with SHA-256 checked.
  **They apply to every RL task unchanged** and aren't repeated here.

The code facts below were read on `feat/env-ui` at `a1c215b` (PR 3 merged,
PR 4 not started).

## Decisions this design adds to the ADRs

Each decision is marked **Owner review**: the owner should confirm or change
it before RL1 starts. RL16 writes the accepted ones into ADR 0078 and the
amendments.

### R1. A review "works remotely" when it runs entirely on the remote; the controller keeps the forge side. **Owner review.**

**Recommendation: run the whole review on the remote, with the remote's
`ws review start` and the remote's own forge sign-in. The controller keeps
the pull request list, posting comments, and the Fix and Ask agents.**

**Where it runs.** On the remote, as `ws review start` runs there today when
a remote agent calls it ([ADR 0072](../../adrs/0072-remote-environments.md):
"A review of a pull request runs where its requester runs"). Everything
lives in the agent's `CONFIG_DIR`:

- the clone cache (`cachedRepo`: `cache.Add`/`cache.Fetch`);
- the review workspace and its one Claude session, whose profile reviewers
  are subagents (`claude --agents`; not child workspaces);
- `state/<id>/review.toml`;
- `reviews/<host>/<owner>/<name>/<sha>/` (submissions, `decisions.json`,
  `runs/<id>.toml`).

The review's id is its workspace id **on that environment**. The controller
stores nothing about it. A review started from the controller has
`--requester none`, so it is a top-level workspace on the remote and
[ADR 0045](../../adrs/0045-finished-reviews-are-removed.md)'s removal pass
**on the remote** removes it when it finishes. Its report stays readable
from the remote's `runs/`.

**Who holds forge credentials.** The remote authenticates to the forge
itself. **No token ever travels over MQTT**:

- no operation takes a token argument;
- no result carries one;
- results are redacted (R3).

What the user sets up **on the remote machine**, as the OS user that runs
`ws env agent run`:

1. **GitHub**: install `gh` and run `gh auth login` for each GitHub host
   (`gh auth status` must list it). Or put `GH_TOKEN=…` in the environment
   of the agent's service (the systemd `EnvironmentFile=` or the launchd
   plist's `EnvironmentVariables`), which `internal/gh` passes on. `ws
   review start --pr` needs it to read the pull request (`gh pr view`).
2. **Private repositories on any forge**: git credentials for `git fetch`,
   for example a credential helper or SSH keys, as a local `ws` needs today
   (the cache clones over HTTPS, [ADR 0019](../../adrs/0019-https-upstream-urls.md)).
3. **Forgejo or Gitea**: nothing more. A Forgejo review starts at the head
   commit without `--pr` ([ADR 0060](../../adrs/0060-review-filters-and-forges.md)),
   and the Go review code never calls the Forgejo API. `WS_FORGEJO_TOKEN_*`
   stays the desktop's, on the controller.
4. **Claude Code** signed in, as for any agent (ADR 0073).

When these are missing, `review.start` fails with `ws`'s own codes
(`gh_missing`, `gh_auth`, `clone_failed`, `fetch_failed`, `claude_missing`)
plus `details.environment`. The UI shows, for example:

- "gh isn't signed in on linux-server. Run `gh auth login` there, as the
  user that runs the agent."

`docs/tools/ws-env.md` gains a "Reviews on an environment" checklist with
items 1-4.

**What stays on the controller** (it is forge data, or it uses the
controller's tokens):

- the Reviews checker's pull request list (`gh pr list`, the Forgejo API);
- merge status, dismissals, auto-review bookkeeping;
- **posting comments** (`review_comment_post`). It reads the findings from
  the remote, posts with the controller's `gh` or token, then records
  `review.acted --commented` on the remote;
- **Fix** (`review_fix_start`) and **Ask** (`reviews_ask`), which start
  ordinary local agents. A Fix of a remote review starts locally with the
  findings in its prompt, as today, and records `review.acted --fix-sent`
  on the remote. Running them on the remote is a follow-up.

**Operations the agent gains.** All are `CallerController` with `Since: 3`
(R6). Each runs `ws review …` in a fresh in-process App (PR 3's D1).

| Operation | Runs | Timeout | Changes | Remembered |
| --- | --- | --- | --- | --- |
| `review.start` | `ws review start --json --requester none …` | 10 min (clone, `gh`, workspace create, as `workspace.create`) | yes | yes |
| `review.status` | `ws review list --json` filtered to one id | 30 s | no | no |
| `review.list` | `ws review list --json [--repo] [--pr]`, at most `limit` runs | 30 s | no | no |
| `review.report` | `ws review report --json <id>`, one page | 30 s | no | no |
| `review.decide` | `ws review decide --json <id> <finding>… (--comment\|--fix [--suggestion N]\|--skip\|--clear) [--note]` | 30 s | no | yes |
| `review.acted` | `ws review acted --json <id> <finding>… (--commented <url>\|--fix-sent <agent>)` | 30 s | no | yes |
| `review.profiles` | `ws review profiles --json` (read-only) | 30 s | no | no |

- **No `review.wait`.** A review takes minutes to hours, so it is
  start-and-poll, never one long request. `ws review wait --env` polls
  `review.status` every 5 s (`defaultReviewPoll`) until it is finished,
  then fetches the report pages.
- **Profiles management stays local.** No `review.profile.add|edit|rm`. The
  Start review dialog shows the **target environment's** profiles
  (`review.profiles`). Settings › Reviews' profile editor is labelled
  "Profiles on this machine". A remote's profiles are edited there with
  `ws review profiles add`.

**Size bounds.** A report is unbounded today: the schema caps fields
(`body` ≤ 4000 characters) but not the number of comments. Design test
documents are 43 and 103 KB. So `review.report` is **paginated by bytes**:

- `section` is `comments` or `superseded`, and `offset` is an index into
  it;
- the agent returns as many items from `offset` as keep the result's JSON
  ≤ `MaxPageBytes` (192 KiB, like `MaxLogBytes`), at least one;
- `next_offset` is null when the section is done;
- an item over the budget alone has its `body`, `code` and each
  suggestion's `code` cut to fit, and is marked `"truncated": true`;
- `review.list` returns at most `limit` runs (1-200, default 100), newest
  first, with `"truncated"`.

**The Reviews checker and a remote repo.**

- Each watched repo gains an **environment** in `desktop.json`
  (`RepoWatch.environment`, default `local`). The Watched repos dialog sets
  it, and auto-review starts there.
- The pull request list is unchanged.
- Each check also runs `ws review list --env <name> --json` once per
  **online** environment that a watched repo names, and joins those runs to
  pull requests with the same rules as local runs. Remote entries in the
  listing (tasks-ui.md T12) join only on their own environment.
- **Start review** (manual) opens with the repo's environment preselected
  in an Environment field (T16's `EnvironmentField`). The plan's profiles
  come from that environment.
- The **findings view** (`review_report`, `review_decide`,
  `review_open_report`) carries the run's environment.
- The report cache key becomes `env:<name>:<repo>@<ref>` for remote runs.
  Local keys are unchanged.

### R2. A loop "works remotely" when its record, history, memory and ticker are all on the remote. **Owner review.**

**Recommendation: the environment agent ticks its loops every 60 s while
it is connected, with at most 2 starts per pass.**

**Where it lives.** `CONFIG_DIR/loops/<name>.toml`, `state/.loops/<name>/`
(history, memory, reports) and the run workspaces are all on the remote, in
the agent's `CONFIG_DIR`. The controller holds only the snapshot's `loops`
summary.

**Who ticks.** **The environment agent process.** It is the remote's one
long-lived `ws` process, and the ADR 0068 daemon's loop hosting isn't built
(`internal/daemon` has no tick).

- `envagent.Agent` gets a `loopTicker` and `Options.LoopTick` (60 s).
- While the control connection is up, the agent runs one pass every
  `LoopTick`. The pass is `ws loop tick --json --quiet --max-starts 2` in a
  fresh in-process App, the same code the desktop's `tick_forever` runs.
- The first pass after a (re)connect waits one full `LoopTick`.
- A pass never runs on the session loop; it runs on its own goroutine, one
  at a time (a pass still running skips the next timer).
- A pass that started or settled anything signals the snapshot to refresh
  (`signalChanged`).
- The pass starts Claude agents on the remote with the remote's `claude`,
  exactly as a local tick does (`startLoopRun`), and `ws` runner runs with
  the remote's endpoints.

**Offline.** "Offline" means the agent has no control connection. It covers
two cases: the agent process isn't running, or it is reconnecting.

- **Loops pause in effect.** No tick runs, and nothing is marked `paused`.
- **Nothing queues.** On reconnect, ADR 0048's schedule rule applies:
  `Advance` re-anchors a loop that missed periods to *now + every*. Each
  overdue loop is due **once**, not once per missed period.
- The new `--max-starts 2` caps how many runs one pass starts. Further due
  loops are left `waiting` with `next_run_at` unchanged, for the next pass
  60 s later. So 10 overdue loops start over 5 minutes, not at once.
- Why not keep ticking while disconnected: ADR 0072's rule, "a start that
  ran later, unseen, on a machine the user forgot is worse than an error".
  Also, a blocked run's notification couldn't reach anyone.
- If the remote's owner also runs a tick on that machine (`ws loop tick
  --watch`, or a desktop app on it) with the same `CONFIG_DIR`, then
  `loops/.lock` serializes the two, and that ticker keeps running loops
  while the agent is offline. `ws-env.md` says not to do this.

**How the controller sees it.**

- `envproto.Snapshot` gains `loops` (`[]SnapshotLoop`, at most
  `MaxSnapshotLoops` = 100, by name). Each entry holds `name`, `title`,
  `every`, `paused`, `hidden`, `runner`, `next_run_at`, `last_run`
  (`started_at`, `workspace`, `session`, `error`) and `last_gate`
  (`at`, `outcome`, `exit_code`, `message`).
- It has no prompt, script or vars (size, and R3). A protocol-2 agent sends
  no `loops`, which decodes as nil and means "unknown".
- Run workspaces are in the snapshot's `workspaces` with their `loop_run`
  (`ws list --json`'s field), so the Agents list and the tray see blocked
  runs with no new code (R5).

**Operations.** All are `CallerController`, `Since: 3`, run as `ws loop …
--json` in a fresh App. The JSON shapes are in RL2.

| Operation | Runs | Timeout | Changes | Remembered | Confirm (R3) |
| --- | --- | --- | --- | --- | --- |
| `loop.list` | `ws loop list`, one page | 30 s | no | no | |
| `loop.show` | `ws loop list`, one loop in full | 30 s | no | no | |
| `loop.add` | `ws loop add` | 30 s | yes | yes | when it has a script, or `permission_mode` `bypassPermissions` |
| `loop.edit` | `ws loop edit` | 30 s | yes | yes | when it sets a script, or `permission_mode` `bypassPermissions` |
| `loop.pause`, `loop.resume` | `ws loop pause\|resume` | 30 s | yes | yes | |
| `loop.rm` | `ws loop rm` | 30 s | yes | yes | always |
| `loop.run` | `ws loop run [--force]` | 15 min (gate ≤ 10 min, clone, start) | yes | yes | |
| `loop.history` | `ws loop history --limit N` | 30 s | no | no | |
| `loop.history_run` | `ws loop history --run SEQ` | 30 s | no | no | |
| `loop.history_clear` | `ws loop history --clear` | 30 s | no | yes | always |
| `loop.memory_list`, `loop.memory_get` | `ws loop memory list\|get` | 30 s | no | no | |
| `loop.memory_set`, `loop.memory_rm` | `ws loop memory set\|rm [--expect-rev]` | 30 s | no | yes | |
| `loop.memory_clear` | `ws loop memory clear` | 30 s | no | yes | always |

Names use `_`, not a third dot, so `strings.HasPrefix(op, "loop.")`
dispatches them all.

**The gate script and the prompt template.**

- A gate script is `/bin/sh -c` run as the agent's user, outside Claude
  Code's permissions, with ws's environment (`internal/gate`).
- A prompt template feeds a Claude agent, which can run anything its
  permission mode allows.
- Both are **the same trust as starting a Claude agent on that machine**,
  which `workspace.create` already allows from the controller. The design
  says so plainly in ADR 0078 and `ws-env.md`: "anyone who can use this
  controller can run code as <user> on every enrolled machine".
- Decision:
  - Both **can** be written from the desktop and the browser (Workspace
    Online).
  - **Confirmation in the UI**: saving a loop on a remote environment with
    a new or changed script, or with `permission_mode` `bypassPermissions`,
    opens a dialog: "This script runs as the agent's user on linux-server,
    every 1h, outside Claude Code's permissions." The dialog shows the
    script read-only and has a **Run on linux-server** button. Then the op
    is sent with `confirm: true`.
  - The agent refuses such a `loop.add`/`loop.edit` without `confirm`
    (`usage`, details `{"needs": "confirm"}`).
  - **The remote's owner can forbid scripts**: `[environments]
    agent_loop_scripts = false` in the **remote's** `config.toml` (default
    `true`). With it set, a `loop.add`/`loop.edit` from the controller that
    carries a script is refused with `usage` "scripts from the controller are
    turned off on linux-server". Existing scripts still run.
  - `loop.run --force` (skip the gate) needs no confirm. It runs less code,
    not more.

### R3. Security of the new operations as a whole. **Owner review.**

**Recommendation: as below, with agent-side redaction by exact secret
values and fixed token patterns.**

**Allow-list and typed arguments, checked three times** (PR 3's D6):

1. `envproto.LookupOp`, then `DecodeArgs` with unknown keys refused, then
   `Validate`, on both the controller and the agent.
2. Then the agent's `CheckWorkspaceRef`-style checks: a review id or loop
   name must name something under the agent's own directories.
3. Then the CLI's own validation.

Each operation has a table row in RL1 or RL2's `Validate` test, and a named
`t.Run` in RL6, RL7 or RL10 (the table in RL10 lists them).

**Only the agent's own reviews and loops.**

- A review id must match `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`. It is a
  workspace id: no `/`, no leading `-` or `.`.
- The agent resolves it only through `findReviewByID`, which looks in its
  `workspaces/` records and its `reviews/…/runs/`. `--repo`/`--ref` lookups
  are **not** offered over the wire.
- A loop name must match ADR 0048's `^[a-z0-9][a-z0-9-]{0,39}$`, so it
  can't name a path. `loop.Store` and `loop.DataStore` join it under
  `loops/` and `state/.loops/`.
- Finding ids are `^[a-z0-9-]{1,40}:[A-Za-z0-9._-]{1,64}$`.
- Memory keys follow ADR 0056's rule.
- `review.start`'s `repo` is a spec with no `@`, checked by
  `repo.ParseSpec` on the agent. `head` is 40 lowercase hex characters.
- Nothing from the controller becomes a flag: every free text (`focus`,
  `note`, `prompt`, a value) goes after `--` or through `--file=-` on
  stdin, and a `focus` may not start with `-`.

**Destructive operations need `confirm: true`:**

- `loop.rm`, which deletes the record, history, memory and reports;
- `loop.history_clear`;
- `loop.memory_clear`;
- `loop.add`/`loop.edit` with a script or `bypassPermissions` (R2).

Without it they are refused with `usage`. Not destructive:

- `review.decide` and `review.acted` (they are reversible with `--clear`
  and a new act);
- `loop.memory_rm` (one key, with optional `expect_rev`);
- `loop.pause` and `loop.resume`.

**Rates, on the agent** (`envagent`, per agent process, a token bucket each):

| Bucket | Limit |
| --- | --- |
| `review.start` | 12 per hour (each starts a Claude session with several subagents) |
| `loop.run` | 30 per hour in all, and 1 per loop per 60 s |
| `loop.add` + `loop.edit` + `loop.rm` | 60 per hour |
| every other `review.*`/`loop.*` | the existing `MaxOps` and queue only |

Over a limit, the reply is the new code **`operation_limited`** (exit 1),
with details `{"op", "retry_after_seconds"}`.

**Size bounds.**

- Arguments:
  - a `prompt` is 1..`MaxPromptBytes` (64 KiB);
  - a `script` is ≤ 16 KiB (`MaxScriptBytes`);
  - vars: ≤ 32, key ≤ 64 B, value ≤ 4 KiB, all of them ≤ 32 KiB
    (`MaxVarsBytes`);
  - a memory value is ≤ 64 KiB (ADR 0056's own limit);
  - `focus` and `note` are ≤ 16 KiB (`MaxMessageBytes`);
  - a finding list holds 1-200 ids;
  - a `title` is ≤ 80 bytes.
- Results:
  - every paged result is ≤ `MaxPageBytes` (192 KiB);
  - `loop.history_run`'s `report_text` is cut to 160 KiB with
    `"report_truncated": true`;
  - `loop.memory_list` previews each value to 1 KiB with
    `"value_truncated"`, and `loop.memory_get` returns the whole value.
- `MaxRequestBytes` (256 KiB) stays the ceiling of every message.

**Results never carry secrets: the agent redacts.** A review comment, a
loop memory value, a history report, a prompt or a var can hold a token
that a model or a person printed. New package `internal/redact` (RL3):

- **Exact values** are the values, of at least 8 bytes, of:
  - every `NAME=value` in the agent's `secrets.env` (`config.ReadSecrets`);
  - every variable in the agent's environment whose name is `GH_TOKEN`,
    `GITHUB_TOKEN` or `CLAUDE_CODE_OAUTH_TOKEN`, starts with
    `WS_FORGEJO_TOKEN_` or `WS_ENDPOINT_TOKEN_`, or ends with `_TOKEN`,
    `_SECRET`, `_API_KEY` or `_PASSWORD`.
- **Patterns** (each match is replaced whole):
  - `gh[pousr]_[A-Za-z0-9]{36,255}`
  - `github_pat_[A-Za-z0-9_]{22,255}`
  - `sk-ant-[A-Za-z0-9_-]{20,}`
  - `glpat-[A-Za-z0-9_-]{20,}`
  - `xox[abprs]-[A-Za-z0-9-]{10,}`
  - `AKIA[0-9A-Z]{16}`
  - `-----BEGIN [A-Z ]*PRIVATE KEY-----` through the matching `END` line
  - `(?i)(authorization:\s*(?:bearer|token)\s+)[A-Za-z0-9._~+/=-]{8,}`,
    which keeps group 1.
- The replacement is `[redacted]`.
- The agent applies `redact.JSON` to **every string value** of the result of
  every `review.*` and `loop.*` operation, and to the snapshot's `loops`.
  It adds `"redacted": N` to the result object, the count of replacements.
- `workspace.*` and `agent.*` results are unchanged in this part.
  `agent.logs` is already not remembered; redacting it is a follow-up.
- **Round trips.** A value that came back redacted must not be saved back
  over the secret. The agent refuses a `loop.add`, `loop.edit` or
  `loop.memory_set` in which any string contains `[redacted]` (`usage`:
  "a value contains [redacted]; retype it or leave it unchanged").
- The UI shows a redacted memory value read-only with "This value holds a
  secret and is hidden; set a new value to replace it".
- The loop form sends only changed fields for a remote loop (RL12).
- Why on the agent: the secret never leaves the machine. Exact values catch
  the agent's own tokens, and patterns catch the common forges' and
  Anthropic's.
- The miss: a secret in an unknown format. ADR 0078 records this.

### R4. Offline and staleness in the Reviews and Loops screens; choosing the environment. **Owner review.**

**Recommendation: as below. Remote data is shown dimmed and read-only when
its environment is offline or stale, using tasks-ui.md D4's reasons.**

Presence and the reasons come from `environmentsSlice` (tasks-ui.md T14,
`offlineReason`).

**Loops screen.**

- The list is **grouped by environment**: `local` first, then each enrolled
  environment by name, each group with its online dot. A filter chip row
  appears as soon as one environment is enrolled. With none, nothing
  changes.
- The list comes from `ws loop list --env <name> --cached` (RL9), which
  reads the snapshot. It costs nothing on the wire and works offline. The
  loops poll is unchanged.
- Opening a loop fetches it live (`loop.show`, then `loop.history`,
  `loop.memory_list`).
- **Offline group:** loops are shown from the snapshot, dimmed, with "linux-server
  is offline (last seen 14:02)". Run now, Pause, Resume, Edit, Delete,
  History and Memory are disabled with that reason. The note says "Loops on
  linux-server don't run while it is offline; missed runs are skipped, not
  made up".
- **Stale but online** (snapshot > 90 s): a `stale` badge, actions enabled.
- **New loop** has an **Environment** field (`EnvironmentField`, T16),
  remembered with `ws.environment.last` like New agent. Offline choices are
  disabled. A loop can't move between environments: Edit shows the
  environment read-only.
- **The loop's workspaces are on that environment by construction**: the
  remote's tick creates them there. A run's row in the Agents list is a
  remote entry (T12) whose `loopRun` comes from the snapshot.

**Reviews screen.**

- Pull requests are listed as today (controller data, never stale because
  of an environment).
- A run on a remote environment shows an environment badge.
- When that environment is offline: its runs keep their last known state
  (the checker keeps the previous cycle's runs for an environment whose
  `review list --env` failed), dimmed, with the reason. Open report,
  decisions, Comment and Fix are disabled with "linux-server is offline
  (last seen 14:02)". Start review on a repo whose environment is offline is
  disabled with the same reason; the user can pick another environment in
  the dialog.
- The **report view** keeps a report it already loaded (`reportsSlice`)
  and shows it read-only with "as of 14:02" while offline.
- **Auto-review** skips a repo whose environment is offline or whose hello
  says `claude: missing`. It retries next check and doesn't count against
  `max_auto_reviews`.

**Settings › Reviews › Watched repos**: each repo row gets an Environment
select (local and enrolled environments; offline ones are still selectable
here, since it's a setting, with a "(offline)" note).

### R5. `ws machine`, trees, `ws review` from agents, tray and notifications. **Owner review.**

**Recommendation: confirm what already works by construction, and add only
the agent guard on `--env` below.**

- **A remote Claude agent that runs `ws machine spawn` or `ws review start`
  works on the remote by construction.** Confirmed by the code:
  - the agent's `PATH` `ws` is the same binary with the agent's
    `CONFIG_DIR` (ADR 0073);
  - `reviewRequester` finds the requester from the working directory;
  - `workspaces.Create` makes the child with `Parent: requester` on that
    machine.
  So it needs no change. It needs the remote's `gh` and git credentials
  (R1). A remote loop run's agent uses `ws machine memory …` and `ws machine
  result BLOCKED` on the remote, with the run mark under the remote's
  `state/`.
- **An agent never picks an environment.** `--env` on `ws review start`
  (and the other `ws review` subcommands, RL8) is refused with `usage`
  ("an agent never chooses an environment; run ws review start without
  --env") when the command runs inside a workspace. That means
  `reviewRequester` found one and `--requester` wasn't given as `none`.
  People and the desktop run it outside any workspace. `ws loop` is people
  only (ADR 0048), so it needs no guard. `ws llm review start` says `--env`
  is for people and the desktop app.
- **Trees**: a review's workspace is a child of its requester on the same
  environment. A controller-started review is top-level (`--requester
  none`). A loop run is top-level, and its children are on its environment
  (ADR 0072). tasks-ui.md T14's `parentOf` already matches on `environment`.
- **Tray and notifications.**
  - **Blocked or needs-attention loop runs on a remote notify like local
    ones** while the environment is online and its snapshot fresh. This
    needs no new code after T12: the snapshot's workspaces carry `loop_run`,
    T12 builds the remote `Entry.loopRun` from it, and the watcher's
    `loop_step` keys by `env:<name>:<id>`.
  - Expect about 10 s more delay than local (the snapshot's check period).
  - RL11 adds the test `remote_blocked_loop_run_notifies` and
    `offline_remote_loop_run_does_not_notify`, because it is a promise.
  - **Reviews finished**: there is no review-specific notification today. A
    review's agent is tracked only when it is a top-level, unhidden agent
    (`is_tracked`), so a controller-started remote review (top-level) gives
    the same "Done" as a local one, through T12. No new notification kind.
  - Workspace Online's push (`online/push.rs`) uses the same watcher.

### R6. Protocol number and `Since`. **Owner review.**

**Recommendation: `Protocol` N+1, where N is the terminals' protocol.**

- tasks-ui.md D9 sets `Protocol = 2` for `terminal.*`. This part sets
  **`Protocol = 3`**: "Protocol N+1 after terminals". If the orchestrator
  merges this part first, or the terminals' number changes, it renumbers.
  Every `Since` here is `ProtocolReviewsLoops`, a named constant equal to
  `Protocol` at merge, so the renumbering is one line.
- **Every `review.*` and `loop.*` operation has `Since: 3`.**
- The snapshot's `loops` is sent by protocol-3 agents. A controller treats
  a missing `loops` as unknown, and the Loops screen shows "linux-server
  runs ws 0.31, which can't run loops from here; update ws there" (the
  hello's `ws_version`).
- A protocol-2 agent gets `environment_outdated` for each new op before it
  is sent, as PR 3's D5 does.
- The agent's own tick (R2) isn't an operation and needs no protocol. A
  protocol-3 agent ticks whatever controller it talks to.

### R7. One new ADR, 0078, for both; amendments to 0072, 0073, 0076, 0016, 0048. **Owner review.**

**Recommendation: one ADR, 0078 "Reviews and loops run on the environment
that owns them".**

- ADR 0073's table says "`cache.*`, `review.*`, `loop.*`: later, one ADR
  each". This design proposes **one** ADR for both, because they share the
  security stance, redaction, confirm flags, rates and protocol number.
  Two ADRs would repeat R3.
- If the owner prefers two, RL16 splits it into 0078 (reviews: R1, R3's
  review rows) and 0079 (loops: R2, R3's loop rows).
- The number is the next free one at merge; 0077 is the latest on
  `feat/env-ui`.
- Amendments (RL16):

| ADR | Change |
| --- | --- |
| 0073, operations table | Replace "`cache.*`, `review.*`, `loop.*` \| later, one ADR each" with a `review.*` row and a `loop.*` row (R1, R2), link 0078; keep `cache.*` later. Add "results of `review.*` and `loop.*` are redacted (0078)". |
| 0072, Decision › Choosing it | "`ws review …`, `ws loop …` take `--env` (people and the desktop; refused for agents inside a workspace)". Amendments: R5's guard. |
| 0076, stack table row 4 | Add "reviews and loops on environments (`review.*`, `loop.*`, the agent's tick, protocol 3, ADR 0078)". |
| 0016, Status | Add `operation_limited` (PR 4, ADR 0078). |
| 0048, decision 7 "Who runs tick" | Add: "On an environment, its agent runs the tick every 60 s while connected, with `--max-starts 2` (0078)". Decision 4 gains `--max-starts`. |
| 0060, Starting a review | Add: "on the repo's environment (0078)". |

## Order

```
T2 ── RL1 ── RL2 ─┬──────────── RL5 (also T4, RL4) ─┐
RL3 ──────────────┤                                 │
RL4 ──────────────┤                                 │
T5 ─── RL6 (RL1, RL3) ── RL7 (RL2, RL3, RL4) ───────┼── RL10 (also T11)
T8, T10 ── RL8 (RL1) ── RL9 (RL2, T9) ──────────────┘
T12, T13 ── RL11 (RL8) ── RL12 (RL9) ── RL13 (T14) ─┬─ RL14 (T16, T17)
                                                    └─ RL15 (T16)
RL10, RL14, RL15, T18 ── RL16
```

Edges:

| Task | Depends on |
| --- | --- |
| RL1 | T2 |
| RL2 | RL1 |
| RL3 | – |
| RL4 | – |
| RL5 | RL2, RL4, T4 |
| RL6 | RL1, RL3, T5 |
| RL7 | RL2, RL3, RL4, RL6 |
| RL8 | RL1, T8, T10 |
| RL9 | RL2, RL8, T9 |
| RL10 | RL5, RL7, RL8, RL9, T11 |
| RL11 | RL8, T12, T13 |
| RL12 | RL9, RL11, T12 |
| RL13 | RL11, RL12, T14 |
| RL14 | RL13, T16, T17 |
| RL15 | RL13, T16 |
| RL16 | RL10, RL14, RL15, T18 |

**Why each T dependency is there (shared files):**

- **T2**: `envproto/ops.go`, `payloads.go` (`Protocol`).
- **T4**: `envagent/agent.go`, `interfaces.go` (the moq list).
- **T5**: `internal/cli/env_agent_ops.go` (`Run`'s dispatch).
- **T8**: `internal/cli/errors.go`, `app_test.go`, `llm.go`, guides.
- **T9**: `internal/cli/list_env.go` (snapshot reading).
- **T10**: the `--env` flag helpers.
- **T11**: `internal/mqttbroker/conformance_test.go`.
- **T12**: `model.rs`, `commands.rs`, `reviews/runs.rs`, `tray/*`,
  `online/dispatch.rs`, `tauri-mock.js`.
- **T13**: `lib.rs`, `dispatch.rs` `COMMANDS` (87 names).
- **T14**: `api.ts`, `store.ts`, `fixtures.ts`, `environmentsSlice`.
- **T16**: `EnvironmentField`.
- **T17**: `EnvBadge`, `remote.ts`.
- **T18**: the ADRs and docs RL16 also edits.

**What can run in parallel:**

- RL3 and RL4 depend on nothing and run beside any T-task.
- RL1 and RL2 are package-local.
- RL5 runs beside RL6.
- RL14 and RL15 run in parallel with each other (no shared file).

| Task | Area | Depends on | Parallel with | Size | Model |
| --- | --- | --- | --- | --- | --- |
| RL1 Protocol: review operations, arguments, protocol 3 | protocol | T2 | RL3, RL4 | | |
| RL2 Protocol: loop operations, arguments, snapshot `loops` | protocol | RL1 | RL3, RL4 | | |
| RL3 `internal/redact`: exact values and token patterns | security | – | everything | `small` | `sonnet` |
| RL4 `ws loop tick --max-starts`; the agent's loop ticker and snapshot loops in `internal/cli` | CLI | – | everything | `small` | |
| RL5 Agent: loop ticking while connected, snapshot `loops`, rate limits | agent | RL2, RL4, T4 | RL6, RL8 | | `sonnet` |
| RL6 Agent side: `review.*` in-process, pages, redaction | agent | RL1, RL3, T5 | RL5, RL8 | | `sonnet` |
| RL7 Agent side: `loop.*` in-process, confirm, scripts switch | agent | RL2, RL3, RL4, RL6 | RL8, RL9 | | `sonnet` |
| RL8 CLI: `--env` on `ws review`, the agent guard, `operation_limited` | CLI | RL1, T8, T10 | RL5-RL7 | | |
| RL9 CLI: `--env` and `--cached` on `ws loop` | CLI | RL2, RL8, T9 | RL7 | | |
| RL10 Conformance: reviews and loops over the real broker | CLI | RL5, RL7, RL8, RL9, T11 | RL11-RL15 | | `sonnet` |
| RL11 Desktop backend: reviews per environment | desktop backend | RL8, T12, T13 | RL10 | | |
| RL12 Desktop backend: loops per environment | desktop backend | RL9, RL11, T12 | RL10 | `small` | |
| RL13 Frontend: types and slices for reviews and loops per environment | desktop frontend | RL11, RL12, T14 | RL10 | | |
| RL14 Reviews screen: environment, badges, offline | desktop frontend | RL13, T16, T17 | RL15 | | |
| RL15 Loops screen: groups, environment field, script confirmation | desktop frontend | RL13, T16 | RL14 | | |
| RL16 ADR 0078, amendments, docs | docs | RL10, RL14, RL15, T18 | – | | |

**Heavy children** (full `internal/cli` tests, `cargo test`, vitest): RL7,
RL8, RL9, RL10, RL11-RL15. Run at most one at a time on an 8 GB pod, as
tasks-ui.md says. RL1-RL5 are package-local.

---

## RL1. Protocol: review operations, arguments, protocol 3

- **Depends on:** T2 (it edits `ops.go` and sets `Protocol = 2`).
  **Parallel:** RL3, RL4.
- **Pinned values touched:** constraints-rl.md › Wire payloads.

### Files

Create:

- `internal/envproto/review.go`
- `internal/envproto/review_test.go`

Change:

- `internal/envproto/ops.go` (`LookupOp`, `newArgs`, `Validate` learn the
  seven ops);
- `ops_test.go`;
- `payloads.go`: `Protocol = 3`, and `const ProtocolReviewsLoops = 3`
  documented as "the protocol `review.*` and `loop.*` need (ADR 0078)";
- `payloads_test.go`: the `"protocol":2` literals T2 set become
  `envproto.Protocol`.

`internal/envproto` imports only the standard library.

### `review.go`

```go
// Operation names of ADR 0078's review rows.
const (
	OpReviewStart    = "review.start"
	OpReviewStatus   = "review.status"
	OpReviewList     = "review.list"
	OpReviewReport   = "review.report"
	OpReviewDecide   = "review.decide"
	OpReviewActed    = "review.acted"
	OpReviewProfiles = "review.profiles"
)

// Bounds of review and loop results and arguments (ADR 0078).
const (
	MaxPageBytes     = 192 << 10 // a paged result's JSON
	MaxFindings      = 200       // finding ids in one decide or acted
	DefaultListLimit = 100
	MaxListLimit     = 200
	MaxFocusBytes    = MaxMessageBytes
)

// Report sections a page reads.
const (
	SectionComments   = "comments"
	SectionSuperseded = "superseded"
)

// Decisions of review.decide.
const (
	DecideComment = "comment"
	DecideFix     = "fix"
	DecideSkip    = "skip"
	DecideClear   = "clear"
)

// ReviewStartArgs are review.start's arguments. Exactly one of PR and Head
// is set: PR for a GitHub pull request (ws reads it with gh on the agent),
// Head for a commit (a Forgejo pull request's head, ADR 0060).
type ReviewStartArgs struct {
	Repo           string   `json:"repo"`            // a spec without @ (repo.ParseSpec on the agent)
	PR             *int     `json:"pr"`              // 1..2^31-1
	Head           *string  `json:"head"`            // 40 lowercase hex
	Name           *string  `json:"name"`            // validateName
	Profiles       []string `json:"profiles"`        // "NAME[:MODEL]", [] for ws's choice
	Auto           []string `json:"auto"`            // "NAME[:MODEL]"
	Model          *string  `json:"model"`           // modelPattern
	PermissionMode *string  `json:"permission_mode"` // one of PermissionModes
	Focus          *string  `json:"focus"`           // 1..MaxFocusBytes, not starting with "-"
}

// ReviewRef is the argument of review.status: one review by id.
type ReviewRef struct {
	Review string `json:"review"` // ValidReviewID
}

// ReviewListArgs are review.list's arguments.
type ReviewListArgs struct {
	Repo  *string `json:"repo"`  // a cache id host/owner/name, or null for all
	PR    *int    `json:"pr"`    // needs Repo
	Limit int     `json:"limit"` // 0: DefaultListLimit; at most MaxListLimit
}

// ReviewReportArgs are review.report's arguments: one page of one section.
type ReviewReportArgs struct {
	Review  string `json:"review"`
	Section string `json:"section"` // SectionComments or SectionSuperseded
	Offset  int    `json:"offset"`  // >= 0
}

// ReviewDecideArgs are review.decide's arguments.
type ReviewDecideArgs struct {
	Review     string   `json:"review"`
	Findings   []string `json:"findings"` // 1..MaxFindings ValidFindingID
	Decision   string   `json:"decision"` // DecideComment, DecideFix, DecideSkip, DecideClear
	Suggestion *int     `json:"suggestion"` // fix only, >= 0, counting from 0 like the CLI
	Note       *string  `json:"note"`       // 1..MaxMessageBytes; not with clear
}

// ReviewActedArgs are review.acted's arguments; exactly one of Commented and FixSent.
type ReviewActedArgs struct {
	Review    string   `json:"review"`
	Findings  []string `json:"findings"`
	Commented *string  `json:"commented"` // an https:// URL, <= 2048 bytes
	FixSent   *string  `json:"fix_sent"`  // the fix agent's workspace id on the controller, validateName
}

// ReviewProfilesArgs are review.profiles's arguments (none).
type ReviewProfilesArgs struct{}

// ReviewStatusResult is review.status's result.
type ReviewStatusResult struct {
	Review   string          `json:"review"`
	Agent    json.RawMessage `json:"agent"`    // ws review list's {"workspace","status"} or null
	Finished bool            `json:"finished"` // as ws review wait decides (reviewFinished)
	Redacted int             `json:"redacted"`
}

// PermissionModes are the permission modes a review or a loop may ask for.
// The implementer copies the set from internal/loop's permission_mode check.
func PermissionModes() []string

// ValidReviewID reports whether id matches ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$.
func ValidReviewID(id string) bool

// ValidFindingID reports whether id matches ^[a-z0-9-]{1,40}:[A-Za-z0-9._-]{1,64}$.
func ValidFindingID(id string) bool
```

`review.list`, `review.report`, `review.decide`, `review.acted` and
`review.profiles` return the CLI's own JSON object (`reviewListJSON`,
`reviewReportJSON`, `reviewDecisionsJSON`, `reviewProfilesJSON`), plus
`"redacted"`. `review.report` and `review.list` also add the paging keys
(RL6):

```json
{"repo":"github.com/o/r","ref":"3f2a…","review":{…},"agent":{"workspace":"review-r-42-3f2a9c1-d8efb24f","status":"idle"},
 "profiles":[…],"comments":[…],"superseded":[],"warnings":[],
 "section":"comments","offset":0,"next_offset":37,"total":112,"redacted":0}
```

In a page, the section that wasn't asked for is `[]`.

### `LookupOp` rows

Every review row has `Caller: CallerController` and `Since:
ProtocolReviewsLoops`.

| Op | Timeout | Changes |
| --- | --- | --- |
| `review.start` | 10 min | true |
| `review.status` | 30 s | false |
| `review.list` | 30 s | false |
| `review.report` | 30 s | false |
| `review.decide` | 30 s | false |
| `review.acted` | 30 s | false |
| `review.profiles` | 30 s | false |

### Validation (`validateReview…`, one function per args type, each ≤ 40 lines)

**`ReviewStartArgs`:**

- `Repo` uses `validateSpec`, and may not contain `@`.
- Exactly one of `PR` and `Head`.
- `PR` is ≥ 1.
- `Head` matches `^[0-9a-f]{40}$`.
- `Name` uses `validateName`.
- Each profile matches `^[a-z0-9][a-z0-9-]{0,39}(:<modelPattern body>)?$`.
  `profiles` and `auto` hold at most 16 entries each.
- `Model` matches `modelPattern`.
- `PermissionMode` is in `PermissionModes()`.
- `Focus` is 1..`MaxFocusBytes` and doesn't start with `-` after leading
  whitespace.

**The other argument types:**

- Every `Review` is `ValidReviewID`, else `usage`.
- `ReviewListArgs`: `Repo` matches `^[A-Za-z0-9.-]+(:[0-9]+)?/[^/\s]+/[^/\s]+$`;
  `PR` needs `Repo`; `Limit` is 0..`MaxListLimit`.
- `ReviewReportArgs`: `Section` is known, and `Offset` is ≥ 0.
- `ReviewDecideArgs`: findings are 1..`MaxFindings` and each one is
  `ValidFindingID`. `Decision` is known. `Suggestion` is only with `fix`
  and ≥ 1. `Note` is not allowed with `clear`.
- `ReviewActedArgs`: exactly one of `Commented` and `FixSent`.
  `Commented` starts with `https://`, has no whitespace or control
  characters, and is ≤ 2048 bytes. `FixSent` uses `validateName`.

### Tests (`review_test.go`)

- `TestLookupOp` gains "review ops are controller ops since 3" and
  "review.start has 10 min and changes".
- `TestValidate_review` is a table with one row per rule above. Each
  refusal is `*ArgsError` with its code (`usage`, `spec_invalid`,
  `name_invalid`). It includes:
  - "a repo with @ is refused";
  - "pr and head together are refused";
  - "a focus starting with a dash is refused";
  - "a finding id without a profile is refused";
  - "201 findings are refused";
  - "a commented URL that is http is refused";
  - "a review id with a slash is refused".
- `TestDecodeArgs` gains "unknown keys in review.report are refused".
- `TestValidReviewID`, `TestValidFindingID`.

### Acceptance

- `envproto.Protocol == 3`, and every `Since` of the new rows is
  `ProtocolReviewsLoops`.
- T2's two ops keep `Since: 2`.
- No new import outside the standard library.

### Verify

```
go test -p 2 -count=1 ./internal/envproto/ ./internal/envagent/ ./internal/environment/
make lint
```

---

## RL2. Protocol: loop operations, arguments, snapshot `loops`

- **Depends on:** RL1. **Parallel:** RL3, RL4.
- **Pinned values touched:** constraints-rl.md › Wire payloads.

### Files

Create:

- `internal/envproto/loop.go`
- `internal/envproto/loop_test.go`

Change:

- `ops.go` (`LookupOp`, `newArgs`, `Validate`);
- `ops_test.go`;
- `payloads.go`: `Snapshot` gains `Loops []SnapshotLoop \`json:"loops,omitempty"\``,
  because a protocol-2 reader must still decode it. Use `omitempty` **only**
  here: a protocol-3 agent always sends `[]`, set by its callers as `Agents`
  is;
- `payloads_test.go` (a snapshot round trip with and without `loops`).

### `loop.go`

```go
const (
	OpLoopList         = "loop.list"
	OpLoopShow         = "loop.show"
	OpLoopAdd          = "loop.add"
	OpLoopEdit         = "loop.edit"
	OpLoopPause        = "loop.pause"
	OpLoopResume       = "loop.resume"
	OpLoopRm           = "loop.rm"
	OpLoopRun          = "loop.run"
	OpLoopHistory      = "loop.history"
	OpLoopHistoryRun   = "loop.history_run"
	OpLoopHistoryClear = "loop.history_clear"
	OpLoopMemoryList   = "loop.memory_list"
	OpLoopMemoryGet    = "loop.memory_get"
	OpLoopMemorySet    = "loop.memory_set"
	OpLoopMemoryRm     = "loop.memory_rm"
	OpLoopMemoryClear  = "loop.memory_clear"
)

const (
	MaxScriptBytes    = 16 << 10
	MaxVars           = 32
	MaxVarValueBytes  = 4 << 10
	MaxVarsBytes      = 32 << 10
	MaxMemoryValue    = 64 << 10 // ADR 0056
	MaxReportText     = 160 << 10
	MaxMemoryPreview  = 1 << 10
	MaxSnapshotLoops  = 100
	MaxHistoryLimit   = 100
	PermissionBypass  = "bypassPermissions"
	RedactedMarker    = "[redacted]"
)

// LoopName is the argument of loop.show, loop.pause, loop.resume, loop.history_clear,
// loop.memory_list, loop.memory_clear and loop.rm (the last three need Confirm).
type LoopName struct {
	Name    string `json:"name"`    // ^[a-z0-9][a-z0-9-]{0,39}$
	Confirm bool   `json:"confirm"` // required true for loop.rm, loop.history_clear, loop.memory_clear
}

// LoopListArgs are loop.list's: a page of loops by name.
type LoopListArgs struct {
	Offset int `json:"offset"`
}

// LoopSpec is loop.add's arguments, and loop.edit's with every field optional.
// Nil means "not given" (edit: unchanged; add: ws's default).
type LoopSpec struct {
	Name           string             `json:"name"`
	Title          *string            `json:"title"`           // <= 80 bytes, no control characters
	Every          *string            `json:"every"`           // a Go duration; add: required. ws checks 5m..168h
	Prompt         *string            `json:"prompt"`          // add: required; 1..MaxPromptBytes
	Specs          *[]string          `json:"specs"`           // each validateSpec; edit: [] clears (--no-specs)
	Keep           *int               `json:"keep"`            // 1..50
	Runner         *string            `json:"runner"`          // "claude" or "ws"
	Endpoint       *string            `json:"endpoint"`        // validateEndpoint
	Model          *string            `json:"model"`           // modelPattern
	PermissionMode *string            `json:"permission_mode"` // PermissionModes()
	Paused         *bool              `json:"paused"`          // add only
	Script         *string            `json:"script"`          // <= MaxScriptBytes; edit: "" removes (--no-script)
	ScriptTimeout  *string            `json:"script_timeout"`  // a Go duration; ws checks 1s..10m
	Vars           *map[string]string `json:"vars"`            // <= MaxVars, keys [A-Za-z_][A-Za-z0-9_]{0,63}, values <= MaxVarValueBytes, all <= MaxVarsBytes; edit: {} clears (--no-vars)
	Hidden         *bool              `json:"hidden"`
	Confirm        bool               `json:"confirm"` // NeedsConfirm(op, spec) must be satisfied
}

// LoopRunArgs are loop.run's.
type LoopRunArgs struct {
	Name  string `json:"name"`
	Force bool   `json:"force"`
}

// LoopHistoryArgs are loop.history's.
type LoopHistoryArgs struct {
	Name  string `json:"name"`
	Limit int    `json:"limit"` // 0: 20; 1..MaxHistoryLimit
}

// LoopHistoryRunArgs are loop.history_run's.
type LoopHistoryRunArgs struct {
	Name string `json:"name"`
	Seq  int    `json:"seq"` // >= 1
}

// LoopMemoryKey is loop.memory_get's and loop.memory_rm's.
type LoopMemoryKey struct {
	Name      string `json:"name"`
	Key       string `json:"key"`         // ADR 0056's key rule
	ExpectRev *int   `json:"expect_rev"`  // rm only; >= 0
}

// LoopMemorySetArgs are loop.memory_set's.
type LoopMemorySetArgs struct {
	Name      string `json:"name"`
	Key       string `json:"key"`
	Value     string `json:"value"`      // <= MaxMemoryValue; may be ""
	ExpectRev *int   `json:"expect_rev"` // >= 0
}

// SnapshotLoop is one loop in the snapshot (ADR 0078): no prompt, script or vars.
type SnapshotLoop struct {
	Name      string          `json:"name"`
	Title     string          `json:"title"`
	Every     string          `json:"every"`
	Paused    bool            `json:"paused"`
	Hidden    bool            `json:"hidden"`
	Runner    string          `json:"runner"`
	NextRunAt *time.Time      `json:"next_run_at"`
	LastRun   json.RawMessage `json:"last_run"`  // ws loop list's last_run, or null
	LastGate  json.RawMessage `json:"last_gate"` // ws loop list's last_gate, or null
}

// NeedsConfirm reports whether op with args needs Confirm (ADR 0078):
// loop.rm, loop.history_clear and loop.memory_clear always; loop.add and
// loop.edit when Script is non-nil and non-empty, or PermissionMode is
// PermissionBypass.
func NeedsConfirm(op string, args any) bool

// HasRedacted reports whether any string in args contains RedactedMarker.
func HasRedacted(args any) bool
```

`LoopName` serves several ops. `Validate` refuses `Confirm: false` for
`loop.rm`, `loop.history_clear` and `loop.memory_clear`. It must know the
op, so `DecodeArgs` calls a new `validateOp(op, args)` after `Validate`.
`validateOp` holds both op-dependent checks: `NeedsConfirm` without
`Confirm` gives `usage` with message "<op> needs confirm", and
`HasRedacted` on `loop.add`, `loop.edit` or `loop.memory_set` gives `usage`
"a value contains [redacted]; retype it or leave it unchanged". A
`ReplyError` can carry `Details`; `ArgsError` can't, so RL7's agent maps the
first refusal to `details {"needs":"confirm"}`.

`loop.edit` needs at least one field besides `Name` and `Confirm`
(`usage` "nothing to change").

### `LookupOp` rows

Every loop row has `Caller: CallerController` and `Since:
ProtocolReviewsLoops`.

| Op | Timeout | Changes |
| --- | --- | --- |
| `loop.run` | 15 min | true |
| `loop.add`, `loop.edit`, `loop.pause`, `loop.resume`, `loop.rm` | 30 s | true |
| every other `loop.*` op | 30 s | false |

### Tests (`loop_test.go`)

- `TestLookupOp` "loop ops are controller ops since 3".
- `TestValidate_loop` is a table with one row per rule. It includes:
  - "a name with a slash is refused";
  - "a script over 16 KiB is refused";
  - "33 vars are refused";
  - "vars over 32 KiB in all are refused";
  - "a var key starting with a digit is refused";
  - "a spec starting with a dash is refused";
  - "edit with nothing to change is refused".
- `TestDecodeArgs_confirm`:
  - "loop.rm without confirm is refused";
  - "loop.add with a script and no confirm is refused";
  - "loop.add with bypassPermissions and no confirm is refused";
  - "loop.add without a script needs no confirm";
  - "loop.edit that removes the script needs no confirm".
- `TestDecodeArgs_redacted`:
  - "a prompt containing [redacted] is refused";
  - "a memory value containing [redacted] is refused";
  - "loop.show ignores the marker".
- `TestSnapshot_loops`:
  - "a snapshot without loops decodes to nil";
  - "an agent's snapshot always has loops".

### Verify

As RL1.

---

## RL3. `internal/redact`: exact values and token patterns (`small`, `sonnet`)

- **Depends on:** nothing. **Parallel:** everything.
- **Security-critical:** the mutation-testing reviewer removes each pattern
  and each env-name rule in turn and confirms that a named subtest fails.

### Files

Create:

- `internal/redact/redact.go`
- `internal/redact/redact_test.go`

Standard library only. `internal/agent/tools/redact.go` stays as it is. It
is the runner's tool output redactor; merging the two is a follow-up.

```go
// Package redact removes secrets from text an environment agent returns (ADR 0078).
package redact

// Marker replaces each secret.
const Marker = "[redacted]"

// minValue is the shortest exact value redacted.
const minValue = 8

// Redactor replaces exact secret values and well-known token patterns.
// Immutable after New; safe for concurrent use.
type Redactor struct { /* values longest first; compiled patterns */ }

// New returns a Redactor for the given exact values (those shorter than 8 bytes are ignored).
func New(values ...string) *Redactor

// SecretValues returns the values to redact from secrets (secrets.env's
// NAME=value pairs) and environ: every secrets value, and every environ
// value whose name is GH_TOKEN, GITHUB_TOKEN or CLAUDE_CODE_OAUTH_TOKEN,
// starts with WS_FORGEJO_TOKEN_ or WS_ENDPOINT_TOKEN_, or ends with
// _TOKEN, _SECRET, _API_KEY or _PASSWORD.
func SecretValues(secrets map[string]string, environ []string) []string

// String returns s with every secret replaced, and how many were.
func (r *Redactor) String(s string) (string, int)

// JSON returns raw with every string value (not keys) redacted, re-encoded
// compactly, and the count. raw must be valid JSON (ErrInvalid otherwise).
func (r *Redactor) JSON(raw []byte) ([]byte, int, error)

// ErrInvalid is returned by JSON for input that isn't JSON.
var ErrInvalid = errors.New("redact: not JSON")
```

The patterns are R3's list, compiled in `New`, not as package variables
(`gochecknoglobals`). The Authorization pattern keeps its prefix group. A
PEM block matches across lines (`(?s)`), lazily up to the first `-----END
… PRIVATE KEY-----`. The exact values are replaced before the patterns.
`JSON` uses `json.Decoder` with `UseNumber`, so numbers keep their text.
Object key order is kept by decoding into an ordered walk: tokens, not
`map[string]any`. The output must keep key order so conformance tests can
compare.

### Tests (`TestRedactor_String`, `TestRedactor_JSON`, `TestSecretValues`, table-driven)

- One subtest per pattern: "a ghp token", "a github_pat token", "an
  sk-ant key", "a glpat token", "a slack token", "an AWS key id", "a PEM
  private key across lines", "an Authorization bearer header keeps its
  prefix".
- "a secrets.env value is redacted wherever it appears".
- "a value shorter than 8 bytes is not redacted".
- "the longer of two overlapping values wins".
- "text without secrets is unchanged and counts 0".
- `JSON`: "keys are not redacted", "nested arrays and objects",
  "numbers keep their text", "key order is kept", "invalid JSON is
  ErrInvalid".
- `SecretValues`: one subtest per name rule, and "PATH is not a secret".

### Verify

```
go test -p 2 -count=1 ./internal/redact/
make lint
```

---

## RL4. `ws loop tick --max-starts`; the agent's loop ticker and snapshot loops in `internal/cli` (`small`)

- **Depends on:** nothing. **Parallel:** everything.
- **Pinned values touched:** constraints-rl.md › Commands and guides
  (`ws-loop-tick.md`).

### Files

Change:

- `internal/cli/loop_tick.go`: the `--max-starts N` flag (default 0,
  meaning no cap; 1..100).
- `loop_tick_test.go`.
- `internal/cli/guides/ws-loop-tick.md`: the flag, and "`ws env agent run`
  uses `--max-starts 2`".
- `internal/cli/env_agent.go`:
  - `agentSnapshots` gains `Loops(ctx) ([]envproto.SnapshotLoop, error)`;
  - new `agentLoops` with `Tick(ctx) (started bool, err error)`.
- `env_agent_test.go`.

**Tick.** In `tick` (`loop_tick.go`), after `max-starts` items have
`action: "started"`, every later due loop is reported with `action:
"waiting"`, and its `next_run_at` is unchanged and not saved. Settling and
pruning still run. With `--dry-run`, `would_start` counts against the cap
the same way.

```go
// agentLoops ticks the agent's loops in a fresh App, as the desktop's
// tick_forever does (ADR 0078).
type agentLoops struct{ newApp func(stdout, stderr io.Writer) *App; root string }

// Tick runs ws --json --config-dir <root> loop tick --quiet --max-starts 2 and
// reports whether any item started or failed (so the snapshot refreshes).
func (l agentLoops) Tick(ctx context.Context) (bool, error)
```

**Snapshot loops.** `agentSnapshots.Loops` runs `loop list` in a fresh App,
the same way `Workspaces` runs `list`, and maps each `loopJSON` to
`envproto.SnapshotLoop`. It keeps at most `MaxSnapshotLoops` by name,
returns `[]` (never nil), and drops the prompt, script and vars.

### Tests

`TestApp_runLoopTick`:

- "max-starts 1 starts one and leaves the next waiting";
- "a waiting loop keeps next_run_at";
- "max-starts 0 starts every due loop";
- "dry-run counts would_start against the cap";
- "max-starts 101 is usage".

`TestAgentSnapshots_Loops`:

- "maps a loop without its prompt, script or vars";
- "no loops is []";
- "keeps 100 by name".

`TestAgentLoops_Tick`: "started is true when an item started".

### Verify

```
go test -p 2 -count=1 -run 'TestApp_runLoopTick|TestAgentSnapshots|TestAgentLoops|TestGuideTopics' ./internal/cli/
make lint
```

---

## RL5. Agent: loop ticking while connected, snapshot `loops`, rate limits (`sonnet`)

- **Depends on:** RL2, RL4 (the `Tick` and `Loops` it calls through
  interfaces), T4 (`agent.go`, `interfaces.go`). **Parallel:** RL6, RL8.
- **Concurrency-heavy and security-critical:** the reviewer runs `-race`,
  removes the "only while connected" check, the one-pass-at-a-time guard
  and each rate limit, and confirms a named test fails for each.
- **Pinned values touched:** constraints-rl.md › `envagent`.

### Files

Create:

- `internal/envagent/loops.go`, `loops_test.go`
- `internal/envagent/limits.go`, `limits_test.go`

Change:

- `interfaces.go`: `snapshotSource` gains `Loops`; add `loopTicker`; append
  `loopTicker` to the moq line and run `go generate ./internal/envagent/`.
- `agent.go`: `Options.LoopTick` (`DefaultOptions` 60 s; 0 means off,
  which the tests use); the ticker is started and stopped by `serve`; the
  snapshot carries `Loops`.
- `ops.go`: `admit` checks the limits after `DecodeArgs`. `remembered`
  gains the read-only `review.*` and `loop.*` ops as `false` (the R1 and
  R2 tables).
- `agent_test.go`, `ops_test.go`.
- `internal/cli/env_agent.go`: pass `agentLoops` to the agent. RL4 made
  `agentSnapshots.Loops`. Add `Agent.SetLoopTicker(t loopTicker)`, set after construction
  so `NewAgent`'s signature doesn't change (T1a adds only an option to `Options`).

```go
// loopTicker runs one pass of the agent's loop scheduler.
type loopTicker interface {
	Tick(ctx context.Context) (changed bool, err error)
}

// SetLoopTicker sets the ticker serve runs every Options.LoopTick while connected.
func (a *Agent) SetLoopTicker(t loopTicker)

// tickLoops runs until ctx ends: wait LoopTick, then one pass unless one is
// still running, then signalChanged when it changed something. serve starts
// it after the hello and cancels it when the connection ends.
func (a *Agent) tickLoops(ctx context.Context)

// limiter holds ADR 0078's token buckets.
type limiter struct { /* mu; buckets by key; now func() time.Time */ }

// allow reports whether op with args may run now, and if not, how long until it may.
func (l *limiter) allow(op string, args any) (ok bool, retry time.Duration)
```

**Ticking:**

- The ticker runs on its own goroutine, never on the session loop.
- `serve`'s context ends the ticker when the connection ends: going offline
  stops ticking.
- A pass that is running when the connection ends **is not cancelled**.
  Its context is the agent's run context, not the session's, so a half
  started run isn't killed. No new pass starts until the next connection
  plus `LoopTick`.
- An `atomic.Bool` makes passes one at a time.
- An error is logged with `WarnContext` and doesn't stop the ticker.

**Limits:**

| Key | Limit |
| --- | --- |
| `review.start` | 12 per hour |
| `loop.run` | 30 per hour in all, plus `loop.run:<name>` 1 per 60 s |
| `loop.add`, `loop.edit` and `loop.rm` together | 60 per hour |

- A refusal is `ReplyError{Code: envproto.CodeOperationLimited, Details:
  {"op", "retry_after_seconds"}}`. Add `const CodeOperationLimited =
  "operation_limited"` to `envproto` here.
- The limit is checked in `admit`, so a refused request takes no slot.
- A duplicate request id (dedupe) doesn't consume a token: check
  `requests` first.

**Snapshot.**

- `refreshSnapshot` reads `Loops`. A failure is logged and publishes `[]`,
  as `snapshotAgents` does.
- Changed `loops` alone triggers a publish.
- The loops are redacted with the agent's `Redactor` (RL6 adds
  `Options.Redactor *redact.Redactor`; here use it if non-nil).

### Tests (`t.Run` names; the D-style table RL10 lists them too)

`TestAgent_tickLoops`:

- "ticks every LoopTick while connected";
- "does not tick while offline";
- "the first pass after a reconnect waits LoopTick";
- "a pass still running skips the next timer";
- "a pass that changed something republishes the snapshot";
- "a tick error does not stop the ticker";
- "LoopTick 0 never ticks".

Use a fake clock (`Options.After`) and `loopTickerMock`.

`TestLimiter_allow`:

- "the 13th review.start in an hour is limited";
- "loop.run of one loop twice in 60 s is limited";
- "the 31st loop.run is limited";
- "tokens come back over time";
- "review.status is never limited".

`TestAgent_dispatch`:

- "a limited op answers operation_limited with retry_after_seconds";
- "a duplicate request id is not limited".

`TestAgent_refreshSnapshot`:

- "the snapshot carries loops";
- "a loops read error publishes []";
- "a loop change alone publishes".

### Verify

```
go generate ./internal/envagent/
go test -p 2 -count=1 -race ./internal/envagent/
go test -p 2 -count=1 -run 'TestApp_runEnvAgent|TestAgentSnapshots' ./internal/cli/
make lint
```

---

## RL6. Agent side: `review.*` in-process, pages, redaction (`sonnet`)

- **Depends on:** RL1, RL3, T5 (which edits `env_agent_ops.go`).
  **Parallel:** RL5, RL8.
- **Security-critical:** the reviewer removes the requester `none`, the
  `--` before free text, the id lookup restriction and the redaction call
  in turn, and confirms a named test fails for each.
- **Pinned values touched:** constraints-rl.md › Agent operations.

### Files

Create:

- `internal/cli/env_agent_review.go`
- `internal/cli/env_agent_review_test.go`

Change:

- `env_agent_ops.go`: `Run` sends `review.` ops to `runReview`, and
  `agentOperations` gains `redactor *redact.Redactor`, built in
  `newAgentOperations` from `redact.SecretValues(secrets, a.environ)`, where
  `secrets, warnings, err := config.ReadSecrets(root)`. On an error, log it
  and use the environment alone; warnings are logged;
- `env_agent_ops_test.go`.

```go
// runReview runs a review.* operation with ws review's own command.
func (o *agentOperations) runReview(ctx context.Context, op string, args any) (json.RawMessage, error)

// reviewStartArgv is the ws review start command line for args:
// --json --config-dir <root> review start --requester none [--name N]
// [--pr N] [--profile P]... [--auto P]... <repo>[@<head>]
// [-- [--model=M] [--permission-mode=P] [<focus>]].
func reviewStartArgv(root string, args *envproto.ReviewStartArgs) []string

// reportPage cuts a ws review report --json object to one page of section
// from offset, within envproto.MaxPageBytes, and adds section, offset,
// next_offset and total.
func reportPage(report json.RawMessage, section string, offset int) (json.RawMessage, error)

// finishResult redacts every string in result, adds "redacted", and checks
// the size: over envproto.MaxRequestBytes - 4 KiB is internal "result too large".
func (o *agentOperations) finishResult(result json.RawMessage) (json.RawMessage, error)
```

**`review.status`** runs `review list --json` and picks the run whose `id`
is the argument. If there is none, the reply is `review_not_found`.
`finished` is `reviewFinished(agent)` (`review_report.go:290`, which takes
`*reviewAgentJSON`): true when `agent` is null, or idle or stopped.

**`review.list`** runs `review list --json [--repo R] [--pr N]`, keeps the
first `limit` runs, and adds `"truncated"`.

**`review.report`** runs `review report --json -- <id>`, then `reportPage`:

- It decodes `comments` and `superseded` as `[]json.RawMessage`. Each item
  is redacted **before** it is measured.
- It adds items from `offset` while the page's encoded size stays ≤
  `MaxPageBytes`. The first item always goes in; an item that alone exceeds
  the budget has its `body`, `code` and `suggestions[].code` cut (UTF-8
  safe) and gets `"truncated": true`.
- The other section is `[]`.
- `next_offset` is null when the section is done. An `offset` past the end
  returns an empty page with `next_offset: null`.

**`review.decide`** and **`review.acted`** run `review decide --json`
(`acted`), with flags from typed fields, then `--`, then `<id>
<finding>...`. `--note` is passed as `--note=<text>` (one argv element, so
it can't be read as a flag), and `--commented=<url>` the same way.
`review.profiles` runs `review profiles --json`.

**Every result** goes through `finishResult`. Errors keep `ws`'s codes
(`replyError`). `review_not_found`, `gh_missing`, `gh_auth`,
`pr_not_found`, `profile_unknown` and `finding_not_found` pass through
unchanged.

### Tests (`TestAgentOperations_review`, building `newApp` from `newMockedApp()` as PR 3's T4 does, with a fake review store under `t.TempDir()`)

- "start runs ws review start with requester none".
- "start with head passes repo@head and no --pr".
- "a focus goes after -- and model flags before it".
- "start fails with gh_auth from the remote's gh" (`ghClientMock`).
- "status of a removed review is finished with agent null".
- "status of an unknown id is review_not_found".
- "a report over 192 KiB is paged and next_offset walks it", which builds
  300 comments of 3000 bytes.
- "a single oversized comment is cut and marked truncated".
- "a token in a comment body comes back redacted and counted".
- "a secrets.env value in a finding note is redacted".
- "decide passes the note as one argument".
- "acted needs exactly one of commented and fix_sent" (through
  `DecodeArgs`).
- "a review id is looked up only by id, never by repo and ref" (the argv
  has no `--repo`).
- "the result equals the CLI's JSON plus redacted, for a report that fits
  one page". This is the conformance rule of ADR 0073.

### Verify

```
go test -p 2 -count=1 -run 'TestAgentOperations|TestReviewStartArgv|TestReportPage' ./internal/cli/
make lint
```

---

## RL7. Agent side: `loop.*` in-process, confirm, the scripts switch (`sonnet`)

- **Depends on:** RL2, RL3, RL4, RL6 (`finishResult`, the dispatch).
  **Parallel:** RL8, RL9.
- **Security-critical:** the reviewer removes the confirm check, the
  `agent_loop_scripts` check, the `[redacted]` refusal and the `--file=-`
  use in turn, and confirms a named test fails for each.
- **Pinned values touched:** constraints-rl.md › Agent operations, Config
  key.

### Files

Create:

- `internal/cli/env_agent_loop.go`
- `internal/cli/env_agent_loop_test.go`

Change:

- `env_agent_ops.go`: `Run` sends `loop.` ops to `runLoop`.
- `internal/config/config.go`: `Environments` gains `AgentLoopScripts *bool
  \`toml:"agent_loop_scripts" json:"agent_loop_scripts"\`` and a method
  `LoopScriptsAllowed() bool` (true when nil).
- `internal/config/edit.go`: `environmentRules()` gains the key as `bool`.
- `config_test.go`, `edit_test.go`.
- `internal/cli/guides/ws-config.md` if it lists keys.

```go
// runLoop runs a loop.* operation with ws loop's own command.
func (o *agentOperations) runLoop(ctx context.Context, op string, args any) (json.RawMessage, error)

// loopAddArgv and loopEditArgv build ws loop add|edit from a LoopSpec. The
// prompt and script are passed as --prompt=<text> and --script=<text> (one
// argv element each), each var as --var=<K>=<V>, specs after --.
func loopAddArgv(root string, spec *envproto.LoopSpec) []string
func loopEditArgv(root string, spec *envproto.LoopSpec) []string

// loopPage cuts ws loop list --json to the loops from offset within MaxPageBytes,
// adding offset, next_offset and total. A loop over the budget alone is replaced
// by its SnapshotLoop fields plus "truncated": true.
func loopPage(list json.RawMessage, offset int) (json.RawMessage, error)
```

**What each op runs:**

- `loop.show` runs `loop list --json`, picks one loop, and gives
  `{"loop": {…}, "warnings": […], "redacted": N}`. A missing loop is
  `loop_not_found`.
- `loop.history` runs `loop history --json --limit N -- <name>`.
- `loop.history_run` runs `loop history --json --run SEQ -- <name>`, and
  cuts `report_text` to `MaxReportText` with `"report_truncated"`.
- `loop.memory_list` runs `loop memory list --json -- <name>`, and cuts
  each `value` to `MaxMemoryPreview` with `"value_truncated": true` per
  entry.
- `loop.memory_set` passes the value on **stdin** (`--file=-`): `newApp`
  gets a reader for it. Add `newAppWithStdin(stdin io.Reader, stdout,
  stderr io.Writer)` beside `newApp`, so a value starting with `-` is never
  a flag.
- `loop.rm`, `loop.history_clear` and `loop.memory_clear` only run with
  `Confirm` (already checked by `DecodeArgs`; this task's tests prove it
  end to end).

**Script switch.** When `!cfg.Environments.LoopScriptsAllowed()` and the
spec has a non-empty `Script`, the reply is `usage` "scripts from the
controller are turned off on <name>". A spec that removes the script is
allowed.

**Confirm.** A `DecodeArgs` confirm refusal is replied as `ReplyError{Code:
"usage", Details: {"needs":"confirm"}}`. `agentOperations` doesn't see it,
because `admit` refuses it first. So the mapping is in
`envagent.refusalReply`: a refusal message ending "needs confirm" gets the
details. Add `envproto.ErrNeedsConfirm`, a wrapped sentinel, so the check
is `errors.Is`, not a string match. This is the one envagent change in this
task.

Every result goes through `finishResult` (RL6).

### Tests (`TestAgentOperations_loop`)

- "add runs ws loop add with the prompt as one argument".
- "a prompt starting with -- is not read as a flag".
- "a var value with = and spaces arrives intact".
- "edit with specs [] passes --no-specs".
- "edit with script \"\" passes --no-script".
- "a script is refused when agent_loop_scripts is false".
- "removing a script is allowed when agent_loop_scripts is false".
- "rm without confirm never reaches ws" (the `DecodeArgs` path through
  `envagent`, with the details).
- "memory_set passes the value on stdin".
- "a memory value starting with a dash is stored as is".
- "memory_list previews values to 1 KiB".
- "history_run cuts a 1 MiB report to 160 KiB".
- "a token in a memory value is redacted in memory_get".
- "a loop list over 192 KiB is paged".
- "show of an unknown loop is loop_not_found".
- "run --force passes --force".
- "loop_skipped from the gate keeps its code".

`TestConfig_environments`:

- "agent_loop_scripts defaults to allowed";
- "false forbids".

### Verify

```
go test -p 2 -count=1 -run 'TestAgentOperations|TestLoopAddArgv|TestLoopPage' ./internal/cli/
go test -p 2 -count=1 ./internal/config/ ./internal/envagent/ ./internal/envproto/
make lint
```

---

## RL8. CLI: `--env` on `ws review`, the agent guard, `operation_limited`

- **Depends on:** RL1; T8 (`errors.go`, `llm.go`, `app_test.go`); T10 (the
  `--env` helpers). **Parallel:** RL5, RL6, RL7.
- **Pinned values touched:** constraints-rl.md › Error codes, Commands and
  guides.

### Files

Create:

- `internal/cli/review_env.go`
- `internal/cli/review_env_test.go`

Change:

- `review_start.go`, `review_report.go` (`wait`, `report`),
  `review_list.go`, `review_decide.go` (`decide`, `acted`),
  `review_profiles.go` (the bare list only): an `--env <name>` flag on
  each;
- `errors.go`: `codeOperationLimited errorCode = "operation_limited"` under
  `// From ADR 0078.`, in `exitCode()`'s exit-1 case and in `known()`;
- `errors_test.go`;
- guides: `ws-review-start.md`, `ws-review-wait.md`, `ws-review-report.md`,
  `ws-review-list.md`, `ws-review-decide.md`, `ws-review-acted.md`,
  `ws-review-profiles.md`. Each gets `--env`, "for people and the desktop
  app; refused inside a workspace", and `operation_limited` under Exit codes
  where an op can be limited (start). `ws.md`'s exit-1 row gains
  `operation_limited`.

```go
// reviewEnv returns the environment --env names ("" for local), refusing it
// with usage when the command runs inside a workspace and --requester isn't
// none (R5): "an agent never chooses an environment; run ws review <cmd>
// without --env".
func (a *App) reviewEnv(flag, requesterFlag string) (string, error)

// runRemoteReviewStart sends review.start and prints its result plus
// "environment" (PR 3's D9), with wait_command "ws review wait --json --env <name> <id>".
func (a *App) runRemoteReviewStart(ctx context.Context, env string, opts *reviewStartOptions) error

// remoteReport fetches every page of both sections and returns the whole
// reviewReportJSON (comments, then superseded, each in order).
func (a *App) remoteReport(ctx context.Context, env, id string) (reviewReportJSON, error)

// waitRemoteReview polls review.status every defaultReviewPoll until it is
// finished or the timeout passes (review_timeout as locally), then prints remoteReport.
func (a *App) waitRemoteReview(ctx context.Context, env, id string, timeout time.Duration) error
```

**How each subcommand maps:**

- `start --env`: `--offline` is refused (`usage`: the remote decides). A
  `<spec>@<ref>` without `--pr` must have a 40-hex ref, sent as `head`.
  Anything after `--` is parsed into `model`, `permission_mode` and
  `focus`. Any other claude flag is `usage` "only --model and
  --permission-mode are passed to a remote review".
- `report --env --format markdown|html` renders locally from
  `remoteReport` through the same renderer as a local report. If the
  renderer takes store types rather than `reviewReportJSON`, add a
  conversion in `review_env.go`; check this first, since it is unverified.
- `--repo/--ref` with `--env` is `usage` ("use the review id on an
  environment").
- `decide --env` and `acted --env` map their flags to the typed args.
- `list --env` and `profiles --env` print the agent's object plus
  `"environment"`.

### Tests

- `TestApp_reviewEnv`:
  - "--env inside a workspace is refused";
  - "--env with --requester none inside a workspace is allowed";
  - "--env outside a workspace is allowed".
- `TestApp_runRemoteReviewStart` (`environmentCallerMock`):
  - "sends review.start with typed args";
  - "a claude flag other than model is refused";
  - "--offline with --env is refused";
  - "the result has environment and an --env wait_command".
- `TestApp_remoteReport`: "joins pages in order", "stops when
  next_offset is null".
- `TestApp_waitRemoteReview` (fake clock):
  - "polls status until finished";
  - "times out with review_timeout";
  - "offline is environment_offline".
- `TestErrorCode_exitCode` gains `operation_limited` → 1.
- `TestGuideTopics` passes (every new flag in its guide; the exit-code
  row).

### Verify

```
go generate ./internal/cli/
go test -p 2 -count=1 -run 'TestApp_reviewEnv|TestApp_runRemoteReview|TestApp_remoteReport|TestApp_waitRemoteReview|TestErrorCode|TestGuideTopics|TestApp_newRootCmd' ./internal/cli/
make lint
```

---

## RL9. CLI: `--env` and `--cached` on `ws loop`

- **Depends on:** RL2, RL8 (the shared `printRemote` helper and guide rows),
  T9 (`list_env.go`'s snapshot reader). **Parallel:** RL7.
- **Pinned values touched:** constraints-rl.md › Commands and guides.

### Files

Create:

- `internal/cli/loop_env.go`
- `internal/cli/loop_env_test.go`

Change:

- `loop.go` (`add`, `list`, `pause`, `resume`, `rm`), `loop_edit.go`,
  `loop_tick.go` (`run` only; `tick` gets no `--env`), `loop_history.go`,
  `loop_memory.go` (every memory subcommand): `--env <name>`. `list` also
  gets `--cached`. `rm`, `history --clear` and `memory clear` also get
  `--yes`, which sets `confirm` for a remote. Locally `--yes` is accepted
  and does nothing, so scripts can pass it either way.
- Each `ws-loop-*.md` guide gets the flags.

**How each subcommand maps:**

- `ws loop add|edit --env` with `--script`, or with `--permission-mode
  bypassPermissions`, needs `--yes`, else `usage` "this runs code on <name>
  as its agent's user; pass --yes". The desktop passes `--yes` after its
  dialog (R2).
- A remote `edit` sends only the flags given: unchanged fields stay nil.
- `memory set --env` reads `--file` or stdin locally and sends the value.
- `list --env <name>` gets every page of `loop.list` when the environment
  is online, and adds `"environment"` and `"stale": false` to each loop.
- `list --env <name> --cached` (also the fallback when it is offline)
  reads the snapshot's `loops`. Each loop has `prompt`, `script`,
  `script_timeout`, `vars`, `specs`, `model`, `permission_mode` and
  `endpoint` set to `null`, `"summary": true`, `"environment"`, and
  `"stale"` per tasks-ui.md D3 (offline, or snapshot > 90 s). The top level
  adds `"environment_status"` and `"loops_unknown": true` when the snapshot
  has no `loops` (protocol < 3).
- Without `--env`, everything stays local, as today. `ws loop list` doesn't
  list every environment: a loop is acted on by name, and two machines can
  share a name. This keeps the D9 reasoning.

```go
// runRemoteLoop sends op with args to env and prints the result plus "environment".
func (a *App) runRemoteLoop(ctx context.Context, env, op string, args any) error

// cachedLoops is the snapshot's loops of env as ws loop list --json prints them (R4).
func (a *App) cachedLoops(env string) (loopListEnvJSON, error)

type loopListEnvJSON struct {
	Loops             []json.RawMessage `json:"loops"`
	Environment       string            `json:"environment"`
	EnvironmentStatus string            `json:"environment_status"`
	LoopsUnknown      bool              `json:"loops_unknown"`
	Warnings          []string          `json:"warnings"`
}
```

### Tests

- `TestApp_runLoopEnv`:
  - "add --env sends loop.add with the prompt";
  - "add --env with a script needs --yes";
  - "rm --env without --yes is refused locally";
  - "edit --env sends only the given fields";
  - "memory set --env reads stdin".
- `TestApp_cachedLoops`:
  - "reads the snapshot's loops";
  - "an offline environment is stale";
  - "a protocol-2 snapshot is loops_unknown";
  - "summary loops have null prompts".
- `TestApp_runLoopList`: "--env online pages through loop.list".
- `TestGuideTopics` passes.

### Verify

```
go test -p 2 -count=1 -run 'TestApp_runLoopEnv|TestApp_cachedLoops|TestApp_runLoopList|TestGuideTopics' ./internal/cli/
make lint
```

---

## RL10. Conformance: reviews and loops over the real broker (`sonnet`)

- **Depends on:** RL5, RL7, RL8, RL9, T11 (the conformance file).
  **Parallel:** RL11-RL15.

### Files

Change:

- `internal/mqttbroker/conformance_test.go`: new `TestConformance_reviewsLoops`.
- `internal/cli/env_ops_conformance_test.go`: new subtests of `TestConformance_EnvOps`, using the
  existing controller-broker-agent harness, `newMockedApp` stores and fake
  `claude` and `gh` scripts.

### Subtests (each a named `t.Run`; the R3 table of checks)

| Check | Subtest |
| --- | --- |
| a review op on a protocol-2 agent is refused before sending | "review.start to a protocol 2 agent is environment_outdated" |
| results equal the CLI's JSON plus `environment` and `redacted` | "review list --env equals the remote's ws review list" and "loop list --env equals the remote's ws loop list" |
| a page walk returns the whole report | "a 300-comment report arrives whole through pages" |
| no token crosses the wire | "a ghp token in a comment never appears in any MQTT payload" (record every payload the broker routes and search them) |
| confirm is enforced on the agent even if a controller skips it | "loop.rm sent raw without confirm is refused by the agent" |
| `[redacted]` can't overwrite | "loop.memory_set of a redacted value is refused" |
| scripts switch | "a script is refused when the remote turned scripts off" |
| rate limits | "the 13th review.start in an hour is operation_limited" |
| ticking follows the connection | "a due loop starts on the remote within one LoopTick of connecting" and "no loop starts while the agent is disconnected" |
| no burst | "three overdue loops start two in the first pass and one in the next" |
| snapshot | "the controller's snapshot lists the remote's loops without prompts" |
| agent guard | "ws review start --env from inside a workspace is refused" |
| remote agents use the remote | "ws review start run by a remote workspace's agent creates the review on the remote" (run the remote App's `review start` from inside a remote workspace directory; the controller's store stays empty) |
| no path escape | "a review id with .. is refused" and "a loop name with / is refused" |

`LoopTick` is set to 200 ms in these tests through `Options`.

### Verify

```
go test -p 2 -count=1 -run 'TestConformance' ./internal/mqttbroker/
go test -p 2 -count=1 -run 'TestConformance_EnvOps' ./internal/cli/
go test -p 2 -count=1 -race -run 'TestConformance_reviewsLoops' ./internal/mqttbroker/
make lint
```

---

## RL11. Desktop backend: reviews per environment

- **Depends on:** RL8 (the CLI it runs); T12 (`model.rs`, `commands.rs`,
  `reviews/runs.rs`, `tray/*`, `online/dispatch.rs`); T13 (`lib.rs` and
  `COMMANDS` at 87). **Parallel:** RL10.
- **Changes files tasks-ui.md also changes**: `online/dispatch.rs` (args
  only), `reviews/runs.rs` (T12 made it local only; this task joins remote
  runs on their own environment), `desktop/test/tauri-mock.js`. No new
  command: `generate_handler!` and `COMMANDS` stay at **87**.
- **Pinned values touched:** constraints-rl.md › Desktop.

### Files

Change, each with its `mod tests`:

- `desktop/src-tauri/src/reviews/store.rs`
- `reviews/mod.rs`
- `reviews/checker.rs`
- `reviews/plan.rs`
- `reviews/runs.rs`
- `reviews/report.rs`
- `reviews/view.rs`
- `reviews/fix.rs`
- `reviews/comment.rs`
- `online/dispatch.rs`
- `tray/watcher.rs` (tests only, R5)
- `desktop/test/tauri-mock.js`: a watched repo on `linux-server`, one
  remote review run with a report, and `win-rig` offline.

**`desktop.json`:**

- `RepoWatch` gains `#[serde(default, skip_serializing_if =
  "Option::is_none")] environment: Option<String>`. `None` means local, so
  old files are unchanged on disk.
- `set_watch` keeps it.

**Commands** (new arguments are `Option<String>` with `#[serde(default)]`
in `args!`, so old frontends still decode):

- `reviews_set_watch(repo, watch, auto_review, environment)`.
- `ReviewPlan` gains `environment: Option<String>`. `reviews_plan(repo,
  number, environment)` fills it with the repo's environment when `None`.
  `plan::start_args` puts `--env <name>` after `start` for a remote. For a
  remote plan, it doesn't pass claude flags other than `--model=` and
  `--permission-mode=`.
- `reviews_profiles(environment)` runs `ws review profiles --json [--env
  <name>]`. Profile show, add, edit and remove stay local.
- `review_report(repo, git_ref, refresh, environment, review_id)`: for a
  remote, `ws review report --json --env <name> -- <review_id>`. The cache
  key is `env:<name>:<repo>@<ref>`.
- `review_decide(repo, git_ref, request, environment, review_id)`, and the
  same pair of new arguments on `review_open_report` (`--format html
  --env`), `review_comment_post` (its `ws review acted --commented` step
  gets `--env`), `review_fix_plan`, `review_fix_start` (the fix agent stays
  local, R1) and `review_fix_sent` (`acted --fix-sent --env`).
- A remote call without `review_id` is `bad_args`.

**Checker.**

- Each cycle gathers `envs`: the set of non-local environments named by
  watched repos.
- For each one that is online (from the listing's
  `environment_status`), it runs `ws review list --json --env <name>`. One
  failing environment keeps its previous runs, marked `stale: true`, and
  never fails the cycle.
- `runs::merge` joins a run to a pull request only when the run's
  environment equals the repo's.
- `auto_review` skips a repo whose environment is offline. It also skips
  one whose hello is `claude: missing`; read it from
  `ws env list --json`, which T13's `environments_list` already runs. Call
  it once per cycle only when `envs` isn't empty.
- `ReviewRun` (view) gains `environment: String` and `stale: bool`
  (`camelCase`).

### Tests

- `store.rs`:
  - `repo_watch_environment_round_trips`;
  - `old_desktop_json_has_no_environment`.
- `plan.rs`:
  - `remote_start_args_put_env_after_start`;
  - `remote_start_args_drop_other_claude_flags`.
- `checker.rs`:
  - `remote_runs_join_only_their_environment`;
  - `offline_environment_keeps_last_runs_stale`;
  - `auto_review_skips_offline_environment`;
  - `no_env_list_call_without_remote_repos`.
- `report.rs`:
  - `remote_report_args_use_env_and_id`;
  - `remote_cache_key_has_environment`.
- `comment.rs`: `acted_after_post_uses_env`.
- `fix.rs`: `fix_agent_starts_local_for_remote_review`.
- `online/dispatch.rs`:
  - `reviews_set_watch_accepts_environment`;
  - `review_report_without_environment_is_local`.
- `tray/watcher.rs` (R5):
  - `remote_blocked_loop_run_notifies`;
  - `offline_remote_loop_run_does_not_notify`;
  - `remote_finished_review_notifies_done`.
- `dispatch::commands_match_generate_handler` and
  `every_command_is_handled` still pass with 87.

### Verify

As tasks-ui.md T12 (the `online` feature commands first; the `desktop`
feature ones unverified in a pod without GUI libraries):

```
cd desktop/src-tauri && cargo test -j 2 --no-default-features --features online
cd desktop/src-tauri && cargo clippy --no-default-features --features online --all-targets -- -D warnings
cd desktop/src-tauri && cargo test -j 2
cd desktop/src-tauri && cargo clippy --all-targets -- -D warnings
cd desktop/src-tauri && cargo fmt --check
```

---

## RL12. Desktop backend: loops per environment (`small`)

- **Depends on:** RL9; RL11 (both edit `online/dispatch.rs`); T12.
  **Parallel:** RL10.
- **Changes files tasks-ui.md also changes**: `online/dispatch.rs` (args),
  `desktop/test/tauri-mock.js`. No new command (87 stays).
- **Pinned values touched:** constraints-rl.md › Desktop.

### Files

Change: `desktop/src-tauri/src/loops.rs` with its `mod tests`,
`online/dispatch.rs`, `desktop/test/tauri-mock.js` (loops on `linux-server`
from a snapshot, one with a script; `win-rig` loops stale).

**Arguments.** Every `loops_*` command gains `environment: Option<String>`,
and the pure `*_args` builders put `--env <name>` right after the
subcommand for a non-local one:

- `loops_list(environment)` runs `ws loop list --json --env <name>
  --cached` for a remote. A list is cheap and works offline (R4).
- `loops_add` and `loops_edit` take `NewLoop`, which gains `environment:
  Option<String>` and `confirmed: bool`. `--yes` is passed only when
  `confirmed`. For a remote edit, `edit_args` sends only the fields that
  differ from the loop's previous values: `NewLoop` gains `previous:
  Option<Box<NewLoop>>`, and a field equal to its previous value is left
  out. This is R3's round-trip rule.
- `loops_remove`, `loops_history_clear` and `loops_memory_clear` gain
  `confirmed: bool` and pass `--yes`. The UI has always confirmed these;
  now the flag says so.
- `loops_set_paused`, `loops_run_now`, `loops_history`,
  `loops_history_run`, `loops_memory`, `loops_memory_set` and
  `loops_memory_remove` gain only `environment`.

**Ticking.** `tick_forever` is unchanged and ticks only local loops (the
remote ticks itself, R2).

`Loop` (serde) gains `#[serde(default)] environment: Option<String>`,
`stale: bool`, `summary: bool`. `Loops` gains `environment_status:
Option<String>` and `loops_unknown: bool`.

### Tests

- `remote_list_args_are_cached`.
- `add_args_with_env_and_confirm_pass_yes`.
- `add_args_without_confirm_have_no_yes`.
- `remote_edit_args_send_only_changed_fields`.
- `local_edit_args_are_unchanged` (the existing `edit_args` cases keep
  their argv).
- `remove_args_with_env_pass_yes`.
- `memory_set_with_env_keeps_stdin`.
- `tick_forever_args_have_no_env`.
- `dispatch`: `loops_list_accepts_environment`,
  `loops_list_without_environment_is_local`.

### Verify

As RL11.

---

## RL13. Frontend: types and slices for reviews and loops per environment

- **Depends on:** RL11, RL12, T14 (`api.ts`, `store.ts`, `fixtures.ts`,
  `environmentsSlice`). **Parallel:** RL10.
- **Changes files tasks-ui.md also changes**: `src/api.ts` (after T14's
  additions), `src/test/fixtures.ts`.
- **Pinned values touched:** constraints-rl.md › Frontend.

### Files

Change:

- `desktop/src/api.ts`
- `src/test/fixtures.ts` (`reviewRun()` and `loop()` gain `environment:
  "local"`, `stale: false`, `summary: false`)
- `features/reviews/reviewsSlice.ts`, `reportsSlice.ts`, `actionsSlice.ts`,
  and their tests
- `features/loops/loopsSlice.ts`, `loopDataSlice.ts`, and their tests

```ts
// api.ts
// RepoWatch gains: readonly environment?: string   (absent = "local")
// ReviewPlan gains: readonly environment?: string
// ReviewRun gains: readonly environment: string; readonly stale: boolean
// Loop gains: readonly environment: string; readonly stale: boolean; readonly summary: boolean
// Loops gains: readonly environmentStatus: EnvironmentStatus | null; readonly loopsUnknown: boolean
// NewLoop gains: readonly environment?: string; readonly confirmed?: boolean; readonly previous?: NewLoop
// api: reviewsSetWatch(repo, watch, autoReview, environment = "local"),
//   reviewsPlan(repo, number, environment?), reviewsProfiles(environment = "local"),
//   reviewReport(repo, ref, refresh, environment = "local", reviewId?),
//   reviewDecide(repo, ref, request, environment = "local", reviewId?), and the same
//   two trailing parameters on reviewOpenReport, reviewCommentPost, reviewFixPlan,
//   reviewFixStart, reviewFixSent;
//   every loops* wrapper gains a trailing environment = "local";
//   loopsRemove, loopsHistoryClear, loopsMemoryClear gain confirmed = false before it.
```

**Slices:**

- `reportsSlice`: `reportKey(repo, ref, environment)` is `${repo}@${ref}`
  for local and `env:${environment}:${repo}@${ref}` otherwise.
- `loopsSlice` holds loops **by environment**: `byEnvironment: Record<string,
  { items: readonly Loop[]; loadedAt: number | null; error: string | null }>`.
- `fetchLoops` fetches `local` and each enrolled environment from
  `environmentsSlice`, in parallel. One failing environment keeps its last
  items and sets its `error`.
- `loopDataSlice` keys history and memory by `${environment}/${name}`.
- Selectors: `selectLoopGroups(state)`, which is `local` first, then
  enrolled environments by name, each `{environment, online, reason, stale,
  loopsUnknown, items}`; and `selectReviewEnvironment(state, repo)`.

### Tests

- Reducer tests per case.
- "a remote report has its own key".
- "a failing environment keeps its last loops".
- "groups put local first".
- "an offline group carries its reason" (uses T14's `offlineReason`).
- `npm run typecheck` passes with the new fields across every fixture.

### Verify

```
cd desktop && npm ci
cd desktop && npm run typecheck
cd desktop && npx vitest run --maxWorkers=2 --testTimeout=60000 src/features/reviews src/features/loops
cd desktop && npm test -- --maxWorkers=2 --testTimeout=60000
```

---

## RL14. Reviews screen: environment, badges, offline

- **Depends on:** RL13, T16 (`EnvironmentField`), T17 (`EnvBadge`,
  `remote.ts`). **Parallel:** RL15.
- **Changes files tasks-ui.md also changes**: none of T15-T17's. It uses
  `EnvBadge` and `EnvironmentField` as they are.
- **Pinned values touched:** constraints-rl.md › Frontend.

### Files

Change, in `desktop/src/features/reviews/`:

- `WatchedReposDialog.tsx`: an Environment select per repo (offline ones
  selectable with "(offline)"), hidden when no environment is enrolled.
- `StartReviewDialog.tsx`: `EnvironmentField` preselected with the repo's
  environment; profiles reload from `reviewsProfiles(environment)` on
  change; Start disabled with the reason when offline or `claude:
  missing`.
- `ReviewCard.tsx`, `RepoReviewsCard.tsx`: `EnvBadge` on a remote run;
  dimmed with the reason when offline or stale.
- `ReportView.tsx`, `FindingRow.tsx`: actions disabled with the reason
  offline; "as of 14:02" on a kept report; a truncated finding shows "This
  finding was cut to fit; open the full report on linux-server".
- `CommentDialog.tsx`, `FixDialog.tsx`: pass `environment` and `reviewId`.
  Fix says "The fix agent runs on this machine".
- Settings › Reviews profiles card (`features/settings/…`, the file that
  lists profiles): the title becomes "Profiles on this machine" when any
  environment is enrolled.
- Tests beside each.

### Tests

- "the watched repo dialog sets an environment".
- "start review preselects the repo's environment".
- "changing the environment reloads its profiles".
- "start is disabled with the reason when the environment is offline".
- "a remote run shows its environment badge".
- "an offline remote run is dimmed and its report actions are disabled".
- "a kept report shows as of".
- "decide on a remote report passes environment and review id".
- "fix of a remote review says it runs on this machine".
- "with no environment enrolled nothing changes", which renders the
  screen with the existing fixtures and the same queries as before.

### Verify

```
cd desktop && npm run typecheck
cd desktop && npx vitest run --maxWorkers=2 --testTimeout=60000 src/features/reviews src/features/settings
cd desktop && npm test -- --maxWorkers=2 --testTimeout=60000
```

---

## RL15. Loops screen: groups, environment field, script confirmation

- **Depends on:** RL13, T16. **Parallel:** RL14.
- **Pinned values touched:** constraints-rl.md › Frontend.

### Files

Create, in `desktop/src/features/loops/`:

- `LoopGroup.tsx`, `LoopGroup.module.css`
- `ConfirmRemoteCodeDialog.tsx`, `ConfirmRemoteCodeDialog.module.css`,
  `ConfirmRemoteCodeDialog.test.tsx`

Change:

- `LoopsList.tsx`: groups from `selectLoopGroups`, with a filter chip row
  when an environment is enrolled (reuse T17's `EnvFilter` if it takes a
  list of names; else a local chip row).
- `LoopsPane.tsx`, `LoopForm.tsx`: `EnvironmentField` on New, read-only on
  Edit. The confirm flow: submitting a remote loop whose script is new or
  changed, or whose permission mode is `bypassPermissions`, opens
  `ConfirmRemoteCodeDialog`, then sends `confirmed: true` and `previous`.
- `RunDialog.tsx`, `HistoryCard.tsx`, `MemoryCard.tsx`, `MemoryDialog.tsx`:
  pass `environment`. A value containing `[redacted]` is read-only with
  R3's note. A truncated preview loads the full value on open
  (`loop.memory_get`).
- `LoopsActions.tsx`: Delete, Clear history and Clear memory pass
  `confirmed: true` after their existing confirm dialogs.
- Tests beside each.

**`ConfirmRemoteCodeDialog`.** The title is "Run code on linux-server?".
The body is: "This script runs as the agent's user on linux-server every
1h, outside Claude Code's permissions. Anyone who can use this app can run
code there." For a bypass mode it is "This loop's agent runs on
linux-server with every permission (bypassPermissions)". The script is
shown read-only, monospace, scrollable. The buttons are **Run on
linux-server** and Cancel; Cancel is the default focus. A local loop never
shows it.

### Tests

- "loops are grouped by environment with local first".
- "an offline group is dimmed with its reason and actions disabled".
- "a protocol 2 environment says loops can't run from here".
- "new loop asks for the environment".
- "edit shows the environment read-only".
- "a new script on a remote loop asks for confirmation".
- "cancelling the confirmation sends nothing".
- "confirming sends confirmed and only changed fields".
- "a local loop with a script never asks".
- "a redacted memory value is read-only".
- "with no environment enrolled the list is unchanged".

### Verify

```
cd desktop && npm run typecheck
cd desktop && npx vitest run --maxWorkers=2 --testTimeout=60000 src/features/loops
cd desktop && npm test -- --maxWorkers=2 --testTimeout=60000
```

---

## RL16. ADR 0078, amendments, docs

- **Depends on:** RL10, RL14, RL15, T18 (the same ADRs and docs; this task
  edits after it). Also the screenshots below, which the orchestrator's
  child commits first. **Parallel:** none.
- **Pinned values touched:** constraints-rl.md › Docs with lists.

### Files

Create `docs/adrs/0078-reviews-and-loops-on-environments.md` (the template
in `docs/adrs/template.md`). It holds R1-R6 as accepted, with the redaction
rule's known miss and the trust statement of R2.

Change:

| File | Change |
| --- | --- |
| `docs/adrs/README.md` | the 0078 row |
| `0073-environment-agent-is-ws.md` | the operations table rows and allow-list sentence (R7) |
| `0072-remote-environments.md` | Choosing it, the agent guard |
| `0076-environment-platforms-and-phasing.md` | stack row 4 |
| `0016-output-contract.md` Status | `operation_limited` |
| `0048-agent-loop-scheduler.md` | decision 7, `--max-starts`: an Amendments section |
| `0060-review-filters-and-forges.md` | starting on the repo's environment |
| `docs/tools/ws-env.md` | new sections "Reviews on an environment" (R1's checklist items 1-4, "no token crosses the network", what stays on the controller) and "Loops on an environment" (the agent ticks while connected; offline means paused with no catch-up; `--max-starts 2`; don't also run another ticker; the trust statement; `agent_loop_scripts`; redaction and `[redacted]`); protocol 3 |
| `docs/tools/ws-review.md`, `docs/tools/ws-loop.md` | `--env`, `--cached`, `--yes`, the snapshot summary shape |
| `docs/state.md` | the snapshot's `loops`; nothing new on the controller's disk |
| `desktop/README.md` | Reviews and Loops bullets: per environment, with the screenshots below |
| `docs/online.md` | the Loops screenshot |
| `docs/design/remote-environments/README.md` | link this file and the Loops-by-environment wireframe |
| `internal/cli/guides/ws.md` | a remote environments paragraph: `ws review --env`, `ws loop --env`, for people |

Record follow-ups:

- Fix and Ask on the environment;
- remote profile editing;
- redacting `agent.logs`;
- merging `internal/redact` with `internal/agent/tools`'s redactor;
- `cache.*`;
- a hello field for `gh` sign-in so the UI can warn before a start;
- the daemon hosting local ticks (ADR 0068).

### Screenshots (the orchestrator's Sonnet child, after RL14 and RL15, by tasks-ui.md D7's method)

Use the same Vite and `tauri-mock.js` method. RL11 and RL12 added the mock
data. Write exactly these files under `desktop/docs/`:

| File | Screen |
| --- | --- |
| `reviews-start-environment.png` | Start review dialog with the Environment field open, `win-rig` disabled, profiles of `linux-server` |
| `reviews-remote-runs.png` | Reviews list with a `linux-server` run badge and a dimmed `win-rig` run with its reason |
| `reviews-remote-report.png` | the findings view of a remote review, one finding marked truncated |
| `loops-environments.png` | Loops list grouped: local, `linux-server` online, `win-rig` offline and dimmed with the no-catch-up note |
| `loop-new-environment.png` | New loop with the Environment field |
| `loop-confirm-script.png` | `ConfirmRemoteCodeDialog` with a script |
| `loops-environments-mobile.png` | the grouped list at 360 px (Workspace Online) |

Layout checks to report by name:

- the confirm dialog's script box scrolls and doesn't clip its buttons at
  360 px;
- group headers don't wrap their dots;
- badges don't push the run status off-screen;
- tap targets are ≥ 40 px at 360 px.

### Verify

```
go test -p 2 -count=1 -run 'TestGuideTopics' ./internal/cli/
make lint
grep -n "review\.\*\|loop\.\*\|0078" docs/adrs/0073-environment-agent-is-ws.md docs/adrs/0076-environment-platforms-and-phasing.md
grep -n "operation_limited" docs/adrs/0016-output-contract.md internal/cli/guides/ws.md
grep -rn "reviews-start-environment.png\|loops-environments.png\|loop-confirm-script.png" desktop/README.md docs/online.md
ls desktop/docs/reviews-start-environment.png desktop/docs/reviews-remote-runs.png desktop/docs/reviews-remote-report.png desktop/docs/loops-environments.png desktop/docs/loop-new-environment.png desktop/docs/loop-confirm-script.png desktop/docs/loops-environments-mobile.png
```
