# ws machine

Commands for agents: start, wait for, list, and remove the workspaces your
own workspace started; record your own verdict; and manage the roles
children are started from.

## Synopsis

```
ws machine spawn [OPTIONS] [--runner claude|ws] [--endpoint <name>] [--role <name> [--var KEY=VALUE]...] [<spec>...] -- "<prompt>" [<claude args>... | --model <m>]
ws machine wait [--any] [--timeout <duration>] <WORKSPACE>...
ws machine result <VERDICT> [--file <path> | --file -]
ws machine children [--all]
ws machine logs [--tail <n>] <WORKSPACE>
ws machine rm [--dry-run] [--keep-children] (<WORKSPACE>... | --all)
ws machine roles
ws machine roles show <name>
ws machine roles add <name> --description <d> [ROLE FLAGS] (--prompt-file <f> | -)
ws machine roles edit <name> [ROLE FLAGS] [--prompt-file <f> | -]
ws machine roles rm <name>
ws machine memory list
ws machine memory get <KEY>
ws machine memory set [--expect-rev <N>] [--file <PATH> | --file -] <KEY> [<VALUE>]
ws machine memory rm [--expect-rev <N>] <KEY>
ws machine progress set [--name <name>] [--total <n>] [--done <n>] [--note <text>]
ws machine progress step [--by <n>] [--note <text>]
ws machine progress show
```

## Description

`ws machine` is for agents running in a workspace, and is the only part of
`ws` they run, apart from [`ws review`](ws-review.md) to get their work
reviewed. People and the desktop app use [`ws claude`](ws-claude.md),
[`ws list`](ws-list.md), [`ws rm`](ws-rm.md), and the other commands
([0046](../adrs/0046-ws-machine.md)). `ws llm` tells agents not to run
those, and `ws --help` lists them under "Commands for people and the
desktop app".

Every `ws machine` command acts for the **caller**: the workspace
containing the working directory. Outside a workspace, it fails with
`outside_workspace`. Each command acts only on the caller's
**descendants**, the workspaces it started and theirs
([0044](../adrs/0044-workspace-trees.md)). `roles` is the exception: roles
are the user's configuration, not part of a tree, and `roles` doesn't
need a workspace.

Output is always JSON, as if `--json` were given; `--json` is accepted
and changes nothing. Errors are one JSON line on stderr.

### Results and the root

A child records its verdict with `ws machine result`. The record is kept
in `$CONFIG_DIR/state/<root>/machine/<child-id>/`, where `<root>` is the
top of the child's tree: the workspace reached by following `parent`
until a workspace has none, or its parent is no longer a workspace. So a
result outlives the child's own workspace, and is deleted with the root
([0050](../adrs/0050-machine-wait-results-and-roles.md)).

A result's **outcome** is `pass` or `fail`. It is `pass` when the verdict
is one of the child's pass verdicts: its role's `pass` list, or for a
child started without a role, `PASS` or `APPROVED`. Every other verdict is
`fail`, except a loop run's own `BLOCKED` verdict, whose outcome is
`blocked` ([`ws machine result`](#ws-machine-result)).

The **result object**, used by `wait`, `children`, and `result`:

```json
{"verdict": "PASS", "outcome": "pass",
 "report": "/…/state/orch-1a2b3c4d/machine/t1-impl-d8efb24f/report.md",
 "recorded_at": "2026-11-06T18:04:05Z"}
```

`report` is the stored copy's absolute path, or `null` when no report was
given. Read it with your file tools.

## ws machine spawn

Creates a workspace and starts a Claude Code background agent in it, as
the caller's child: `ws claude --json --parent <caller>`. Everything
after `--` goes to `claude`; the first argument is the prompt. `spawn`
never attaches a terminal. With `--runner ws` it starts `ws`'s own runner
instead, as described under [Runners](#runners).

| Option              | Description                                                  |
| ------------------- | ------------------------------------------------------------ |
| `--name <name>`     | Name the workspace and derive its id from the name, as for `ws claude` |
| `--offline`         | Don't fetch; use the cache as-is                             |
| `--hidden`          | Hide the workspace from `ws list` and the desktop app's default views. Off by default: children show in the workspace tree |
| `--role <name>`     | Start the child from this role: its prompt template, model, `claude` arguments, and verdicts |
| `--var KEY=VALUE`   | A value for the role's prompt; repeatable; only with `--role` |
| `--runner claude\|ws` | Which agent runs the child. Default: the role's `runner` in `[runner.roles]`, else `claude` |
| `--endpoint <name>` | With `--runner ws`: another endpoint from `config.toml`, instead of the model's. `usage` with `claude` |

Without `--role`, the prompt after `--` is required (`usage`).

**With `--role`:**

1. The role's prompt is rendered as a Go `text/template` with
   `missingkey=error`, with `{{.Role}}` (the role's name) and
   `{{.Vars.KEY}}` for each `--var`. `{{index .Vars "KEY"}}` renders a var
   that wasn't given as empty. Every `--var` key must be one of the role's
   `vars`. Keys are `[A-Za-z_][A-Za-z0-9_]*`, at most 64 bytes; values at
   most 4096 bytes; at most 32 vars; a key given twice is `usage`.
2. The prompt after `--` is optional. When it isn't blank, it is appended
   to the rendered prompt after one blank line (`"\n\n"`), verbatim.
3. `claude` gets, in order: the prompt; `--model <model>` when the role
   has a model; the role's `claude_args`; then the arguments after the
   prompt. A `--model` there overrides the role's. To pass arguments with
   no extra prompt, give an empty one: `-- "" --effort high`.
4. Once the child has started, `spawn` writes its `spawn.toml` (the role,
   model, verdict lists, and vars) under the root. If that fails, it's a
   warning: the child is running, and its result is judged by the default
   verdicts.

Every check in steps 1 and 2 happens before anything is created.

Prints `ws claude --json`'s object plus `role`, `runner` and `model`:
`{"id", "session", "path", "parent", "repos", "warnings", "role", "runner", "model", "ws_runner"}`.
`role` is the role's name, or `null`. `runner` is `claude` or `ws`, and
`model` is the child's model, or `null`. `ws_runner` is `null` for a Claude
child; for a `ws` child it has `model_alias`, `endpoint`, `url`, `api`,
`context_tokens` and `mcp_servers`.

### Runners

A child runs on Claude Code (`claude`, the default) or on `ws`'s own runner
(`ws`), which drives a model configured in `config.toml`, such as one on
LM Studio, Ollama or llm-router. `--runner` chooses; without it,
`runner.roles.<role>.runner` does for `--role`, else `claude`.

A `ws` child takes `--model <alias>`, or a raw model id, after the prompt,
and nothing else there (`usage` otherwise). Without it, the role's model
is used, else `runner.model`. Before anything is created, the daemon
(started on demand, unless `WS_DAEMON_AUTOSTART=0`) checks that the
endpoint answers, serves the model and, when it can tell, that the model
supports tools: `endpoint_unreachable`, `model_not_served`,
`model_without_tools`. A failed check fails the spawn and nothing else
starts. There is no fallback to Claude Code: spawn again with
`--runner claude` if you want the task on Claude.

A `claude` child never uses the daemon, and never reads the runner's
configuration, so a broken `[runner]` table can't stop it.

## ws machine wait

Waits for children to finish, then prints each one's status and result.

| Option                 | Description                                                  |
| ---------------------- | ------------------------------------------------------------ |
| `--any`                | Return when any one child has finished, not all of them      |
| `--timeout <duration>` | Give up after this long, such as `90m`; default `60m`; must be positive |

Each `WORKSPACE` is an id, path, or agent session id, and must be one of
the caller's descendants. A child whose workspace was already removed can
be named by its id while its result is kept under the root: its status is
`gone`. Naming the same workspace twice waits once. All of this is
checked before waiting; a failure fails the whole command.

Every 5 seconds, `wait` reads each child's session, its descendants'
sessions, and the results. A child is **finished** when:

- its workspace is gone; or
- its session is `idle` or `stopped`, none of its descendants is `working`
  or `starting`, and either it has recorded a result, or it was already
  in that state at the previous read.

The second read stops a child that is between turns, waiting on its own
background command, from counting as finished. A child whose session
`ws` can't read (`unknown`) isn't finished.

`wait` returns when every child is finished, or with `--any` when at
least one is, or at the timeout. Run it as a background command and read
its output when it exits.

```json
{
  "children": [
    {"id": "t1-impl-d8efb24f", "session": "e8cdf5d8", "role": "implementer",
     "status": "idle", "finished": true, "outcome": "pass",
     "result": {"verdict": "PASS", "outcome": "pass",
                "report": "/…/state/orch-1a2b3c4d/machine/t1-impl-d8efb24f/report.md",
                "recorded_at": "2026-11-06T18:04:05Z"},
     "error": null},
    {"id": "t2-impl-3be0a9c1", "session": "a71c09f2", "role": "implementer",
     "status": "working", "finished": false, "outcome": "pending", "result": null,
     "error": {"code": "wait_timeout", "message": "t2-impl-3be0a9c1 is still working after 1h0m0s; run ws machine wait again to keep waiting"}}
  ],
  "warnings": []
}
```

| Field      | Meaning                                                           |
| ---------- | ----------------------------------------------------------------- |
| `id`       | The child's workspace id                                          |
| `session`  | Its agent's Claude Code session id, or `null` (a shell, or unknown for a gone child) |
| `role`     | The role it was spawned with, or `null`                           |
| `status`   | `working`, `idle`, `starting`, `stopped`, `unknown`, `running` (a shell), or `gone` |
| `finished` | Whether it finished, as above                                     |
| `outcome`  | `pass` or `fail` from its result; `missing` when finished with no result; `pending` when not finished |
| `result`   | The result object, or `null`                                      |
| `error`    | `null`, or `{"code", "message"}`                                  |

Items are in the order the children were named. Each item's `error`:

- `child_failed` when it finished and its outcome is `fail` or `missing`;
- `wait_timeout` when the timeout passed and it hadn't finished. With
  `--any`, unfinished children get no error unless none finished.

The exit code is the first item error's (both are exit 1), or 0. So exit
0 means every child waited for finished and passed.

## ws machine result

Records the caller's own verdict, and optionally a report, for whoever
waits on it. Run it as your last step.

| Option          | Description                                                     |
| --------------- | --------------------------------------------------------------- |
| `--file <path>` | Copy this file as the report; 1 byte to 1 MiB                   |
| `--file -`      | Read the report from stdin                                      |

`VERDICT` is 1-32 characters of letters, digits, and `_`, starting with a
letter, and is stored in upper case (`pass` becomes `PASS`). If the
caller was spawned with a role, the verdict must be one of the role's
`pass` or `fail` verdicts. Running `result` again replaces the verdict and
the report; without `--file`, the earlier report is deleted.

A loop run (a workspace `ws loop` started, not one of its children) can
record `BLOCKED` to ask a person for help: `--file` is required and holds
the question. Its outcome is `blocked`. Only a later `ws machine result`
clears it. Anywhere else `BLOCKED` is `result_invalid`
([0057](../adrs/0057-loop-visibility-and-blocked-runs.md)).

```json
{"id": "t1-impl-d8efb24f", "root": "orch-1a2b3c4d", "role": "implementer",
 "result": {"verdict": "PASS", "outcome": "pass",
            "report": "/…/state/orch-1a2b3c4d/machine/t1-impl-d8efb24f/report.md",
            "recorded_at": "2026-11-06T18:04:05Z"},
 "warnings": []}
```

`id` is the caller, `root` the root of its tree, and `role` the role it
was spawned with, or `null`. `result.outcome` is `pass` or `fail`, or
`blocked` for a loop run's `BLOCKED`.

## ws machine children

Lists the caller's direct children, or every descendant with `--all`,
hidden ones included, oldest first. Prints `ws list --json`'s object,
`{"workspaces": [...], "hidden_count": 0, "warnings": []}`, with each
workspace's `parent`, and two more fields on each workspace, after the
others:

| Field    | Meaning                                         |
| -------- | ----------------------------------------------- |
| `role`   | The role it was spawned with, or `null`         |
| `result` | Its result object, or `null` until it records one |

| Option  | Description                               |
| ------- | ----------------------------------------- |
| `--all` | Every descendant, not only direct children |

## ws machine logs

Prints a descendant's log as JSON: a `ws` child's transcript events, or a
Claude child's `claude logs` text. Use it instead of `claude logs`, which
doesn't know runner sessions (`rn-…`).

## ws machine rm

Removes workspaces as [`ws rm`](ws-rm.md) does, with their descendants,
deepest first. Each `WORKSPACE` must be a descendant of the caller: any
other, the caller itself included, is refused as its item's error,
`not_descendant`, and the rest are still removed. Prints `ws rm --json`'s
object, `{"workspaces": [{"id", "parent", "removed", "error"}], "warnings"}`,
one item per workspace removed or refused. `parent` is set on a
descendant removed with a target, and `null` on a workspace named. A
removed child's result stays under the root.

| Option            | Description                                               |
| ----------------- | --------------------------------------------------------- |
| `--all`           | Remove every direct child, and with them their descendants |
| `--dry-run`       | Print what would be removed, change nothing                |
| `--keep-children` | Make the target's children children of the caller instead of removing them |

`WORKSPACE` is an id, path, or agent session id, as for other commands.

## ws machine roles

A **role** is what a child is started as: a prompt template, a model,
more `claude` arguments, and the verdicts that mean pass and fail. Roles
are Markdown files in `$CONFIG_DIR/machine/roles/<name>.md`, over three
built into `ws`. A file with a built-in's name overrides it.

```markdown
---
description: Implements one task from a tasks file, pushes a branch, and records PASS or FAIL
model: haiku
pass: ["PASS"]
fail: ["FAIL"]
vars: ["TASK", "TASKS", "BASE", "BRANCH"]
claude_args: ["--permission-mode", "auto"]
---

You implement exactly one task, {{.Vars.TASK}}, in this repo. …
```

| Key           | Meaning                                                       |
| ------------- | ------------------------------------------------------------- |
| `description` | Required; one line, at most 120 characters                    |
| `model`       | Passed as `claude --model`; absent or empty for `claude`'s default |
| `pass`        | Verdicts that mean pass; JSON list; default `["PASS"]`        |
| `fail`        | Verdicts that mean fail; JSON list; default `["FAIL"]`; none in `pass` too |
| `vars`        | The var names the prompt may use; JSON list; default `[]`     |
| `claude_args` | More `claude` arguments, one per item; JSON list; not `--model` or `--settings`; default `[]` |
| body          | The prompt template; can't be blank                           |

Names are lowercase letters, digits, and hyphens, 1-64 characters,
starting with a letter or digit. Verdicts are as for `ws machine result`,
in upper case. A role is valid only if its prompt parses and renders with
every var in `vars` set; so a prompt that uses a var not in `vars` is
invalid.

**Built-in roles:**

| Role          | Model    | Pass            | Fail                | Vars |
| ------------- | -------- | --------------- | ------------------- | ---- |
| `implementer` | `haiku`  | `PASS`          | `FAIL`              | `TASK` (such as `T1`), `TASKS` (the tasks file), `BASE` (the branch it starts on), `BRANCH` (the branch it pushes) |
| `reviewer`    | `sonnet` | `APPROVED`      | `CHANGES_REQUESTED` | `TASK`, `TASKS`, `BASE` (the branch the task is diffed against), `KNOWN_ISSUES` (optional: a report of known failures) |
| `verifier`    | `haiku`  | `PASS`, `FLAKY` | `FAIL`              | `BRANCH` (the branch checked), `CHECKS` (the commands to run) |

Each passes `claude_args = ["--permission-mode", "auto"]`.

### ws machine roles (list)

```json
{"roles": [{"name": "implementer", "description": "Implements one task …", "model": "haiku",
            "pass": ["PASS"], "fail": ["FAIL"], "vars": ["TASK", "TASKS", "BASE", "BRANCH"],
            "claude_args": ["--permission-mode", "auto"], "source": "builtin"}],
 "warnings": []}
```

Sorted by name. `source` is `builtin`, `user` (a role you added), or
`override` (your file replacing the built-in of that name). A role file
that can't be parsed is skipped with a warning; an unparsable file with a
built-in's name hides the built-in.

### ws machine roles show

```
ws machine roles show <name>
```

```json
{"name": "implementer", "description": "…", "model": "haiku", "pass": ["PASS"], "fail": ["FAIL"],
 "vars": ["TASK", "TASKS", "BASE", "BRANCH"], "claude_args": ["--permission-mode", "auto"],
 "source": "builtin", "prompt": "You implement exactly one task, {{.Vars.TASK}}, …",
 "path": null, "warnings": []}
```

The list item plus `prompt` (the template) and `path`, the file, or
`null` for a built-in.

### ws machine roles add

```
ws machine roles add <name> --description <d> [--model <m>] [--pass <V>]... [--fail <V>]...
                     [--vars <K>]... [--claude-arg <A>]... (--prompt-file <f> | -)
```

Writes `$CONFIG_DIR/machine/roles/<name>.md`. The name can't be any
role's already, built in or yours.

| Option              | Description                                                |
| ------------------- | ---------------------------------------------------------- |
| `--description <d>` | Required                                                   |
| `--model <m>`       | The model                                                  |
| `--pass <V>`        | A pass verdict; repeatable or comma-separated; default `PASS` |
| `--fail <V>`        | A fail verdict; repeatable or comma-separated; default `FAIL` |
| `--vars <K>`        | A var name; repeatable or comma-separated                  |
| `--claude-arg <A>`  | One more `claude` argument; repeatable, kept in order      |
| `--prompt-file <f>` | Read the prompt template from this file                    |
| `-`                 | After the name: read the prompt template from stdin        |

Prints the role as `show` does.

### ws machine roles edit

```
ws machine roles edit <name> [--description <d>] [--model <m>] [--pass <V>]... [--fail <V>]...
                      [--vars <K>]... [--no-vars] [--claude-arg <A>]... [--no-claude-args]
                      [--prompt-file <f> | -]
```

Changes the given fields; the others are kept. `--pass`, `--fail`,
`--vars`, and `--claude-arg` replace the whole list; `--no-vars` and
`--no-claude-args` empty it; `--model ""` clears the model. Editing a
built-in writes a full copy to `$CONFIG_DIR/machine/roles/<name>.md` first,
so it becomes an `override`; `rm` undoes that. Children already started
keep what they were started with. Prints the role as `show` does.

### ws machine roles rm

```
ws machine roles rm <name>
```

Deletes your role file. For an `override`, the built-in is back.

```json
{"name": "implementer", "path": "/…/machine/roles/implementer.md", "restored": true, "warnings": []}
```

Every write is a temp file, parsed back, then renamed into place, under a
lock on the directory, so a role that wouldn't load is never left behind.

## ws machine memory

Reads and changes the memory of the loop the caller is a run of: the
key–value pairs that [`ws loop memory`](ws-loop.md#ws-loop-memory) also
shows. The loop is the one whose run is the root of the caller's tree;
a caller whose root isn't a loop run, or whose loop was removed, gets
`not_loop_run`.

Output is always JSON, in the same shapes as `ws loop memory`, with
`"loop"` set to the caller's loop. `updated_by` is the caller's workspace
id. There's no `clear`. Keys follow the same rules, and `--expect-rev` and
`--file` work as they do for `ws loop memory`.

```console
$ ws machine memory get seen
{"loop":"deps-bump","entry":{"key":"seen","value":"#412 #415","rev":3,"updated_at":"2026-11-10T08:14:00Z","updated_by":"loop-deps-bump-1a2b3c4d"},"warnings":[]}

$ ws machine memory set --expect-rev 3 seen "#412 #415 #418"
```

## ws machine progress

Reports how far the caller's task has got, so a person can watch a long or
orchestrated run as a progress bar ([0077](../adrs/0077-agent-progress-reporting.md)).
Each workspace keeps one record, `progress.toml` under the root's `machine/`
directory, holding a short `name` (up to 40 characters), `total` steps, `done`
steps (never above `total`), and a one-line `note` (up to 200). It's the
caller's own steps only: work handed to children is theirs to report.

- `set` changes the fields whose flags are given; at least one is required.
- `step` adds `--by` (default 1) to `done`, optionally setting the note.
- `show` prints the caller's tree.

Nesting follows the workspace tree ([0044](../adrs/0044-workspace-trees.md)),
so a child spawned by the caller needs no link. A node's **rollup** is the
steps of that workspace plus its descendants': `total` and `done` are sums,
and `fraction` is `done / total`, or `null` while no step is planned. A
workspace that never reported has no steps of its own, unless it has a result
(below), and passes its children's up. `set` and `step` print the caller's own `progress` and `rollup`; only `show` adds the `tree` of descendants. `wait` and `children` add the same
`progress` and `rollup` to each child's item: `progress` is `null` if the child
never reported, and `rollup` is `null` until a step is planned. A passing `ws machine result` sets `done` to
`total` when a total was declared, and a finished child with a result but no
record counts as one step, done only if it passed
([0077](../adrs/0077-agent-progress-reporting.md)). Every command prints the same object, and an agent
writes only its own record.

```console
$ ws machine progress set --name "build auth" --total 4
$ ws machine progress step --note "design done"
{"id":"plan","progress":{"name":"build auth","total":4,"done":1,"note":"design done","updated_at":"2026-11-22T20:00:00Z"},"rollup":{"total":7,"done":3,"fraction":0.4286},"warnings":[]}

$ ws machine progress show
{"id":"plan","progress":{…},"rollup":{…},"tree":{"id":"plan","progress":{…},"rollup":{…},"children":[{"id":"t1-impl-d8efb24f","progress":{…},"rollup":{…},"children":[]}]},"warnings":[]}
```

## Errors

| Situation                                       | Error code          | Exit |
| ----------------------------------------------- | ------------------- | ---- |
| Run outside a workspace (every command but `roles`) | `outside_workspace` | 2 |
| `spawn` without a prompt and without `--role`; `--var` without `--role`, a `--var` not in the role's `vars`, a bad or duplicate `--var`, or a role prompt that doesn't render; `rm` with neither workspaces nor `--all`, or both; `wait` with no workspace or a `--timeout` that isn't positive; `roles add` without `--description` or a prompt; `roles edit` with nothing to change; `--prompt-file` and `-` together; `--vars` with `--no-vars`, `--claude-arg` with `--no-claude-args` | `usage` | 2 |
| `result`: a malformed verdict, a verdict not in the role's lists, or a report that is empty, over 1 MiB, or unreadable | `result_invalid` | 2 |
| `result BLOCKED`: from anything but a loop run's own workspace, or without `--file` | `result_invalid` | 2 |
| `roles add` and `edit`: an invalid name, description, verdict, var name, or `claude_args`, the same verdict in `pass` and `fail`, an empty `pass` or `fail`, or a prompt that is blank, doesn't parse, or doesn't render with its `vars` | `role_invalid` | 2 |
| `spawn --role`, `roles show`, `edit`, `rm`: no role has the name | `role_unknown` | 3 |
| `spawn`: `--runner` isn't `claude` or `ws`; `--endpoint` with `claude`; after a `ws` child's prompt, anything but `--model <m>` | `usage` | 2 |
| `spawn --runner ws`: no model is configured, or a `[runner]` table is broken | `config_invalid` | 1 |
| `spawn --runner ws`: the endpoint isn't in `config.toml` | `endpoint_unknown` | 3 |
| `spawn --runner ws`: the endpoint is unreachable, or the model has no tools | `endpoint_unreachable`, `model_without_tools` | 1 |
| `spawn --runner ws`: the model isn't served by the endpoint | `model_not_served` | 3 |
| `spawn --runner ws`: no daemon answers, or the runner didn't write its status within 10 s | `daemon_unavailable`, `runner_failed` | 1 |
| Workspace not found; for `wait`, also not a gone child with a result | `workspace_not_found` | 3 |
| `rm` or `wait` of a workspace that isn't the caller's descendant | `not_descendant` | 4 |
| `roles add`: a role already has the name        | `role_exists`       | 4    |
| `roles rm` of a built-in with no override       | `role_builtin`      | 4    |
| `wait`: a finished child whose outcome is `fail` or `missing` (an item's error) | `child_failed` | 1 |
| `wait`: a child not finished at the timeout (an item's error) | `wait_timeout` | 1 |
| `roles`: your role file can't be parsed, for `edit` | `config_invalid` | 1  |
| `progress`: a bad name or note, a step count out of range, or `done` above `total` | `progress_invalid` | 2 |
| `progress set` with no flag; `progress step --by` under 1 | `usage` | 2 |
| `memory`: a key that isn't valid | `memory_invalid` | 2 |
| `memory set`: neither or both of `<VALUE>` and `--file` | `usage` | 2 |
| `memory`: a data file from a newer `ws` | `loop_invalid` | 2 |
| `memory get` or `rm`: no such key | `memory_not_found` | 3 |
| `memory set` or `rm`: `--expect-rev` didn't match | `memory_conflict` | 4 |
| `memory set`: a value over 64 KiB, over 100 keys, or over 256 KiB in all | `memory_full` | 4 |
| `memory`: the caller's root isn't a loop run, or its loop was removed | `not_loop_run` | 4 |

`spawn` fails as [`ws claude`](ws-claude.md) does, and `rm`'s items as
[`ws rm`](ws-rm.md)'s do.

## Examples

```console
$ ws machine spawn --name fix-login buff-bear -- "Fix the login bug, push a branch, and open a PR"
{"id":"fix-login-d8efb24f","session":"e8cdf5d8","path":"/…/workspaces/fix-login-d8efb24f","parent":"plan-auth-1a2b3c4d","repos":[…],"warnings":[],"role":null}

$ ws machine spawn --name t1-impl --role implementer --var TASK=T1 \
    --var TASKS=docs/design/login/tasks.md --var BASE=feat/login --var BRANCH=feat/login-t1 \
    buff-bear@feat/login
$ ws machine spawn --name t1-impl --role implementer --var TASK=T1 … buff-bear@feat/login \
    -- "Review feedback to address, fix every item: 1. …"

$ ws machine wait t1-impl-d8efb24f t2-impl-3be0a9c1
$ ws machine wait --any --timeout 2h t1-impl-d8efb24f t2-impl-3be0a9c1

$ ws machine result PASS --file ../report.md
$ ws machine result CHANGES_REQUESTED --file - < review.md

$ ws machine children
{"workspaces":[{"id":"fix-login-d8efb24f","parent":"plan-auth-1a2b3c4d",…,"role":null,"result":null}],"hidden_count":0,"warnings":[]}

$ ws machine rm fix-login-d8efb24f
{"workspaces":[{"id":"fix-login-d8efb24f","parent":null,"removed":true,"error":null}],"warnings":[]}

$ ws machine roles
$ ws machine roles show reviewer
$ ws machine roles edit reviewer --model opus
$ ws machine roles rm reviewer
```

## See also

[`ws claude`](ws-claude.md), [`ws list`](ws-list.md), [`ws rm`](ws-rm.md),
[`ws review wait`](ws-review.md), [0044](../adrs/0044-workspace-trees.md),
[0046](../adrs/0046-ws-machine.md),
[0050](../adrs/0050-machine-wait-results-and-roles.md)
