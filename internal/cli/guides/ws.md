# ws: workspaces for agents

`ws` gives each agent, or shell, its own disposable workspace: a directory
holding git repos, each checked out at a requested branch, tag, or commit,
or nothing at all. Repos are cloned from a local cache, so creating a workspace is
fast and works with short names. Agents run as Claude Code background
sessions, so Claude Code's own commands work on them: `claude agents`,
`claude logs <id>`, `claude attach <id>`, `claude stop <id>`.

## Rules for agents

- To hand work to another agent, use `ws claude --json <spec>... -- "<prompt>"`
  and keep the returned session id. Follow it with `claude logs <id>`.
- Don't remove, stop, or work in a workspace you didn't create unless told
  to.
- Parse `--json` output, never the tables.
- Never run `ws restart` unless told to: it interrupts every background
  agent on the machine, not only yours.
- Removing a workspace discards everything in it, whatever its git state.
  Push, or copy out, anything worth keeping before `ws rm` or `claude rm`.

## Use cases

```console
# Hand a task to a new agent in a new workspace, and keep its ids
$ ws claude --json --name fix-login buff-bear -- "Fix the login bug"
{"id":"fix-login","session":"e8cdf5d8","path":"/…/workspaces/fix-login","repos":[…],"warnings":[]}
$ claude logs e8cdf5d8

# See every workspace and its session
$ ws list --json

# Remove a workspace you created, once its work is pushed
$ ws rm --json fix-login
```

A person also uses `ws shell [<spec>...]` for a throwaway shell, and
`ws restore` after a reboot.

## Sessions and lifetime

Every workspace has exactly one session, started when the workspace is
created, and lives exactly as long as it:

| Session                 | Workspace removed when                                      |
| ----------------------- | ----------------------------------------------------------- |
| agent (`ws claude`)     | the Claude Code session is deleted: `claude rm <id>`, deleting it in `claude agents`, or `ws rm` |
| shell (`ws shell`)      | the shell exits                                             |

- An agent that has finished, or was stopped with `claude stop`, keeps its
  workspace, so its conversation can be reviewed next to the code.
  `claude stop` doesn't remove anything; `claude rm` does.
- Removal ignores git state: uncommitted changes, unpushed commits, and
  stashes go with the workspace.
- After `claude rm`, the workspace is removed at the start of a later `ws`
  command. `ws` checks at most once a minute, except `ws list`, which
  checks every time, and `ws llm`, which never does.
- After a reboot, agents are stopped, not gone; `ws restore` brings them
  back with their conversations.
- A session starts in the workspace, or in its repo's directory when the
  workspace has exactly one repo.
- Each repo in a workspace is a full clone with two remotes: `origin`
  (upstream, for fetch and push) and `cache` (the local cache). Push to
  `origin`.
- `ws` writes nothing inside a workspace. Its records live in
  `$CONFIG_DIR/state/<id>/`.

## Repo specs

```
[[host/]owner/]name[@ref]
<url>[@ref]
```

- `buff-bear`: a short name, found in the cache first, then under the
  configured default owners. An ambiguous name is an error, never a guess.
- `bobcob7/buff-bear`: on the default host (normally `github.com`).
- `github.com/bobcob7/buff-bear`, `git@github.com:bobcob7/buff-bear.git`,
  `https://github.com/bobcob7/buff-bear`: fully qualified.
- `@ref` is a branch, then a tag, then a commit. Without one, the default
  branch is checked out. A branch is checked out tracking `origin/<branch>`;
  a tag or commit as a detached HEAD.
- Local paths and `file://` URLs aren't specs, and URLs with a password are
  rejected.

Use `ws cache resolve <spec>` to see what a spec resolves to, and
`ws cache search <text>` to find a repo already cached.

## Workspace arguments

Commands that take a workspace accept its id (`fix-login`,
`ws-20261018-k3f9qa`), its path, or its agent's Claude session id
(`e8cdf5d8`).

## Global flags

| Flag                  | Meaning                                                   |
| --------------------- | --------------------------------------------------------- |
| `--json`              | One JSON object on stdout; every command but `ws shell` and `ws llm` |
| `--quiet`             | No progress output on stderr                              |
| `-v`, `--verbose`     | Log the git and claude commands `ws` runs, with timings, on stderr |
| `--config-dir <path>` | Config, cache, and workspace directory; env `WS_CONFIG_DIR`. Leave it unset unless told otherwise |
| `-h`, `--help`        | Flags of one command                                      |
| `--version`           | The `ws` version                                          |

## Output and exit codes

Results go to stdout and diagnostics to stderr. Commands never prompt.
`--json` output, error codes, and exit codes are stable; tables and
messages aren't.

- With `--json`, stdout gets one JSON object. Lists sit under a plural key
  (`{"workspaces": [...]}`), and every object has a `warnings` array of
  strings: problems that didn't stop the command. Fields are `snake_case`,
  times are RFC 3339 UTC, and a field with no value is `null`, never
  missing. Accept enum values and fields you don't know.
- On failure, stdout is empty, and stderr gets `ws: <message>`, or with
  `--json` one line:
  `{"error": {"code": "repo_not_found", "message": "…", "details": {…}}}`.
  `details` is `null` or an object, such as `{"stderr": "…"}` when git or
  claude failed, or `{"candidates": [...]}` for an ambiguous name.
- Commands that act on several items (`ws rm`, `ws restore`, `ws restart`,
  `ws cache add`, `update`, `remove`) still print a result when some items
  fail: each item has its own `error` (`{"code", "message"}` or `null`),
  and the exit code is the first failure's.

| Exit | Meaning                       | Error codes |
| ---- | ----------------------------- | ----------- |
| 0    | Success                       |             |
| 1    | Failed                        | `fetch_failed`, `clone_failed`, `git_failed`, `claude_missing`, `claude_untrusted`, `claude_failed`, `config_invalid`, `internal` |
| 2    | Usage error                   | `usage`, `spec_invalid`, `name_invalid` |
| 3    | Not found or ambiguous        | `repo_not_found`, `repo_ambiguous`, `ref_not_found`, `workspace_not_found` |
| 4    | Refused, to protect something | `workspace_exists`, `shell_running`, `workspace_busy`, `session_mismatch` |

Once its shell has started, `ws shell` exits with the shell's exit code
instead. `ws claude` that attached a terminal exits with `claude attach`'s.

## Commands

Run `ws llm <command>` for one command's guide, or `ws llm --all` for
everything.

- `ws llm claude`: new workspace with a Claude Code background agent
- `ws llm shell`: new workspace with an interactive shell, for people
- `ws llm list`: list workspaces and their sessions
- `ws llm rm`: remove workspaces and their sessions now
- `ws llm restore`: restart agents that stopped, after a reboot or crash
- `ws llm restart`: restart Claude Code's background service and every agent; only when told to
- `ws llm cache`: the repo cache, and its subcommands:
  - `ws llm cache add`: clone repos into the cache
  - `ws llm cache update`: fetch cached repos from upstream
  - `ws llm cache list`: list cached repos
  - `ws llm cache search`: search cached repos, branches, and tags
  - `ws llm cache refs`: a cached repo's branches and tags, with their last commits
  - `ws llm cache resolve`: resolve specs to canonical repo ids
  - `ws llm cache path`: a cached repo's directory
  - `ws llm cache remove`: delete cached repos
  - `ws llm cache prune`: delete commit refs no workspace uses
- `ws llm llm`: this guide
