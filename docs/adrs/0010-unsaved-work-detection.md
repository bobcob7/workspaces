# 0010. Detect unsaved git work with three porcelain checks against `origin`

- Status: Rejected. Unclean git state shouldn't block cleanup: workspaces
  are often used for investigations and proofs of concept whose output lives
  outside the workspace. See [0013](0013-cleanup-ignores-git-state.md).
- Date: 2026-10-17

## Context

### Problem

[0006](0006-session-based-cleanup.md) decided that automatic removal skips
workspaces whose git repos have uncommitted, unpushed, or stashed work, and
that explicit `ws cleanup <id>` deletes them anyway and reports what was
discarded. That needs a definition of "unsaved git work" and a way to detect
it.

The underlying question is whether detection is worth having at all. Without
it, 0006's rule can't be implemented, and a different rule would have to
replace it.

### What a solution needs

1. **No false "clean".** A repo reported clean must not hold commits or
   changes that exist nowhere else.
2. **Cheap enough for the removal pass**, which checks each workspace that
   is due ([0006](0006-session-based-cleanup.md)).
3. **Parse only stable output** ([0003](0003-use-git-cli.md)).
4. **Few false "unsaved".** Each one leaves a workspace behind until someone
   removes it by hand.

### Relevant facts

- **"Pushed to a remote" isn't always durable.** In a test of nine
  unsaved-work scenarios, a branch pushed only to the workspace's `cache`
  remote passed `git rev-list --branches --not --remotes` as clean. The next
  cache `fetch --prune` then deleted it. Restricting the check to
  `--remotes=origin` caught it. The other eight scenarios gave the same
  result under both checks[^bench-checks].
- **Without any check, 7 of the 9 scenarios lose work** when the workspace
  is removed. The other two are the clean repo and a branch already pushed
  to `origin`.
- **Cost.** The three checks below took about 57 ms on a cargo workspace
  repo[^bench-cost].
- **`git status --porcelain=v1` is stable** across git versions and user
  configuration[^git-status]. It lists untracked files and leaves out
  ignored ones by default: in the same test, an untracked file was flagged
  and an ignored one wasn't[^bench-checks].
- **Some workspaces have no git repos.** Detection can only cover git
  content.

## Decision

We recommend keeping 0006's rule and defining unsaved work as any output
from these, run in each workspace repo:

- `git status --porcelain=v1`: uncommitted or untracked changes
- `git rev-list --branches --not --remotes=origin`: local commits not on
  upstream
- `git stash list`: stashes

Automatic removal skips a workspace with unsaved work. `ws cleanup <id>`
deletes it and reports what was found. `--dry-run` previews either.

### Why this option

1. **No false "clean".** `--remotes=origin` caught the cache-only push that
   `--remotes` missed. The three checks flagged all seven scenarios that would
   lose work, and reported the two safe ones as clean.
2. **Cheap.** About 57 ms per cargo repo, and only for workspaces that are
   due for removal.
3. **Stable output.** Porcelain status, plus two commands whose output is
   only checked for being empty.
4. **False "unsaved" is the weak point.** Untracked build output that isn't
   in `.gitignore` counts as unsaved. So does a branch an agent created and
   abandoned without pushing. These workspaces stay in `ws list` as
   `idle (unsaved work)` until removed explicitly.

## Alternatives

### No detection

Automatic removal deletes everything once the grace period passes, and
explicit cleanup deletes without reporting.

**Pros**

- Simplest, with no git dependency in cleanup.
- Matches "sometimes I want to lose unpushed work" for every removal.
- Nothing lingers. `ws list` never shows `idle (unsaved work)`.

**Cons**

- Contradicts [0006](0006-session-based-cleanup.md)'s rule and its "never
  silent" need, so it needs an ADR superseding 0006.
- An agent that commits but never pushes loses that work one grace period
  after its last session, with no record.

### Detect and report, but never skip

Automatic removal deletes everything, and prints or logs what it discarded.

**Pros**

- Nothing lingers, and losses are recorded.

**Cons**

- Automatic removal runs at the start of unrelated commands, so the report
  is a line in the output of some later `ws init`, easy to miss.
- Also needs an ADR superseding 0006.

### Archive before removal

Save unsaved work into the cache as refs, then delete the workspace.

**Pros**

- Nothing lingers and nothing is lost.

**Cons**

- Uncommitted and untracked changes would need temporary commits, and
  restoring them needs commands of its own.
- Not prototyped, so the costs are unknown.
- Archive refs accumulate in the cache.

### Checking against any remote (`--remotes`)

**Pros**

- One less thing to configure.

**Cons**

- Reported a cache-only push as clean, and the next cache update deleted
  that work.

## Consequences

- Automatic cleanup can't remove git work that exists only in the
  workspace. Explicit cleanup always reports what it discarded.
- Ignored files, such as build output, aren't checked. They can be
  regenerated.
- If detection is dropped later, the ADR replacing 0006 removes the skip
  rule, and this ADR becomes `Superseded`.
- Conformance: an integration test reproduces all nine scenarios and asserts
  the `--remotes=origin` results.

## References

[^bench-checks]: Measured 2026-10-17 on an Apple M4, macOS 26.6.2, git 2.50.1. Each scenario builds a fresh workspace repo from a local bare upstream and cache using the [0009](0009-workspace-repository-remotes.md) sequence, then introduces one state: clean, modified, staged, untracked, unpushed commit on the current branch, unpushed commit on another branch, stash, branch pushed to `origin`, branch pushed only to `cache`, plus a tenth control with a file under a `.gitignore`d directory. The three checks are then run with `--remotes=origin` and with `--remotes`. Results were identical except for the cache-only push, which only `--remotes=origin` flagged. After `git fetch --prune origin` in the cache, that branch (`feat3`) was gone from it. Repeated the same day with the same results.
[^bench-cost]: Measured 2026-10-17, same machine. A clean cargo workspace repo built with the [0009](0009-workspace-repository-remotes.md) sequence; the three commands in a bash script, timed as one process from Python with `time.time()`: 55.7–57.9 ms (5 warm runs; bash startup is about 2 ms).
[^git-status]: git-status(1), "Porcelain Format Version 1." <https://git-scm.com/docs/git-status> (accessed 2026-10-17). "guaranteed not to change in a backwards-incompatible way between Git versions or based on user configuration."
