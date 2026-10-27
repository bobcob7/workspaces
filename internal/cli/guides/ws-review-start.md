# ws review start

```
ws review start [--pr <n>] [--profile <p>[:<model>]]... [--auto <p>[:<model>]]... [--requester <id>|none] [--name <name>] [--offline] [--json] <spec> [-- <claude args>... [<focus>]]
```

Starts a review of one repo's commit: a new workspace at that commit, with
a Claude Code review agent that starts a reviewer subagent per profile.
Returns as soon as the agent has started.

- With `--pr <n>`, the commit is the pull request's head, read with `gh pr
  view`, and the spec has no `@ref`. Fork pull requests work. Needs `gh`,
  signed in.
- Without it, the commit is the spec's ref: a branch's or tag's commit, or
  a commit; the default branch without one.
- **Profiles.** By default: every `always` profile, and each `auto`
  profile whose patterns match a changed file (`go` for `*.go`), as
  `config.toml`'s `[review.profiles.<name>]` sets them. `--profile` and
  `--auto` replace that set: `--profile` profiles always join, `--auto`
  ones only when a file matches. `NAME:MODEL` pins the subagent's model
  (`security:opus`). The changed files are the pull request's against its
  base, or the commit's against its first parent. `ws review profiles`
  lists the profiles.
- **Requester**: the workspace you run this in, recorded so the review
  can be traced back; `--requester <id>` names another, `none` none.
- After `--`: options for `claude` (`--model opus`), and last, the
  **focus**: what you want the reviewers to look at, added to their
  prompts. Give options their values with `=` (`--model=opus`) when no
  focus follows, since the last argument that isn't an option is the
  focus. `--settings` and `--agents` are refused.
- The workspace is named `review-<repo>-<n>-<sha7>`, or
  `review-<repo>-<sha7>` without `--pr`, plus a random suffix, unless
  `--name` is given.

## Flags

| Flag                      | Meaning                                                     |
| ------------------------- | ----------------------------------------------------------- |
| `--pr <n>`                | Review this pull request's head commit                      |
| `--profile <p>[:<model>]` | Review with this profile; repeatable. Replaces the configured profiles |
| `--auto <p>[:<model>]`    | Review with this profile if a changed file matches it; repeatable |
| `--requester <id>\|none`  | Who asked for the review; default: the workspace you're in  |
| `--name <name>`           | Name the workspace instead of `review-…`                    |
| `--offline`               | Don't fetch the cache; `gh` still runs with `--pr`          |
| `--json`                  | Print the result as JSON                                    |

## JSON output

```json
{
  "id": "review-buff-bear-42-3f2a9c1-d8efb24f",
  "session": "e8cdf5d8",
  "path": "/…/workspaces/review-buff-bear-42-3f2a9c1-d8efb24f",
  "repos": [{"id": "github.com/bobcob7/buff-bear", "path": "buff-bear", "ref": "3f2a9c1e…", "ref_kind": "commit", "commit": "3f2a9c1e…"}],
  "warnings": [],
  "review": {"version": 1, "repo": "github.com/bobcob7/buff-bear", "ref": "3f2a9c1e…",
             "requester": "fix-login-d8efb24f", "started_at": "2026-10-26T18:00:00Z",
             "profiles": [{"name": "security", "model": "opus", "auto": false}, {"name": "go", "model": "", "auto": true}],
             "pr": {"number": 42, "url": "https://github.com/bobcob7/buff-bear/pull/42", "base": "main",
                    "head_branch": "feat/login", "cross_repository": false}},
  "wait_command": "ws review wait --json review-buff-bear-42-3f2a9c1-d8efb24f"
}
```

`id`, `session`, `path`, and `repos` are `ws claude`'s. `review` is the
review record; `pr` is null without `--pr`, and `requester` is `""` for
none. Run `wait_command` in the background to get the report (see `ws llm
review`). Without `--json`: `ws claude`'s line, then the wait command.

## Exit codes

| Situation                                              | Error code         | Exit |
| ------------------------------------------------------ | ------------------ | ---- |
| Not exactly one spec, `@ref` with `--pr`, a malformed `--profile` or `--auto`, or no profile left | `usage`, `spec_invalid` | 2 |
| A profile that doesn't exist                           | `profile_unknown`  | 2    |
| `--requester` names no workspace                       | `workspace_not_found` | 3 |
| Repo or ref not found                                  | `repo_not_found`, `repo_ambiguous`, `ref_not_found` | 3 |
| The pull request doesn't exist                         | `pr_not_found`     | 3    |
| `gh` isn't on `PATH`, or isn't signed in               | `gh_missing`, `gh_auth` | 1 |
| Fetch, clone, or `claude` failed, as for `ws claude`   | `fetch_failed`, `clone_failed`, `git_failed`, `claude_missing`, `claude_untrusted`, `claude_failed` | 1 |
| `config.toml` or a profile file is malformed           | `config_invalid`   | 1    |

## Examples

```console
# Review your pull request, with a focus
$ ws review start --json --pr 42 buff-bear -- "Check the retry loop's cancellation"

# Only security and go, on chosen models
$ ws review start --json --pr 42 --profile security:opus --auto go:sonnet buff-bear

# A commit, no pull request
$ ws review start --json buff-bear@3f2a9c1e4b5d6f7a8b9c0d1e2f3a4b5c6d7e8f90
```
