# 0033. `ws cache refs` lists a cached repo's branches and tags with their last commits, and `ws cache list` counts them

- Status: Proposed
- Date: 2026-10-24
- Amends: [0014](0014-command-set.md) (adds `ws cache refs`)

## Context

### Problem

The desktop app's Repos section ([0031](0031-desktop-sections-rail.md),
[design](../design/desktop-sections/repos.md)) shows each cached repo's
branches and tags in a table: name, the subject, author, and date of the
commit it points at, and the workspaces using it. Its list rows also say
how many branches each repo has. `ws` prints none of this today. Where
should the app get it?

### What a solution needs

1. **Git stays behind one interface.** `ws` runs git only through
   `internal/git` ([0003](0003-use-git-cli.md)), and the app gets
   everything it shows through the `ws` and `claude` CLIs
   ([0029](0029-desktop-app.md)), so the cache's layout can change without
   breaking the app.
2. **Enough for the table**: kind, name, commit, subject, author, and date
   per ref, for a repo with hundreds of branches.
3. **Fast enough to read on selecting a repo**, and counts for every repo
   on entering the section.
4. **Small and additive**: no change to existing output that scripts and
   agents parse ([0016](0016-output-contract.md)).

### Relevant facts

- `ws cache list --json` prints each repo's `id`, `path`, `upstream`,
  `default_branch`, `last_fetched_at`, and `size_bytes`, and no refs
  ([`ws cache`](../tools/ws-cache.md#list)). `ws cache search --refs`
  prints the names of branches and tags that contain a query, and nothing
  else about them.
- The cache holds `refs/heads/*` and `refs/tags/*` mirrored from upstream,
  plus `refs/ws/commits/*` for commits fetched by hash
  ([0008](0008-repository-cache.md), [0007](0007-repo-spec-syntax-and-resolution.md)).
- `git for-each-ref --format` prints any field per ref, and can separate
  fields with NUL: "%00 interpolates to \0 (NUL)"[^for-each-ref-format].
  For an annotated tag, "a fieldname prefixed with an asterisk (*) expands
  to the fieldname value of the peeled object, rather than that of the tag
  object itself"[^for-each-ref-peel].
- On a cached repo with 601 refs (1 default branch, 500 branches, 100
  tags), `git for-each-ref` with nine fields took a median 18.9 ms, and the
  same read through a `ws` subcommand 44.2 ms; on this project's own cache
  entry (5 refs), 8.5 ms and 43.7 ms. Adding a per-repo ref count to `ws
  cache list` took it from 32.4 ms to 41.3 ms for the 3 repos in the
  maintainer's cache, and from 25.9 ms to 43.7 ms for the 601-ref
  repo[^bench-refs]. The JSON for 601 refs was 99 KB.
- The app already runs `ws cache list --json` for the New agent dialog's
  suggestions (`desktop/src-tauri/src/commands.rs`, `cache_repos`).

## Decision

We will add **`ws cache refs <spec>`** and two counts to **`ws cache list`**.

```
ws cache refs [--json] <spec>
```

- The spec must name a cached repo (looked up like `ws cache path`; a ref
  in it is ignored). Nothing is fetched or locked: it reads the cache as it
  is, like `ws cache search --refs`.
- It runs one `git for-each-ref` over `refs/heads` and `refs/tags`, with
  `%(refname)`, `%(objectname)`, `%(subject)`, `%(authorname)`, and
  `%(committerdate:iso-strict)`, then the same five peeled (`*`), separated
  by `%00`. For an annotated tag, the peeled fields are used, so every ref
  describes a commit. `refs/ws/commits/*` aren't listed.
- Order: branches first, the default branch leading and the rest newest
  first; then tags, newest first; ties by name.
- `--json`:

  ```json
  {"id": "github.com/bobcob7/buff-bear",
   "default_branch": "main",
   "refs": [{"name": "main", "kind": "branch", "commit": "<40 hex>",
             "subject": "…", "author": "…", "date": "2026-10-24T09:12:00Z"}],
   "warnings": []}
  ```

  `kind` is `branch` or `tag`; `date` is the committer date in UTC
  ([0016](0016-output-contract.md)), or `null` when git gives none (a tag
  of a tag, whose peeled fields are empty). Text output is one line per
  ref: kind, short commit, name, subject.
- Errors are the cache's: `repo_not_found`, `repo_ambiguous` (exit 3),
  `git_failed` (exit 1).

`ws cache list --json` gains **`branches`** and **`tags`**: how many of
each the repo has, from one more `for-each-ref --format=%(refname)` per
repo, run in the listing's existing parallel read.

The command is in `ws llm cache refs` and [`ws cache`](../tools/ws-cache.md),
and 0014's command line becomes `ws cache <op>` with `refs` among the ops.

### Why this option

1. **Git stays in `ws`.** The app never reads the cache directory, so the
   layout and the refs `ws` keeps for itself stay private; it is the same
   rule `ws cache path` documents for agents ("Don't commit or push in the
   cache").
2. **Enough for the table**, in one call per repo, including annotated
   tags.
3. **Fast enough.** 44 ms per repo when one is selected; most of it is
   starting `ws` and its config and removal-pass checks, not git (19 ms of
   it at 601 refs). The counts add about 3–18 ms to a listing that runs
   only on entering the section and after an action
   ([design](../design/desktop-sections/repos.md)).
4. **Additive.** A new subcommand and two new keys; nothing existing
   changes meaning.

It is weaker than reading git directly in one way: every read pays for
starting `ws` (about 25 ms more per repo than `git` alone). The app reads
one repo's refs at a time, so that cost is paid once per selection.

## Alternatives

### The app runs `git for-each-ref` on `ws cache path`'s directory

**Pros**

- No change to `ws`; 25 ms faster per read[^bench-refs].
- The app could ask for any field without a `ws` release.

**Cons**

- A second git caller, outside `internal/git` and its version check and
  environment ([0003](0003-use-git-cli.md)), and outside 0029's "everything
  through `ws`".
- The app would have to know which refs are the cache's own
  (`refs/ws/commits/*`) and how `ws` picks the default branch, which are
  `ws`'s internals ([0008](0008-repository-cache.md),
  [0021](0021-cache-default-branch.md)).
- Agents and scripts get nothing from it.

### Extend `ws cache search --refs` with commit details

**Pros**

- No new subcommand.

**Cons**

- Search matches a query across every repo and is capped by `--limit`;
  listing one repo's refs would need an empty-query mode and an unlimited
  limit, which changes what search means.
- Its `{"matches": [{"id", "ref"}]}` shape would need per-match fields
  that only make sense for refs.

### Put every ref in `ws cache list --json`

**Pros**

- One call for the whole section.

**Cons**

- Every listing pays for every repo's refs: 99 KB for one 601-ref repo, to
  show a count in the list.
- Changes the cost of a command the app and agents already call for other
  reasons.

## Consequences

- `ws` has a ninth cache operation to document and keep in the agent
  guides; the `ws llm` coverage test fails if its guide is missing.
- `ws cache list` reads one more git command per repo. Its parallel read
  (8 at a time) keeps this small, but a cache of many large repos lists a
  little slower.
- The ref listing is as fresh as the last fetch. The app shows the fetch
  age next to it and offers Fetch.
- Conformance: `internal/cache/refs_test.go` checks the order, annotated
  tags peeling to their commit, and the counts against a real git repo;
  `internal/cli/cache_test.go` checks the JSON shape, `null` dates, and the
  error codes.
- Branch actions beyond reading (deleting a cached branch) stay out, as the
  design says; they would need a command of their own.

## References

[^for-each-ref-format]: git-for-each-ref(1), git 2.50.1 (Apple Git-155), macOS 26.6.2, `<format>`. "%00 interpolates to \0 (NUL), %09 to \t (TAB), and %0a to \n (LF)."
[^for-each-ref-peel]: git-for-each-ref(1), git 2.50.1 (Apple Git-155), macOS 26.6.2, FIELD NAMES. "For tag objects, a fieldname prefixed with an asterisk (*) expands to the fieldname value of the peeled object, rather than that of the tag object itself."
[^bench-refs]: Measured 2026-10-24 on an Apple M4, macOS 26.6.2, git 2.50.1, `ws` built from this branch (`make build`) and, for "before", the previously installed `go install` build (`ws version dev`, without `ws cache refs` or the counts). The 601-ref repo was built with `git fast-import` into a bare repo in a scratch `--config-dir` (one commit on `main`, 500 branches each one commit ahead of it, 100 lightweight tags). Each command was run once to warm up, then 15 times from a Python `subprocess.run` loop timing with `time.perf_counter`; median reported, with ranges: `git for-each-ref` (601 refs) 17.8–30.9 ms; `ws cache refs --json` 41.9–80.2 ms; on the real 5-ref repo 7.9–10.4 ms and 32.8–60.6 ms; `ws cache list --json` on the real cache 31.1–37.7 ms before and 39.5–46.6 ms after, on the scratch cache 24.8–35.4 ms before and 40.4–57.2 ms after. The real cache's `ws` runs included its removal pass, which adds variance.
