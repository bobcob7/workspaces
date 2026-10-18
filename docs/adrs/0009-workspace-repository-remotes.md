# 0009. Hard-linked workspace clones with upstream as `origin`

- Status: Accepted (2026-10-17)
- Date: 2026-10-17

## Context

### Problem

A workspace repo is created from the cache
([0008](0008-repository-cache.md)). How should it be cloned, and how should
its remotes be set up?

### What a solution needs

1. **Survive whatever happens to the cache later**, such as `gc`, branch
   deletion, or `ws cache remove`.
2. **Agents pull, push, and open PRs against upstream with no extra setup.**
3. **Fast, with little extra disk per workspace.**
4. **No network beyond the cache fetch** that `ws init` already does.

### Relevant facts

git offers four ways to clone from a local repo. Measured on the cargo cache
(median of 3 runs)[^bench-clone]:

| Strategy                   | Time   | Extra disk | Contacts upstream |
| -------------------------- | -----: | ---------: | ----------------- |
| plain local clone          | 0.28 s | 0.5 MB     | no                |
| `--no-hardlinks`           | 0.32 s | 80 MB      | no                |
| `--shared` (alternates)    | 0.29 s | 0.5 MB     | no                |
| `--reference --dissociate` | 1.37 s | 80 MB      | yes               |

- **A plain local clone hard-links** the object files "when
  possible"[^git-clone]. The cache and one clone together took 89,212 KB,
  against 88,676 KB for the cache alone.
- **On another filesystem it silently copies instead.** Cloning onto a tmpfs
  mount succeeded with no warning, the pack file had a link count of 1, and
  `git fsck --full` passed[^bench-crossfs]. Copying made the clone step
  14–38% slower (`--no-hardlinks` against hard-linked, 3 repos)[^bench-clone].
- **`--shared` corrupts once the source drops objects.** git warns that "the
  cloned repository will become corrupt"[^git-clone]. After deleting a
  branch upstream and running `fetch --prune` and `gc --prune=now` in the
  cache, the `--shared` clone failed with `fatal: bad object HEAD`. The
  hard-linked clone still passed `git fsck --full`[^bench-gc].
- **Push goes to `remote.pushDefault` when set**, which "Overrides
  branch.<name>.remote for all branches"[^git-config]. Otherwise it follows
  the branch's tracking remote, which after a plain clone is the clone
  source.
- **Work pushed only to the cache is lost.** A branch pushed from a
  workspace to its cache remote was deleted by the next cache
  `fetch --prune` (measured for [0010](0010-unsaved-work-detection.md), since rejected).

## Decision

We will set up each workspace repo like this:

```sh
git clone -o cache --no-checkout <cache-repo> <dir>   # hard-linked; remote named "cache"
git -C <dir> remote add origin <upstream-url>
git -C <dir> fetch --no-tags cache \
    '+refs/heads/*:refs/remotes/origin/*' '+refs/tags/*:refs/tags/*'   # local, no network
git -C <dir> config remote.pushDefault origin
git -C <dir> switch -C <branch> --track origin/<branch>   # or: switch --detach <tag|commit>
```

For a commit fetched into the cache as `refs/ws/commits/<hash>`
([0007](0007-repo-spec-syntax-and-resolution.md)), the workspace first runs
`git fetch cache refs/ws/commits/<hash>`, then `switch --detach <hash>`.

As a result, `origin` points at upstream and is the default for fetch, pull,
and push, and every branch tracks `origin/<branch>`. The `cache` remote
remains for explicit offline fetches.

Two details came from running the sequence:

- **`switch -C`, not `-c`.** `clone --no-checkout` has already created the
  default branch, so `-c` fails with "a branch named 'master' already
  exists"[^bench-setup].
- **`clone -o cache`**[^git-clone] instead of cloning and then running
  `remote rename`. The rename took 186 ms on cargo because it rewrites every
  remote-tracking ref[^bench-setup].

The sequence took a median of **164 ms on tokio and 394 ms on cargo**. With
the no-op cache fetch, creating a workspace repo is **3.0× (tokio) to 5.2×
(cargo) faster** than cloning from upstream[^bench-total]. On the result,
`git status -sb` shows `## master...origin/master`, and `remote.pushDefault`
is `origin`.

### Why this option

1. **Survives the cache.** Hard links share files, not references, so a `gc`
   in the cache can't remove objects the workspace holds.
2. **Upstream by default.** `origin` is upstream, so push, pull, and PR
   tooling work unchanged.
3. **Fast and small.** About as fast as `--shared`, and 0.5 MB of extra disk
   on cargo.
4. **No extra network.** Everything comes from the cache.

The weak point is the filesystem requirement. If the cache and workspaces
end up on different filesystems, every workspace silently costs a full copy.

## Alternatives

### `--shared`

**Pros**

- As fast and small as a hard-linked clone, on any filesystem.

**Cons**

- Corrupts the workspace once the cache garbage-collects objects it uses
  (reproduced above).

### `--reference --dissociate`

**Pros**

- Safe, and keeps no link to the cache.

**Cons**

- 4.9× slower than a hard-linked clone on cargo, 80 MB more disk, and it
  contacts upstream.

### `--no-hardlinks`

**Pros**

- Safe on any filesystem, and behaves the same everywhere.

**Cons**

- 80 MB more disk per cargo workspace, and 14–38% slower.

### The cache as `origin`

**Pros**

- No remote setup; this is what a plain `git clone <cache>` gives.

**Cons**

- `git push` writes to the local cache instead of upstream, and the next
  cache `fetch --prune` deletes the pushed branch.

## Consequences

- After creation, a workspace doesn't depend on the cache.
- `origin/*` is only as fresh as the cache fetch at creation time, until the
  agent runs `git fetch`.
- [0011](0011-config-dir.md) keeps workspaces next to the cache under one
  directory, so they share a filesystem unless a user mounts something in
  between.
- Conformance: an integration test runs `gc --prune=now` in the cache after
  creating a workspace and asserts that `git fsck --full` passes in the
  workspace. Another asserts that `@{upstream}` is `origin/<branch>` and
  that `remote.pushDefault` is `origin`.

## References

[^bench-clone]: Measured 2026-10-17 on an Apple M4, macOS 26.6.2 (APFS), git 2.50.1, HTTPS to github.com. Against bare caches of ripgrep, tokio, and cargo: 3 runs per strategy, timed with Python's `time.perf_counter` within one process, including checkout. Disk use from `du -sk`; the hard-link figure runs `du` over the cache and clone together so linked files count once.
[^bench-crossfs]: Measured 2026-10-17 in an `alpine:3.20` container (git 2.45.4) on Podman, kernel 7.1.3 aarch64, started with `--tmpfs /mnt/t`. A bare cache on the container's overlay filesystem, after `git gc`. `git clone cache.git /mnt/t/other` printed only "Cloning into…" and "done."; `stat -c %h` on the clone's pack file gave 1, against 2 for a clone on the same filesystem; `git fsck --full` exited 0.
[^bench-gc]: Measured 2026-10-17 on an Apple M4, macOS 26.6.2 (APFS), git 2.50.1. Local upstream with a 3-commit `feature` branch; bare cache; one `--shared` and one plain clone of `feature`. Then delete `feature` upstream, `git fetch --prune` the cache, and `git gc --prune=now` with reflogs expired. Run `git fsck --full` and `git log -1` in each clone.
[^bench-setup]: Observed 2026-10-17, same machine, while developing the sequence against the tokio and cargo caches. `git switch -c master --track origin/master` after `clone --no-checkout` failed with "fatal: a branch named 'master' already exists". `git remote rename origin cache` on a cargo clone took 186 ms.
[^bench-total]: Measured 2026-10-17, same machine. The five commands above (plus one `git symbolic-ref` on the cache to find the default branch) in a bash script, timed as one process from Python with `time.time()`, 5 runs each: tokio 163–176 ms, cargo 357–437 ms. Bash startup is about 2 ms. No-op `git fetch --prune` on each cache, 3 runs: tokio median 483 ms, cargo 448 ms. Upstream clone medians from the clone benchmark: tokio 1.93 s, cargo 4.40 s.
[^git-config]: git-config(1), git 2.50.1 (`git help config`, 2026-10-17). `remote.pushDefault`: "The remote to push to by default. Overrides branch.<name>.remote for all branches, and is overridden by branch.<name>.pushRemote for specific branches."
[^git-clone]: git-clone(1). <https://git-scm.com/docs/git-clone> (accessed 2026-10-17). `--local`: "The files under `.git/objects/` directory are hardlinked to save space when possible." `--shared`: "this is a possibly dangerous operation … If these objects are removed and were referenced by the cloned repository, then the cloned repository will become corrupt." `--origin`: "Instead of using the remote name `origin` to keep track of the upstream repository, use <name>."
