# ws: workspaces for agents

`ws` gives each agent, or shell, its own disposable workspace: a directory
holding git repos, each checked out at a requested branch, tag, or commit,
or nothing at all. Repos are cloned from a local cache, so creating a workspace is
fast and works with short names. Agents run as Claude Code background
sessions, so Claude Code's own commands work on them: `claude agents`,
`claude logs <id>`, `claude attach <id>`, `claude stop <id>`.

## Rules for agents

- Use only `ws machine` commands; see `ws llm machine`. They act only for
  your workspace and the ones it started, and always print JSON. The other
  commands (`ws claude`, `ws list`, `ws rm`, `ws continue`, `ws restore`,
  `ws restart`, `ws cache`, …) are for people and the desktop app: don't
  run them, even when one seems to do what you need.
- The one exception is `ws review`: to get a pull request reviewed, run
  `ws review start --json --pr <n> <repo>` and run its `wait_command` as a
  background command; see `ws llm review`. A reviewer agent also runs the
  `ws review` commands its prompt names. A review removes its own agent
  and workspace once every reviewer has reported; its report stays
  readable.
- If a `ws machine` command can't do what you need, stop and tell a
  person what you wanted to run, rather than reaching for another `ws`
  command. Above all never run `ws restart`: it interrupts every
  background agent on the machine, not only yours.
- Parse JSON output, never tables; pass `--json` to `ws review`.
- To hand work to another agent, use `ws machine spawn <spec>... --
  "<prompt>"` and keep the returned session id. Follow it with
  `claude logs <id>`.
- Don't remove, stop, or work in a workspace you didn't create unless
  told to; `ws machine rm` enforces this.
- What you start from your workspace (`ws machine spawn`, `ws review
  start`) is your child, and is removed with your workspace. Remove each
  child with `ws machine rm` as soon as you've used its result;
  `ws machine children` lists them. If work must outlive you, ask a
  person to start it.
- Removing a workspace discards everything in it, whatever its git state.
  Push, or copy out, anything worth keeping before `ws machine rm` or
  `claude rm`.

## Use cases

```console
# Hand a task to a helper agent in a new workspace, and keep its ids
$ ws machine spawn --name fix-login buff-bear -- "Fix the login bug"
{"id":"fix-login-d8efb24f","session":"e8cdf5d8","path":"/…/workspaces/fix-login-d8efb24f","parent":"plan-auth-1a2b3c4d","repos":[…],"warnings":[]}
$ claude logs e8cdf5d8

# List the workspaces you started, and their sessions
$ ws machine children

# Get your pull request reviewed; run wait_command in the background
$ ws review start --json --pr 42 buff-bear
{"id":"review-buff-bear-42-3f2a9c1-d8efb24f",…,"wait_command":"ws review wait --json review-buff-bear-42-3f2a9c1-d8efb24f"}

# Remove a helper once its work is pushed, or every one you started
$ ws machine rm fix-login-d8efb24f
$ ws machine rm --all
```

A person uses the other commands: `ws claude` and `ws shell` to start
workspaces, `ws list` and `ws rm` to manage them, `ws restore` after a
reboot, and `ws continue` for agents stopped by a usage limit.

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
- An agent that hits a 5-hour or weekly usage limit waits; a person runs
  `ws continue` to tell it to carry on once the limit resets.
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

A spec that doesn't resolve fails `ws machine spawn` with
`repo_not_found` or `repo_ambiguous`; `details.candidates` lists the
matches for an ambiguous name.

## Workspace arguments

Commands that take a workspace accept its id (`fix-login-d8efb24f`,
`ws-20261018-k3f9qa`), its path, or its agent's Claude session id
(`e8cdf5d8`). Not its name: `--name` and `ws rename` set a display name,
which needn't be unique.

## Global flags

| Flag                  | Meaning                                                   |
| --------------------- | --------------------------------------------------------- |
| `--json`              | One JSON object on stdout; every command but `ws shell` and `ws llm` |
| `--quiet`             | No progress output on stderr                              |
| `-v`, `--verbose`     | Log the git and claude commands `ws` runs, with timings, on stderr |
| `--config-dir <path>` | Config, cache, and workspace directory; env `WS_CONFIG_DIR`. Leave it unset unless told otherwise |
| `-h`, `--help`        | Flags of one command                                      |
| `--version`           | The `ws` version: `make build`'s `git describe`, else the module version or commit Go recorded |

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
- Commands that act on several items (`ws rm`, `ws machine rm`, `ws restore`, `ws restart`, `ws continue`,
  `ws cache add`, `update`, `remove`) still print a result when some items
  fail: each item has its own `error` (`{"code", "message"}` or `null`),
  and the exit code is the first failure's.

| Exit | Meaning                       | Error codes |
| ---- | ----------------------------- | ----------- |
| 0    | Success                       |             |
| 1    | Failed                        | `fetch_failed`, `clone_failed`, `git_failed`, `claude_missing`, `claude_untrusted`, `claude_failed`, `config_invalid`, `internal`, `review_invalid`, `review_unverifiable`, `review_timeout`, `gh_missing`, `gh_auth` |
| 2    | Usage error                   | `usage`, `spec_invalid`, `name_invalid`, `profile_unknown`, `profile_invalid`, `outside_workspace` |
| 3    | Not found or ambiguous        | `repo_not_found`, `repo_ambiguous`, `ref_not_found`, `workspace_not_found`, `review_not_found`, `pr_not_found`, `finding_not_found` |
| 4    | Refused, to protect something | `workspace_exists`, `shell_running`, `workspace_busy`, `session_mismatch`, `profile_exists`, `profile_builtin`, `child_kept`, `not_descendant` |

Once its shell has started, `ws shell` exits with the shell's exit code
instead. `ws claude` that attached a terminal exits with `claude attach`'s.

## Commands

Run `ws llm <command>` for one command's guide, or `ws llm --all` for
everything.

For agents:

- `ws llm machine`: the commands to use: start, list, and remove the workspaces yours started:
  - `ws llm machine spawn`: new child workspace with a Claude Code background agent
  - `ws llm machine children`: list the workspaces yours started
  - `ws llm machine rm`: remove workspaces yours started, and theirs
- `ws llm review`: structured reviews: getting your work reviewed, and the reviewer's commands:
  - `ws llm review start`: start a review of a pull request or commit
  - `ws llm review wait`: wait for a review, then print its report
  - `ws llm review report`: a review's combined report
  - `ws llm review list`: reviews, including finished ones whose agent was removed
  - `ws llm review decide`: decide to comment on, fix, or skip findings
  - `ws llm review acted`: record that decided findings were commented on or sent to fix
  - `ws llm review profiles`: the review profiles and their settings
  - `ws llm review profiles show`: one review profile, with its guide
  - `ws llm review profiles add`: add a review profile
  - `ws llm review profiles edit`: edit a review profile, or override a built-in
  - `ws llm review profiles rm`: remove a review profile, or restore a built-in
  - `ws llm review prime`: how to write and submit a review, and a profile's guide
  - `ws llm review describe`: the review document, a field, check, or topic at a time
  - `ws llm review submit`: check a review and store it
- `ws llm llm`: this guide

For people and the desktop app, not agents:

- `ws llm claude`: new workspace with a Claude Code background agent
- `ws llm shell`: new workspace with an interactive shell
- `ws llm list`: list workspaces and their sessions
- `ws llm rm`: remove workspaces and their sessions now
- `ws llm rename`: change a workspace's display name
- `ws llm hide`: hide a workspace from `ws list` and the desktop app's default views
- `ws llm unhide`: show a hidden workspace again
- `ws llm restore`: restart agents that stopped, after a reboot or crash
- `ws llm restart`: restart Claude Code's background service and every agent
- `ws llm continue`: tell agents stopped by a usage limit to carry on once it resets
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
