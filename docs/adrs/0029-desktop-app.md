# 0029. A Tauri desktop app drives agents through `ws --json` and `claude attach` in a pseudo-terminal

- Status: Proposed
- Date: 2026-10-23

## Context

### Problem

`claude agents` shows background sessions in a terminal, but it doesn't
know about workspaces: which repos an agent has, or how to start one in a
new workspace. `ws list` knows workspaces but shows a session only as
`working`, `idle`, `starting`, or `stopped`. Someone running several agents
wants one window that shows which agents are working, which are waiting for
them, and which are done; lets them answer an agent; and starts new
workspaces and agents.

What should that window be built with, and how does it get its data and
talk to agents without a second implementation of `ws`?

### What a solution needs

1. **One source of truth.** Workspace creation, removal, and session
   reading stay in `ws` ([0015](0015-workspace-lifetime-follows-session.md),
   [0024](0024-claude-code-client.md)); the window mustn't re-implement
   them.
2. **Full interaction with an agent**, including permission prompts and
   slash commands, not only a text box.
3. **Tell "waiting for you" from "finished".** Both are `idle` to `ws list`.
4. **Native, light, macOS first**, buildable on Linux.

### Relevant facts

- `ws` commands take `--json` and print one object on stdout, or an error
  object on stderr ([0016](0016-output-contract.md)).
- `ws list --json` took 0.37–0.45 s, and `claude agents --json --all`
  0.08–0.09 s, with 4 workspaces[^bench-poll].
- `claude attach <id>` is a full-screen terminal UI. Closing it leaves the
  session running: after the app's terminal killed an attach, `claude
  agents --json --all` listed the session with the same `pid` and no
  `claude attach` process remained[^bench-attach]. `claude stop` documents
  that `claude attach` opens a stopped session again[^claude-help].
- Claude Code keeps `jobs/<id>/state.json` per background session, with
  `state` (`working`, `blocked`, `done`), `detail`, `intent`,
  `output.result`, and `children` links. It's undocumented. A new session
  waiting for a prompt reads `blocked`
  ([0024](0024-claude-code-client.md), footnote `bench-review`).
- A macOS app started from Finder doesn't get the PATH a login shell
  sets **(unverified here; the app asks the login shell for its PATH
  either way)**.

## Decision

We will add a desktop app in `desktop/`, built with Tauri 2: a Rust backend
and a React, TypeScript, and Redux Toolkit frontend.

- **Reading.** Every 4 s the app runs `ws list --json` and `claude agents
  --json --all` concurrently. It shows every workspace `ws` lists and every
  background session no workspace records. Either command failing is a
  warning while the other answers.
- **Status.** `ws list`'s reading decides working, starting, and stopped.
  For an `idle` session, Claude Code's job `state` decides: `blocked` is
  "needs input", `done` is "done", anything else, or no file, "waiting".
  The job's `detail`, `intent`, result, and links are shown when present.
- **Acting.** New agent: `ws claude --json [--name] [--offline] <specs> --
  [--model] [--permission-mode] [<prompt>]`. Remove: `ws rm --json`, or
  `claude rm` for a session outside a workspace. Stop: `claude stop`.
  Restore: `ws restore --json`. Errors keep `ws`'s codes, so
  `claude_untrusted` can say which directory to trust.
- **Talking to an agent.** Selecting a running agent runs `claude attach
  <id>` in a pseudo-terminal (`portable-pty`) rendered with xterm.js, so
  everything `claude attach` can do works. A message box types into the
  same terminal, as a bracketed paste when it has several lines. At most 4
  attach terminals stay open; hidden ones beyond that detach, least
  recently shown first. A stopped agent is attached only on "Resume".
- **Shell workspaces** run `ws shell --name <id> <specs>` in the same kind
  of terminal. The app picks the id, sanitized as `ws` does
  ([0012](0012-workspace-layout.md)), so it knows which workspace the
  terminal is before `ws list` shows it.

### Why this option

1. `ws` stays the only code that creates, reads, or removes workspaces. The
   app adds no on-disk state; quitting it loses only open terminals.
2. A real terminal gives full interaction without depending on Claude Code
   internals; the message box is a convenience over it.
3. The job file is the only place the difference between waiting and done
   is recorded. It's read best-effort: a missing or changed file falls back
   to "waiting", the same thing `ws list` says.
4. Tauri uses the system webview, so the app has no bundled browser
   (a 27 MB debug bundle[^bench-attach]); Rust handles the pty and process
   management. It is a second language in the repo (see Consequences).

## Alternatives

### Extend `ws` with a TUI or a local web UI

**Pros:** one language, one binary; no webview.
**Cons:** a TUI can't host `claude attach`'s own full-screen UI beside a
list without writing a terminal emulator; a web UI still needs a pty bridge
and a browser tab. Either grows `ws` beyond its non-interactive command
contract ([0016](0016-output-contract.md)).

### Wails (Go) instead of Tauri

**Pros:** same language as `ws`; could call its packages in-process.
**Cons:** calling `ws`'s packages in-process is what criterion 1 rules
out, and the app needs only the CLI. The maintainer asked for Tauri.

### Talk to agents by sending prompts with `claude -p --resume`

**Pros:** no terminal emulation.
**Cons:** it starts a separate process on the same conversation while the
background session is running, and can't answer a permission prompt the
background session is showing.

## Consequences

- The repo gains Rust and TypeScript, with their own toolchains and tests:
  `cargo test` in `desktop/src-tauri`, `npm test` in `desktop`, run by
  `make desktop-test`. The Go `make lint` rules for ADRs 0003 and 0024 cover
  Go code only; the app runs `claude` directly for `attach`, `stop`, `rm`,
  and `agents`, which `ws` doesn't wrap.
- Polling runs `ws list` every 4 s while the app is open. `ws list` runs
  the removal pass each time ([0026](0026-removal-pass-timing.md)), so a
  workspace whose session is gone disappears within one poll.
- Status depends on an undocumented Claude Code file. Tests pin the mapping
  (`model.rs`); if Claude Code changes the file, idle agents show as
  "waiting" until the mapping is updated.
- A shell workspace started in the app is removed when the app quits, as
  it would be when its terminal closes ([0015](0015-workspace-lifetime-follows-session.md)).
- **Unverified:** behavior on Linux; the app is built and tested on macOS
  only.

## References

[^bench-poll]: Measured 2026-10-23 on an Apple M4, macOS 26.6.2, Claude Code 2.1.309, `ws` at this commit. 3 runs each of `/usr/bin/time -p ws list --json` and `/usr/bin/time -p claude agents --json --all`, alternating, with 4 workspaces and 5 sessions listed; range of `real` reported.
[^bench-attach]: Measured 2026-10-23, same machine. `WS_DESKTOP_ATTACH=3dd81c7a cargo test attach_live -- --ignored` opened `claude attach 3dd81c7a` in the app's pty, received 16 436 bytes of terminal output in 4 s, and closed it. `claude agents --json --all` listed the session with `pid` 47838 and `status` `idle` before and 2 s after; `pgrep -fl "claude attach"` found nothing. `du -sh` of `Workspaces.app` from `tauri build --debug --bundles app`: 27 MB. One run.
[^claude-help]: Claude Code 2.1.309, `claude --help`, accessed 2026-10-23. "stop|kill <id> Stop a background session. Its conversation is kept: `claude attach <id>` opens it again".
