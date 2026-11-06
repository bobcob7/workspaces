# ws machine spawn

```
ws machine spawn [--name <name>] [--offline] [--hidden] [--role <name> [--var KEY=VALUE]...] [<spec>...] -- "<prompt>" [<claude args>...]
```

Creates a workspace from the repo specs, or an empty one with none, and
starts a Claude Code background agent in it, as your child: it is
`ws claude --json --parent <your workspace>`. Removing your workspace
removes it.

## When to use it

- To give a task to a helper agent in its own copy of the repos. Keep the
  returned `id`, wait for it with `ws machine wait <id>` as a background
  command, and follow it with `claude logs <session>`.
- With `--role`, to start a child from a stored role: `implementer`,
  `reviewer`, `verifier`, or one the user added (`ws machine roles`). The
  role gives the prompt, the model, and the verdicts the child records
  with `ws machine result`.
- Remove it with `ws machine rm` once you've used its result.
- Not to run something yourself: work in your own workspace instead.

Everything before `--` is a repo spec. Everything after `--` goes to
`claude` unchanged; the first argument is the prompt, so `spawn` never
attaches a terminal. Options for `claude`, such as `--model opus`, follow
the prompt. `--settings` is refused there. `ws machine spawn` returns as
soon as Claude Code has accepted the session; the agent keeps working in
the background.

Without `--role`, the prompt is required. Ask the child to finish with
`ws machine result PASS` or `FAIL`, and a report, so `ws machine wait`
can tell you how it went.

## Roles

With `--role <name>`, the role's prompt template is rendered with each
`--var KEY=VALUE`, and is the prompt. `ws machine roles show <name>` lists
its `vars`: each `{{.Vars.KEY}}` in its prompt must be given, and a key it
doesn't list is refused. The prompt after `--` is then optional; when
given, it's appended after a blank line, for example review feedback to
address. `claude` gets the role's `--model` and arguments before yours, so
a `--model` after the prompt overrides the role's. To pass `claude`
arguments without an extra prompt, give an empty one: `-- "" --model opus`.

## Flags

| Flag                | Meaning                                                     |
| ------------------- | ----------------------------------------------------------- |
| `--name <name>`     | Name the workspace and derive its id from the name, as for `ws claude` (`"Fix Login"` → `fix-login-d8efb24f`) |
| `--offline`         | Don't fetch; use the cache as it is. Uncached repos and refs fail |
| `--hidden`          | Hide the workspace from `ws list` and the desktop app's default views, for helpers the user needn't follow |
| `--role <name>`     | Start the child from this role                              |
| `--var KEY=VALUE`   | A value for the role's prompt; repeatable; only with `--role` |

Output is always JSON; `--json` changes nothing.

## JSON output

```json
{
  "id": "fix-login-d8efb24f",
  "session": "e8cdf5d8",
  "path": "/…/workspaces/fix-login-d8efb24f",
  "parent": "plan-auth-1a2b3c4d",
  "repos": [
    {"id": "github.com/bobcob7/buff-bear", "path": "buff-bear",
     "ref": "main", "ref_kind": "branch", "commit": "3f2a9c1e4b…"}
  ],
  "warnings": [],
  "role": null
}
```

`ws claude --json`'s object; `parent` is your workspace's id, and `role`
the role's name or `null`.

## Exit codes

| Situation                                              | Error code         | Exit |
| ------------------------------------------------------ | ------------------ | ---- |
| Run outside a workspace                                | `outside_workspace` | 2   |
| No prompt after `--` without `--role`, or `--settings` after it | `usage`   | 2    |
| `--var` without `--role`, a var the role doesn't list, a key given twice, or a role prompt that doesn't render with the vars given | `usage` | 2 |
| No role has the name                                   | `role_unknown`     | 3    |
| An unparsable spec, the same repo twice, or two repos with the same name | `spec_invalid` | 2 |
| `--name` has no letters or digits, or a control character | `name_invalid`  | 2    |
| Repo not found, or a short name matches several        | `repo_not_found`, `repo_ambiguous` | 3 |
| Ref not found                                          | `ref_not_found`    | 3    |
| No free id after several random suffixes               | `workspace_exists` | 4    |
| Fetch or clone failed                                  | `fetch_failed`, `clone_failed`, `git_failed` | 1 |
| `claude` isn't on `PATH`                               | `claude_missing`   | 1    |
| Claude Code doesn't trust the workspace root           | `claude_untrusted` | 1    |
| `claude --bg` failed; `details.stderr` has its output  | `claude_failed`    | 1    |

On `claude_untrusted`, tell the person to run `claude` once in
`details.directory` and accept the trust prompt.

## Examples

```console
$ ws machine spawn --name fix-login buff-bear -- "Fix the login bug, push a branch, and open a PR"
$ ws machine spawn --hidden buff-bear@feat/new-thing -- "Write tests for the new parser" --model opus
$ ws machine spawn --name t1-impl --role implementer --var TASK=T1 --var TASKS=docs/design/x/tasks.md \
    --var BASE=feat/x --var BRANCH=feat/x-t1 buff-bear@feat/x
$ ws machine spawn --name t1-review --role reviewer --var TASK=T1 --var TASKS=docs/design/x/tasks.md \
    --var BASE=feat/x buff-bear@feat/x-t1
$ ws machine spawn --name x-verify --role verifier --var BRANCH=feat/x --var CHECKS="make lint; make test" buff-bear@feat/x
```
