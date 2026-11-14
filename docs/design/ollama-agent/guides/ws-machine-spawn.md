# ws machine spawn

```
ws machine spawn [--runner claude|ollama] [--endpoint <name>] [--name <name>] [--offline] [--hidden]
                 [--role <name> [--var KEY=VALUE]...] [<spec>...] -- "<prompt>" [<claude args>... | --model <m>]
```

Creates a workspace from the repo specs, or an empty one with none, and
starts a Claude Code background agent in it, as your child: it is
`ws claude --json --parent <your workspace>`. Removing your workspace
removes it.

## When to use it

- To give a task to a helper agent in its own copy of the repos. Keep the
  returned `id`, wait for it with `ws machine wait <id>` as a background
  command, and follow it with `ws machine logs <id>`.
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

## Runners

A child runs on Claude Code (`claude`, the default) or on an on-prem
model through `ws`'s own Ollama runner (`ollama`). `--runner` chooses.
Without it, `ollama.roles.<role>.runner` in `config.toml` chooses for
`--role`, and otherwise it's `claude`. Your output's `runner` says which
one started.

An `ollama` child talks to `--endpoint`, or the role's or default
endpoint from `config.toml`. It uses `--model <m>` after the prompt, or
the configured model; no other `claude` arguments are accepted. Before
anything is created, spawn checks that the endpoint answers, serves the
model, and that the model supports tools. A failure is an error, not a
silent switch to Claude, unless the user set `ollama.fallback = "claude"`;
then a Claude child starts and a warning says why.

An `ollama` child records its own verdict, or `ws` records a fail verdict
for it with a report whose first line gives the reason, such as `ws ollama
runner: looping`. Read its log with `ws machine logs <id>`, not `claude
logs`. It can't be attached to or sent messages.

## Flags

| Flag                | Meaning                                                     |
| ------------------- | ----------------------------------------------------------- |
| `--name <name>`     | Name the workspace and derive its id from the name, as for `ws claude` (`"Fix Login"` → `fix-login-d8efb24f`) |
| `--offline`         | Don't fetch; use the cache as it is. Uncached repos and refs fail |
| `--hidden`          | Hide the workspace from `ws list` and the desktop app's default views. Off by default: children show in the tree |
| `--role <name>`     | Start the child from this role                              |
| `--var KEY=VALUE`   | A value for the role's prompt; repeatable; only with `--role` |
| `--runner claude\|ollama` | Which agent runs the child; default from `config.toml` for the role, else `claude` |
| `--endpoint <name>` | An Ollama endpoint from `config.toml`; only with the `ollama` runner |

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
  "role": null,
  "runner": "claude",
  "ollama": null
}
```

For an `ollama` child, `session` is the runner's id (`ol-…`) and:

```json
"runner": "ollama",
"ollama": {"endpoint": "gpu", "url": "http://ollama-a.inference.svc:11434", "model": "qwen3-coder:30b", "context_tokens": 65536}
```

`ws claude --json`'s object; `parent` is your workspace's id, `role`
the role's name or `null`, `runner` `claude` or `ollama`, and `ollama`
the endpoint and model for an Ollama child, else `null`.

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
| `--runner` isn't `claude` or `ollama`; `--endpoint` with `claude`; an argument other than `--model` after an `ollama` child's prompt | `usage` | 2 |
| The endpoint isn't in `config.toml`; `details.endpoints` lists them | `endpoint_unknown` | 3 |
| No endpoint or model configured for an `ollama` child   | `config_invalid`   | 1    |
| The endpoint didn't answer; `details.url`, `details.cause` | `ollama_unreachable` | 1 |
| The model isn't on the endpoint; `details.models`      | `ollama_model_missing` | 3 |
| The model doesn't support tools; `details.capabilities` | `ollama_no_tools` | 1  |
| The runner didn't start; `details.log`                  | `runner_failed`    | 1    |

On `ollama_unreachable`, `ollama_model_missing` or `ollama_no_tools`,
tell a person, or spawn the child with `--runner claude`.

On `claude_untrusted`, tell the person to run `claude` once in
`details.directory` and accept the trust prompt.

## Examples

```console
$ ws machine spawn --name fix-login buff-bear -- "Fix the login bug, push a branch, and open a PR"
$ ws machine spawn buff-bear@feat/new-thing -- "Write tests for the new parser" --model opus
$ ws machine spawn --name t1-impl --role implementer --var TASK=T1 --var TASKS=docs/design/x/tasks.md \
    --var BASE=feat/x --var BRANCH=feat/x-t1 buff-bear@feat/x
$ ws machine spawn --name t1-review --role reviewer --var TASK=T1 --var TASKS=docs/design/x/tasks.md \
    --var BASE=feat/x buff-bear@feat/x-t1
$ ws machine spawn --name x-verify --role verifier --var BRANCH=feat/x --var CHECKS="make lint; make test" buff-bear@feat/x
$ ws machine spawn --runner ollama --name x-verify --role verifier --var BRANCH=feat/x --var CHECKS="make test" buff-bear@feat/x
$ ws machine spawn --runner ollama --endpoint gpu --name t2-impl --role implementer --var TASK=T2 \
    --var TASKS=docs/design/x/tasks.md --var BASE=feat/x --var BRANCH=feat/x-t2 buff-bear@feat/x -- "" --model qwen3-coder:30b
```
