# ws cache

Manage, search, and resolve repositories in the local cache.

## Synopsis

```
ws cache add <SPEC>...
ws cache update [<SPEC>... | --all]
ws cache list
ws cache search <QUERY>
ws cache resolve <SPEC>...
ws cache path <SPEC>
ws cache remove <SPEC>...
ws cache prune
```

## Subcommands

### `add`

Resolve each spec and clone it into the cache if not already present. Refs in
specs are ignored.

```console
$ ws cache add bobcob7/buff-bear gitlab.example.com/team/svc
github.com/bobcob7/buff-bear
gitlab.example.com/team/svc
```

### `update`

Fetch the given cached repos (or all with `--all`) from upstream, pruning
deleted branches.

```console
$ ws cache update --all
```

### `list`

List cached repos.

```console
$ ws cache list
github.com/bobcob7/buff-bear
github.com/bobcob7/api
gitlab.example.com/team/svc

$ ws cache list --json
{ "repos": [
  { "id": "github.com/bobcob7/buff-bear",
    "path": "/Users/me/Library/Application Support/workspaces/cache/repos/github.com/bobcob7/buff-bear.git",
    "upstream": "git@github.com:bobcob7/buff-bear.git",
    "default_branch": "main",
    "last_fetched_at": "2026-10-17T15:48:00Z",
    "size_bytes": 18432000 }
  ],
  "warnings": [] }
```

### `search`

Case-insensitive substring/fuzzy match over cached repo ids, ranked by match
quality (exact name > name prefix > name substring > owner/host match). Unlike
spec resolution, search never errors on multiple matches.

| Option             | Description                              |
| ------------------ | ---------------------------------------- |
| `--limit <n>`      | Maximum results (default 20)             |
| `--refs`           | Also search branch and tag names         |

```console
$ ws cache search bear
github.com/bobcob7/buff-bear
github.com/someone/bear-tools

$ ws cache search --refs new-thing
github.com/bobcob7/buff-bear@feat/new-thing
```

### `resolve`

Resolve specs to canonical ids without cloning or fetching (except the
`ls-remote` probe for uncached bare names). Accepts full URLs, so it doubles
as reverse lookup from an upstream URL to its id and cache entry.

```console
$ ws cache resolve buff-bear@feat/new-thing git@github.com:bobcob7/api.git
github.com/bobcob7/buff-bear@feat/new-thing
github.com/bobcob7/api

$ ws cache resolve --json buff-bear
{ "repos": [
  { "id": "github.com/bobcob7/buff-bear",
    "upstream": "git@github.com:bobcob7/buff-bear.git",
    "cached": true,
    "source": "cache" }
  ],
  "warnings": [] }
```

`source` is `spec` (fully qualified), `cache` (found by short name), or
`probe` (found via `defaults.owners`). Ambiguous names fail with `repo_ambiguous` (exit 3), and
`details.candidates` lists the matches.

### `path`

Print the cache directory for a spec (useful for `git -C "$(ws cache path x)"`).

### `remove`

Delete cache entries. Existing workspaces are unaffected: their objects are
hard links, which survive the cache's copy being deleted
([0009](../adrs/0009-workspace-repository-remotes.md)). Removing a repo that
another process holds a lock on waits for the lock.

### `prune`

Delete `refs/ws/commits/*` refs that no existing workspace has checked out.
These are created when a workspace asks for a commit that only upstream has,
such as a PR commit ([0007](../adrs/0007-repo-spec-syntax-and-resolution.md)).

| Option            | Description                                  |
| ----------------- | -------------------------------------------- |
| `-n`, `--dry-run` | List what would be deleted                   |

## Errors

| Situation                                   | Error code                         | Exit |
| ------------------------------------------- | ---------------------------------- | ---- |
| Unparsable spec                             | `spec_invalid`                     | 2    |
| Repo not found, or short name ambiguous     | `repo_not_found`, `repo_ambiguous` | 3    |
| Fetch or clone failed                       | `fetch_failed`, `clone_failed`     | 1    |

## See also

[0007](../adrs/0007-repo-spec-syntax-and-resolution.md),
[0008](../adrs/0008-repository-cache.md),
[0014](../adrs/0014-command-set.md)
