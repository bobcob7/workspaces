# ws continue

Tell agents that stopped at a usage limit to carry on once it resets.

## Synopsis

```
ws continue [OPTIONS] [<WORKSPACE>...]
```

## Description

When an agent reaches Claude's 5-hour or weekly limit, its turn ends with
"You've hit your 5-hour limit · resets 11:30pm" and the session waits for
input. Nothing carries it on after the reset
([0047](../adrs/0047-ws-continue-after-usage-limit.md)).

`ws continue` reads `claude agents --json --all` and, for each agent
workspace whose session is `idle`, the end of its transcript. A session is
**limited** when its last message is Claude Code's usage-limit entry
(`apiError: "usage_limit_reached"`); the entry gives the limit's type and
when it resets. Once that time is a minute past, the session is **due**,
and `ws continue` sends it:

```
Your usage limit has reset. Continue where you left off.
```

It sends it by running `claude attach <id>` in a pseudo-terminal, typing
the message, pressing Enter, and detaching once the transcript records it.
The agent then carries on as if someone had replied.

**Dark wakes.** A Mac with Power Nap wakes for about 45 s at a time while
it sleeps, without the display. A turn started then stops when the Mac
goes back to sleep. On macOS, `ws continue` sends nothing unless the Mac
is fully awake, which it reads from `ioreg` (the graphics bit of
`IOPMrootDomain`'s `System Capabilities`). Due sessions are reported as
`asleep` instead and are continued by the first pass after a real wake.

**Tries.** If an agent hits the limit again right after a continue (as
when the reset is applied late), the next try waits at least 5 minutes.
After 3 tries with nothing else in between, the session is `gave_up` and
left alone until it does something else or someone replies. The tries are
counted from the transcript, so `ws` keeps no state of its own for them.

Without arguments it looks at every agent workspace; with `WORKSPACE`
arguments, only those. Sessions that are `working`, `starting`, or
`stopped`, and idle sessions whose turn ended any other way, are left
alone. A stopped session that hit a limit is continued once
[`ws restore`](ws-restore.md) brings it back.

One pass sends at a time: each holds a lock on `state/.continue.lock`
from before it lists the sessions until its last message is sent, and
another pass waits for it. `--dry-run` takes no lock and sends nothing.

If Claude Code lists the recorded session in a directory other than the
workspace's, `ws continue` touches nothing and fails that item with
`session_mismatch`, as [`ws restore`](ws-restore.md) does.

## Options

| Option      | Description |
| ----------- | ----------- |
| `--dry-run` | Print what would be continued, send nothing |
| `--watch`   | Run a pass every minute until interrupted, printing only what was continued, given up on, or failed. Text only: `--json` with `--watch` is a usage error |
| `--json`    | Print `{"workspaces": [{"id", "session", "state", "limit", "action", "error"}], "warnings"}` |

- `state` is the session as [`ws list`](ws-list.md) shows it: `idle`,
  `working`, `starting`, `stopped`, or `unknown`; `null` when it wasn't
  read.
- `limit` is `null`, or `{"type": "five_hour" | "seven_day" | …,
  "resets_at": "<RFC 3339>"}` for a limited session. Other types may
  appear.
- `action` is what this pass did:

| `action`         | Meaning |
| ---------------- | ------- |
| `continued`      | The message was sent and recorded |
| `would_continue` | `--dry-run`: it would have been sent |
| `waiting`        | Limited, but the limit hasn't reset, or the last try was less than 5 minutes ago |
| `asleep`         | Due, but the Mac isn't fully awake; nothing sent |
| `gave_up`        | 3 tries in a row didn't help; left alone |
| `null`           | Not limited, or not idle |

## Examples

```console
$ ws continue
continued  fix-login-d8efb24f  e8cdf5d8  five_hour limit reset 2026-10-29T04:30:00Z
waiting    docs-pass-0f7c2a19  3c11e0f2  seven_day limit resets 2026-11-01T14:00:00Z

$ ws continue --dry-run --json
```

### Run it every 5 minutes with launchd

Save as `~/Library/LaunchAgents/com.github.bobcob7.ws-continue.plist`,
with the path `which ws` prints and a `PATH` that finds `claude`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.github.bobcob7.ws-continue</string>
  <key>ProgramArguments</key>
  <array><string>/Users/me/go/bin/ws</string><string>continue</string><string>--quiet</string></array>
  <key>EnvironmentVariables</key>
  <dict><key>PATH</key><string>/Users/me/.local/bin:/opt/homebrew/bin:/usr/bin:/bin</string></dict>
  <key>StartInterval</key><integer>300</integer>
  <key>StandardErrorPath</key><string>/tmp/ws-continue.log</string>
</dict>
</plist>
```

```console
$ launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.github.bobcob7.ws-continue.plist
```

launchd also runs the job during dark wakes; those passes send nothing.

## Errors

| Situation | Error code | Exit |
| --------- | ---------- | ---- |
| `claude agents` failed; nothing is sent | `claude_failed` | 1 |
| `--json` with `--watch` | `usage` | 2 |
| A named workspace not found, a shell workspace, or one whose session was never recorded | `workspace_not_found` | 3 |
| A named workspace still being handed to its session | `workspace_busy` | 4 |
| Its session is listed in another directory; nothing is touched | `session_mismatch` | 4 |
| A message wasn't recorded within 15 s; the others are still sent | `claude_failed` | 1 |

A transcript that can't be read is a warning, and its session is left
alone. With several workspaces, the exit code is that of the first
failure.

## See also

[`ws restore`](ws-restore.md), [`ws list`](ws-list.md),
[0024](../adrs/0024-claude-code-client.md),
[0047](../adrs/0047-ws-continue-after-usage-limit.md)
