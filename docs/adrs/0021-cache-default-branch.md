# 0021. The cache follows upstream's default branch through `ls-remote --symref`

- Status: Accepted (2026-10-18)
- Date: 2026-10-18

## Context

### Problem

[0008](0008-repository-cache.md) has every fetch of a cached repo followed
by `git remote set-head origin --auto`, so the cache's `HEAD` tracks
upstream's default branch. A spec without a ref checks out that default
branch ([0007](0007-repo-spec-syntax-and-resolution.md)).

With 0008's own refspecs, that command fails. How should the cache follow
upstream's default branch?

### What a solution needs

1. **The cache's `HEAD` matches upstream's default branch** after each
   fetch, including after upstream changes it.
2. **No more network round trips** than 0008 intended.
3. **A fetch that worked still counts as worked**: the default branch is
   secondary to having current refs.
4. **No change to the cache's refspecs**, which 0008 chose to keep only
   heads and tags.

### Relevant facts

- **What `set-head --auto` does.** "The remote is queried to determine its
  HEAD, then the symbolic-ref `refs/remotes/<name>/HEAD` is set to the same
  branch … This will only work if `refs/remotes/origin/next` already
  exists"[^git-remote].
- **The cache has no `refs/remotes/`.** 0008's refspecs map
  `refs/heads/*` to `refs/heads/*`. On a cache set up that way,
  `git remote set-head origin --auto` exited 1 with "error: Not a valid ref:
  refs/remotes/origin/main"[^bench-sethead]. Even where it works, it sets
  `refs/remotes/origin/HEAD`, not the bare repo's own `HEAD`.
- **A plain fetch doesn't follow.** After upstream's `HEAD` moved from
  `main` to `trunk`, `git fetch --prune origin` left the cache's `HEAD` at
  `refs/heads/main`[^bench-sethead].
- **`ls-remote --symref`** shows "the underlying ref pointed by" a symbolic
  ref, and "upload-pack only shows the symref HEAD"[^git-ls-remote]. It
  printed `ref: refs/heads/trunk	HEAD`, and
  `git symbolic-ref HEAD refs/heads/trunk` then pointed the cache at it.
  Both it and `set-head --auto` start one `git-upload-pack` on the
  remote[^bench-sethead].

## Decision

We will replace 0008's `git remote set-head origin --auto` step. After each
successful `git fetch --prune origin` of a cached repo, `ws`:

1. runs `git ls-remote --symref origin HEAD` and reads the branch from the
   `ref: refs/heads/<branch>	HEAD` line, and
2. runs `git symbolic-ref HEAD refs/heads/<branch>` in the cached repo.

If either step fails, or upstream reports no `HEAD` branch, the fetch still
succeeds and the command adds a warning naming the repo. `--offline` skips
this along with the fetch. The first clone needs neither step, since
`git clone --bare` already sets `HEAD` from upstream.

### Why this option

1. **Follows changes.** The cache's own `HEAD` is set, which is what
   workspace clones read.
2. **Same cost.** One query to upstream, as `set-head --auto` would have
   made.
3. **Fetches still count.** A failure here only warns.
4. **Refspecs unchanged.**

The weak point: the query is a second connection after the fetch's, so it
can fail on its own, and then the default branch stays stale until the next
fetch.

## Alternatives

### Also fetch into `refs/remotes/origin/*`, then `set-head --auto`

**Pros**

- Uses the command 0008 named.

**Cons**

- Every branch is stored twice, against 0008's choice of refspecs.
- It still only sets `refs/remotes/origin/HEAD`; the cache's `HEAD` would
  need a further `symbolic-ref` step.

### Set `HEAD` only on the first clone

**Pros**

- No extra query per fetch.

**Cons**

- A changed default branch is never noticed, so specs without a ref keep
  checking out the old one.

## Consequences

- 0008's status line notes this amendment.
- Every fetch makes two connections to upstream.
- Conformance: an integration test that changes a local upstream's `HEAD`,
  fetches, and checks the cache's `HEAD`; and one that a failing query
  leaves the fetch successful with a warning.

## References

[^git-remote]: git-remote(1), git 2.50.1, `set-head`: "With `-a` or `--auto`, the remote is queried to determine its HEAD, then the symbolic-ref `refs/remotes/<name>/HEAD` is set to the same branch. e.g., if the remote HEAD is pointed at next, git remote set-head origin -a will set the symbolic-ref `refs/remotes/origin/HEAD` to `refs/remotes/origin/next`. This will only work if `refs/remotes/origin/next` already exists; if not it must be fetched first."
[^git-ls-remote]: git-ls-remote(1), git 2.50.1, `--symref`: "In addition to the object pointed by it, show the underlying ref pointed by it when showing a symbolic ref. Currently, upload-pack only shows the symref HEAD, so it will be the only one shown by ls-remote."
[^bench-sethead]: Measured 2026-10-18 on an Apple M4, macOS 26.6.2, git 2.50.1. A local upstream with branches `main` (its `HEAD`) and `trunk`; a cache made with `git clone --bare`, 0008's two refspecs, and `git fetch --prune origin`. `git -C cache.git remote set-head origin --auto` printed "error: Not a valid ref: refs/remotes/origin/main", exit 1. After `git -C up.git symbolic-ref HEAD refs/heads/trunk`, a cache fetch left `symbolic-ref HEAD` at `refs/heads/main`. `git ls-remote --symref origin HEAD` printed `ref: refs/heads/trunk	HEAD`, and `git symbolic-ref HEAD refs/heads/trunk` updated the cache. Under `GIT_TRACE=1`, both `ls-remote --symref` and `set-head --auto` ran `git-upload-pack` on the upstream.
