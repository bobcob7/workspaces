# 0057. Loop runs are hidden by default and shown again, with one notification, when they are blocked or need attention

- Status: Proposed
- Date: 2026-11-11

## Context

### Problem

Every run of a loop ([0048](0048-agent-loop-scheduler.md)) makes an
ordinary, visible workspace[^create]. A loop that runs every 15 minutes
with `keep = 5` keeps five rows in the Agents list at all times, and each
idle run counts toward "N waiting for you", the rail badge and the window
title[^attention], although most runs finish their job and need nothing.
Hidden workspaces ([0043](0043-hidden-workspaces.md)) solve the noise for
reviews, but a hidden workspace never counts and never notifies[^tracked],
so a run that does need a person (a question, a missing credential, a
judgment call) would go unseen. History ([0056](0056-loop-history-and-memory.md))
records `done` for an idle run with no verdict[^settle], which reads the
same as a run that finished quietly.

How should loop runs stay out of the way by default, and still reach a
person when, and only when, a run is waiting on one?

### What a solution needs

1. **Quiet by default.** A run that finishes with a verdict doesn't show
   in the Agents list or count as waiting.
2. **A waiting run is never missed.** A run that asks for help, or stops
   without saying how it went, shows in the Agents list, counts as
   waiting, and notifies once.
3. **One rule, everywhere.** `ws list`, `ws loop history`, the desktop
   app, the tray and Workspace Online's Web Push agree on which runs are
   waiting, without waiting for the next `ws loop tick`.
4. **Agents stay inside `ws machine`** ([0046](0046-ws-machine.md)): a run
   asks for help with `ws machine result`, nothing else.
5. **Existing loops and records keep working**, and an older `ws` refuses
   a record it can't read rather than rewriting it ([0027](0027-respawn-lock-and-record-versions.md)).

### Relevant facts

- `ws loop tick` and `ws loop run` create a run's workspace with
  `workspace.CreateOptions{Name: "loop-" + name, Specs: …}`; `Hidden`
  is never set[^create].
- A loop run's workspace has no parent, so it is the root of its own
  `ws machine` tree, and `ws machine result` writes its verdict to
  `state/<run>/machine/<run>/result.toml`[^result]. Without a role, any
  verdict is accepted; only `PASS` and `APPROVED` pass, and everything
  else is `fail`[^outcome].
- The loop record rejects unknown keys (`meta.Undecoded()`), and refuses
  versions above `FormatVersion` (2)[^store].
- History is settled only during a tick pass, at most once a minute when
  the app ticks[^tick]; the app lists every 4 seconds[^watch].
- The tray treats a key it hasn't seen before as already announced, so
  an entry that appears (app start, unhide) never notifies[^first-seen].
  Hidden entries aren't tracked at all[^tracked].
- `pruneRuns` removes every run beyond `keep` whose session isn't
  working or starting, oldest first[^prune]. An idle run waiting for an
  answer is not working.

## Decision

We will hide loop runs by default, add a `BLOCKED` verdict for a run's
own agent, infer `needs_attention` for an idle run with no verdict, and
derive both from one pure function that `ws list`, history and the app
all use. A hidden run that is blocked or needs attention is shown as if
it weren't hidden, for as long as that lasts.

1. **Per-loop `hidden`, default true.** A loop record gains
   `hidden = true|false` (TOML), shown as `"hidden"` in every loop JSON.
   There is no global setting. `ws loop add` takes `--hidden[=BOOL]`
   (default true) and `--show` (same as `--hidden=false`); `ws loop edit`
   takes the same two flags. The desktop and web loop form has a switch,
   **Hide runs in Agents**, on by default, and the loop view's Agent card
   shows a **Runs** row. A run's workspace is created with
   `Hidden: loop.RunsHidden()`. Changing the setting affects only runs
   that start afterwards.
2. **Record version 3.** `loop.FormatVersion` becomes 3. The `hidden`
   key is a version 3 key: a version 1 or 2 record that has it is
   refused, as a version 1 record with version 2 keys is today. A record
   without the key (every existing one) reads as hidden, the default.
   `ws loop add` writes version 3 with the key always present. `ws loop
   edit` that sets `hidden` on a version 2 record makes it version 3;
   on a version 1 record it does too when the prompt has no `{{` (a
   literal prompt with no actions renders the same as a template), and
   otherwise is `loop_invalid` asking for `--prompt` as well. Tick keeps
   each record's version. No file is migrated on disk.
3. **`BLOCKED` is a run's own verdict.** `ws machine result BLOCKED
   --file <question.md>` is accepted only from a loop run's root
   workspace (no parent, and `state/<id>/loop.toml` exists), and only
   with a report: the question. Anywhere else, including a run's
   children and any role, it is `result_invalid`. Its outcome is
   `blocked` (`machine.OutcomeBlocked`), a third outcome next to `pass`
   and `fail`. Running `ws machine result` again replaces it, as today.
4. **Attention is derived, never stored.** `loop.AttentionOf(state,
   verdict)`:
   - session `working` or `starting`: none (the run resumed);
   - else verdict `BLOCKED`: `blocked`;
   - else no verdict and session `idle`: `needs_attention`;
   - else none (`PASS`/`FAIL`, or a stopped session with no verdict).

   History settles to the same answer: statuses `blocked` and
   `needs_attention` join the existing ones; `done` now means a run with
   no verdict whose session stopped, or whose workspace was removed while
   it needed attention. A blocked run stays `blocked` when its workspace
   goes, so its question is kept as the run's report. The rules are in
   [reference.md](../design/loop-visibility/reference.md#history-status-rules).
5. **Settling a block.** Only a new verdict ends `blocked`: after a
   person answers, the session works (`running`), and when it next goes
   idle the run is `blocked` again unless the agent recorded `PASS`,
   `FAIL` or a new `BLOCKED`. The prompt guidance tells agents to do
   that. `needs_attention` ends when the session works, stops, or a
   verdict arrives.
6. **Effective visibility is computed, the hidden flag stays.** The
   workspace keeps `hidden = true`; nobody runs `ws unhide`. `ws list
   --json` gives each workspace `loop_run: {"loop", "attention"}` (null
   for other workspaces), computed live from the run mark, the machine
   result and the session state. `ws list` without `-a` lists a hidden
   workspace whose `loop_run.attention` is set, and leaves it out of
   `hidden_count`. The desktop backend sets `Entry.hidden` to `hidden &&
   attention == null` and adds `Entry.loopRun`, so the Agents list's
   groups, "N waiting for you", the rail badge, the window title and the
   tray need no change of their own. An entry with attention whose
   status is `idle` or `done` is shown as `needsInput`, so it always
   counts as waiting. When the run is resolved it drops back into the
   Hidden group by itself.
7. **One notification per run.** The tray watcher, shared by the desktop
   app and Workspace Online, keeps a second, loop-only step: a run whose
   `loopRun.attention` has held for `DWELL_MS` notifies once ("deps-bump
   run is blocked" / "… needs attention"), and its workspace id is never
   notified again while the app runs. The usual needs-input/done
   notification is suppressed for such an entry, so there is never a
   second one. The first listing after the app starts records every
   waiting run as notified without notifying, the rule the tray already
   uses for keys it hasn't seen. Loop notifications follow the existing
   **When an agent needs input** setting; Web Push sends them with the
   tag `loop:<workspace id>`.
8. **Pruning is unchanged.** See Consequences.
9. **Prompt guidance.** `ws llm loop`, `ws llm loop add`, `ws llm loop
   edit` and `ws llm machine result` say how a loop prompt should end:
   record `PASS` or `FAIL` with a report, or, to ask a person, write the
   question to a file and run `ws machine result BLOCKED --file <it>`,
   then stop and wait.

### Why this option

1. **Quiet by default:** runs are created hidden; a run with a verdict
   has no attention and stays in the Hidden group.
2. **Never missed:** a blocked or idle-without-verdict run is shown,
   counted and notified even though its workspace is hidden. The weak
   spot is a run already waiting when the app starts: it's shown and
   counted, but not notified (fact: first-seen keys never notify), and
   a run beyond `keep` can be pruned while waiting (Consequences).
3. **One rule:** `AttentionOf` is pure and lives in `internal/loop`;
   `ws list` evaluates it on every call, so the app sees a block within
   one 4-second listing, not at the next tick. History uses the same
   function when it settles, so the two can disagree only between a
   listing and the next tick.
4. **Inside `ws machine`:** the agent uses `ws machine result`, which it
   already knows; the question rides in the existing report.
5. **Compatible:** existing records read as hidden without being
   rewritten; a record only becomes version 3 when `ws loop add` or an
   edit of `hidden` writes it, and an older `ws` then says "upgrade ws"
   instead of rejecting an unknown key. History needs no version bump:
   statuses and outcomes are open enums that clients must accept[^enums].

## Alternatives

### Tick runs `ws unhide` on a blocked run

The tick un-hides a run when it settles to `blocked` and hides it again
when it resolves.

**Pros**

- No change to `ws list` or the desktop model; the hidden flag is the
  whole truth.

**Cons**

- Up to a minute late (ticks), and nothing happens while no tick runs.
- Writes workspace records from the tick, and must remember which runs
  it un-hid to re-hide only those; a person's own `ws unhide` would be
  undone.

### A global "hide loop runs" setting

**Pros**

- One switch.

**Cons**

- The user decided per-loop only: a loop being developed wants its runs
  visible while others stay quiet.

### Store `hidden` as a version 2 key, or as inverted `show_runs`

**Pros**

- No version bump.

**Cons**

- An older `ws` still refuses the record (unknown key), with a less
  helpful message than "written by a newer ws; upgrade ws".
- `show_runs` would make the TOML and the JSON (`hidden`) disagree.

### Infer `needs_attention` only, no `BLOCKED` verdict

**Pros**

- Nothing for agents to learn.

**Cons**

- Can't tell "I have a question" from "I forgot to record a verdict",
  and the question isn't kept with the run's history.

### Persist which runs were notified

**Pros**

- No missed notification for a run that blocked while the app was
  closed, and no repeat after a restart.

**Cons**

- A new file and writer in the app for a case the Agents list and badge
  already cover; the first-seen rule matches the tray's existing
  behaviour.

## Consequences

- **Loops without a verdict get loud.** A loop whose prompt never runs
  `ws machine result` now has every finished run shown as needing
  attention, and notified. That's intended (decision 4), but existing
  loops should add a verdict step; the guides say how. After upgrading,
  open runs that were `done` settle to `needs_attention` at the next
  tick if their session is idle.
- **Open risk: blocked runs can be pruned.** `pruneRuns` keeps the
  newest `keep` runs and removes older ones whose session isn't working
  or starting. A blocked or needs-attention run is idle, so once `keep`
  newer runs have started it is removed: its workspace and conversation
  go, the history entry closes and stays `blocked` (with the question as
  its report) or becomes `done`, and the Agents row disappears. New runs
  keep starting while one is blocked (`busy` counts only working or
  starting). **Proposal:** in `pruneRuns`, also skip a run whose
  `loop.AttentionOf` is not none, so it is kept beyond `keep` until
  resolved.
- A blocked run whose session is stopped (reboot, `claude stop`) stays
  shown, in the Stopped group, and isn't counted as waiting until
  `ws restore` brings it back.
- A run that becomes blocked while no app is running is shown and
  counted when the app starts, but not notified.
- `BLOCKED` was accepted before from children without a role, as a
  failure; it is now refused outside a loop run's root.
- The Agents header shows no Hide/Unhide for an entry with attention;
  once resolved, it's back in the Hidden group, where Unhide works.
- Checked by: `TestAttentionOf` and `TestEntry_Settle` (every row of the
  rules), `ws list` and `ws machine result` CLI tests, the Rust model
  and tray tests, and the desktop tests listed in
  [tasks.md](../design/loop-visibility/tasks.md).

## References

[^create]: `internal/cli/loop_tick.go`, `startLoopRun`: `a.workspaces.Create(ctx, workspace.CreateOptions{Name: "loop-" + l.Name, Specs: parseSpecs(l.Specs)})`.
[^attention]: `desktop/src/features/agents/status.ts`, `needsAttention`: `!entry.hidden && entry.kind === "agent" && (entry.status === "needsInput" || entry.status === "idle")`.
[^tracked]: `desktop/src-tauri/src/tray/attention.rs`, `is_tracked`: an agent "with no parent, not hidden"; [docs/design/hidden-workspaces](../design/hidden-workspaces/README.md) lists "Notifying about a hidden agent that needs input" as out of scope.
[^settle]: [docs/design/loop-history/reference.md](../design/loop-history/reference.md), "Status rules", rule 6: "`State` is `idle` or `stopped`: `Status = done`".
[^result]: `internal/cli/machine_result.go`, `runMachineResult`: `root := machine.Root(list, caller)` and `store.WriteResult(root, caller, …)`; `internal/cli/loop_tick.go`, `observe`: `Read(e.Workspace, e.Workspace)`.
[^outcome]: `internal/machine/result.go`: `DefaultPass = []string{"PASS", "APPROVED"}`; `OutcomeOf` "is pass when verdict is in spawn's Pass list, or with a nil spawn in DefaultPass; every other verdict is fail".
[^store]: `internal/loop/store.go`, `readLoop`: `if undecoded := meta.Undecoded(); len(undecoded) > 0 { … unknown key … }` and `l.Version > FormatVersion` is "written by a newer ws".
[^tick]: `internal/cli/loop_tick.go`, `tick`: `settleHistory` runs once per loop per pass; `defaultLoopPoll = time.Minute`.
[^watch]: `desktop/src-tauri/src/tray/watcher.rs`: `WATCH_INTERVAL = Duration::from_secs(4)`, "the same as the frontend's poll".
[^first-seen]: [docs/design/tray/README.md](../design/tray/README.md): "Key not seen before (app start, new agent, unhide, `--keep-children`) | record as is, announced: **no notification**".
[^prune]: `internal/cli/loop_tick.go`, `pruneRuns`: "removes the workspaces of l's runs beyond the newest l.Keep, oldest first, except those whose agent is working or starting".
[^enums]: `ws llm`, "Output and exit codes": "Accept enum values and fields you don't know."
