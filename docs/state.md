# Workspace state

How `ws` records workspaces, which locks it takes, and when it removes
them. This page describes the current design; the decisions behind each part
are in the ADRs it links. When a decision changes, this page changes with
it.

## Layout

Under `CONFIG_DIR` ([config](config.md)):

| Path | What it is | Written |
| --- | --- | --- |
| `workspaces/<id>/` | the workspace: the user's repos and files only | moved into place by creation |
| `workspaces/CLAUDE.md` | tells agents to run `ws llm` ([0014](adrs/0014-command-set.md)) | when `workspaces/` is first created, if missing, through a temp file linked into place; never overwritten |
| `state/<id>/workspace.toml` | the workspace record ([0012](adrs/0012-workspace-layout.md)) | by creation; its `name` rewritten by `ws rename` ([0036](adrs/0036-workspace-id-suffix-and-rename.md)), its `hidden` by `ws hide` and `ws unhide` ([0043](adrs/0043-hidden-workspaces.md)); its `parent` cleared by `ws rm --keep-children` on its parent ([0044](adrs/0044-workspace-trees.md)) |
| `state/<id>/session` | the session record ([0023](adrs/0023-shell-session-keeps-creation-lock.md)) | once, when the session is known |
| `state/<id>/runner/` | a runner session's files: `run.toml`, `status.json`, `transcript.jsonl`, `runner.log`, `stop`, and for a run without a parent `report.md` and `usage.json` ([0063](adrs/0063-runner-children-are-supervised-sessions.md), [0065](adrs/0065-runner-agent-loop-tools-and-budgets.md)) | `run.toml` by the spawn; `status.json` and `transcript.jsonl` by `ws runner run`; `run.toml`'s endpoint history appended by the runner only; `stop` when a run is asked to stop; `report.md` and `usage.json` at a run's end; kept until `ws rm` |
| `state/<id>/citations.json` | the desktop's transcript scan of the workspace: its cursor (transcript path, offset) and the newest 4096 (message, artifact) pairs already recorded ([0084](adrs/0084-bidirectional-artifact-links.md)); absent until the first scan | by `ws artifact scan record`, under `state/<id>/citations.lock`, only while `workspace.toml` exists; deleted with `state/<id>/` |
| `state/<id>/lock` | the workspace lock | by creation |
| `daemon/` under `CONFIG_DIR`: `lock`, `daemon.json` and `daemon.log`, the [ws daemon](tools/ws-daemon.md)'s files ([0068](adrs/0068-ws-daemon.md)) | `lock` while a daemon runs; `daemon.json` rewritten at its start; `daemon.log` by the daemon, and by an auto-started daemon's stderr; kept |
| `secrets.env` under `CONFIG_DIR`: runner endpoint tokens, one `NAME=value` per line, mode 0600 ([0066](adrs/0066-runner-security-boundaries.md)) | by its owner, by hand; read by the daemon and by each runner, and never printed |
| `state/<id>/review.toml` | a review workspace's record: repo, commit, profiles, requester, and pull request ([0039](adrs/0039-ws-review-command.md)) | by `ws review start`, before the workspace is moved into place; read by `ws review`, and shown as `review` by `ws list --json` |
| `reviews/<host>/<owner>/<name>/<sha>/` | submitted reviews and the decisions on their findings; see [Reviews](#reviews) | by `ws review submit`, `decide`, and `acted`; outlives the workspace; removed by `ws cache prune` once the repo isn't cached |
| `state/<root>/machine/<child>/` | records of `ws machine` children, kept under the **root** of their tree, so a child's outlive its own workspace and go with the root: `spawn.toml` (role, model, verdict lists, vars, ancestors), `result.toml` (verdict, ancestors, time), `report.md` ([0050](adrs/0050-machine-wait-results-and-roles.md)), `progress.toml` (name, total, done, note, time; [0077](adrs/0077-agent-progress-reporting.md)) and, for a runner child, `usage.json` ([0065](adrs/0065-runner-agent-loop-tools-and-budgets.md)) | `spawn.toml` by `ws machine spawn --role` once the child started; `result.toml` and `report.md` by the child's `ws machine result` or its runner, replaced when it runs again; `progress.toml` by the workspace's own `ws machine progress`; `usage.json` by a runner at its exit; read by `ws machine wait`, `children` and `progress show`; deleted with `state/<root>/` |
| `state/<id>/trust` | the Claude Code trust `ws claude` added for a one-repo workspace's repo ([0028](adrs/0028-single-repo-session-directory.md)) | once, before the trust is written; read by every removal, which takes the trust back |
| `state/<id>/building/` | the workspace while it's being built ([0022](adrs/0022-workspace-record-before-rename.md)) | by creation, then moved |
| `state/.removal-pass` | its modification time is when the removal pass last started ([0026](adrs/0026-removal-pass-timing.md)) | by the removal pass |
| `state/.respawn.lock` | the respawn lock ([0027](adrs/0027-respawn-lock-and-record-versions.md)) | by `ws restore` and `ws restart` |
| `state/.continue.lock` | held by a `ws continue` pass while it sends messages ([0047](adrs/0047-ws-continue-after-usage-limit.md)) | by `ws continue` |
| `loops/<name>.toml` | an agent loop: its prompt, period, schedule, whether its runs are hidden (`hidden`, record version 3; [0057](adrs/0057-loop-visibility-and-blocked-runs.md)), its runner and endpoint (`runner = "ws"` and `endpoint`, record version 4, written only when set, so a Claude loop stays version 3; [0070](adrs/0070-runner-agents-for-people-and-loops.md)), and runs ([0048](adrs/0048-agent-loop-scheduler.md)); in `CONFIG_DIR`, not `state/` | by `ws loop add`, `pause`, `resume`, `run`, and `tick`; removed by `ws loop rm` |
| `loops/.lock` | held by every `ws loop` command that writes a loop, by `ws loop history --clear`, and for the whole of `ws loop tick` and `ws loop run` ([0048](adrs/0048-agent-loop-scheduler.md)); `ws loop memory` doesn't take it ([0056](adrs/0056-loop-history-and-memory.md)) | by `ws loop` |
| `state/.loops/<name>/history.json` | a loop's runs, oldest first, up to 200 ([0056](adrs/0056-loop-history-and-memory.md)) | by `ws loop tick` and `ws loop run`, under `loops/.lock`; emptied by `ws loop history --clear`; deleted by `ws loop rm`, and by `ws loop add` when a leftover one is there |
| `state/.loops/<name>/reports/<seq>.md` | the report of run `<seq>`, copied from the run's `machine/` record when the run settles; kept for the newest 50 runs with a verdict ([0056](adrs/0056-loop-history-and-memory.md)) | by `ws loop tick`; emptied by `ws loop history --clear`; deleted by `ws loop rm` |
| `state/.loops/<name>/memory.json` | a loop's shared memory: each key's value, revision, time and writer ([0056](adrs/0056-loop-history-and-memory.md)) | by `ws loop memory` and `ws machine memory`, under `.memory.lock`; deleted by `ws loop rm` |
| `state/.loops/<name>/.memory.lock` | the memory lock, for memory writes only; never `loops/.lock` ([0056](adrs/0056-loop-history-and-memory.md)) | by the memory writers |
| `state/<id>/loop.toml` | the run mark: the loop a workspace is a run of, and when the run started ([0056](adrs/0056-loop-history-and-memory.md)); it also marks the workspace for `ws list`'s `loop_run` ([0057](adrs/0057-loop-visibility-and-blocked-runs.md)) | by `ws loop tick` and `ws loop run`, after the workspace is created and before its agent starts |
| `environments/ca/` | the controller's CA: `ca.key` (mode 0600) and `ca.crt`, and the broker's `server.key` and `server.crt`; a rotation keeps the new CA in `ca.key.new` and `ca.crt.new` until it's done ([0075](adrs/0075-environment-enrollment-and-certificates.md)) | by `ws env add` when there is no CA; the server pair by the daemon when the CA is made or rotated; by `ws env ca rotate`, and `--finish` removes the old CA |
| `environments/<id>.toml` | a remote environment's record: its state, the secret's hash, and its certificate's serial, expiry and CA ([Remote environments](#remote-environments)) | by `ws env add`, `ws env rotate`, and enrollment; removed by `ws env rm` |
| `environments/revoked` | the serials of revoked certificates, one per line, which the broker refuses | by `ws env rm` and `ws env rotate`; never trimmed |
| `environments/.lock` | held while the environments, the CA or the revocations are written | by the environment store |
| `state/.environments/<name>/snapshot.json` | the last workspace snapshot an agent sent, which `ws env list` counts and `ws list --env` reads ([Remote environments](#remote-environments)) | by the daemon, as snapshots arrive; removed by `ws env rm` |
| `environment/` on an agent: `agent.toml`, `key.pem` (mode 0600), `cert.pem`, `ca.pem` | the agent's identity: its environment, its key and certificate, and the CA certificates it verifies the broker with ([0075](adrs/0075-environment-enrollment-and-certificates.md)) | by `ws env agent enroll`; `cert.pem` by a renewal and `ca.pem` by a CA update in `ws env agent run`; removed by `ws env agent unenroll` |
| `artifacts/<id>/artifact.json` | an artifact's record: name, title, kind, creator, and its revisions ([0078](adrs/0078-artifacts.md)) | by `ws artifact add` and `ws machine artifact add`, and by `put`; written through `artifacts/.tmp/` and renamed into place under `<id>/.lock` |
| `artifacts/<id>/r<n>` | revision `n`'s bytes, never rewritten | by `add` and `put`, through `artifacts/.tmp/` and a rename under `<id>/.lock`; deleted with `<id>/` by `rm` |
| `artifacts/<id>/comments.json` | threads on the artifact, with anchors ([0079](adrs/0079-artifact-comments-and-anchors.md)); absent until the first comment | by the comment commands and by `put` (moved anchors), under `<id>/.lock`; deleted with `<id>/` |
| `artifacts/<id>/links.json` | reads (`ws machine artifact get`) and citations (`ws claude`, `ws machine spawn`, `ws artifact cite`, `ws artifact scan record`) of the artifact, one entry per workspace, action and revision, at most 500 ([0084](adrs/0084-bidirectional-artifact-links.md)); absent until the first | by those commands under `<id>/.lock`, waiting at most 2 s; deleted with `<id>/` |
| `artifacts/<id>/.lock` | exclusive for every write to the artifact; readers take none | by the writers |
| `artifacts/.tmp/` | temporary files; a write deletes leftovers older than 1 h; `rm` renames `<id>/` here before deleting it | by the writers |
| `<workspace>/.ws-artifacts/<id>/r<n>` | a revision an agent asked for with `ws machine artifact get`, copied out of the store | by `ws machine artifact get`, which creates the directory 0700 and the file 0600; with `--out`, the file goes to that directory instead |

- **Workspace contents.** Nothing `ws` writes goes inside
  `workspaces/<id>/`, except `.ws-artifacts/`, which `ws machine artifact
  get` writes when an agent asks, and the `--out` directory an agent names
  with it.
- **Hidden names.** A name in `state/` that starts with `.` is never a
  workspace.
- **Unrecorded directories.** A directory under `workspaces/` without a
  record isn't a workspace, and `ws` never deletes it.

## Records

The records are TOML files with a top-level `version`. They're written
through a temporary file renamed into place, so a reader sees all of a
record or none of it.

The session record holds these fields:
- `kind`: `"shell"`, `"claude"` or `"runner"`;
- `claude_session_id`: empty for a shell and a runner;
- `runner_session_id`: a runner's `rn-` id, empty otherwise;
- `started_at`: RFC 3339, UTC.

A session record with no `version` is version 1. A shell or claude record
is version 1; a runner record is version 2, so a `ws` from before runners
leaves it alone. A version above 2 is refused.

`workspace.toml` is version 1; version 2 when it has `hidden = true`,
so a `ws` from before hidden workspaces leaves a hidden one alone
([0043](adrs/0043-hidden-workspaces.md)); and version 3 when it has
`parent = "<id>"`, the workspace it was started from, so a `ws` from
before trees never removes a parent without its children
([0044](adrs/0044-workspace-trees.md)); and version 4 when it has
`environment = "<name>"`, the environment it runs on, written only for a
remote one, so a `ws` from before environments leaves it alone
([0072](adrs/0072-remote-environments.md)). `hidden`, `parent` and
`environment` are written only when set.

A review workspace also has `review.toml`, version 1
([0039](adrs/0039-ws-review-command.md)):
- `repo`: the reviewed repo's id, `host/owner/name`;
- `ref`: the reviewed commit, a full SHA;
- `requester`: the id of the workspace that asked for the review, or
  empty;
- `started_at`: RFC 3339, UTC;
- `profiles`: one `{name, model, auto}` per profile; `model` is empty for
  the session's, and `auto` is set when the profile joined because the
  review's files matched it;
- `[pr]`, absent for a review of a commit: `number`, `url`, `base`,
  `head_branch`, `cross_repository`.

A `review.toml` from a newer `ws` makes `ws review` fail on that
workspace, naming the file.

It's TOML, like the other records under `state/`, because it's `ws`'s
record of a workspace: written once by `ws`, read by `ws`, and next to
`workspace.toml` and the session record ([0012](adrs/0012-workspace-layout.md),
[0039](adrs/0039-ws-review-command.md)). What crosses `ws`'s boundary is
JSON: the review documents reviewers submit, the stored submissions, and
the record itself wherever a command prints it with `--json`.

A workspace whose record is from a newer `ws` (a version this `ws` doesn't
know), or can't be parsed, is left alone
([0027](adrs/0027-respawn-lock-and-record-versions.md)):
- nothing removes it;
- commands warn about it, naming the file;
- `ws cache prune` removes no commit while it exists.

**Loop history and memory** are JSON, not TOML. `history.json` and
`memory.json` are each `{"version": 1, …}`, written atomically. A file
from a newer `ws` is `loop_invalid`, and `ws` leaves it alone. Limits:
the newest 200 history entries; a report, up to 1 MiB, for the newest 50
of them that have one (older entries keep their verdict and lose the
text); 100 memory keys; 64 KiB per value; 256 KiB across all values. When
a loop is removed, `ws loop rm` deletes its `state/.loops/<name>/`
([0056](adrs/0056-loop-history-and-memory.md)).

## Locks

**The workspace lock** (`state/<id>/lock`) is taken exclusively when
creation makes `state/<id>/`. Who holds it then depends on the kind of
workspace:

- **A shell workspace** holds it for its whole life
  ([0023](adrs/0023-shell-session-keeps-creation-lock.md)).
  - `ws` holds it, and so do the shell and every command the shell runs:
    they get it as descriptor 3.
  - It's released after removal. If `ws` was killed, it's released when
    the shell and its commands have all exited.
- **An agent workspace** holds it until its session record is written
  ([0025](adrs/0025-ws-claude-session-hand-off.md)). `claude` never gets it.
  After that the lock is free, and Claude Code's listing decides whether
  the workspace is live.

**The respawn lock** (`state/.respawn.lock`) is taken exclusively by
`ws restore` and `ws restart`
([0027](adrs/0027-respawn-lock-and-record-versions.md)):

- Each takes it before listing sessions, and holds it until its last
  respawn has shown up. For `ws restart --no-wait`, that's the last idle
  session's respawn.
- A second run waits for it; Ctrl-C stops the wait.
- A `ws restore` dry run, or one that involves no agent workspace, doesn't
  take it. `ws restart` always takes it, since it replaces the background
  service even when no session runs.

**Checking a lock** takes a shared lock for a moment, without waiting and
without creating the file or its directory
([0026](adrs/0026-removal-pass-timing.md)).

**Lock order.** No command waits for one lock while holding another:
- the removal pass, `ws rm`, and creation never take the respawn lock;
- a command holding the respawn lock only checks workspace locks.

## Life of a workspace

| Stage | On disk | Kept because |
| --- | --- | --- |
| Building | `state/<id>/building/`, no `workspace.toml` | workspace lock held |
| Recorded | `workspace.toml`, no `workspaces/<id>/` yet | workspace lock held |
| Handing off | `workspaces/<id>/`, no session record | workspace lock held |
| Live shell | session record `shell` | workspace lock held |
| Live agent | session record `claude` | Claude Code lists the session, or still has its jobs directory |

Creation goes through the stages in order
([0022](adrs/0022-workspace-record-before-rename.md)):
1. Make `state/<id>/` and lock it.
2. Clone the repos into `building/`.
3. Write `workspace.toml`.
4. Rename `building/` to `workspaces/<id>/`.
5. Write the session record.

A workspace stops being kept in one of four ways:

- **Gone**: its session ended.
  - A shell workspace's lock is free.
  - An agent's session isn't listed **and** its jobs directory is missing
    ([0015](adrs/0015-workspace-lifetime-follows-session.md)).
- **Abandoned**: it's in one of the first three stages, its lock is free,
  and `state/<id>/` hasn't changed for a minute. This is left behind by an
  interrupted creation or hand-off.
- **Finished review**: a review workspace whose agent is idle or stopped
  and whose every profile has a current submission, unless `[review]
  remove_finished = false` ([0045](adrs/0045-finished-reviews-are-removed.md)).
  It's removed as `ws rm` removes it.
- **With its parent**: an ancestor is gone, abandoned, or finished, or named in
  `ws rm`. Descendants are removed first, deepest first, each as `ws rm`
  removes it; one that can't be keeps its ancestors until a later pass
  ([0044](adrs/0044-workspace-trees.md)).

A Claude Code session's state comes only from its `pid`, `status` and
`startedAt` ([0024](adrs/0024-claude-code-client.md)):

| State | Meaning |
| --- | --- |
| running | it has a process |
| working | its status is `busy` |
| starting | no process yet, started less than 10 s ago |
| stopped | none of the above |

## Removal

**Leftover cleanup** runs at the start of every command that reads
`CONFIG_DIR`, which is all of them except `ws llm`, `--help` and
`--version`. It removes an abandoned `state/<id>/` that has no
`workspace.toml`, or whose `workspaces/<id>/` doesn't exist.

**The removal pass** runs at the start of the same commands, at most once a
minute; `ws list` runs it every time
([0026](adrs/0026-removal-pass-timing.md)). It:

1. Reads every session record, then lists Claude Code's sessions once, and
   only if a workspace needs it.
2. Removes each gone or abandoned workspace. For an abandoned one it first
   runs `claude rm` on each background session whose working directory is
   exactly that workspace, or its one repo
   ([0025](adrs/0025-ws-claude-session-hand-off.md),
   [0028](adrs/0028-single-repo-session-directory.md)).
3. If the listing fails, keeps agent and abandoned workspaces, with a
   warning.

**Every removal** goes through the deletion guard
([0012](adrs/0012-workspace-layout.md)), then deletes `workspaces/<id>/`,
then takes back the Claude Code trust recorded in `state/<id>/trust`, if
any (a failure is a warning), then deletes `state/<id>/`
([0028](adrs/0028-single-repo-session-directory.md)).
- A workspace another command already removed counts as removed.
- One whose removal fails stays listed, with a warning.

## Reviews

`reviews/` holds submitted reviews, outside any workspace, so they outlive
the review agent ([0039](adrs/0039-ws-review-command.md)). `ws review
report --json` is the contract; this layout isn't.

```
reviews/<host>/<owner>/<name>/<sha>/
  .lock               the directory's lock
  <profile>.json      the profile's current submission
  <profile>.<n>.json  its earlier submissions, n from 1, oldest first
  decisions.json      the decisions on the current findings
  runs/<id>.toml      the record of each review run of the commit, by workspace id
```

`decisions.json`, version 1
([0040](adrs/0040-review-decisions-kept-with-findings.md)), maps a
current finding's `<profile>:<id>` to its decision:

```json
{ "version": 1, "decisions": {
  "security:dropped-context-1": {
    "decision": "fix", "suggestion": 0, "note": "",
    "at": "2026-10-26T18:30:00Z",
    "done": null } } }
```

- `decision`: `comment`, `fix`, or `skip`. `suggestion`: an index into
  the finding's suggestions, or null. `at`: when it was decided.
- `done`: null, or how the decision was acted on, from `ws review acted`:
  `{"commented": "<url>", "at": …}` or `{"fix_sent": "<agent>", "at": …}`.
- No profile can be named `decisions`, so the file is never read as a
  submission. A `decisions.json` of another version makes `ws review
  report`, `decide`, and `acted` fail.

- **Writing.** `ws review submit` takes the directory's lock exclusively,
  renames the profile's current file to the next free `<n>`, and writes
  the new one through a temporary file renamed into place. A resubmission
  also drops the profile's decisions, since its ids may now name other
  findings. `ws review decide` and `acted` hold the same lock while they
  check the findings against the current report and rewrite
  `decisions.json` the same way.
- **Reading.** Readers take the lock shared. A report combines the files,
  with each current finding's decision:
  each profile's current submission, and the earlier ones marked
  superseded. Comment ids are qualified as `<profile>:<id>` for a current
  submission and `#<ordinal>:<id>` otherwise.
- **Runs.** `ws review start` writes the review's record, the same
  `review.toml`, to `runs/<workspace id>.toml` before the agent starts, and
  removes it if the agent doesn't start. `ws review wait`, `report`,
  `decide`, and `list` find a review whose workspace is gone through it
  ([0045](adrs/0045-finished-reviews-are-removed.md)).
- **Removal.** Nothing removes `reviews/` with a workspace. `ws cache
  prune` removes `reviews/<host>/<owner>/<name>/`, every commit of it, for
  each repo that is no longer cached, and then the parent directories
  that leaves empty.

## Remote environments

A remote environment ([0072](adrs/0072-remote-environments.md)) is one
record, `environments/<id>.toml`, with these fields
([0075](adrs/0075-environment-enrollment-and-certificates.md)):

| Field | What it is |
| --- | --- |
| `version`, `id`, `name`, `created_at` | the record's version; the environment's id (`env-` and 8 hex digits); its name; when `ws env add` made it, in RFC 3339 UTC |
| `state` | `pending` from `ws env add` or `ws env rotate` until enrollment, then `enrolled` |
| `secret_hash` | the SHA-256 of the enrollment secret; the secret itself is never stored |
| `enrolled_at`, `cert_serial`, `cert_not_after`, `cert_ca` | when the agent enrolled; the current certificate's serial, expiry and CA fingerprint; empty until enrollment |
| `prior_serials` | up to 5 earlier certificate serials, oldest first, that renewal superseded; cleared by `ws env rotate`; omitted when empty |

`ws env rotate` revokes the current certificate, if there is one, and the
earlier ones in `prior_serials`, and returns the record to `pending` with a
new code. `ws env rm` revokes the same serials and deletes the record and the
snapshot. A `ws` built before `prior_serials` can't read a record that has
one, so upgrade every `ws` that reads environments together. A certificate
superseded before the last 5 isn't revoked, so it stays valid until it
expires. `ws env agent unenroll` on the remote
leaves the record alone, so the environment is listed as offline until
`ws env rm`.

`state/.environments/<name>/snapshot.json` is `{"version", "id", "name",
"received_at", "snapshot"}`: the controller's stamp, `received_at`, and the
agent's `snapshot`, which is `{"at", "protocol", "workspaces", "agents", "loops"}`
([0072](adrs/0072-remote-environments.md)). `at` is when the agent made it;
`protocol` is the operation protocol the agent speaks (0 from an agent older
than PR 3); `workspaces` is its `ws list --json --all` array, without unknown
directories, each with `progress` (its `note` is always `null`) and its `rollup` counting only this machine's children (the controller adds the remote ones);
`agents` is its Claude Code sessions, `[]` from an agent older
than PR 3; `loops` is a summary of its loops (name, title, period, paused,
hidden, runner, next and last run, last gate; at most 100, without prompts,
scripts or variables; absent from an agent older than protocol 3, which means
unknown). Reviews and loops leave nothing else on the controller's disk. A snapshot over 256 KiB isn't published, and its entries go stale. Terminals
([0074](adrs/0074-mqtt-transport-embedded-broker.md)) leave nothing on disk on
the controller: their streams live in memory and end with the terminal, the
connection or the environment's presence. `ws list`'s `agent_state` isn't
stored either: it is read from the snapshot's `agents`, matched by session id.

### Scope and remote child files

- `state/<id>/scope.json`: a workspace's scope (`version`, `environments`,
  `self`, `source`, `parent`). Missing means its own environment only; an
  invalid file is the same, with a warning.
- `environments/scopes.json` and `environments/scopes.lock` under the config
  directory: the controller's ledger of remote workspaces and cross-environment
  children.
- `state/<root>/machine/remote/<env-id>/<child>/`: the copy of a finished child
  on another environment (`result.toml`, `spawn.toml` for a role, `report.md`),
  written once. `ws machine wait` reads it after the child is removed. Its
  `spawn.toml` is reconstructed from the role and the verdict, not the role's real
  file.
- `<12 hex>-agent.sock` beside the daemon socket: how an enrolled machine's
  workspaces reach the controller.

## What each command does

- **`ws shell`**: creates the workspace, records a `shell` session and
  runs the shell under the lock. When the shell exits, it removes the
  workspace ([0023](adrs/0023-shell-session-keeps-creation-lock.md)).
- **`ws review start`**: creates the workspace as `ws claude` does, at
  the reviewed commit, writing `review.toml` after `workspace.toml` and
  before the workspace is moved into place, then starts the review agent.
- **`ws claude`**: creates the workspace and runs `claude --bg`, which
  finishes even if `ws` is interrupted. Then it records a `claude` session
  and releases the lock.
  - If `claude --bg` fails, it removes the workspace.
  - If the record can't be written, it runs `claude rm` on the new session
    and then removes the workspace
    ([0025](adrs/0025-ws-claude-session-hand-off.md)).
- **`ws rm`**: runs the pass first, then handles each workspace named:
  - **a running shell**: refuses with `shell_running`;
  - **one being handed off**: refuses with `workspace_busy`;
  - **an agent whose session Claude Code lists in another directory**
    than the workspace or its one repo:
    refuses with `session_mismatch`;
  - **any other agent**: stops and removes its session, then the
    workspace ([0026](adrs/0026-removal-pass-timing.md)).
- **`ws restore`**: takes the respawn lock, then respawns each stopped
  session that Claude Code lists in its own workspace, one at a time,
  waiting up to 10 s for each to show a process ([ws restore](tools/ws-restore.md)).
- **`ws restart`**: takes the respawn lock, then replaces the background
  service and respawns every background session that has a process, `ws`'s
  or not ([0018](adrs/0018-restart-claude-daemon.md)). After each respawn it
  waits as `ws restore` does. It reads session records only to show each
  session's workspace, and changes no record.
- **`ws list`**: runs the pass, then shows what remains. An agent is
  `stale` when its process started before the background service's
  ([0026](adrs/0026-removal-pass-timing.md)).
