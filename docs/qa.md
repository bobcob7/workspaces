# Questions and answers

> `ws` is designed but not implemented yet. These answers describe the
> decided behavior; the [ADRs](adrs/README.md) and
> [command reference](tools/README.md) have the details.

- [Setup](#setup)
  - [How do I set up Claude Code's folder trust for `ws`?](#how-do-i-set-up-claude-codes-folder-trust-for-ws)
  - [Why does `ws claude` fail with "Workspace not trusted"?](#why-does-ws-claude-fail-with-workspace-not-trusted)
  - [Where does `ws` keep its files?](#where-does-ws-keep-its-files)
- [Workspaces](#workspaces)
  - [When is a workspace deleted?](#when-is-a-workspace-deleted)
  - [Will `ws` delete uncommitted or unpushed work?](#will-ws-delete-uncommitted-or-unpushed-work)
  - [Where do I push from a workspace?](#where-do-i-push-from-a-workspace)
  - [Are workspace repos git worktrees?](#are-workspace-repos-git-worktrees)
- [Agents](#agents)
  - [How do I see, follow, or talk to my agents?](#how-do-i-see-follow-or-talk-to-my-agents)
  - [How do I review what a finished agent did?](#how-do-i-review-what-a-finished-agent-did)
  - [Can an agent start another agent?](#can-an-agent-start-another-agent)
  - [My machine rebooted. How do I get my agents back?](#my-machine-rebooted-how-do-i-get-my-agents-back)
  - [Why doesn't the agent see `WS_ID` or my other environment variables?](#why-doesnt-the-agent-see-ws_id-or-my-other-environment-variables)
  - [I rotated a token, changed MCP servers, or updated Claude Code. How do my agents pick it up?](#i-rotated-a-token-changed-mcp-servers-or-updated-claude-code-how-do-my-agents-pick-it-up)
  - [Why doesn't Claude create its own worktree in a workspace?](#why-doesnt-claude-create-its-own-worktree-in-a-workspace)
  - [How do agents learn to use `ws`?](#how-do-agents-learn-to-use-ws)
  - [Can I use an agent other than Claude Code?](#can-i-use-an-agent-other-than-claude-code)

## Setup

### How do I set up Claude Code's folder trust for `ws`?

Trust the workspace root, `$CONFIG_DIR/workspaces`, once. That covers every
workspace `ws` creates in it.

Claude Code asks you to trust a folder the first time you start it there,
and won't start a background session in a folder you haven't
trusted[^cc-bg-trust]. Every workspace is a new folder, so without this step
every `ws claude` would fail. Outside a git repository, trust "covers any
subdirectory of that directory apart from a git repository nested inside
it"[^cc-trust-keys]. The workspace root isn't a git repository, and `ws`
starts each agent at the top of its workspace, so trusting the root once is
enough[^bench-trust].

**Interactively** (recommended):

```console
# macOS default; on Linux, ~/.config/workspaces/workspaces
$ cd "$HOME/Library/Application Support/workspaces/workspaces"
$ claude
# accept the trust prompt, then /exit
```

If `workspaces/` doesn't exist yet, create it first with `mkdir -p`.

**Non-interactively**, for example in a setup script: Claude Code's docs
describe setting the trust flag by hand in `~/.claude.json`[^cc-trust-manual]:

```console
$ ROOT="$HOME/Library/Application Support/workspaces/workspaces"
$ jq --arg p "$ROOT" '.projects[$p].hasTrustDialogAccepted = true' ~/.claude.json > /tmp/claude.json \
    && mv /tmp/claude.json ~/.claude.json
```

Do this while no Claude Code session is running, since Claude Code also
writes this file.

**To check** whether it's trusted:

```console
$ jq --arg p "$ROOT" '.projects[$p].hasTrustDialogAccepted' ~/.claude.json
true
```

**To revoke** it, set the flag to `false` or remove the entry. `ws claude`
then fails until you trust the root again; existing agents are unaffected
until they're restarted, since Claude Code checks trust "before it starts or
restarts a background session"[^cc-bg-trust].

**What trusting it means.** Accepting the dialog lets a folder's
`.claude/settings.json` grant permissions (`permissions.allow`,
`additionalDirectories`), which Claude Code otherwise holds
back[^cc-trust-allow]. `ws` writes no `.claude/` settings into the
workspace root, so trusting it grants nothing by itself. The repos inside a
workspace are nested git repositories, which a parent's trust doesn't cover
for these rules[^cc-trust-keys]: if you start `claude` interactively inside
one of them, it asks you separately.

Notes:

- Don't make your home directory the workspace root. Trust accepted there
  "is held for the current session only and is not written to
  disk"[^cc-home].
- If you set `CLAUDE_CONFIG_DIR`, Claude Code keeps its state elsewhere, and
  `ws` must run with the same value to see your agents.

### Why does `ws claude` fail with "Workspace not trusted"?

Claude Code refused to start a background session because the workspace
root isn't trusted[^cc-bg-trust]. `ws` removes the half-made workspace and
prints the directory to trust. Follow the [setup
above](#how-do-i-set-up-claude-codes-folder-trust-for-ws), then run the
command again.

### Where does `ws` keep its files?

Everything is under one config directory, `CONFIG_DIR`: `$WS_CONFIG_DIR` if
set, otherwise `~/Library/Application Support/workspaces` on macOS and
`~/.config/workspaces` on Linux ([0011](adrs/0011-config-dir.md)).

| Path                      | Contents                                        |
| ------------------------- | ----------------------------------------------- |
| `config.toml`             | settings                                        |
| `cache/`                  | one bare clone per upstream repo                |
| `workspaces/<id>/`        | the workspaces: your repos, nothing else        |
| `state/<id>/`             | `ws`'s metadata and session record per workspace |
| `workspaces/CLAUDE.md`    | tells agents to run `ws llm`                    |

## Workspaces

### When is a workspace deleted?

When its session is gone ([0015](adrs/0015-workspace-lifetime-follows-session.md)):

- **Agent workspace** (`ws claude`): when the Claude Code session is
  deleted, with `claude rm`, in `claude agents`, or with `ws rm`. A finished
  agent's workspace stays until then.
- **Shell workspace** (`ws shell`): when the shell exits.

After `claude rm`, or a shell killed by a closed terminal or a reboot, the
directory is removed at the start of a later `ws` command, which checks at
most once a minute.

### Will `ws` delete uncommitted or unpushed work?

Yes. Removal ignores git state: uncommitted changes, unpushed commits, and
stashes go with the workspace. Workspaces are often used for investigations
whose results live elsewhere, so a dirty repo isn't a reason to keep one
([0010](adrs/0010-unsaved-work-detection.md), rejected). Push, or copy out,
anything worth keeping before you delete the session or exit the shell.

### Where do I push from a workspace?

To `origin`, as usual. In every workspace repo, `origin` is the upstream
repository, and a second remote, `cache`, points at the local cache for fast
fetches ([0009](adrs/0009-workspace-repository-remotes.md)).

### Are workspace repos git worktrees?

No. Each one is a separate clone from the local cache, with its own `.git`
and objects hard-linked from the cache where the filesystem allows
([0009](adrs/0009-workspace-repository-remotes.md)). Worktrees share
branches, config, and remotes with the cache and with each other, refuse the
same branch twice, and break the cache's fetches while a fetched branch is
checked out. A clone took the same time and disk to create
([0017](adrs/0017-clones-not-worktrees.md)).

## Agents

### How do I see, follow, or talk to my agents?

With Claude Code's own commands, since `ws claude` starts ordinary
background sessions named after the workspace:

| Want to                     | Run                    |
| --------------------------- | ---------------------- |
| see all agents              | `claude agents`, or `ws list` for workspaces too |
| read recent output          | `claude logs <id>`     |
| talk to it                  | `claude attach <id>`   |
| stop it, keeping the workspace | `claude stop <id>`  |
| delete it and its workspace | `ws rm <id>` or `claude rm <id>` |

### How do I review what a finished agent did?

Its workspace stays until you delete the session. Attach to read the
conversation (`claude attach <id>`), and look at the code at the path
`ws list` shows, with `git status`, `git diff`, and `git log` in each repo.

### Can an agent start another agent?

Yes. That's one of the main use cases:

```console
$ ws claude --json buff-bear -- "Write the migration for the new schema"
```

It returns once the new session has started, with the workspace id, the
Claude session id, and the path. The calling agent can then follow it with
`claude logs <id>`. The new agent appears in `claude agents` like any other.

### My machine rebooted. How do I get my agents back?

Run `ws restore`. A reboot stops background sessions, and they don't come
back on their own[^cc-reboot]. `ws restore` runs `claude respawn` for each
agent workspace whose session isn't running; each comes back with its
conversation, waiting for input. Reply to it (`claude attach <id>`) to have
it continue. Shell workspaces aren't restored.

### Why doesn't the agent see `WS_ID` or my other environment variables?

Claude Code runs background sessions under its own long-running service, not
as children of the command that started them, so variables set in your shell
don't reach them[^bench-env]. Sessions get the environment of whatever
started that service. The agent's working directory is the workspace. For
settings an agent needs, use Claude Code's settings (`env`) or pass them in
the prompt.

### I rotated a token, changed MCP servers, or updated Claude Code. How do my agents pick it up?

Run `ws restart` from a shell that already has the change: a new terminal,
or after `source ~/.zshrc`. It replaces Claude Code's background service
with one started from that shell, then respawns every running background
session, each keeping its conversation. A session that is in the middle of
a turn is respawned when the turn ends, so no work is interrupted; `ws list`
marks sessions that still have the old environment `stale`
([`ws restart`](tools/ws-restart.md),
[0018](adrs/0018-restart-claude-daemon.md)).

It affects every background session on the machine, including ones `ws`
didn't start. Interactive `claude` sessions aren't affected; restart those
yourself.

### Why doesn't Claude create its own worktree in a workspace?

A background session normally moves into a git worktree of its own before
editing. In a workspace that's unnecessary, since the workspace is already
isolated, so `ws claude` starts the session with
`worktree.bgIsolation` set to `"none"` and Claude edits the workspace's
repos directly ([0015](adrs/0015-workspace-lifetime-follows-session.md),
[0017](adrs/0017-clones-not-worktrees.md)).

### How do agents learn to use `ws`?

`ws llm` prints a guide written for agents, and `ws llm <command>` the
details of one command ([`ws llm`](tools/ws-llm.md)). Claude Code agents
find it on their own: `ws` writes `workspaces/CLAUDE.md`, which Claude Code
loads in every workspace below it. Other agents need to be told to run
`ws llm`.

### Can I use an agent other than Claude Code?

Not as a `ws` command yet. `ws claude` relies on Claude Code's background
sessions for listing, restoring, and knowing when a session is gone
([0014](adrs/0014-command-set.md)). You can run any agent from a
`ws shell`, but its workspace goes away when the shell exits.

[^cc-bg-trust]: Claude Code docs, "Configure permissions," section "Project allow rules and workspace trust." <https://code.claude.com/docs/en/permissions> (accessed 2026-10-18). "Before it starts or restarts a background session, Claude Code also checks workspace trust for the directory the session runs in. If you run `claude --bg` from a terminal in a directory you haven't trusted, the trust dialog appears first and the session starts once you accept it. Where no dialog can appear, such as in a script, the command exits with a `Workspace not trusted` error instead."
[^cc-trust-keys]: Same section. "Outside a repository, Claude Code keys the trust on the directory you started it from, and the trust covers any subdirectory of that directory apart from a git repository nested inside it, such as a clone."
[^cc-trust-manual]: Same page, section "What runs before you trust a folder." "trust it by hand: set `projects["<path>"].hasTrustDialogAccepted` to `true` in `~/.claude.json`, where `<path>` is the repository root, or the folder itself outside a repository."
[^cc-trust-allow]: Same page, section "Project allow rules and workspace trust." "`permissions.allow` rules and `permissions.additionalDirectories` entries in a project's `.claude/settings.json` grant capability, so Claude Code applies them only after you accept the workspace trust dialog for that folder."
[^cc-home]: Claude Code docs, "Security," section "Additional safeguards." <https://code.claude.com/docs/en/security> (accessed 2026-10-18). "When you start Claude Code directly in your home directory, trust acceptance is held for the current session only and is not written to disk".
[^cc-reboot]: Claude Code docs, "Agent view." <https://code.claude.com/docs/en/agent-view> (accessed 2026-10-18). "Shutting down or restarting your machine stops running background sessions."
[^bench-trust]: Measured 2026-10-18 on an Apple M4, macOS 26.6.2, Claude Code 2.1.302. From a non-interactive shell, `claude --bg` in a new, untrusted directory exited 1 with "Workspace not trusted". In a new subdirectory of a trusted directory, and in one holding a nested git repo, it started, and the agent edited a file in the nested repo.
[^bench-env]: Measured 2026-10-18, same setup. `WS_TEST=xyz123 claude --bg '<prompt>'`, asked to run `printenv WS_TEST`, got nothing; `pwd` printed the starting directory. The session's process was a child of `claude daemon run`, not of the shell that started it.
