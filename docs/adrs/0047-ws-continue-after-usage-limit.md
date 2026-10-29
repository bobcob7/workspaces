# 0047. `ws continue` tells an agent stopped by a usage limit to carry on once the limit resets, and only while the Mac is fully awake

- Status: Proposed
- Date: 2026-10-28

## Context

### Problem

A Claude subscription has a 5-hour limit and a weekly limit. When an
agent reaches one, its turn ends with "You've hit your 5-hour limit ·
resets 11:30pm" and the session waits for input. Nothing restarts it once
the limit resets: on this machine someone typed "continue" by hand, or an
orchestrating agent sent one with its `SendMessage` tool. An agent that
stops at 2 a.m. waits until someone notices. How should `ws` notice that
an agent stopped at a limit, and carry it on once the limit resets, given
that a Mac spends most of the night asleep and wakes briefly on its own
("dark wakes")?

### What a solution needs

1. **Never wrong about the limit.** Continue only sessions whose turn
   really ended at a usage limit, and not before it resets. A continue
   sent early just hits the limit again.
2. **Never during a dark wake.** A turn started in a dark wake stops when
   the Mac goes back to sleep, about 45 s later.
3. **Bounded.** If a continue doesn't work, stop after a few tries rather
   than send one every minute.
4. **Safe to run on a timer**, from launchd, a terminal, or the desktop
   app, even with several copies running at once.
5. **Within [0024](0024-claude-code-client.md)'s client**: Claude Code
   runs, and its files are read, only through `internal/claude`.

### Relevant facts

**How a limit appears.** A turn that reaches a limit ends with a
synthetic assistant entry in the session's transcript,
`projects/<dir>/<sessionId>.jsonl`. It has `apiError:
"usage_limit_reached"`, `error: "rate_limit"`, `apiErrorStatus: 429`,
and `apiErrorParams.rate_limit_info` with `resetsAt` (Unix seconds) and
`rateLimitType` `five_hour` or `seven_day`[^bench-transcripts]. The
displayed text has changed between releases ("session limit", "5-hour
limit", "weekly limit"), and the reset time in it is local and has no
date. The session keeps its process, `status: "idle"`, and Claude Code's
`state` becomes `blocked`, with the text as its detail[^bench-jobs].

**Claude Code's own auto-continue.** Claude Code 2.1.312 contains a
"quota auto-resume" behind a setting, `autoContinueAtUsageLimit`. It
arms only when the user explicitly set that key and a server-side flag
allows it, only for subscription billing, and only when the reset is
less than 24 hours away (`86400000` ms). It does not fire if the machine
was asleep when the reset passed: it marks the episode `stale`
instead[^bench-binary]. None of the 170 limit entries on this machine was
followed by an automatic continuation; the next message, when there was
one, was a person typing "continue" or a task notification[^bench-after].

**Sending a message to a session.** Claude Code has no command that sends
a prompt to a background session: `claude --help` lists `agents`,
`attach`, `logs`, `respawn`, `rm`, and `stop`, and `SendMessage` is a
tool inside a session, not a CLI command[^bench-cli]. `claude attach
<id>` in a pseudo-terminal, with the text typed and then Enter, delivered
the prompt: the transcript recorded it as a user message with origin
`human`, the session answered, and hanging up the terminal left the
session's process running[^bench-attach]. The desktop app already drives
`claude attach` in a pseudo-terminal ([0029](0029-desktop-app.md)).

**Dark wakes.** With Power Nap on, this Mac's power log shows 750 dark
wakes and 36 full wakes, each dark wake about 45 s long. Dark wakes have
capabilities `[CDNP]`; full wakes have `[CDNVA]`, with video (V) and
audio (A)[^bench-pmset]. `IOPMrootDomain`'s `System Capabilities`
property is a bit mask of the same capabilities: 15 (CPU 1, graphics 2,
audio 4, network 8) in a full wake. `ioreg -r -n IOPMrootDomain -d 1 -a`
prints it as a property list in about 0.01 s[^bench-ioreg]. A timer set
for the reset time may fire during a dark wake, or late after a sleep
(Go's timers are thought not to count time asleep on macOS[^go-sleep]).

**Prior art.** An orchestrating agent on this machine waited for agents
by polling `claude agents --json` every minute, treated `blocked` as
stuck, and then sent "Your usage limit has reset. Please continue…" with
`SendMessage`[^bench-orchestrator].

## Decision

We will add **`ws continue`**, which carries on agents stopped by a
usage limit once it has reset:

```
ws continue [--dry-run] [--watch] [<WORKSPACE>...]
```

1. **Which sessions.** Every agent workspace, or the ones named. A
   session is **limited** when it is `idle` (it has a process and isn't
   busy, [0024](0024-claude-code-client.md)) and the last entry in its
   transcript's main thread is a usage-limit entry: `apiError:
   "usage_limit_reached"` with `resetsAt`. Subagent (sidechain) entries
   and entries that aren't messages are skipped when finding the last
   entry. The displayed text is never parsed. A session that is
   `working`, `starting`, or `stopped` is left alone (`ws restore` brings
   back a stopped one), and so is an idle session whose turn ended any
   other way.
2. **When.** A limited session is **due** once the clock passes
   `resetsAt` plus one minute. The time is read from the wall clock on
   every pass, never from a timer set for the reset.
3. **Only in a full wake.** On macOS, before sending anything, `ws`
   reads `System Capabilities` with `ioreg` and goes on only if the
   graphics bit (2) is set. Otherwise every due session is reported as
   `asleep` and nothing is sent. If `ioreg` fails, nothing is sent. On
   other systems there is no check.
4. **How.** `ws` runs `claude attach <id>` in a pseudo-terminal, waits
   until Claude Code draws its prompt (`❯`) or 10 s pass, types
   `Your usage limit has reset. Continue where you left off.`, presses
   Enter, and waits up to 15 s for the transcript to record the message
   as a user entry. Then it hangs up, which detaches. If the message
   isn't recorded, that workspace fails with `claude_failed`.
5. **Bounded.** The transcript counts the tries. Walking back from the
   last limit entry over limit entries and `ws`'s own message, `ws`
   counts its messages since the session last did anything else. After
   3, the session is `gave_up` and is left alone until someone replies.
   A new try waits at least 5 minutes after the last one.
6. **One at a time.** A pass holds `state/.continue.lock` from before it
   lists sessions until its last message is sent. Another pass waits for
   it, then finds those sessions working. `--dry-run` takes no lock and
   sends nothing.
7. **`--watch`** runs a pass every minute until interrupted, printing a
   line only for the sessions it continued, gave up on, or failed on. It
   prints text only, so `--json` with `--watch` is a `usage` error.
8. **Output.** `--json` prints `{"workspaces": [{"id", "session",
   "state", "limit", "action", "error"}], "warnings"}`. `limit` is `null`
   or `{"type", "resets_at"}`, and `action` is one of `continued`,
   `would_continue`, `waiting`, `asleep`, `gave_up`, or `null`.

Running it on a timer is left to the caller. The command's page gives a
launchd agent that runs `ws continue --quiet` every 5 minutes, and the
desktop app can run a pass on its own refresh.

### Why this option

1. **The limit.** `apiError` and `resetsAt` are structured fields that
   kept their meaning across every Claude Code release on this machine,
   from 2.1.292 to 2.1.312[^bench-transcripts]. The text changed in that
   time. Requiring the session to be idle with the limit entry as its
   last entry means a session that has moved on, or is working, is never
   continued. The extra minute covers clock skew between this machine and
   the API.
2. **Dark wakes.** The graphics bit is clear in a dark wake and set in a
   full one, which is the difference the power log shows between the two
   kinds of wake. Checking it right before sending means a pass that
   launchd starts in a dark wake does nothing, and the next pass after a
   real wake does the work. Reading the wall clock on every pass instead
   of sleeping until the reset avoids timers that fall behind while the
   Mac sleeps.
3. **Bounded.** Counting `ws`'s own messages in the transcript needs no
   state of its own, survives `ws` upgrades, and resets as soon as the
   agent does any work or a person replies. Three tries, at least 5
   minutes apart, cover a reset that the API applies a little late.
4. **Timers.** The lock prevents two passes (say launchd and the desktop
   app) from both sending a message to the same session. A pass with
   nothing due takes as long as `claude agents` and one transcript read
   per idle agent.
5. **The client.** The transcript read and `claude attach` live in
   `internal/claude`. `claude attach` is a documented command, and the
   desktop app already depends on it. Its terminal is one `ws` creates
   and writes to, not `ws`'s own stdin, so a prompt still can't be read
   from `ws`'s input ([0024](0024-claude-code-client.md) decision 6).

Its weak points: typing into a terminal UI depends on the prompt glyph
and on Enter sending the message, which a release could change (the
15 s check turns that into `claude_failed` rather than silence). The
transcript format is undocumented. If someone is attached to the session
at the same moment, the text is typed into their prompt too. A user who
doesn't run `ws continue` on a timer gets nothing automatic.

## Alternatives

### Turn on Claude Code's `autoContinueAtUsageLimit`

`ws claude` would set the setting for its sessions.

**Pros**

- No new command, and Claude Code continues at the exact reset time.

**Cons**

- It's gated by consent and a server-side flag `ws` can't control. It
  never fired in 170 limits here[^bench-after].
- It doesn't fire after the machine slept through the reset, which is the
  common case overnight, and doesn't arm for a weekly limit more than a
  day away[^bench-binary].

### Respawn the session

`claude respawn <id>` instead of sending a message.

**Pros**

- One documented command, already used by `ws restore`.

**Cons**

- A respawned session comes back waiting for input
  ([0015](0015-workspace-lifetime-follows-session.md)), so it doesn't
  carry on by itself.

### Talk to Claude Code's background service directly

Send the message over the service's control socket, as `SendMessage`
presumably does.

**Pros**

- No terminal UI to type into.

**Cons**

- The protocol is undocumented and internal. `attach` is a documented
  command that the desktop app already depends on.

### Wait for the reset in a background `ws` process

Start a detached `ws` that sleeps until `resetsAt` when a limit is seen.

**Pros**

- No timer to set up.

**Cons**

- Its timer may fire late after a sleep[^go-sleep], and the process
  would still need the dark-wake check when it woke.
- One more long-lived process per limited agent, and something has to
  notice the limit first.

### Hold the Mac awake until the reset

Run `caffeinate` until the limit resets.

**Pros**

- The continue can happen at the reset time.

**Cons**

- It keeps a laptop awake, on battery, for up to five hours, or days for
  the weekly limit, against the user's power settings.

## Consequences

- `ws` reads Claude Code transcripts: `type`, `isSidechain`,
  `timestamp`, `message.content`, `apiError`, and
  `apiErrorParams.rate_limit_info.{resetsAt,rateLimitType}`. It finds the
  transcript as the desktop app does: `projects/<cwd with every
  character other than A-Z, a-z, 0-9 replaced by ->/<sessionId>.jsonl`
  under the Claude config directory. Unit tests pin each field with
  sample lines.
- `ws` drives `claude attach` in a pseudo-terminal, using
  `golang.org/x/sys/unix` on macOS and Linux, with no new module. Unit
  tests drive a fake `claude` that draws a prompt and appends to a
  transcript.
- `state/.continue.lock` joins [Workspace state](../state.md).
- [0014](0014-command-set.md)'s status line notes that `ws continue` was
  added; [`docs/tools/ws-continue.md`](../tools/ws-continue.md) and its
  agent guide describe the command.
- Agents shouldn't run `ws continue` on others; its guide says so, and
  `ws machine` doesn't wrap it.
- **Unverified:** that a session stopped at a limit takes the typed
  message the same way the idle session in the test did (no limit could
  be produced on demand); that `System Capabilities` lacks the graphics
  bit during a dark wake (inferred from the power log's capability
  letters, not read during one); and how Claude Code handles a message
  sent before the API has applied the reset (expected: another limit
  entry, which the 3-try bound covers).

## References

[^bench-transcripts]: Read 2026-10-28 on an Apple M4, macOS 26.6.2: every `~/.claude/projects/*/*.jsonl` line with `"usage_limit_reached"`, parsed as JSON. 170 entries in 157 transcripts, written by Claude Code 2.1.292 to 2.1.312; 168 in the main thread, 2 in sidechains; `rateLimitType` `five_hour` 169 times, `seven_day` once. Every entry had `type: "assistant"`, `isApiErrorMessage: true`, `error: "rate_limit"`, `apiErrorStatus: 429`, and a numeric `resetsAt`. Texts seen, digits masked: "You've hit your session limit · resets 9:99pm (America/Chicago)", "You've hit your 9-hour limit · resets 9:99pm (America/Chicago)", "You've hit your weekly limit · resets 9am (America/Chicago)". `resetsAt` was at most 4.8 h after the entry for `five_hour` and 11.0 h for the one `seven_day`.
[^bench-jobs]: Read 2026-10-28, same machine: `~/.claude/jobs/*/timeline.jsonl` entries at limits, such as `{"state":"blocked","detail":"You've hit your 5-hour limit · resets 11:30pm (America/Chicago)",…}`.
[^bench-binary]: Read 2026-10-28 with `strings` on Claude Code 2.1.312 (`~/.local/share/claude/versions/2.1.312`). The `/config` entry "…automatically at usage limit" is backed by `autoContinueAtUsageLimit`, marked `consentGated`. Arming requires `autoContinueKeyPresence`, a flag read through the feature-flag client, `billingType !== "usage_based"`, and `resetsAt*1000 - now <= 86400000`. When the gap between observations is larger than a grace period and the fire time has passed, it sets `sleptThroughReset`, emits `tengu_quota_auto_resume_stale`, and moves to phase `stale` instead of firing.
[^bench-after]: Read 2026-10-28, same machine: for each of the 170 entries in [^bench-transcripts], the next `type: "user"` entry that isn't `isMeta`. 144 had none. 15 were a person (`origin.kind: "human"`) typing a variant of "continue", 1 another message, 7 tool results, and 2 task notifications. None had an auto-continuation origin.
[^bench-cli]: Claude Code CLI 2.1.312, `claude --help`, `claude agents --help`, `claude attach --help`, and `claude respawn --help` (2026-10-28). No command sends a prompt to an existing session. `attach`: "Open the background session in this terminal. ← returns to agent view, Ctrl+Z drops back to your shell. The session keeps running either way."
[^bench-attach]: Measured 2026-10-28, same machine, Claude Code 2.1.312. `claude --bg -n ws-attach-probe --model haiku --settings '{"worktree":{"bgIsolation":"none"}}' </dev/null` in a new directory below the trusted workspace root; 5 s later it had a `pid` and `status: "idle"`. A Python script opened `claude attach <id>` in a pseudo-terminal of 120×40 with `TERM=xterm-256color`, read for 4 s (3877 bytes, including `❯`), wrote "Reply with only the word pong.", waited 0.5 s, wrote `\r`, read for 3 s, and sent SIGHUP. `claude attach` exited 1. The session kept the same `pid`. Its transcript then had a user entry with that text and `origin: {"kind": "human"}`, followed by an assistant entry "pong". The session was removed with `claude rm` afterwards.
[^bench-pmset]: Read 2026-10-28, same machine: `pmset -g` shows `powernap 1`; `pmset -g log` has 731 "DarkWake from Deep Idle [CDNP]", 19 "[CDNPB]", 34 "Wake from Deep Idle [CDNVA]", and 2 "[CDNVAP]" lines; dark wakes are logged as "… Using BATT (Charge:100%) 45 secs".
[^bench-ioreg]: Measured 2026-10-28, same machine, during a full wake: `ioreg -n IOPMrootDomain -r -d1` shows `"System Capabilities" = 15`, `"IOPMUserIsActive" = Yes`, `"Wake Type" = "UserActivity Assertion"`. `ioreg -n IOPMrootDomain -d1 -a -k "System Capabilities"` took 0.008 s. The bit values are IOKit's `kIOPMSystemCapabilityCPU` (1), `Graphics` (2), `Audio` (4), and `Network` (8) in `IOPMPrivate.h`.
[^go-sleep]: Not measured here. Go's monotonic clock on macOS is understood to stop while the machine sleeps, so a timer set for a wall-clock time can fire late after a sleep. The decision doesn't depend on it: it reads the wall clock on every pass.
[^bench-orchestrator]: Read 2026-10-28, same machine: `~/.claude/jobs/0853e7d4/tmp/wait.sh` polls `claude agents --json --all` every 60 s and exits 2 after `state` is `blocked` three times; the session's transcript has `SendMessage` calls such as `{"to": "impl-13-chats-viewer-ui-99fc0c07", "message": "Your usage limit has reset. Please continue step 13 where you left off…"}`.
