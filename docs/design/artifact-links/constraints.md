# Constraints for artifact links

What a task in [tasks.md](tasks.md) breaks if it forgets something. Ids
`C1`…`C8` follow [the artifacts constraints](../artifacts/constraints.md),
whose rows still hold; this page lists only what [0084](../../adrs/0084-bidirectional-artifact-links.md)
changes. Measured 2026-11-26 on `main` at `41a30b2`. A task updates each row
it touches in the same commit as the code.

## C1 Error codes

None new. A link write failure is a `warnings` entry. `ws artifact cite`
reuses `artifact_unknown` (an id not in the store) and `usage`; `ws artifact
scan record` reuses `usage` (bad input, a newer `citations.json`) and
`workspace_not_found` (no `workspace.toml`). `docs/adrs/0016-output-contract.md`'s
`Status:` line is unchanged, and so are `errors.go` and `TestErrorCode_*`.

## C2 Command tree and guides

| Where | What changes |
| --- | --- |
| `internal/cli/artifact.go`, `newArtifactCmd` | add `a.newArtifactCiteCmd()` and `a.newArtifactScanCmd()` (with `cursors` and `record`); all under the hidden `ws artifact`, so `TestGuideTopics` needs no guide and `TestApp_newRootCmd` is unchanged |
| `artifact.go`, `newArtifactListCmd` | `--conversation <id>`; `--workspace` keeps its meaning |
| `internal/cli/guides/ws-machine-artifact-{add,show,get,put}.md` `## JSON output` | `conversations` in the sample, one sentence on its rows |
| `internal/cli/guides/ws-machine-artifact-list.md` `## JSON output` | `conversation_count` |
| `internal/cli/guides/ws.md`, `## Rules for agents`, the `@artifact:` bullet | one clause: `get` records that your conversation read it, and naming it in a reply records a citation |
| `internal/cli/llm.go` `guideTopicsFromReview()` | unchanged: no new topic |

## C3 Config paths, C4 runner guard, C5 lint

- C3: none. `state/<id>/citations.json` lives in a directory `ws` already
  makes; `artifacts/<id>/links.json` beside `comments.json`.
- C4: none. `get` is already allowed for runners.
- C5 (unchanged rules that bite here): no blank lines in function bodies;
  `funlen` 60/40, `cyclop` 15, `gocognit` 20; `internal/artifact` runs no
  programs and imports no cobra; files 0600, dirs 0700; `tagliatelle`
  snake_case JSON; `forbidigo` (stdin through `a.stdin`).

## C6 Docs with lists

| File | What to add |
| --- | --- |
| `docs/tools/ws-artifact.md` | `cite`, `scan cursors`, `scan record`, `list --conversation`, the `conversations` field |
| `docs/tools/ws-machine.md` | `conversations` and `conversation_count` in the JSON of `machine artifact` |
| `docs/state.md` `## Layout` | rows for `artifacts/<id>/links.json` and `state/<id>/citations.json` with `state/<id>/citations.lock` |
| `README.md` `## Roadmap` | one line for artifact links (0084) |
| `docs/adrs/README.md` | row 0084, added by the docs PR |

## C7 Tauri and web command lists

| Where | What changes |
| --- | --- |
| `desktop/src-tauri/src/lib.rs` `generate_handler!` | append `artifacts::artifact_cite` after `artifacts::artifact_comment_resolve` |
| `desktop/src-tauri/src/online/dispatch.rs` `COMMANDS` | append `"artifact_cite"` after `"artifact_comment_resolve"`, same order; `commands_match_generate_handler` compares them |
| `dispatch.rs` `match` | an `"artifact_cite"` arm; the `"artifact_list"` arm reads an optional `conversation` |
| `desktop/src-tauri/src/lib.rs` `setup` | spawn `citations::sweep_forever(runner, jobs_dir)` beside `loops::tick_forever`; `mod citations;` |
| `desktop/src/api.ts` | `artifactCite(workspace, refs)`; `artifactList` gains `conversation`; types `ArtifactConversation`, `conversations?` on `Artifact`, `conversation?` and `conversationCount?` on `ArtifactSummary` |
| `desktop/test/tauri-mock.js` | `artifact_cite`; `artifact_list` honouring `conversation`; sample `conversations` on `artifact_show` |

## C8 Sections

None: no new section; the strip is inside the agent's conversation view and
the Conversations section inside the artifact pane.

## What the pod can run — C9

As in [the artifacts constraints](../artifacts/constraints.md#what-the-pod-can-and-cant-run--c9):
Go 1.27 and `make lint`; `cargo test` and `clippy` for `desktop/src-tauri`;
Node 20 here is too old for vitest 5, so the desktop task installs Node 24
into `$CLAUDE_JOB_DIR/tmp/node` (or lists the frontend checks as unverified);
headless Chromium for screenshots via Vite and the committed Tauri mock.
