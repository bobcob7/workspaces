# ws cache

Manage, search, and resolve repositories in the local cache.

## Synopsis

```
ws cache add <SPEC>...
ws cache update [<SPEC>... | --all]
ws cache list
ws cache search <QUERY>
ws cache refs <SPEC>
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

Every spec is resolved before anything is cloned, so a spec that doesn't
resolve fails the command and clones nothing. A failed clone doesn't stop
the others: each failure is reported on stderr, and the exit code is that
of the first. With `--json`:
`{"repos": [{"id", "path", "upstream", "added", "error"}], "warnings"}`,
where `added` is `false` for a repo that was already cached and `error` is
`{"code", "message"}` or `null`.

### `update`

Fetch the given cached repos (or all with `--all`) from upstream, pruning
deleted branches and tags, and following a change of upstream's default
branch. Specs must name cached repos; nothing is probed or cloned.

```console
$ ws cache update --all
updated  github.com/bobcob7/api
updated  github.com/bobcob7/buff-bear
```

Up to 8 repos are fetched at once; output keeps the order of the repos. One
repo failing doesn't stop the others; each failure is reported on stderr,
and the exit code is that of the first. With `--json`:
`{"repos": [{"id", "error"}], "warnings"}`, where `error` is
`{"code", "message"}` or `null`.

### `list`

List cached repos, reading up to 8 at once.

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
    "size_bytes": 18432000,
    "branches": 14,
    "tags": 6 }
  ],
  "warnings": [] }
```

`branches` and `tags` count the repo's branches and tags as cached
([0033](../adrs/0033-ws-cache-refs.md)).

### `search`

Case-insensitive substring match over cached repo ids, ranked by match
quality (exact name > name prefix > name substring > owner/host match), then
by id. With `--refs`, branches and tags containing the query follow the repo
matches. Unlike spec resolution, search never errors on multiple matches.

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

With `--json`: `{"matches": [{"id", "ref"}], "warnings"}`, where `ref` is
`null` for a repo match.

### `refs`

List a cached repo's branches and tags, each with the commit it points at
and that commit's subject, author, and committer date
([0033](../adrs/0033-ws-cache-refs.md)). Branches come first, the default
branch leading and the rest newest first, then tags newest first, ties by
name. It reads the cache as it is, without fetching or taking the repo's
lock; a ref in the spec is ignored. The spec must name a cached repo.

```console
$ ws cache refs buff-bear
branch  9c04e2a  main  Merge pull request #39 from bobcob7/fix/session-ttl
branch  41b7c0d  feat/rate-limit  Add a token bucket per client IP
tag     3e8f1a2  v1.2.0  Release 1.2.0

$ ws cache refs --json buff-bear
{ "id": "github.com/bobcob7/buff-bear",
  "default_branch": "main",
  "refs": [
    { "name": "main", "kind": "branch",
      "commit": "9c04e2a7d1…", "subject": "Merge pull request #39 from bobcob7/fix/session-ttl",
      "author": "bobcob7", "date": "2026-10-24T09:12:00Z" }
  ],
  "warnings": [] }
```

`kind` is `branch` or `tag`. For an annotated tag, `commit`, `subject`,
`author`, and `date` are those of the commit it tags. `date` is `null` if
git gives none, such as for a tag of a tag.

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
    "ref": null,
    "upstream": "git@github.com:bobcob7/buff-bear.git",
    "cached": true,
    "source": "cache" }
  ],
  "warnings": [] }
```

`source` is `spec` (the spec names the host), `cache` (found by short name),
`default` (an uncached `owner/name`, taken to be on `defaults.host`), or
`probe` (found via `defaults.owners`). A cached repo's `upstream` is the URL
it was cloned from; a URL spec that differs adds a warning naming both
([0019](../adrs/0019-https-upstream-urls.md)). Ambiguous names fail with
`repo_ambiguous` (exit 3), and `details.candidates` lists the matches. The
ref is echoed, not checked.

The probe tries each owner in `defaults.owners` in turn. Upstream answering
that the repo isn't there moves on to the next owner, and `repo_not_found`
is reported only when none has it. Anything else, such as an unknown host, a
refused connection, a TLS error, or a rejected login, stops the probe with
`fetch_failed`, so an outage isn't mistaken for a missing repo. Upstream
asking for a login it can't get (`terminal prompts disabled`) counts as "not
there", since GitHub and GitLab answer that way for repos that don't exist
as well as for private ones ([0019](../adrs/0019-https-upstream-urls.md)).

### `path`

Print the cache directory for a cached repo (useful for
`git -C "$(ws cache path x)"`). With `--json`: `{"id", "path", "warnings"}`.

### `remove`

Delete cache entries, printing each id. Existing workspaces are unaffected:
their objects are hard links, which survive the cache's copy being deleted
([0009](../adrs/0009-workspace-repository-remotes.md)). Removing a repo that
another process holds a lock on waits for the lock. Every spec must name a
cached repo before anything is deleted; a failed removal doesn't stop the
others, and the exit code is that of the first failure. With `--json`:
`{"repos": [{"id", "path", "error"}], "warnings"}`.

Cache commands also clear leftovers from `cache/tmp/`, where clones are
built and repos are removed: entries older than a day, which a killed `ws`
left behind. A failure there is a warning.

### `prune`

Delete `refs/ws/commits/*` refs that no existing workspace has checked out.
These are created when a workspace asks for a commit that only upstream has,
such as a PR commit ([0007](../adrs/0007-repo-spec-syntax-and-resolution.md)).
A ref is kept if any workspace's record names that repo and commit, and
every ref is kept while a workspace is being created, since it may be about
to check one out. If a workspace record can't be read, nothing is pruned,
since that workspace might use one of the refs.

Then delete the stored reviews of every repo that is no longer cached:
`reviews/<host>/<owner>/<name>/`, with every commit's submissions
([0039](../adrs/0039-ws-review-command.md), [state](../state.md#reviews)).

```console
$ ws cache prune
pruned  github.com/bobcob7/buff-bear  9b1d22a0c7e1f3a8b2d4c6e8f0a1b3c5d7e9f1a2
pruned reviews  github.com/bobcob7/old-repo  /Users/me/Library/Application Support/workspaces/reviews/github.com/bobcob7/old-repo

$ ws cache prune --json -n
{ "refs": [ { "id": "github.com/bobcob7/buff-bear", "commit": "9b1d22a0c7…" } ],
  "reviews": [ { "id": "github.com/bobcob7/old-repo", "path": "/…/reviews/github.com/bobcob7/old-repo" } ],
  "warnings": [] }
```

With `-n`, text output says `would prune` instead.

| Option            | Description                                  |
| ----------------- | -------------------------------------------- |
| `-n`, `--dry-run` | List what would be deleted                   |

## Errors

| Situation                                   | Error code                         | Exit |
| ------------------------------------------- | ---------------------------------- | ---- |
| Unknown operation, or wrong arguments for one | `usage`                          | 2    |
| Unparsable spec                             | `spec_invalid`                     | 2    |
| Repo not found, or short name ambiguous     | `repo_not_found`, `repo_ambiguous` | 3    |
| Upstream doesn't have a full commit hash that was asked for | `ref_not_found` | 3 |
| Fetch or clone failed, or upstream couldn't be reached while probing or fetching a commit | `fetch_failed`, `clone_failed` | 1 |
| git missing, too old, or failed otherwise   | `git_failed`                       | 1    |

When git failed, `details.stderr` holds its error output.

## See also

[0007](../adrs/0007-repo-spec-syntax-and-resolution.md),
[0008](../adrs/0008-repository-cache.md),
[0014](../adrs/0014-command-set.md),
[0033](../adrs/0033-ws-cache-refs.md)
