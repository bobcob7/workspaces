# 0014. `ws` command set: one command per use case, and an agent guide

- Status: Accepted (2026-10-18); `ws restart` added by [0018](0018-restart-claude-daemon.md) (2026-10-18); `ws claude` attach conditions and session name amended by [0025](0025-ws-claude-session-hand-off.md) (2026-10-19); `ws cache refs` added by [0033](0033-ws-cache-refs.md) (proposed); `ws review` added by [0039](0039-ws-review-command.md) (2026-10-27); `ws machine` added by [0046](0046-ws-machine.md) (proposed)
- Date: 2026-10-18
- Supersedes: [0004](0004-single-binary-with-subcommands.md)

## Context

### Problem

[0004](0004-single-binary-with-subcommands.md) defined one `ws` binary with
`init`, `list`, `cleanup`, and `cache`. Since then:

- `ws` has four use cases ([0015](0015-workspace-lifetime-follows-session.md)):
  a person starts a Claude Code agent in a new workspace; an agent starts
  another agent in a new workspace; a person opens a shell in a new
  workspace; and agents are restored after a reboot or a crash.
- Each workspace now has exactly one session, started when it's created, and
  is removed when that session is gone
  ([0015](0015-workspace-lifetime-follows-session.md)).
- Agents use `ws` as much as people do, and need to learn it without a human
  explaining it each time.
- The name `ws` was questioned against `workspace`.

What commands does `ws` have, and how do agents learn to use them?

### What a solution needs

1. **One command per use case**, each creating the workspace and its session
   in one step.
2. **An agent can start an agent** and get its session id and workspace path
   back, without waiting for it to finish.
3. **Agents can learn the tool from the tool**, in one or two commands, with
   the rules that matter (which workspaces they may remove, what output to
   parse).
4. **Short to type**, without clashing with common tools.

### Relevant facts

- **Name clashes.** Neither `ws` nor `workspace` is a Homebrew formula, and
  no Debian stable package installs either as a command. The npm package
  `local-web-server` installs a global `ws` command (72,006 downloads in the
  week to 2026-10-16). Nothing on npm installs `workspace`[^bench-names].
- **`claude --bg`** returns once the session has started, exits 1 when it
  can't start (an untrusted directory, for example), and passes its other
  options to the session. Without a prompt it starts an idle session, and
  `claude attach <id>` opens a background session in the
  terminal[^bench-bg-start][^claude-attach].
- **Claude Code loads CLAUDE.md files from parent directories.** "CLAUDE.md
  and CLAUDE.local.md files in the directory hierarchy above the working
  directory are loaded at launch"[^claude-memory]. A `CLAUDE.md` placed in
  the directory that holds the workspaces was loaded by `claude -p` started
  in a workspace below it[^bench-parent-md].
- **Prior art for agent-facing docs:** the llms.txt proposal is "a proposal
  to standardise on using an /llms.txt file to provide information to help
  agents use a website"[^llmstxt]. It is written for websites, not CLIs.
- **`--help` exists for every command** through cobra
  ([0005](0005-cli-framework.md)). It lists flags, one command at a time.

## Decision

We keep the name **`ws`**, with these commands:

```
ws claude [--name <n>] <spec>... [-- <claude args>...]   new workspace with a Claude Code background agent
ws shell  [--name <n>] <spec>...                         new workspace with an interactive shell
ws restore                                               restart agents that aren't running
ws list                                                  workspaces and their sessions
ws rm <ws>...                                            remove workspaces and their sessions now
ws cache <op> [args]                                     add | update | list | search | resolve | path | remove | prune
ws llm [<command>...]                                    guide for agents
```

- **`ws claude`** creates the workspace, then runs `claude --bg` in it as
  [0015](0015-workspace-lifetime-follows-session.md) describes, passing
  everything after `--` to `claude` unchanged, so the prompt and options
  such as `--model` go there. It also passes `-n <workspace name>`, so
  `claude agents` shows the workspace's name. Once Claude has accepted the
  session, `ws` prints the workspace id, the Claude session id, and the
  path, and exits; `--json` prints them as an object. If the session can't
  start, it removes the workspace and exits non-zero. With no prompt after
  `--`, it attaches the terminal to the new session (`claude attach`)
  instead of printing.
- **`ws shell`** creates the workspace and runs `$SHELL -i` (or `/bin/sh`) in
  it in the foreground. The workspace is removed when the shell exits, and
  `ws` exits with the shell's exit code.
- **`ws restore`** runs `claude respawn` for every agent workspace whose
  session has no process, and prints what it restarted.
- **`ws rm`** removes a workspace now: `claude stop` and `claude rm` for an
  agent, then the directory. A running shell's workspace is refused.
- There is no command that creates a workspace without a session, since
  nothing would remove it.

**`ws llm`** prints guidance written for agents, as Markdown on stdout:

- `ws llm` prints an overview: what `ws` is for, the use cases, the rules
  (only remove workspaces you created; parse `--json`, not tables), the
  spec syntax, and one line per command pointing to `ws llm <command>`.
- `ws llm <command>`, for example `ws llm claude` or `ws llm cache search`,
  prints that command's guide: when to use it, its flags, its JSON output,
  exit codes, and examples.
- `ws llm --all` prints everything as one document.
- The text is embedded in the binary (`go:embed`). A test fails if a command
  has no guide, or if a guide doesn't mention one of that command's flags.

**Discovery.** When `ws` creates `workspaces/`, it also writes
`workspaces/CLAUDE.md`, if missing and never overwritten, saying that these
are `ws` workspaces and to run `ws llm` before using `ws`. Claude Code
sessions in any workspace load it, and `ws` still writes nothing inside a
workspace.

### Why this option

1. **One command per use case.** Each of the four use cases is one `ws`
   command. Reviewing, following, and stopping an agent are Claude Code's
   own commands, so `ws` doesn't duplicate them.
2. **Agents can start agents.** `ws claude --json … -- "<prompt>"` returns
   as soon as the session has started, with the ids the caller needs for
   `claude logs` or `claude attach`.
3. **Agents learn from the tool.** One `ws llm` gives the use cases and the
   rules. `ws llm <command>` gives the details. Claude Code finds out about
   it without being told, through the parent `CLAUDE.md`. Other agents need
   to be told to run `ws llm`.
4. **Short.** Two letters. The clash is only with a global
   `local-web-server` install from npm.

The weak points: only Claude Code is supported as an agent, and the agent
guide is a second body of text to keep in step with the commands, which the
test partly enforces.

## Alternatives

### Keep `ws init`, and run agents with `ws init --run`, `ws run`, `ws logs`

**Pros**

- Works with any agent or command, not only Claude Code.
- A workspace can be created without starting anything in it.

**Cons**

- Those sessions don't appear in `claude agents` and can't be attached to
  ([0015](0015-workspace-lifetime-follows-session.md)).
- A workspace without a session has nothing to end it, so it needs a timer
  or manual cleanup again.

### `ws claude <name> <spec>... -- <prompt>` with a positional name

**Pros**

- Shorter to type.

**Cons**

- A name and a short repo spec look the same: in `ws claude buff-bear api`,
  `buff-bear` could be either. A flag keeps it unambiguous.

### Name the binary `workspace`

**Pros**

- No known clash, and the name says what it does.

**Cons**

- Nine characters for a command typed constantly. An `alias ws=workspace`
  doesn't reach agents, which run commands without the user's aliases.

### Agents rely on `--help`

**Pros**

- No extra text to maintain.

**Cons**

- Help lists flags one command at a time. It has no use cases, no rules, and
  no JSON shapes, so an agent needs several calls and still has to guess.

### Write a CLAUDE.md or AGENTS.md into each workspace

**Pros**

- Every agent that reads those files finds it, not only Claude Code.

**Cons**

- Contradicts [0012](0012-workspace-layout.md): `ws` writes nothing inside a
  workspace.

### An MCP server

**Pros**

- Structured tool calls, with schemas, instead of parsing text.

**Cons**

- A server to run and register with every agent. Too heavy for a CLI whose
  commands already have `--json`.

## Consequences

- Other agents (Codex, Gemini CLI) would each need their own command and
  their own way of telling when a session is gone.
- `ws cache prune` removes `refs/ws/commits/*` refs that no workspace uses
  ([0007](0007-repo-spec-syntax-and-resolution.md)).
- JSON output and exit codes still need their own ADR.
- Conformance: the `ws llm` coverage test above; tests with a fake `claude`
  on `PATH` that `ws claude` passes the arguments after `--` unchanged,
  prints the session only after `claude --bg` exits 0, and leaves no
  workspace when it fails; and a test that `workspaces/CLAUDE.md` is created
  once and never overwritten.

## References

[^bench-names]: Checked 2026-10-17. `type -a ws workspace` on macOS 26.6.2: not found. `brew info ws` and `brew info workspace`: "No available formula". packages.debian.org contents search, suite stable, exact filename `ws`: one match, `/usr/share/yasnippet-snippets/bazel-mode/ws`, not a command; `workspace`: no match under a `bin/` directory. `npm view local-web-server bin`: `{ ws: 'bin/cli.js' }`; `https://api.npmjs.org/downloads/point/last-week/local-web-server`: 72,006. `npm view workspace bin`: none.
[^bench-bg-start]: Measured 2026-10-18 on an Apple M4, macOS 26.6.2, Claude Code 2.1.302. In one run, `claude --bg -n <name> '<prompt>'` returned in 0.32 s with exit 0, printing `backgrounded · <id> · <name>`; in an untrusted directory it exited 1. With `--settings '<json>'` before the prompt, the setting took effect in the session ([0015](0015-workspace-lifetime-follows-session.md), footnote `bench-bg-edit`). `claude --bg -n <name> </dev/null` with no prompt printed `backgrounded · <id> · <name> (idle — send a prompt to start)` and exited 0.
[^claude-attach]: Claude Code CLI 2.1.302, `claude attach --help` (2026-10-18). "Open the background session in this terminal. … The session keeps running either way."
[^claude-memory]: Claude Code docs, "How Claude remembers your project." <https://code.claude.com/docs/en/memory> (accessed 2026-10-17). "CLAUDE.md and CLAUDE.local.md files in the directory hierarchy above the working directory are loaded at launch. Files in subdirectories load on demand."
[^bench-parent-md]: Measured 2026-10-17, Claude Code 2.1.302, macOS 26.6.2. `<scratch>/workspaces/CLAUDE.md` contained a verification code. `claude -p` run in `<scratch>/workspaces/ws-one` and asked for a verification code in its loaded instructions replied with that code.
[^llmstxt]: J. Howard, "The /llms.txt file." <https://llmstxt.org/> (accessed 2026-10-17). "A proposal to standardise on using an /llms.txt file to provide information to help agents use a website."
