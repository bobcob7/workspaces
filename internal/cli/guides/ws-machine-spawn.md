# ws machine spawn

```
ws machine spawn [--name <name>] [--offline] [--hidden] [<spec>...] -- "<prompt>" [<claude args>...]
```

Creates a workspace from the repo specs, or an empty one with none, and
starts a Claude Code background agent in it, as your child: it is
`ws claude --json --parent <your workspace>`. Removing your workspace
removes it.

## When to use it

- To give a task to a helper agent in its own copy of the repos. Keep the
  returned `session` and follow it with `claude logs <session>`.
- Remove it with `ws machine rm` once you've used its result.
- Not to run something yourself: work in your own workspace instead.

Everything before `--` is a repo spec. Everything after `--` goes to
`claude` unchanged; the first argument is the prompt, which is required,
so `spawn` never attaches a terminal. Options for `claude`, such as
`--model opus`, follow the prompt. `--settings` is refused there.
`ws machine spawn` returns as soon as Claude Code has accepted the
session; the agent keeps working in the background.

## Flags

| Flag            | Meaning                                                     |
| --------------- | ----------------------------------------------------------- |
| `--name <name>` | Name the workspace and derive its id from the name, as for `ws claude` (`"Fix Login"` → `fix-login-d8efb24f`) |
| `--offline`     | Don't fetch; use the cache as it is. Uncached repos and refs fail |
| `--hidden`      | Hide the workspace from `ws list` and the desktop app's default views, for helpers the user needn't follow |

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
  "warnings": []
}
```

`ws claude --json`'s object; `parent` is your workspace's id.

## Exit codes

| Situation                                              | Error code         | Exit |
| ------------------------------------------------------ | ------------------ | ---- |
| Run outside a workspace                                | `outside_workspace` | 2   |
| No prompt after `--`, or `--settings` after it         | `usage`            | 2    |
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
$ ws machine spawn --name notes -- "Draft release notes from these issues: ..."
```
