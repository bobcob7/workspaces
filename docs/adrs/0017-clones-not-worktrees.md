# 0017. Workspace repos are clones of the cache, not git worktrees

- Status: Accepted (2026-10-18)
- Date: 2026-10-18

## Context

### Problem

[0009](0009-workspace-repository-remotes.md) made each workspace repo a
hard-linked clone of the local cache. It didn't compare that with git
worktrees, which are the obvious alternative: Claude Code itself isolates
background sessions in worktrees[^cc-bg-worktree], and a worktree also gives
each agent its own checkout without copying history.

Should a workspace repo be a clone of the cache, or a worktree?

### What a solution needs

1. **Agents can't affect each other or the cache**: branches, config, and
   remotes in one workspace are invisible to the others.
2. **The cache keeps updating** while workspaces exist
   ([0008](0008-repository-cache.md)).
3. **Deleting a workspace is just deleting its directory**
   ([0015](0015-workspace-lifetime-follows-session.md)).
4. **Fast and small**: creation in well under a second, little disk per
   workspace.
5. **Any branch in any number of workspaces**, including the same one.

### Relevant facts

Measured with git 2.50.1 on a small local upstream and a bare cache using
0008's refspecs (`+refs/heads/*:refs/heads/*`, `+refs/tags/*:refs/tags/*`),
with worktrees added from the cache[^bench-wt]:

| Check | Worktree of the cache | Clone of the cache |
| ----- | --------------------- | ------------------ |
| Same branch in a second workspace | refused: "fatal: 'main' is already used by worktree at …" | allowed |
| `git remote add` in one workspace | appears in every other workspace | local to that clone |
| Branch an agent creates | created in the cache's `refs/heads/` | local to that clone |
| Cache `fetch --prune` while a worktree has a fetched branch checked out | whole fetch fails, exit 128: "refusing to fetch into branch 'refs/heads/feat' checked out at …"; no ref updated | unaffected |
| Cache `fetch --prune` while a worktree has an agent's own branch checked out | branch deleted (`- [deleted] (none) -> agent-co`); the worktree shows "No commits yet on agent-co", its commit no longer on any branch | unaffected |
| Workspace directory deleted with `rm -rf` | cache still lists it, marked `prunable` | nothing left behind |

Speed and size on rust-lang/cargo (about 2,900 files checked out)[^bench-wt-time]:

| | Clone of the cache | Worktree of the cache |
| - | ------------------ | --------------------- |
| Time to create, median of 4 | 0.30 s | 0.31 s |
| Disk beyond the cache | 26.5 MB | 26.0 MB |

A clone's objects are hard links into the cache, so most of its size is the
checked-out files, which a worktree needs too
([0009](0009-workspace-repository-remotes.md)).

## Decision

We will keep workspace repos as hard-linked clones of the cache, as 0009
specifies, and not use git worktrees, either of the cache or of the user's
own checkouts.

### Why this option

1. **Isolation.** A clone has its own refs, config, and remotes. A
   worktree shares all of them with the cache and with every other
   worktree.
2. **The cache keeps updating.** With worktrees, one workspace with a
   fetched branch checked out made every cache fetch fail, and a fetch that
   did run deleted a branch an agent was working on.
3. **Deleting is `rm -rf`.** A worktree also leaves metadata in the cache
   until `git worktree prune`.
4. **Speed and size** are the same within measurement noise.
5. **Same branch twice** works with clones; worktrees refuse it.

The cost is a full `.git` directory per repo, made cheap by hard links, and
that an agent's local branches live only in its workspace until pushed.

## Alternatives

### Worktrees of the cache

**Pros**

- One object store and one set of refs: a branch an agent creates is
  visible from the cache without pushing.
- The standard git feature for several checkouts of one repo, and what
  Claude Code uses[^cc-bg-worktree].

**Cons**

- Every row of the table above: shared config, refused duplicate branches,
  cache fetches that fail or delete agent branches.
- The cache's refspecs and `--prune` would need changing, which reopens
  [0008](0008-repository-cache.md).

### Worktrees of the user's own checkout

What Claude Code does on its own: a worktree under `.claude/worktrees/` of
the repo the session starts in[^cc-bg-worktree].

**Pros**

- The agent sees the user's local branches and unpushed commits, and its
  branches show up in the user's repo.
- No cache needed.

**Cons**

- Needs an existing checkout of every repo, and a workspace spans several
  repos ([0014](0014-command-set.md)).
- The same shared-config and one-branch-per-worktree limits, now against
  the user's own repo.

### Let Claude Code isolate sessions itself, without `ws`

**Pros**

- Nothing to build.

**Cons**

- One repo per session, and no specs, cache, or multi-repo workspaces.

## Consequences

- Nothing changes in [0009](0009-workspace-repository-remotes.md); this ADR
  records why worktrees were rejected.
- Work on an agent's local branch exists only in its workspace. Removing the
  workspace removes it ([0015](0015-workspace-lifetime-follows-session.md)).
- Claude Code mustn't create its own worktrees inside a workspace, which is
  why `ws claude` sets `worktree.bgIsolation` to `"none"`
  ([0015](0015-workspace-lifetime-follows-session.md)).
- Conformance: a test that `ws` never runs `git worktree`, and a test that
  two workspaces on the same branch can both be created and committed to.

## References

[^cc-bg-worktree]: Claude Code docs, "Agent view," section "How file edits are isolated." <https://code.claude.com/docs/en/agent-view> (accessed 2026-10-18). "Before editing files, Claude moves the session into an isolated git worktree under `.claude/worktrees/`, so parallel sessions can read the same checkout but each writes to its own."
[^bench-wt]: Measured 2026-10-18 on an Apple M4, macOS 26.6.2, git 2.50.1. A one-commit upstream with branches `main` and `feat`; a bare cache configured with 0008's two refspecs and fetched with `--prune`. Worktrees added with `git -C cache.git worktree add <dir> <branch>` (or `-b agent-co <dir> main`). Remote sharing: `git remote add upstream <url>` in one worktree, then `git remote -v` in another. Prune case: a commit on `agent-co` in its worktree, a new commit upstream on `main`, then `git -C cache.git fetch --prune origin` (exit 0). Clone case: `git clone cache.git`, a new branch and commit in the clone, then `git -C cache.git branch --list` showed no such branch.
[^bench-wt-time]: Measured 2026-10-18, same machine. A copy of a rust-lang/cargo bare cache (83.5 MB). 4 alternating runs of `git clone -q cache.git <dir>` and `git -C cache.git worktree add -q --detach <dir> master`, timed with Python `time.time()` around each command: clone 0.356, 0.306, 0.301, 0.303 s; worktree 0.308, 0.304, 0.315, 0.310 s. Disk: `du -sk` over the cache and one workspace together, minus the cache alone: clone 26,536 KB, worktree 26,004 KB.
