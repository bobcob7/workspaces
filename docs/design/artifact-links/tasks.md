# Tasks: artifact links

Implements [ADR 0084](../../adrs/0084-bidirectional-artifact-links.md); the
shapes and rules are in [README.md](README.md). Each task reads
[constraints.md](constraints.md) and updates the rows it names in the same
commit. Three stacked PRs; the second and third name the base they stack on.

## PR 1: docs

- [x] **T1** ADR 0084 (Accepted), this folder (README, mockups and their
  PNGs, constraints, tasks), the ADR index row (C6).

## PR 2: CLI and store (Go), base PR 1

- [ ] **T2** `internal/artifact/links.go`: `links.json` (version 1), `Link`,
  `RecordLink` under the artifact lock with a 2 s bound, the 500-entry cap,
  a newer version ignored with a warning; `Conversations(rec, threads,
  links)` merging derived and stored actions, one row per workspace, latest
  first. Table tests for merge, cap, eviction, lock timeout, concurrent
  records.
- [ ] **T3** `internal/artifact/citations.go`: `state/<id>/citations.json`,
  `RecordCitations` (dedupe by `seen`, 4096 cap, cursor rules, `at`
  clamped, refuses without `workspace.toml`), `Cursors`. Tests as in
  README.md › Tests.
- [ ] **T4** `internal/cli`: `get` records `read`; `ws claude` and
  `ws machine spawn` record `cited` (with `environment` for `--env`);
  `conversations` on full JSON with `removed`; `conversation_count` on
  summaries; `ws artifact cite`, `ws artifact scan cursors|record`,
  `ws artifact list --conversation`. C2.
- [ ] **T5** Guides and docs (C2, C6).

## PR 3: desktop (TS and Rust), base PR 2

- [ ] **T6** Rust: `citations.rs` (scanner and sweep), `artifact_cite`,
  `artifact_list`'s `conversation`, `conversations` in `Artifact` (C7).
- [ ] **T7** TS: the remark linkifier and its use in the conversation view,
  tool results, comment bodies and user text; the hint row; the strip; the
  Conversations section; `ReplyBox` calling `artifact_cite` (C7).
- [ ] **T8** Mock data and screenshots (`desktop/docs/artifact-links-*.png`),
  linked from the PR.
