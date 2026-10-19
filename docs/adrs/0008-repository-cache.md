# 0008. Bare, locked, heads-and-tags repository cache

- Status: Accepted (2026-10-17); default-branch step amended by [0021](0021-cache-default-branch.md) (2026-10-18)
- Date: 2026-10-17

## Context

### Problem

Agents create many short-lived workspaces, often of the same repos. How
should `ws` keep local copies of upstream repos so that workspaces are quick
to create?

### What a solution needs

1. **Never corrupt or fail under concurrent use.** Several agents may run
   `ws init` on the same repo at once.
2. **Fast workspace creation**, including bringing the copy up to date.
3. **Hold what workspaces use** (branches and tags) without paying for what
   they don't.
4. **Searchable and resolvable offline**, for short names
   ([0007](0007-repo-spec-syntax-and-resolution.md)).

### Relevant facts

**A local copy pays off.** Cloning from a local bare repo was 15–20× faster
than cloning from upstream (median of 3 runs per repo)[^bench-clone]:

| Repo    | Cache size | Upstream clone | Clone from cache |
| ------- | ---------: | -------------: | ---------------: |
| ripgrep | 6.2 MB     | 0.99 s         | 0.05 s           |
| tokio   | 20.0 MB    | 1.93 s         | 0.13 s           |
| cargo   | 86.6 MB    | 4.40 s         | 0.28 s           |

A no-op `git fetch` to bring a copy up to date took 0.37–0.49 s per repo
(9 runs across the three repos)[^bench-clone].

**Most refs on GitHub are pull-request refs.** `--mirror` copies "all
refs"[^git-clone]. Counting with `git ls-remote` on 2026-10-17 gave:

| Repo       | heads | tags  | `refs/pull/*` | Share that is PR refs |
| ---------- | ----: | ----: | ------------: | --------------------: |
| ripgrep    | 16    | 544   | 1,366         | 71%                   |
| tokio      | 78    | 414   | 5,280         | 91%                   |
| cargo      | 94    | 233   | 9,579         | 97%                   |
| kubernetes | 62    | 2,499 | 102,190       | 81%                   |

**Concurrent fetches into one repo fail without a lock.** 50 trials of 8
parallel `git fetch --prune` into one bare repo, each run right after
force-pushing 5 branches upstream[^bench-lock]:

| Platform               | No lock          | Exclusive `unix.Flock` around each fetch |
| ---------------------- | ---------------- | ---------------------------------------- |
| macOS 26.6.2, git 2.50 | 354 of 400 failed | 0 of 400 failed                         |
| Linux 7.1, git 2.45    | 351 of 400 failed | 0 of 400 failed                         |

Every failure was git's own ref lock: `cannot lock ref … is at <sha> but
expected <sha>`.

**A directory tree is fast to scan.** Walking 1,000 cached repos laid out as
`<host>/<owner>/<name>.git` took about 2 ms[^bench-walk].

## Decision

We will store each upstream as a bare repo at
`$CONFIG_DIR/cache/repos/<host>/<owner>/<name>.git`
([0011](0011-config-dir.md)), set up like this:

```sh
git clone --bare <url> <tmp>
git -C <tmp> config remote.origin.fetch '+refs/heads/*:refs/heads/*'
git -C <tmp> config --add remote.origin.fetch '+refs/tags/*:refs/tags/*'
mv <tmp> <final>        # atomic, so a crashed clone never looks valid
```

- `ws init` runs `git fetch --prune origin` and
  `git remote set-head origin --auto` on each repo it uses before cloning.
  If the fetch fails, the command fails. `--offline` skips the fetch.
- Each repo has a lock file at
  `$CONFIG_DIR/cache/locks/<host>/<owner>/<name>.lock`, taken with
  `golang.org/x/sys/unix.Flock` ([0002](0002-implementation-language.md)).
  Anything that writes to the cache repo takes it **exclusive**: the first
  clone, fetches, and fetching a commit into `refs/ws/commits/`
  ([0007](0007-repo-spec-syntax-and-resolution.md)). Cloning a workspace
  from it takes it **shared**.
- The directory tree is the index. Listing, search, and short-name
  resolution walk `repos/`.

### Why this option

1. **Concurrency.** The lock removed every failure on both platforms.
2. **Speed.** A workspace pays one no-op fetch, and creating it is still
   3–5× faster than an upstream clone
   ([0009](0009-workspace-repository-remotes.md)).
3. **Only what's used.** Heads and tags only. PR refs, 71–97% of refs on the
   repos measured, are skipped. A PR commit is fetched on demand by its hash.
4. **Offline search.** The tree is the index, and scanning it is
   milliseconds even at 1,000 repos.

The weak point is that an `ws init` waits while another process fetches the
same repo.

## Alternatives

### `--mirror` clones

**Pros**

- One flag, and the copy has every ref, including PR heads.

**Cons**

- Stores PR refs, which are 71–97% of refs on the repos measured, and
  fetches them every time.
- Its refspec, `+refs/*:refs/*`, deletes `refs/ws/commits/*` on the next
  `fetch --prune`[^bench-mirror].

### Non-bare clones

**Pros**

- The cache can be inspected and built in directly.

**Cons**

- Adds a working tree nobody uses (26 MB for cargo).
- Invites accidental edits to the cache.

### No locking, relying on git's ref locks

**Pros**

- No lock files and no waiting.

**Cons**

- 87–89% of concurrent fetches failed in the test above. Retrying would
  hide that, not fix it.

### A separate SQLite or JSON index

**Pros**

- Can hold extra data, such as last fetch time, without touching the repos.

**Cons**

- A second source of truth to keep in sync with the tree.
- The tree scan it would replace takes about 2 ms.

## Consequences

- `ws init` waits on another fetch of the same repo instead of failing.
- The cache grows until entries are removed. `ws cache list --json` reports
  size and last-fetch time for a later prune.
- `refs/ws/commits/*` refs accumulate until `ws cache` prunes them.
- Conformance: an integration test runs 8 parallel `ws init` against a
  freshly force-pushed local upstream and asserts 0 failures. Another asserts
  that `fetch --prune` keeps `refs/ws/commits/*`.

## References

[^bench-clone]: Measured 2026-10-17 on an Apple M4, macOS 26.6.2 (APFS), git 2.50.1, HTTPS to github.com. For each repo: `git clone --bare` into a cache, then 3 runs each of an upstream `git clone` and a `git clone <cache>`, timed with Python's `time.perf_counter` within one process; plus 3 no-op `git fetch --prune` runs per cache. Clone times include checkout. Upstream times depend on network conditions on the day.
[^bench-lock]: Measured 2026-10-17. macOS: Apple M4, macOS 26.6.2 (APFS), git 2.50.1. Linux: `alpine:3.20` container (git 2.45.4) on Podman, kernel 7.1.3 aarch64. A local bare upstream with 5 branches and a bare cache with the refspecs above. Each trial adds a commit to and force-pushes each branch, then starts 8 background `git -C cache.git fetch -q --prune origin` and counts non-zero exits. The locked variant runs each fetch under a 20-line Go wrapper that takes `unix.Flock(fd, LOCK_EX)` on a shared lock file (Go 1.27.1, `golang.org/x/sys`, built static for each OS).
[^bench-walk]: Measured 2026-10-17, Apple M4, macOS 26.6.2, Go 1.27.1. 1,000 directories `repos/github.com/owner{01..50}/name{01..20}.git`, each with `HEAD`, `objects/`, and `refs/`. `filepath.WalkDir`, collecting each `*.git` directory and skipping into none of them: 1.6–2.5 ms (5 warm runs).
[^bench-mirror]: Measured 2026-10-17, git 2.50.1. A `--mirror` clone of a one-commit local bare repo, plus a local ref `refs/ws/commits/<hash>`. After `git fetch --prune origin`, `git show-ref` listed no `refs/ws/` refs.
[^git-clone]: git-clone(1), `--mirror`. <https://git-scm.com/docs/git-clone> (accessed 2026-10-17). "Compared to `--bare`, `--mirror` not only maps local branches of the source to local branches of the target, it maps all refs (including remote-tracking branches, notes etc.)"
