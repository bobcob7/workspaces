# ws claude

```
ws claude [--runner claude|ws] [--endpoint <name>] [--name <name>] [--offline] [--hidden] [--parent <id>|none] [--json] [<spec>...] [-- <claude args>...]
```

Creates a workspace from the repo specs, or an empty one with none, then
starts a Claude Code background session in it. This is how to hand work to another agent.

`ws claude` is for people and the desktop app. Agents use
`ws machine spawn` to start a helper; see `ws llm machine spawn`.

## When to use it

- To give a task to a new agent in its own copy of the repos. Put the
  prompt after `--`.
- Run inside a workspace, the new one is that workspace's child and is
  removed with it; `--parent none` makes it top-level.
- Remove it with `ws rm` once its work is pushed.

With a prompt, `ws` returns as soon as Claude Code has accepted the
session; the agent keeps working in the background. Without a prompt, on a
terminal and without `--json`, `ws` attaches the terminal to the idle
session (`claude attach`), which is for people. With `--json` or without a
terminal it prints the result instead, leaving the session idle.

With `--runner ws`, the agent runs on `ws`'s own runner instead of Claude
Code, started as `ws machine spawn --runner ws` starts one (ADR 0070). It
needs a prompt after `--`, takes only `--model` after it, and never attaches.
The output is the same, with `runner`, `model` and `ws_runner` filled in.

Everything before `--` is a repo spec, so the prompt must come after `--`.
Everything after `--` goes to `claude` unchanged: the prompt, and options
such as `--model opus` or `--permission-mode`. `--settings` is refused
there (`usage`), since `ws` passes its own. The session is named after the
workspace id, and its working directory is the workspace, or the repo's
directory when there is exactly one repo. For that, `ws` marks the repo
trusted in Claude Code's config, if the workspace root is trusted; a
warning says when it couldn't, and the agent starts in the workspace.

Creating the workspace: each spec is resolved, cloned into the cache or
fetched, and its ref checked out. If anything fails, including Claude Code
refusing to start, the workspace is removed, so nothing is left behind.

After it starts, follow the agent with `claude logs <session>`, talk to it
with `claude attach <session>`, and stop it with `claude stop <session>`.
Its workspace stays, even after it finishes, until the session is deleted
(`claude rm <session>` or `ws rm <id>`).

## Flags

| Flag            | Meaning                                                     |
| --------------- | ----------------------------------------------------------- |
| `--name <name>` | Name the workspace. The name is shown by `ws list` and can be changed with `ws rename`; the id is made from it once: lowercased, accents dropped, other characters become `-`, and 8 random hex digits added (`"Fix Login"` → `fix-login-d8efb24f`). Without it the id is generated, like `ws-20261018-k3f9qa` |
| `--offline`     | Don't fetch; use the cache as it is. Uncached repos and refs fail |
| `--hidden`      | Hide the workspace: `ws list` shows it only with `-a`, and the desktop app keeps it in a collapsed Hidden group that doesn't count as waiting for input. For helpers the user needn't follow; see `ws llm hide` |
| `--parent <id>\|none` | The workspace this one is removed with; default: the workspace you run `ws` in, so a helper you start is yours. `none` for work meant to outlive you |
| `--runner claude\|ws` | Which agent runs it: Claude Code (the default) or `ws`'s own runner (see `ws llm machine spawn`). A `ws` agent needs a prompt and can't be attached; after the prompt only `--model <alias or id>` is accepted |
| `--endpoint <name>` | With `--runner ws`, another endpoint from config.toml   |
| `--json`        | Print the result as JSON                                    |

## JSON output

```json
{
  "id": "fix-login-d8efb24f",
  "session": "e8cdf5d8",
  "path": "/Users/me/Library/Application Support/workspaces/workspaces/fix-login-d8efb24f",
  "repos": [
    {"id": "github.com/bobcob7/buff-bear", "path": "buff-bear",
     "ref": "main", "ref_kind": "branch", "commit": "3f2a9c1e4b…"}
  ],
  "warnings": [],
  "runner": "claude", "model": null, "ws_runner": null
}
```

- `id`: the workspace id. `session`: the Claude Code session id, for
  `claude logs`, `claude attach`, `claude stop`, and `claude rm`.
- `path`: the workspace; each repo is in `path/<repo path>`.
- `ref_kind`: `branch`, `tag`, or `commit`. `ref` is the default branch's
  name when the spec gave none.

Without `--json`: `<id>  <session>  <path>` on one line.

## Exit codes

| Situation                                              | Error code         | Exit |
| ------------------------------------------------------ | ------------------ | ---- |
| An unparsable spec, the same repo twice, or two repos with the same name | `usage`, `spec_invalid` | 2 |
| `--settings` after `--`                                | `usage`            | 2    |
| `--runner ws` with no prompt after `--`, or other arguments after it; `--endpoint` without `--runner ws` | `usage` | 2 |
| `--endpoint` not in config.toml                        | `endpoint_unknown` | 3    |
| `--model` not served by the endpoint                   | `model_not_served` | 3    |
| Endpoint unreachable, or the model can't use tools     | `endpoint_unreachable`, `model_without_tools` | 1 |
| The runner wrote no status in time                     | `runner_failed`    | 1    |
| The daemon isn't available                             | `daemon_unavailable` | 1  |
| `[runner]`, `[endpoints]` or `[models]` is invalid     | `config_invalid`   | 1    |
| `--name` has no letters or digits, or a control character | `name_invalid`  | 2    |
| Repo not found, or a short name matches several        | `repo_not_found`, `repo_ambiguous` | 3 |
| Ref not found                                          | `ref_not_found`    | 3    |
| No free id after several random suffixes               | `workspace_exists` | 4    |
| Fetch or clone failed                                  | `fetch_failed`, `clone_failed`, `git_failed` | 1 |
| `claude` isn't on `PATH`                               | `claude_missing`   | 1    |
| Claude Code doesn't trust the workspace root; `details.directory` is the directory a person must trust | `claude_untrusted` | 1 |
| `claude` is older than 2.1.302, or `claude --bg` failed; `details.stderr` has its output | `claude_failed` | 1 |

On `claude_untrusted`, tell the person to run `claude` once in
`details.directory` and accept the trust prompt; don't try to work around
it.

## Examples

```console
# Hand off a task; keep the session id
$ ws claude --json --name fix-login buff-bear -- "Find why login fails on empty passwords and fix it"

# An agent in an empty workspace, for work that needs no repo
$ ws claude --json --name notes -- "Draft release notes from these issues: ..."

# Several repos at given refs, with a model choice
$ ws claude --json buff-bear@feat/new-thing bobcob7/api@v2.1.0 -- --model opus "Update the client for the new API"

# Follow it, then remove it once its work is pushed
$ claude logs e8cdf5d8
$ ws rm --json fix-login-d8efb24f
```
