# 0037. Reviews are a `ws` feature built from a fork of loam-refinery's code and specialized for workspaces; `ws` doesn't run or depend on loam-refinery

- Status: Accepted (2026-10-27)
- Date: 2026-10-26

## Context

### Problem

Review agents ([0032](0032-review-agent-per-pr-head.md)) report prose.
loam-refinery defines a review document with addressable, prioritized,
anchored findings; checks it (structural errors, advisory quality
checks, anchors verified against the commit); teaches it to a model a
piece at a time (`prime`, `describe --lens`); focuses reviewers with
profiles; keeps passing reviews in a store; and combines all reviews of
one commit. Both projects have the same owner, so `ws` may run it,
import it, copy it, or rewrite the idea. Which?

### What a solution needs

1. **One version of the contract**: the code that checks a review and the
   code that reads it back always agree.
2. **Fits workspaces**: a review is already a workspace with a known repo
   and commit ([0032](0032-review-agent-per-pr-head.md)); the format and
   commands should use that, not ask the agent to restate it.
3. **Free to change**: combining, triage, and posting
   ([0040](0040-review-decisions-kept-with-findings.md),
   [0041](0041-desktop-acts-on-findings.md)) need things loam-refinery
   rules out on purpose.
4. **Follows `ws`'s rules**: one binary ([0002](0002-implementation-language.md)),
   git only through `internal/git` ([0003](0003-use-git-cli.md)), the
   output contract ([0016](0016-output-contract.md)).

### Relevant facts

- loam-refinery's packages are all under `internal/`, which Go doesn't
  let another module import[^go-internal]. Its non-test Go is about
  10,400 lines; the largest packages are `render`, `advisory`, `verify`,
  `collect`, and `schema`[^count].
- Its docs call the store layout "not a contract"[^lr-store], and list as
  non-goals "Posting comments to GitHub or any other forge" and "Merging,
  ranking, or reconciling reviews"[^lr-readme].
- Its document requires `ref` (a full SHA) on every review, and
  `profile` is "caller authored and unverifiable"[^lr-collect]; its exit
  codes are 0, 1, 2, 3, and 101[^lr-help], where `ws`'s are 0–4
  ([0016](0016-output-contract.md)).
- Its store uses SQLite through `modernc.org/sqlite`, a pure-Go driver;
  its other dependencies are `santhosh-tekuri/jsonschema` and
  `alecthomas/chroma`, both already in `ws`'s module graph as indirect
  dependencies[^count].
- Submitting two profiled reviews and combining them took 0.19–0.24 s
  with the loam-refinery binary[^bench-lr], so the work itself is cheap
  wherever it runs.

## Decision

We will fork loam-refinery's code into `ws` at commit f7b52c0 and make it
a `ws` feature, owned and changed here from then on:

- **What comes over**, under `internal/review/`: the document types and
  JSON Schema, structural and advisory checks, anchor verification
  (rewired to `internal/git`), the `prime`/`describe` entry registry with
  its token budgets, the profiles, combining, and the Markdown and HTML
  renderers, each with its tests. The package doc names the source commit.
  Nothing is synced afterwards.
- **What doesn't**: its CLI, config, and SQLite store.
- **Agent commands** are `ws review prime [--profile P]`, `ws review
  describe [--lens N]`, and `ws review submit [path|-]`, run inside a
  review workspace ([0039](0039-ws-review-command.md)).
- **Specialized for workspaces:**
  1. `submit` reads the workspace's review record: `ref` may be left out
     and is filled in from it, and if given must match; `profile` must be
     one of the record's profiles. A review can only be submitted from
     its review workspace, where anchors are verified.
  2. `ws` owns the store, as JSON files under `CONFIG_DIR/reviews/`
     ([0039](0039-ws-review-command.md)); `ws review report --json` is the
     contract, the layout is described in `docs/state.md`.
  3. Results follow [0016](0016-output-contract.md): an invalid review is
     `review_invalid` (exit 1) with its diagnostics and the lenses to open
     in `details`; anchors that can't be checked (working tree diverged,
     commit missing) are `review_unverifiable` (exit 1).
  4. Profiles are `ws`'s: the eight from loam-refinery built in, more or
     replacements in `CONFIG_DIR/review/profiles/<name>.md`, each with
     file patterns that pull it into a review and a model
     ([0038](0038-review-fans-out-to-profile-subagents.md)).
  5. loam-refinery's non-goals don't bind `ws`: findings can carry
     decisions ([0040](0040-review-decisions-kept-with-findings.md))
     and be posted from the app ([0041](0041-desktop-acts-on-findings.md)).

### Why this option

1. One binary checks, stores, and reads reviews, so there is no version to
   match and nothing to install besides `ws`.
2. The review record already knows the repo, commit, and profiles, so the
   agent can't get them wrong; checks loam-refinery has to leave advisory
   or unverifiable (who submitted, for which commit) become hard checks.
3. Owning the store and the format is what lets 0040 and 0041 attach
   decisions and actions to findings without working around another
   project's contract.
4. Starting from the fork keeps a tested format, checks, and token-tuned
   guides, instead of rewriting them.

It's weak in that `ws` takes on about 10,000 lines it didn't have, two
projects now carry the same ideas and will drift apart, and an
improvement to one doesn't reach the other unless it is ported by hand.

## Alternatives

### Run the `loam-refinery` binary

Require it on PATH, run `collect-reviews` from one package with a version
check; agents run `prime` and `submit-review` themselves.

**Pros:** no code taken on; loam-refinery's improvements arrive with an
upgrade; its store is shared with reviews made outside `ws`.
**Cons:** a second install with a version to check, and no release tag to
pin yet; the agent restates repo and commit that `ws` already knows;
`ws` must not read its store ("not a contract"), so decisions would live
in another file beside it; its non-goals and exit codes stay in the way.

### Import loam-refinery as a Go module

Move its packages out of `internal/` and call them.

**Pros:** one binary; improvements arrive by `go get`.
**Cons:** loam-refinery would have to keep a public Go API for one
caller; every specialization above becomes a hook in another project.

### Rewrite the idea from scratch

**Pros:** only what `ws` uses; no inherited structure.
**Cons:** re-deriving checks, advisories, and guides that already have
tests and measured budgets. Forking and deleting what isn't used reaches
the same place sooner.

## Consequences

- `go.mod` gains `santhosh-tekuri/jsonschema` and `alecthomas/chroma` as
  direct dependencies; the build stays `CGO_ENABLED=0`.
- The forked tests come along, including the token budgets for `prime`
  and `describe`; a review checks that only `internal/git` runs git.
- loam-refinery itself needs no change and isn't a requirement.
- Reviews submitted with loam-refinery outside `ws` aren't in `ws`'s
  store, and the other way round.

## References

[^go-internal]: The Go Programming Language, "Go 1.4 Release Notes", Internal packages. <https://go.dev/doc/go1.4#internalpackages> (accessed 2026-10-26). "An import of a path containing the element “internal” is disallowed if the importing code is outside the tree rooted at the parent of the “internal” directory."
[^count]: Counted 2026-10-27 at loam-refinery f7b52c0: `find internal -name '*.go' ! -name '*_test.go' | xargs wc -l` totals 10,392; `go.mod` requires `modernc.org/sqlite`, `santhosh-tekuri/jsonschema/v6`, and `alecthomas/chroma/v2`. `ws`'s `go.mod` at 2c36b53 lists the last two as `// indirect`.
[^lr-store]: loam-refinery `docs/config.md` §4, at f7b52c0. "Neither half is an interface. §4.9 says so at length, and that is what lets this split be revisited later: `loam-refinery reviews` is the contract, the layout underneath it is not."
[^lr-readme]: loam-refinery `README.md`, Non-goals, at f7b52c0. "Posting comments to GitHub or any other forge"; "Merging, ranking, or reconciling reviews."
[^lr-collect]: loam-refinery `docs/features/combined-reviews.md` §7.2, "Caller authored and unverifiable", at f7b52c0; and `describe` at the same commit: "ref 40-char lowercase commit SHA; required, read by every anchor".
[^lr-help]: `loam-refinery --help`, built from f7b52c0. "exit 0 valid, 1 revise the review, 2 fix the invocation, 3 escalate: not a commit, 101 the tool failed".
[^bench-lr]: Measured 2026-10-26 on an Apple M4, macOS 26.6.2, git 2.50.1, loam-refinery built from f7b52c0, `LOAM_REFINERY_HOME` set to an empty temporary directory, in a `git clone --local` of the workspaces cache at 2c36b53. Two one-comment reviews (profiles `tests` and `security`) submitted, both exit 0; `collect-reviews --ref=<sha>` 3 runs, range of `real` from `/usr/bin/time -p`. Local only.
