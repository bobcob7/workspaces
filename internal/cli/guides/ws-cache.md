# ws cache

```
ws cache add <spec>...
ws cache update [<spec>... | --all]
ws cache list
ws cache search [--limit <n>] [--refs] <query>
ws cache refs <spec>
ws cache resolve <spec>...
ws cache path <spec>
ws cache remove <spec>...
ws cache prune [-n | --dry-run]
```

The cache holds one bare clone of each upstream repo, under
`$CONFIG_DIR/cache/repos/<host>/<owner>/<name>.git`. Workspaces are cloned
from it, and short repo names are resolved against it. `ws claude` and
`ws shell` add and fetch repos on their own, so you rarely need these
commands to create a workspace.

`ws cache` is for people and the desktop app. Agents don't run it: use `ws machine` (see `ws llm machine`), or tell a person what you need.

## When to use it

- `ws cache search` and `ws cache resolve` to find the right spec before
  `ws claude`, especially when a short name is ambiguous.
- `ws cache refs` to see which branches and tags a repo has, and the
  last commit on each, before picking one for a spec.
- `ws cache path` to read a repo's history without a workspace:
  `git -C "$(ws cache path buff-bear)" log main`. Don't commit or
  push in the cache.
- `ws cache add` and `ws cache update` to prepare repos ahead of time, for
  example before `ws claude --offline`.
- `ws cache remove` and `ws cache prune` only when told to.

All take `--json`. Run `ws llm cache <op>` for each one's guide.

## Exit codes

| Situation                                   | Error code                         | Exit |
| ------------------------------------------- | ---------------------------------- | ---- |
| An unknown operation, or wrong arguments or flags for one | `usage`              | 2    |
| Unparsable spec                             | `spec_invalid`                     | 2    |
| Repo not found, or short name ambiguous     | `repo_not_found`, `repo_ambiguous` | 3    |
| A full commit hash upstream doesn't have    | `ref_not_found`                    | 3    |
| Fetch or clone failed, or upstream unreachable | `fetch_failed`, `clone_failed`  | 1    |
| git missing, too old, or failed otherwise; `details.stderr` has its output | `git_failed` | 1 |
