# 0076. Remote environments ship Linux and macOS first; Windows follows a spike that decides the session model; the build is four stacked PRs

- Status: Accepted (2026-11-23); phase 1 (Linux and macOS agents, the four PRs) is done; "Until then" amended by [0081](0081-windows-environment-agent.md) (proposed); Windows terminals by [0085](0085-windows-conpty-terminals.md) (proposed)
- Date: 2026-11-22

## Context

### Problem

The user wants a Linux server and a Windows rig. `ws` today supports
Linux and macOS: its terminal, locking, process and daemon code has
unix-only and per-OS files. Claude Code's background sessions
(`claude --bg`) are what `ws` supervises
([0024](0024-claude-code-client.md)). What works on Windows, in what
order do we ship, and where do we refuse to guess?

### What a solution needs

1. **Linux first**, because it works with the least new code and gives the
   user a real remote soon.
2. **Honesty about Windows**: what's known to break, what's unknown.
3. **Each PR reviewable alone**, per the repo's stacked pattern
   (docs, then CLI, then desktop).

### Relevant facts

- Platform-split files today: `internal/claude/pty_{linux,darwin,other,unix}.go`,
  `internal/daemon/peercred_{linux,darwin}.go` (the daemon's Unix socket
  and peer credentials), `internal/proc/proc_{linux,darwin,other}.go`,
  `internal/power/power_{darwin,other}.go`; `syscall` or `x/sys/unix` in
  `internal/{lockfile,gate,shell,mcp,exectest,backup,daemon}`[^grep].
- Whether Claude Code's `--bg` sessions, `claude daemon run`, and
  `claude attach` exist and behave the same on Windows is **not known to
  this ADR**. This ADR didn't test it.
- `ws` clones with the `git` CLI ([0003](0003-use-git-cli.md)); Windows
  needs Git for Windows on `PATH`.

## Decision

**Phase 1, Linux and macOS agents.** Everything in
[0072](0072-remote-environments.md) to
[0075](0075-environment-enrollment-and-certificates.md), for agents on
Linux (amd64, arm64) and macOS. The controller runs where `ws` already runs.

**Phase 2, a Windows spike (no feature code).** One short document,
`docs/design/remote-environments/windows.md`, answering with commands run
on a real Windows machine:

1. Does `claude --bg`, `claude agents`, `claude logs`, `claude attach` and
   `claude stop` work there, natively or only in WSL?
2. Which `ws` packages fail to compile for `GOOS=windows`, and which
   need real redesign (the daemon's Unix socket, the lock files, the
   pty, signals) rather than a build-tag file?
3. Does git cloning into `CONFIG_DIR` work with long paths and
   case-insensitive names?
4. How do we run the agent as a service (a Windows Service, or a
   per-user scheduled task), and protect its key (DPAPI or an ACL)?
5. Is WSL2 the pragmatic answer: the agent runs inside it as Linux, and
   the Windows rig counts as a Linux environment?

The spike ends in one of: (a) native Windows, with a list of ports; (b)
the agent in WSL2, documented; (c) a Windows agent that runs only the
`ws` runner ([0062](0062-agent-runner-built-into-ws.md)) and not Claude
Code. The owner picks; the choice is its own ADR.

**Until then**, `ws env agent run` on Windows exits with `unsupported`
and a pointer to this ADR, and the docs don't claim support.

**The stack.**

| PR | Contents | Accepts |
| --- | --- | --- |
| 1. This one | ADRs 0072-0076, [design](../design/remote-environments/README.md) | |
| 2. Core | `internal/environment` (records, CA, enrollment), broker in the daemon, `ws env`, `ws env agent`, hello and snapshot | 0073, 0074, 0075 |
| 3. Operations | `workspace.create/list/rm`, agent status/logs/send/stop, `ws claude --env`, `ws list --env` (plain `ws list` stays local, see below), trees (`environment` on records) | 0072 |
| 4. Terminals and desktop | `terminal.*` streams, `ws shell --env` (moved from PR 3, see below), `ws attach`, `ws stop`, `--env` on `rm`/`rename`/`hide`/`unhide`, `agent_state`, the error code `terminal_limit`, reviews and loops on environments (`review.*`, `loop.*`, the agent's tick, protocol 3, [ADR 0080](0080-reviews-and-loops-on-environments.md)), desktop and Workspace Online: Settings › Environments, picker, badges, offline state, `ws list` showing every environment by default. The remote conversation view and stopping a `ws` runner agent on a remote are later | |

**PR 3 deviations (owner decisions, 2026-11-22).** (1) Plain `ws list` lists
local workspaces only until PR 4, and remote ones appear with `ws list --env
<name>`: the desktop app runs `ws list --all` and acts on ids with local
commands, so a remote id equal to a local one would make it act on the wrong
workspace. (2) `ws shell --env` moves to PR 4: a shell needs the `terminal.*`
streams that PR 4 adds. Both were resolved in PR 4. Both are recorded in
[0072](0072-remote-environments.md)'s Amendments.

### Why this option

1. Linux and macOS compile and work today (criterion 1); the Windows
   unknowns are written down and tested before anyone depends on them
   (criterion 2).
2. Each PR has its own tests and screens (criterion 3).

Where it's weak: the user's Windows rig, half the motivation, isn't served
until the spike's answer is in. WSL2, if that's the answer, would serve it
early with no `ws` port.

## Alternatives

### Port to Windows before shipping anything

**Pros:** one complete release.
**Cons:** a large blind port ahead of knowing whether Claude Code's
sessions work there.

### Ship a Windows agent that only forwards commands over SSH

**Pros:** avoids the port.
**Cons:** reintroduces the inbound-port and SSH-server requirements
[0073](0073-environment-agent-is-ws.md) rejected.

## Consequences

- README roadmap gets an entry for each PR; the
  [roadmap](../roadmap.md) is unaffected (it covers model independence).
- Conformance: CI cross-compiles `GOOS=linux,darwin` agents for
  `amd64,arm64`; a `windows` job is added with the spike's outcome.
- **Unverified:** everything about Windows above.

## Amendments (ADR 0081, Windows)

Proposed in [0081](0081-windows-environment-agent.md), 2026-11-24.

- **"Until then" never shipped.** No `unsupported` exit was built: `ws`
  didn't compile for Windows at all, so there was nothing to refuse. 0081
  replaces the sentence: `ws env agent run` runs on Windows, and only
  terminals and `agent.send` refuse, with `platform_unsupported`.
  [0085](0085-windows-conpty-terminals.md) then built terminals and
  `agent.send` on ConPTY; they refuse only on systems without
  pseudo-terminals.
- **Spike question 2 is answered** from cross-compiling, in
  [constraints.md](../design/env-windows/constraints.md). Questions 1 and
  3-5 are still open: nothing has been run on a Windows machine.

## References

[^grep]: Measured 2026-11-22 on the repository at `2cbb103`; see [0073](0073-environment-agent-is-ws.md).
