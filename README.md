# workspaces

`ws`: a CLI that gives each AI agent, or shell, its own disposable workspace.

A **workspace** is a short-lived directory containing git repositories, each
checked out at a requested branch, tag, or commit, or nothing at all. Repositories are cloned
from a **local cache** of upstream repos, so creating a workspace is fast, works
with short names, and doesn't re-download history every time.

```console
# Hand a task to a Claude Code agent in a new workspace
$ ws claude --name fix-login buff-bear -- "Fix the login bug"
fix-login-d8efb24f  e8cdf5d8  /Users/me/Library/Application Support/workspaces/workspaces/fix-login-d8efb24f

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
Agents learn the tool by running `ws llm`. A [desktop app](desktop/README.md)
shows every agent's status, attaches to them, and starts new ones. Common questions, including how to
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
| [`ws rename`](docs/tools/ws-rename.md)   | Change a workspace's display name                    |
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
`config.toml` schema, and how to use SSH instead of HTTPS, and
[Workspace state](docs/state.md) for what `ws` keeps under it, its locks,
and when workspaces are removed.

## Install

Download the binary for your platform from the
[latest release](https://github.com/bobcob7/workspaces/releases/latest),
check it against `SHA256SUMS`, and install it on your `PATH` as `ws`:

```console
$ os=$(uname -s | tr '[:upper:]' '[:lower:]')
$ arch=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')
$ base=https://github.com/bobcob7/workspaces/releases/latest/download
$ curl -fLO "$base/ws-$os-$arch" -fLO "$base/SHA256SUMS"
$ shasum -a 256 --check --ignore-missing SHA256SUMS
$ mkdir -p ~/.local/bin && install -m 755 "ws-$os-$arch" ~/.local/bin/ws
```

On Linux without `shasum`, check with
`sha256sum --check --ignore-missing SHA256SUMS`.

The macOS binaries aren't signed with an Apple Developer ID or notarized;
the arm64 one has only the Go linker's ad-hoc signature. Downloaded with
`curl` as above, they run. Downloaded with a browser, Gatekeeper blocks
them; clear the quarantine flag with
`xattr -d com.apple.quarantine ws-darwin-<arch>` before installing.

Releases have binaries for darwin and linux, on amd64 and arm64, built
with `CGO_ENABLED=0`, so the linux ones are static
([0002](docs/adrs/0002-implementation-language.md)). To build from source
instead, run `make build` and copy `bin/ws`.

## Requirements

- `git` 2.23 or newer on `PATH`; `ws` shells out to it
  ([0003](docs/adrs/0003-use-git-cli.md))
- Claude Code 2.1.302 or newer, with background sessions (`claude --bg`),
  on `PATH` for `ws claude`; the version is enforced. Last tested with 2.1.304
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
- [x] **Cache**: bare clones, refspecs, fetch with prune, locking; short-name
  and ref resolution; `ws cache`; Time Machine exclusion for
  `cache/` ([0007](docs/adrs/0007-repo-spec-syntax-and-resolution.md),
  [0008](docs/adrs/0008-repository-cache.md), [0011](docs/adrs/0011-config-dir.md))
- [x] **Workspaces**: ids and `--name`, `state/<id>/workspace.toml`,
  hard-linked clones with `origin` and `cache` remotes, the deletion guard;
  Time Machine exclusion for `workspaces/`; `ws cache prune`
  ([0009](docs/adrs/0009-workspace-repository-remotes.md),
  [0012](docs/adrs/0012-workspace-layout.md),
  [0017](docs/adrs/0017-clones-not-worktrees.md))
- [x] **`ws shell`**: run the shell under the session lock, remove the
  workspace when it exits
  ([0015](docs/adrs/0015-workspace-lifetime-follows-session.md),
  [0023](docs/adrs/0023-shell-session-keeps-creation-lock.md))
- [x] **Claude Code client** `internal/claude`: the only place claude runs;
  `claude --bg`, `agents --json`, `stop`, `rm`, `respawn`, `daemon`, and the
  2.1.302 version check ([0015](docs/adrs/0015-workspace-lifetime-follows-session.md),
  [0024](docs/adrs/0024-claude-code-client.md))
- [x] **`ws claude`**, including the trust error and attaching without a
  prompt ([0014](docs/adrs/0014-command-set.md),
  [0025](docs/adrs/0025-ws-claude-session-hand-off.md))
- [x] **Lifetime**: remove workspaces whose session is gone; `ws list`,
  `ws rm` ([0015](docs/adrs/0015-workspace-lifetime-follows-session.md),
  [0026](docs/adrs/0026-removal-pass-timing.md), [state](docs/state.md))
- [x] **`ws restore`** ([0015](docs/adrs/0015-workspace-lifetime-follows-session.md),
  [0027](docs/adrs/0027-respawn-lock-and-record-versions.md)):
  respawns, one at a time, each stopped session that Claude Code lists in
  its own workspace
- [x] **`ws restart`**, under the respawn lock
  ([0018](docs/adrs/0018-restart-claude-daemon.md),
  [0027](docs/adrs/0027-respawn-lock-and-record-versions.md))
- [x] **`ws llm`**: embedded guides with the coverage test, and
  `workspaces/CLAUDE.md`, written if missing
  ([0014](docs/adrs/0014-command-set.md))
- [x] **Desktop app** ([0029](docs/adrs/0029-desktop-app.md), proposed):
  agents grouped by status, attach and reply, new agents and shells
- [x] **Release**: `make dist` binaries for darwin and linux, amd64 and
  arm64, with `SHA256SUMS`; CI, and a GitHub release for each `v*` tag
  ([0002](docs/adrs/0002-implementation-language.md))
- [ ] **Reviews with loam-refinery** ([design](docs/design/loam-refinery-reviews/README.md),
  [0037](docs/adrs/0037-loam-refinery-as-external-binary.md)–[0041](docs/adrs/0041-desktop-acts-on-findings.md),
  proposed): `ws review start`, `wait`, and `report`; one subagent per
  reviewer profile; findings triaged, commented, and fixed from the
  desktop app

## Development

```console
$ make build      # bin/ws
$ make test
$ make lint       # golangci-lint; `make fmt` applies its formatters
$ make dist       # dist/: a binary per platform, and SHA256SUMS
$ make desktop    # desktop app bundle; `make desktop-dev`, `make desktop-test`
```

CI lints, tests, and builds every pull request on Linux and macOS. To
release, push a tag such as `v0.1.0`. The
[release workflow](.github/workflows/release.yml) runs CI, then `make dist`,
and attaches the binaries and checksums to a GitHub release for the tag. A
tag with a `-`, such as `v0.2.0-rc.1`, is a prerelease.

```
cmd/ws/                       # main: streams, signals, exit code
internal/
  cli/                        # cobra commands and the output contract (0005, 0016)
  config/                     # CONFIG_DIR and config.toml (0011)
  git/                        # the only place git is executed (0003)
  claude/                     # the only place claude is executed (0015, 0018, 0024)
  repo/                       # repo@ref specs, canonical ids, upstream URLs (0007, 0019, 0020)
  cache/                      # bare clones, locking, spec and ref resolution (0007, 0008)
  workspace/                  # creating, finding, and removing workspaces (0009, 0012, 0017)
  lifetime/                   # removing workspaces whose session is gone; ws list, ws rm, ws restore (0015, 0025, 0027)
  proc/                       # when processes started, from the operating system (0018, 0026)
  shell/                      # ws shell's interactive shell: lock, signals, exit status (0015, 0023)
  lockfile/                   # flock locks, exclusive and shared (0008)
  writeonce/                  # files written once, never overwritten: config.toml, workspaces/CLAUDE.md
  backup/                     # Time Machine exclusion (0011)
  gittest/                    # local upstreams for tests, via insteadOf (0020)
  exectest/                   # fake executables for tests
desktop/                      # Tauri desktop app: Rust backend in src-tauri/, React frontend in src/ (0029)
docs/
  adrs/                       # architecture decision records
  tools/                      # command reference
```

## Contributing

Significant design decisions are recorded as ADRs. Read the
[ADR process](docs/adrs/README.md) before proposing a change to behavior
described in an accepted ADR.
