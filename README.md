# workspaces

`ws`: a CLI that gives each AI agent, or shell, its own disposable workspace.

A **workspace** is a short-lived directory containing one or more git repositories,
each checked out at a requested branch, tag, or commit. Repositories are cloned
from a **local cache** of upstream repos, so creating a workspace is fast, works
with short names, and doesn't re-download history every time.

```console
# Hand a task to a Claude Code agent in a new workspace
$ ws claude --name fix-login buff-bear -- "Fix the login bug"
fix-login  e8cdf5d8  /Users/me/Library/Application Support/workspaces/workspaces/fix-login

# Open a shell in a new workspace with two repos
$ ws shell buff-bear@feat/new-thing bobcob7/other-repo@v1.2.0

# Restart agents after a reboot
$ ws restore

# Give every agent a new environment (a rotated token, new MCP servers,
# a Claude Code update); run from a shell that has the change
$ ws restart
```

Agents run as Claude Code background sessions, so `claude agents`,
`claude attach`, and `claude logs` work on them. Each workspace lives as long
as its session: an agent's workspace is removed when its session is deleted
(`claude rm` or `ws rm`), so its conversation can be reviewed next to the
code until then, and a shell's workspace is removed when the shell exits.
Agents learn the tool by running `ws llm`. Common questions, including how to
set up Claude Code's folder trust, are in the [Q&A](docs/qa.md).

> **Status:** in progress; see the [roadmap](#roadmap). The commands
> described here are decided in the [ADRs](docs/adrs/README.md).

## Concepts

### Repository specs

Every command that takes a repository accepts a **repo spec**:

```
[[host/]owner/]name[@ref]
<url>[@ref]
```

| Spec                                   | Resolves to                                         |
| -------------------------------------- | --------------------------------------------------- |
| `buff-bear`                            | `github.com/bobcob7/buff-bear` (via cache or config) |
| `bobcob7/buff-bear`                    | `github.com/bobcob7/buff-bear`                      |
| `github.com/bobcob7/buff-bear`         | `github.com/bobcob7/buff-bear`                      |
| `git@github.com:bobcob7/buff-bear.git` | `github.com/bobcob7/buff-bear`                      |
| `buff-bear@feat/new-thing`             | branch `feat/new-thing`                             |
| `buff-bear@v1.2.0`                     | tag `v1.2.0`                                        |
| `buff-bear@3f2a9c1`                    | commit `3f2a9c1`                                    |

Short names are resolved by searching the cache first, then by trying the
default owners from configuration. Ambiguous names are an error, never a guess.
See [0007](docs/adrs/0007-repo-spec-syntax-and-resolution.md).

### Repository cache

Each upstream repository is stored once as a bare clone under the cache
directory (default `$CONFIG_DIR/cache/repos/<host>/<owner>/<name>.git`). The
cache is the index: listing, searching, and short-name resolution are derived
from its layout. Before a workspace is created, every involved cache repo is
fetched so workspaces start from current upstream state.
See [0008](docs/adrs/0008-repository-cache.md).

### Workspaces

A workspace is a directory under the workspace root (default
`$CONFIG_DIR/workspaces`) containing one directory per repository and
nothing else. `ws` keeps its own state about the workspace (metadata,
session record) in `$CONFIG_DIR/state/<id>/`
([0012](docs/adrs/0012-workspace-layout.md)). Each repository is a
hard-linked clone of the cache, not a git worktree
([0017](docs/adrs/0017-clones-not-worktrees.md)), with two remotes:

| Remote   | URL                                  | Purpose                              |
| -------- | ------------------------------------ | ------------------------------------ |
| `origin` | upstream (e.g. `git@github.com:...`) | default for fetch, pull, and push    |
| `cache`  | local cache path                     | fast, offline fetches of known refs  |

See [0009](docs/adrs/0009-workspace-repository-remotes.md).

## Commands

`ws` is a single binary with subcommands
([0014](docs/adrs/0014-command-set.md)):

| Command                                  | Purpose                                              |
| ---------------------------------------- | ---------------------------------------------------- |
| [`ws claude`](docs/tools/ws-claude.md)   | New workspace with a Claude Code background agent    |
| [`ws shell`](docs/tools/ws-shell.md)     | New workspace with an interactive shell              |
| [`ws restore`](docs/tools/ws-restore.md) | Restart agents after a reboot or crash               |
| [`ws restart`](docs/tools/ws-restart.md) | Restart all agents with a fresh environment          |
| [`ws list`](docs/tools/ws-list.md)       | List workspaces and their sessions                   |
| [`ws rm`](docs/tools/ws-rm.md)           | Remove workspaces and their sessions now             |
| [`ws cache`](docs/tools/ws-cache.md)     | Manage, search, and resolve cached repos             |
| [`ws llm`](docs/tools/ws-llm.md)         | Print guidance for AI agents                         |

Commands are non-interactive, write results to stdout and diagnostics to
stderr, and take `--json` for machine-readable output
([0016](docs/adrs/0016-output-contract.md)). See the
[command reference](docs/tools/README.md).

## Locations and configuration

| What           | Where                                                     |
| -------------- | --------------------------------------------------------- |
| `CONFIG_DIR`   | `--config-dir`, else `$WS_CONFIG_DIR`, else `os.UserConfigDir()/workspaces` (`~/.config/workspaces` on Linux, `~/Library/Application Support/workspaces` on macOS) |
| Config file    | `$CONFIG_DIR/config.toml`, generated with comments on first run; [schema](docs/config.md#schema) |
| Repo cache     | `$CONFIG_DIR/cache/`                                      |
| Workspaces     | `$CONFIG_DIR/workspaces/`; each removed when its session is gone ([0015](docs/adrs/0015-workspace-lifetime-follows-session.md)). Trust it once in Claude Code |
| Workspace state | `$CONFIG_DIR/state/<id>/`: metadata and the session record |
| Agent pointer  | `$CONFIG_DIR/workspaces/CLAUDE.md`: tells agents to run `ws llm` |

See [Configuration](docs/config.md) for how `CONFIG_DIR` is chosen, the
`config.toml` schema, and how to use SSH instead of HTTPS.

## Requirements

- `git` 2.23 or newer on `PATH`; `ws` shells out to it
  ([0003](docs/adrs/0003-use-git-cli.md))
- Claude Code with background sessions (`claude --bg`) on `PATH` for `ws claude`;
  tested with 2.1.302
- Go 1.27+ to build ([0002](docs/adrs/0002-implementation-language.md))

## Roadmap

Each step builds on the ones before it. A command is done when it matches
its page in [`docs/tools`](docs/tools/README.md), with unit tests.

- [x] **CLI skeleton**: every command and flag as a stub; error codes, exit
  codes, and JSON output; golangci-lint and the Makefile
  ([0005](docs/adrs/0005-cli-framework.md), [0016](docs/adrs/0016-output-contract.md))
- [x] **Config**: resolve `CONFIG_DIR`; read `config.toml`, writing a
  commented default when it's missing; `--config-dir` and `WS_CONFIG_DIR`
  ([0011](docs/adrs/0011-config-dir.md))
- [x] **Git runner** `internal/git`: the only place git runs, with
  `GIT_TERMINAL_PROMPT=0` and the 2.23 version check
  ([0003](docs/adrs/0003-use-git-cli.md))
- [x] **Repo specs**: parse `repo@ref`, canonical ids, upstream HTTPS URLs
  ([0007](docs/adrs/0007-repo-spec-syntax-and-resolution.md),
  [0019](docs/adrs/0019-https-upstream-urls.md))
- [ ] **Cache**: bare clones, refspecs, fetch with prune, locking; short-name
  and ref resolution; `ws cache`; Time Machine exclusion for `cache/`
  ([0011](docs/adrs/0011-config-dir.md))
  ([0007](docs/adrs/0007-repo-spec-syntax-and-resolution.md),
  [0008](docs/adrs/0008-repository-cache.md))
- [ ] **Workspaces**: ids and `--name`, `state/<id>/workspace.toml`,
  hard-linked clones with `origin` and `cache` remotes, the deletion guard;
  Time Machine exclusion for `workspaces/`
  ([0009](docs/adrs/0009-workspace-repository-remotes.md),
  [0012](docs/adrs/0012-workspace-layout.md),
  [0017](docs/adrs/0017-clones-not-worktrees.md))
- [ ] **`ws shell`**: run the shell under the session lock, remove the
  workspace when it exits
  ([0015](docs/adrs/0015-workspace-lifetime-follows-session.md))
- [ ] **Claude Code client**: `claude --bg`, `agents --json`, `stop`, `rm`,
  `respawn`, `daemon` ([0015](docs/adrs/0015-workspace-lifetime-follows-session.md))
- [ ] **`ws claude`**, including the trust error and attaching without a
  prompt ([0014](docs/adrs/0014-command-set.md))
- [ ] **Lifetime**: remove workspaces whose session is gone; `ws list`,
  `ws rm` ([0015](docs/adrs/0015-workspace-lifetime-follows-session.md))
- [ ] **`ws restore`** ([0015](docs/adrs/0015-workspace-lifetime-follows-session.md))
- [ ] **`ws restart`** ([0018](docs/adrs/0018-restart-claude-daemon.md))
- [ ] **`ws llm`**: embedded guides with the coverage test, and
  `workspaces/CLAUDE.md` ([0014](docs/adrs/0014-command-set.md))
- [ ] **Release**: `make dist` binaries for darwin and linux, amd64 and
  arm64 ([0002](docs/adrs/0002-implementation-language.md))

## Development

```console
$ make build      # bin/ws
$ make test
$ make lint       # golangci-lint; `make fmt` applies its formatters
```

```
cmd/ws/                       # main: streams, signals, exit code
internal/
  cli/                        # cobra commands and the output contract (0005, 0016)
  config/                     # CONFIG_DIR and config.toml (0011)
  git/                        # the only place git is executed (0003)
  spec/                       # repo@ref parsing, canonical ids, upstream URLs (0007, 0019)
docs/
  adrs/                       # architecture decision records
  tools/                      # command reference
```

## Contributing

Significant design decisions are recorded as ADRs. Read the
[ADR process](docs/adrs/README.md) before proposing a change to behavior
described in an accepted ADR.
