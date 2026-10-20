# 0027. `ws restore` and `ws restart` share one respawn lock, and a record from a newer `ws` is left alone

- Status: Accepted (2026-10-20); the no-agent exception applies only to `ws restore`, since `ws restart` always takes the respawn lock (2026-10-20)
- Date: 2026-10-20

## Context

### Problem

Two questions about workspace state are still open (the design is
described in [docs/state.md](../state.md)):

1. **Respawning.** `ws restore` and `ws restart`
   ([0018](0018-restart-claude-daemon.md)) both run `claude respawn`, and
   `ws restart` also stops and starts Claude Code's background service.
   What keeps two of these commands from respawning the same session, or
   from both replacing the service?
2. **Versions.** Once binaries are released, one version of `ws` will read
   records that another version wrote. `workspace.toml` has a `version`
   ([0012](0012-workspace-layout.md)); the session record has none. What
   does a `ws` do with a version it doesn't know?

### What a solution needs

1. **A session is respawned at most once per stop**, and the service is
   replaced by one command at a time.
2. **No workspace is lost** to a version mismatch.
3. **Simple**: no new error for the case where waiting would resolve the
   conflict.

### Relevant facts

- **Respawning a running session kills its process**
  ([0018](0018-restart-claude-daemon.md), footnote `bench-respawn-kill`).
- **Without coordination, two runs respawn twice.** Two `ws restore` runs
  started together both respawned the same session. Of six run back to
  back, one did so too, because a listing taken just after a respawn can
  still show no process. With an exclusive lock held until each respawned
  session showed a process, there was one respawn per stop in all nine
  pairs[^bench-restore].
- **A file lock is released when its holder exits**, however it
  exits[^close].
- **Unreadable records are already left alone.** The deletion guard
  deletes only a directory named by a record that parses
  ([0012](0012-workspace-layout.md)).

## Decision

We will:

1. **Take one respawn lock in both commands.** Both `ws restore` and
   `ws restart` take an exclusive lock on `state/.respawn.lock` before they
   list sessions.
   - Each holds it until every session it respawns shows a process, or for
     up to 10 s per session, then warns.
   - A second command waits for the lock, and Ctrl-C stops the wait.
   - A dry run doesn't take it, and neither does a run that involves no
     agent workspace.
   - Nothing else takes it, and no command holding it waits for another
     lock.
2. **Version both records, and leave unknown versions alone.**
   - The session record gains `version = 1`, and a record without a
     version reads as version 1.
   - A record with a version this `ws` doesn't know is treated like one it
     can't parse:
     - nothing removes the workspace;
     - the command warns, naming the file, and says to upgrade `ws`;
     - `ws cache prune` removes no commit while the record exists.
   - Each release reads every earlier version. A change that an older `ws`
     could misread raises the version.

### Why this option

- **Once per stop.** The lock orders the respawns of both commands, and
  waiting for each respawned process means the next holder sees it running
  and leaves it alone, which held in every measured pair[^bench-restore].
  Only one command at a time can be stopping or starting the service.
- **No workspace lost.** An older `ws` never deletes or rewrites what it
  can't read; the worst case is a workspace kept until `ws` is upgraded.
- **Simple.** Waiting turns a conflict into a correct result, with no new
  error code. The version rule reuses how unreadable records are already
  handled.

Where it's weak:

- `ws restore` waits behind a `ws restart` that is itself waiting for busy
  agents.
- A process that takes more than 10 s to show up can be respawned again.
- A `claude respawn` typed by hand isn't covered by the lock.

## Alternatives

### Separate locks for each command

**Pros**

- `ws restore` never waits behind a long `ws restart`.

**Cons**

- Each command can respawn a session the other has just
  respawned[^bench-restore], and two `ws restart` runs can both stop the
  service.

### No lock: wait after each respawn only

**Pros**

- No file in `state/`.

**Cons**

- Two runs started together both see the session stopped and both
  respawn it[^bench-restore].

### Fail at once when the lock is held

**Pros**

- The caller learns at once that another run is busy.

**Cons**

- A script and a person running `ws restore` after the same reboot get a
  failure, where waiting would have given both of them the right result.

### Fail every command on an unknown version

**Pros**

- The user learns at once that `ws` is out of date.

**Cons**

- One newer workspace stops `ws` from working on every other one.

## Consequences

- These status lines note it:
  - **0012**: "`state/.respawn.lock` and session record version added by
    0027";
  - **0018**: "`ws restart` takes the respawn lock, per 0027".
- [docs/state.md](../state.md) describes the result.
- The `ws restart` step takes the lock and waits after each respawn.
- Conformance is checked by tests:
  - **unit tests**:
    - the lock is taken before listing, waited for, and released;
    - it's not taken by a dry run;
    - each respawn is followed by a wait for its process;
    - a session record without a version reads as 1;
    - a newer version is never removed, and the warning says to upgrade;
  - **end to end**: two runs respawn a session once.
- **Unverified:** two respawns with no background service running;
  behaviour on Linux.

## References

[^close]: macOS 26.6.2 manual pages: close(2), "on the last close of a file holding an advisory lock the lock is released (see further flock(2))" and "When a process exits, all associated file descriptors are freed"; _exit(2), "All of the descriptors that were open in the calling process are closed."
[^bench-restore]: Measured 2026-10-19 and 2026-10-20 on an Apple M4, macOS 26.6.2, Claude Code 2.1.304, Go 1.27.1, with builds of `ws restore`, `WS_CONFIG_DIR` below a directory whose trust prompt had been accepted, a local upstream reached through `insteadOf`, and `claude` on `PATH` a script that logged every call and let `stop`, `rm`, and `respawn` through only for sessions it had seen started. Separately, `claude respawn` on a stopped session returned in 0.41 s, and a `claude agents --json --all` started as it returned listed the session 0.16 s later with a new `pid`. Without a lock or a wait: of six `ws restore` runs each started as soon as one that had respawned a session exited, one saw the session still without a process and respawned it again, and Claude Code's service log showed the first new process killed 0.9 s after it started; two runs started together both called `respawn` in the same millisecond, and the service log showed the session started twice, 6 s apart. With an exclusive lock held from before the listing until each respawned session was listed with a process: six back-to-back pairs and three pairs started together, each after `claude stop` and 11 s, gave one `restored` and one `running` per pair; the log showed nine `respawn` calls for nine stops, and the service log nine kills and nine starts. Afterwards `lsof` found no process holding the lock file.
