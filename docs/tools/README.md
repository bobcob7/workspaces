# `ws` commands

| Command                       | Purpose                                                  |
| ----------------------------- | -------------------------------------------------------- |
| [`ws claude`](ws-claude.md)   | New workspace with a Claude Code background agent        |
| [`ws shell`](ws-shell.md)     | New workspace with an interactive shell                  |
| [`ws restore`](ws-restore.md) | Restart agents that aren't running, after a reboot or crash |
| [`ws restart`](ws-restart.md) | Restart Claude Code's background service and every running agent, to pick up new environment variables, MCP servers, or a Claude Code update |
| [`ws list`](ws-list.md)       | List workspaces and their sessions                       |
| [`ws rm`](ws-rm.md)           | Remove workspaces and their sessions now                 |
| [`ws rename`](ws-rename.md)   | Change a workspace's display name                        |
| [`ws cache`](ws-cache.md)     | Manage, search, and resolve cached repos                 |
| [`ws review`](ws-review.md)   | Start structured reviews, wait for their reports, and check, store, and explain reviews |
| [`ws llm`](ws-llm.md)         | Print guidance for AI agents                             |

> Not every command is implemented yet; the
> [roadmap](../../README.md#roadmap) says which are. This reference
> describes the decided behavior: the command set in [0014](../adrs/0014-command-set.md),
> workspace lifetime in [0015](../adrs/0015-workspace-lifetime-follows-session.md),
> output in [0016](../adrs/0016-output-contract.md), and `ws restart` in
> [0018](../adrs/0018-restart-claude-daemon.md).

## Use cases

```console
# Start an agent in a new workspace; a person or another agent can run this
$ ws claude --name fix-login buff-bear -- "Fix the login bug"
fix-login-d8efb24f  e8cdf5d8  /Users/me/Library/Application Support/workspaces/workspaces/fix-login-d8efb24f

# Start an agent and talk to it right away
$ ws claude buff-bear@feat/new-thing

# Open a shell in a new workspace
$ ws shell buff-bear@main

# Get a pull request reviewed, then wait for the report
$ ws review start --pr 42 buff-bear
$ ws review wait review-buff-bear-42-3f2a9c1-d8efb24f

# After a reboot
$ ws restore

# After changing a token in ~/.zshrc, MCP config, or updating Claude Code (from a new shell)
$ ws restart
```

Follow, review, and stop agents with Claude Code's own commands:
`claude agents`, `claude attach <id>`, `claude logs <id>`, `claude stop <id>`.

## Sessions and lifetime

Every workspace has exactly one **session**, started when the workspace is
created: a Claude Code background session (`ws claude`) or an interactive
shell (`ws shell`). The workspace lives as long as its session
([0015](../adrs/0015-workspace-lifetime-follows-session.md)):

| Session | Workspace removed when                                         |
| ------- | -------------------------------------------------------------- |
| Agent   | the session is deleted: `claude rm`, deleting it in `claude agents`, or `ws rm` |
| Shell   | the shell exits                                                 |

A finished agent's workspace stays, so its conversation can be reviewed
next to the code. Removal ignores git state: uncommitted changes and
unpushed commits go with the workspace. Removal after `claude rm`, or after
a shell whose `ws` was killed, happens at the start of the next `ws`
command; `ws` checks at most once a minute, except `ws list`, which checks
every time, and `ws llm`, which never does
([0026](../adrs/0026-removal-pass-timing.md), proposed).

## Creating a workspace

`ws claude` and `ws shell` create the workspace the same way. With no repo
specs, the workspace is an empty directory. For each repo spec:

1. Resolve it to a canonical repo id (cache first, then config defaults).
2. Clone it into the cache if missing; otherwise fetch it.
3. Resolve the ref (branch, then tag, then commit). A full 40-character
   commit hash missing from the cache, such as a PR-only commit, is fetched
   from upstream; an abbreviated one must already be in the cache.

Then clone each repo from the cache into a directory named after the repo,
point `origin` at upstream and `cache` at the local cache, and check out
the requested ref. The repos are cloned inside
`$CONFIG_DIR/state/<id>/`; once all are ready, `ws` writes
`state/<id>/workspace.toml` and moves them into `workspaces/<id>`, so a
workspace never appears half-made. If any step fails, everything is
removed, and an interrupted creation leaves only state, which a later `ws`
command cleans up.

| Spec ref | Checkout                                    |
| -------- | ------------------------------------------- |
| none     | default branch, tracking `origin/<default>` |
| branch   | local branch tracking `origin/<branch>`     |
| tag      | detached HEAD at the tag                    |
| commit   | detached HEAD at the commit                 |

```
origin  git@github.com:bobcob7/buff-bear.git   (fetch, push)
cache   $CONFIG_DIR/cache/repos/github.com/bobcob7/buff-bear.git
```

Both commands take these options:

| Option          | Description                                                 |
| --------------- | ----------------------------------------------------------- |
| `--name <name>` | Name the workspace, and derive its id from the name instead of generating one: lowercased, accents folded, other characters become `-`, and a random 8-hex-digit suffix added (`"Fix Login"` → `fix-login-d8efb24f`). The name can be changed later with [`ws rename`](ws-rename.md); the id can't. See [0012](../adrs/0012-workspace-layout.md) and [0036](../adrs/0036-workspace-id-suffix-and-rename.md) |
| `--offline`     | Don't fetch; use the cache as-is (uncached repos/refs fail) |

And can fail with:

| Situation                                           | Error code                     | Exit |
| --------------------------------------------------- | ------------------------------ | ---- |
| Unparsable spec, the same repo listed twice, or two repos with the same name | `spec_invalid` | 2 |
| `--name` is empty after sanitizing, or holds a control character | `name_invalid`  | 2    |
| Repo not found, or short name ambiguous             | `repo_not_found`, `repo_ambiguous` | 3 |
| Ref not found                                       | `ref_not_found`                | 3    |
| No free id after several random suffixes            | `workspace_exists`             | 4    |
| Fetch or clone failed, or upstream unreachable      | `fetch_failed`, `clone_failed` | 1    |

See [0007](../adrs/0007-repo-spec-syntax-and-resolution.md),
[0008](../adrs/0008-repository-cache.md),
[0009](../adrs/0009-workspace-repository-remotes.md), and
[0012](../adrs/0012-workspace-layout.md).

## Global flags

| Flag                  | Description                                              |
| --------------------- | -------------------------------------------------------- |
| `--config-dir <path>` | Config, cache, and workspace directory (env `WS_CONFIG_DIR`, see [0011](../adrs/0011-config-dir.md)) |
| `--json`              | Machine-readable JSON on stdout (not `ws shell` or `ws llm`) |
| `--quiet`             | No progress output on stderr (`-q` is reserved for `ws list`, where it prints only IDs, as in `docker ps -q`) |
| `-v`, `--verbose`     | Show git and claude commands and timings on stderr       |
| `-h`, `--help`        | Help                                                     |

## Output and exit codes

Results go to stdout and diagnostics to stderr. Commands never prompt.
`--json` output, error codes, and exit codes are a stable contract; tables
and messages aren't ([0016](../adrs/0016-output-contract.md)).

- With `--json`, stdout gets one JSON object, compact unless stdout is a
  terminal. Lists sit under a plural key (`{"workspaces": [...]}`), and
  every object has a `warnings` array. Fields are `snake_case`, times are
  RFC 3339 UTC, and a field with no value is `null`, never missing.
- On failure, stdout is empty. stderr gets `ws: <message>`, or with
  `--json`, one line:
  `{"error": {"code": "repo_not_found", "message": "…", "details": {…}}}`.

| Exit | Meaning                       | Error codes |
| ---- | ----------------------------- | ----------- |
| 0    | Success                       |             |
| 1    | Failed                        | `fetch_failed`, `clone_failed`, `git_failed`, `claude_missing`, `claude_untrusted`, `claude_failed`, `config_invalid`, `internal`, `review_invalid`, `review_unverifiable`, `review_timeout`, `gh_missing`, `gh_auth` |
| 2    | Usage error                   | `usage`, `spec_invalid`, `name_invalid`, `profile_unknown` |
| 3    | Not found or ambiguous        | `repo_not_found`, `repo_ambiguous`, `ref_not_found`, `workspace_not_found`, `review_not_found`, `pr_not_found` |
| 4    | Refused, to protect something | `workspace_exists`, `shell_running`, `workspace_busy`, `session_mismatch` |

`ws shell` exits with the shell's exit code once the shell has started,
or 128+N when signal N killed it.
`ws claude` exits with `claude attach`'s status once it has attached
([0025](../adrs/0025-ws-claude-session-hand-off.md), proposed).

## Repo specs

```
[[host/]owner/]name[@ref]
<url>[@ref]
```

See [0007](../adrs/0007-repo-spec-syntax-and-resolution.md) for the grammar
and resolution order. Local paths and `file://` URLs aren't specs; a URL's
non-default port stays in its id (`h.com:2222/o/r`); and URLs with a
password are rejected ([0020](../adrs/0020-repo-spec-edge-cases.md)).

## Workspace arguments

Commands that take a `WORKSPACE` accept its id (`fix-login-d8efb24f`,
`ws-20261017-k3f9qa`), its path, or its agent's Claude session id
(`e8cdf5d8`).
