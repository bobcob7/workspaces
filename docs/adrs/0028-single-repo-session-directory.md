# 0028. A one-repo workspace's session starts in its repo, which `ws claude` first trusts in Claude Code's config

- Status: Proposed
- Date: 2026-10-23

## Context

### Problem

`ws shell` and `ws claude` start their session at the top of the workspace
([0015](0015-workspace-lifetime-follows-session.md)). With one repo, the
first thing a person or agent does is `cd` into it, and an agent started
above the repo doesn't load that repo's `CLAUDE.md` or `.claude/`
settings as its project. Should a one-repo workspace's session start in
the repo, and if so, how does `ws claude` get Claude Code to start there?

### What a solution needs

1. **`ws claude` keeps working with only the workspace root trusted**: no
   new setup step, and no trust prompt per workspace.
2. **Trust stays the person's decision**: `ws` doesn't trust anything the
   person hasn't already trusted a parent of.
3. **Claude Code's config is never damaged**: no lost keys, no half-written
   file, no lost concurrent write.
4. **Lifetime checks still find the session**, including for workspaces
   created before this change.

### Relevant facts

- Inside a repository, Claude Code keys trust "on the git repository root";
  outside one, a directory's trust covers subdirectories "apart from a git
  repository nested inside it, such as a clone"[^cc-trust-keys]. Only a
  worktree inherits from its main checkout; workspace repos are clones
  ([0017](0017-clones-not-worktrees.md)).
- `claude --bg` checks trust "for the directory the session runs in" and,
  without a terminal, exits with `Workspace not trusted`[^cc-bg-trust]. In a
  new git repo below the trusted workspace root, it did[^bench-clone].
- Claude Code's docs say to trust a folder by hand by setting
  `projects["<path>"].hasTrustDialogAccepted` to `true` in
  `~/.claude.json`[^cc-trust-manual]; it lives in `$CLAUDE_CONFIG_DIR` when
  that is set[^bench-config-dir].
- Claude Code writes `~/.claude.json` through a temp file and a rename,
  holding a `.claude.json.lock` directory while it does[^bench-lock].
- Accepting trust lets a folder's `.claude/settings.json` allow rules and
  additional directories apply[^cc-trust-keys].

## Decision

With exactly one repo, `ws shell` and `ws claude` start the session in the
repo's directory (`Workspace.SessionDir`); otherwise in the workspace, as
before. `WS_WORKSPACE` and printed paths stay the workspace.

Before `claude --bg`, `ws claude` sets
`projects[<repo dir>].hasTrustDialogAccepted = true` in Claude Code's global
config, with both paths' symlinks resolved, and only when
`projects[<workspace root>]` already has it set. It takes Claude Code's
lock (makes `<file>.lock` with `mkdir`, waits up to 5 s, and takes over a
lock older than 10 s), re-reads the file, changes only that key, keeping
every other value's raw JSON, and writes through a temp file and a rename
with the file's mode. If the root isn't trusted, the session starts in the
workspace, so the trust error names the root as before. If the config can't
be updated, `ws` adds a warning and starts in the workspace.

A session belongs to a workspace (`ws rm`, `ws restore`, `ws restart`,
orphan removal) when it runs in the workspace's directory or, for a
one-repo workspace, in that repo's directory joined to the guarded
directory, without resolving the repo's symlinks.

### Why this option

1. Only the root needs trusting, as before; the repo's trust is derived.
2. `ws` writes trust only below a root the person trusted, and revoking the
   root's trust stops it. Weak: the person no longer reviews each repo's
   `.claude/settings.json` before its allow rules apply. They chose the repo
   by naming it; the docs say so.
3. The lock, the re-read under it, and the rename match how Claude Code
   writes the file, so neither side's write is lost or seen half-done.
   Weak: the lock protocol is observed, not documented.
4. Accepting both directories keeps workspaces created before this change,
   whose session runs at the top, and makes a session started below the
   repo still count as elsewhere.

## Alternatives

### Start at the workspace and tell the agent to `cd`

`--append-system-prompt` naming the repo.

**Pros**

- Writes nothing outside `ws`'s own directories.

**Cons**

- The repo's `CLAUDE.md` and settings still don't load as the project, and
  the agent's own tools still start above the repo.

### Ask the person to trust each repo

**Cons**

- Every workspace is a new clone[^bench-clone], so every `ws claude` would
  fail until someone ran `claude` there interactively.

### Make workspace repos worktrees of a trusted checkout

**Cons**

- Reverses [0017](0017-clones-not-worktrees.md) for a trust side effect.

## Consequences

- `ws` now writes one file it doesn't own. This amends
  [0015](0015-workspace-lifetime-follows-session.md)'s "`ws` doesn't edit
  Claude Code's settings" for this key, and its and
  [0025](0025-ws-claude-session-hand-off.md)'s "exactly the workspace's
  directory" session rules.
- Trust entries for removed workspaces stay in `~/.claude.json`, as Claude
  Code's own per-directory entries already do.
- An older `ws` sees a one-repo session as in another directory, and
  refuses to remove it (`session_mismatch`) rather than removing the wrong
  thing.
- Conformance: `internal/workspace` tests `SessionDir`;
  `internal/claude` tests trust writing, key preservation, symlinks, the
  untrusted parent, and the lock; `internal/lifetime` tests both
  directories; `internal/cli` end-to-end tests check where the shell and
  agent start and what was trusted.
- **Unverified**: that Claude Code honors `.claude.json.lock` on every write
  path. Checked by observation only[^bench-lock].

## References

[^cc-trust-keys]: Claude Code docs, "Configure permissions," section "Project allow rules and workspace trust." <https://code.claude.com/docs/en/permissions> (accessed 2026-10-23). "In a repository, Claude Code keys the trust on the git repository root, so the trust covers the whole repository apart from any git repository nested inside it, such as a submodule. In a worktree, it uses the main checkout's root"; "Outside a repository, Claude Code keys the trust on the directory you started it from, and the trust covers any subdirectory of that directory apart from a git repository nested inside it, such as a clone."
[^cc-bg-trust]: Same section. "Before it starts or restarts a background session, Claude Code also checks workspace trust for the directory the session runs in." "Where no dialog can appear, such as in a script, the command exits with a `Workspace not trusted` error instead."
[^cc-trust-manual]: Same page, section "What runs before you trust a folder." "trust it by hand: set `projects[\"<path>\"].hasTrustDialogAccepted` to `true` in `~/.claude.json`, where `<path>` is the repository root, or the folder itself outside a repository."
[^bench-clone]: Measured 2026-10-23 on an Apple M4, macOS 26.6.2, Claude Code 2.1.309, git 2.50.1. In a new directory below the trusted workspace root, `git init` and one empty commit, then `claude --bg -n ws-trust-probe --settings '{"worktree":{"bgIsolation":"none"}}' </dev/null`: exit 1, "Workspace not trusted. Run `claude` in <that directory> once and accept the trust prompt, then retry." One run.
[^bench-config-dir]: Measured 2026-10-23, same machine. `CLAUDE_CONFIG_DIR=<empty dir> claude agents --json` created `<dir>/.claude.json` and `<dir>/backups/`.
[^bench-lock]: Measured 2026-10-23, same machine. While `CLAUDE_CONFIG_DIR=<empty dir> claude agents --json` ran, a loop listing the directory 4,000 times saw `.claude.json.lock` twice and `.claude.json.tmp.<pid>.<hex>` once; a second run with `stat` saw the lock 3 times, each a directory. Two runs.
