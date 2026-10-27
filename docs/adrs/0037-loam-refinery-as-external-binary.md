# 0037. Reviews are written and combined with loam-refinery, which `ws` runs as an external binary and never links or reads the store of

- Status: Proposed
- Date: 2026-10-26

## Context

### Problem

Review agents ([0032](0032-review-agent-per-pr-head.md)) report prose.
loam-refinery defines a review document with addressable, prioritized,
anchored findings, keeps every passing review in a local store, and
combines all reviews of one commit with `collect-reviews`. Both projects
have the same owner, so `ws` could run it, import its packages, or copy
them. How should `ws` get at loam-refinery?

### What a solution needs

1. **One version of the contract.** The binary that checks and stores a
   review and the code that reads reviews back must agree on the format
   and the store.
2. **Agents can use it directly.** A review agent has to run `prime`,
   `describe --lens`, and `submit-review` itself; that loop is how the
   tool teaches the format and makes the reviewer fix its own mistakes.
3. **Follows `ws`'s existing pattern**: one package is the only place an
   external tool runs, with a version check
   ([0003](0003-use-git-cli.md), [0024](0024-claude-code-client.md)).
4. **Cheap** next to a review, which takes minutes.

### Relevant facts

- loam-refinery's packages are all under `internal/` (`collect`,
  `store`, `validate`, `render`, …), which Go doesn't let another module
  import[^go-internal].
- Its docs say of the store: "Neither half is an interface … `loam-refinery
  reviews` is the contract, the layout underneath it is not"[^lr-store].
- `submit-review` verifies anchors in the repository it runs in, and stores
  under the normalized `origin` remote; in a `ws` clone (origin
  `https://github.com/bobcob7/workspaces.git`) that was
  `github.com/bobcob7/workspaces`, the same string as `ws`'s cache
  id[^bench-lr]. An untracked file in the repo didn't stop submission.
- `collect-reviews --ref=SHA` outside any repository fails with exit 2
  ("not inside a repository; pass --repo=NAME"); with `--repo` it works
  and reports `head_check.source: "none"`[^bench-lr].
- Timings[^bench-lr]: `loam-refinery version` 0.01–0.06 s, `prime --list`
  0.01 s, `collect-reviews` of 2 reviews 0.19–0.24 s; the HTML report was
  22 KB.
- `loam-refinery version` prints `loam-refinery 0.2.0-dev`, a commit, and
  `schema 1`. The repo has no release tags yet[^bench-lr].
- loam-refinery's exit codes are 0 valid, 1 revise, 2 usage, 3 not a
  commit, 101 tool failure[^lr-help].

## Decision

We will require `loam-refinery` on PATH for reviews, and run it only from a
new package, `internal/refinery`:

- **Version check**, once per command that needs it: `loam-refinery
  version`; the first line must be `loam-refinery <semver>` at 0.2.0 or
  newer (pre-releases of 0.2.0 included) and a line must read `schema 1`.
  Missing is `refinery_missing`; too old or another schema is
  `refinery_version`. Both are exit 1, with an install hint (`go install
  github.com/bobcob7/loam-refinery/cmd/loam-refinery@latest`).
- **What `ws` runs**: `prime --list` (to check profile names before a
  review starts) and `collect-reviews --ref=<sha> --repo=<cache id>
  --format json|markdown|html`, in the review's repo directory when it
  still exists, so `head_check` compares against it, and from `CONFIG_DIR`
  otherwise. `--repo` is always passed, so the store bucket is the cache id
  whatever the current directory.
- **What agents run**: everything else (`prime --profile`, `describe`,
  `submit-review`), as loam-refinery's own docs teach.
- **Never** read the store's files or database, never set its environment
  variables, and never parse its human output. A loam-refinery exit 101 is
  `refinery_failed` with its stderr's first line.

### Why this option

1. The agent and `ws` run the same binary, so the format and store can't
   disagree. Linking packages would put a second copy of the format in
   `ws`, pinned by `go.mod`, next to whatever version the agent has on
   PATH.
2. Agents need the binary regardless; an import wouldn't remove the
   requirement, only add a second copy.
3. It's the git and Claude Code pattern: one package, one version check,
   fakes in tests via `internal/exectest`.
4. `collect-reviews` costs a quarter of a second; the version check is
   negligible.

It's weak in two ways: a second binary to install, and loam-refinery's
JSON output becomes a contract `ws` depends on without a release tag to pin.

## Alternatives

### Import loam-refinery as a Go module

Move `collect`, `store`, and `review` out of `internal/` and call them.

**Pros:** one binary for the user; typed results; no output parsing.
**Cons:** loam-refinery calls its store layout "not a contract", so `ws`
reading it would break on any store change the agent's binary makes; two
versions of the format whenever `go.mod` and the PATH binary differ; and
agents still need the binary. Could complement this later if
loam-refinery publishes a stable Go API.

### Copy the code into `ws`

**Pros:** no dependency at all; free to adapt.
**Cons:** the same two-versions problem, with no `go.mod` to tell you;
fixes in loam-refinery have to be ported by hand.

### `ws review submit`, embedding loam-refinery whole

`ws` would vendor loam-refinery and agents would run `ws review submit`.

**Pros:** one binary on PATH; `ws` controls the version.
**Cons:** re-exposes loam-refinery's whole CLI and token-budgeted guides
under another name; a review submitted with plain `loam-refinery` from the
same machine would use a different version against the same store.

## Consequences

- Reviews need loam-refinery as well as `gh` (for `--pr`); the rest of
  `ws` doesn't. The README's Requirements gains it.
- `internal/refinery` is the only place it runs; review checks that no
  other package runs `loam-refinery`, as for git.
- Tests fake it with a script on PATH; one test runs the real binary when
  it's on PATH and skips otherwise.
- The fields `ws` reads (`total`, `submissions[].profile`,
  `submissions[].superseded_by`, `comments[].id`) are pinned by a test
  against a captured `collect-reviews` output.
- **Open question:** loam-refinery should tag 0.2.0 so the check has a
  release to name. Until then `0.2.0-dev` passes.

## References

[^go-internal]: The Go Programming Language, "Go 1.4 Release Notes", Internal packages. <https://go.dev/doc/go1.4#internalpackages> (accessed 2026-10-26). "An import of a path containing the element “internal” is disallowed if the importing code is outside the tree rooted at the parent of the “internal” directory."
[^lr-store]: loam-refinery `docs/config.md` §4, at commit f7b52c0. "Neither half is an interface. §4.9 says so at length, and that is what lets this split be revisited later: `loam-refinery reviews` is the contract, the layout underneath it is not."
[^lr-help]: `loam-refinery --help`, built from loam-refinery f7b52c0. "exit 0 valid, 1 revise the review, 2 fix the invocation, 3 escalate: not a commit, 101 the tool failed".
[^bench-lr]: Measured 2026-10-26 on an Apple M4, macOS 26.6.2, git 2.50.1, loam-refinery built from f7b52c0 (`version`: `0.2.0-dev`, `schema 1`), with `LOAM_REFINERY_HOME` set to an empty temporary directory. A `git clone --local` of the workspaces cache with origin set to `https://github.com/bobcob7/workspaces.git`, at 2c36b53; an untracked `review-untracked.json` in it; two one-comment reviews with `profile` `tests` and `security` submitted (one on stdin, one from a path outside the repo), both exit 0. `collect-reviews --ref=<sha>` 3 runs, `real` from `/usr/bin/time -p`, range reported; `version` and `prime --list` likewise. Then from the temporary directory, `collect-reviews --ref=<sha>` without and with `--repo=github.com/bobcob7/workspaces`. `git tag` in loam-refinery printed nothing. Local only; no network.
