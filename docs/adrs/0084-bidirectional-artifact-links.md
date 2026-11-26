# 0084. Artifacts and conversations link both ways: references render as links, `ws` records reads and citations in `links.json`, the desktop scans local transcripts for citations, and each side lists the other

- Status: Accepted
- Date: 2026-11-26

## Context

### Problem

[0078](0078-artifacts.md) lets a prompt or reply cite an artifact as
`@artifact:<id>[/r<n>]`, and [0079](0079-artifact-comments-and-anchors.md)
adds comments. But the two sides don't know each other: in a conversation a
citation is plain text, and an artifact's page can't say which agents made,
read, discussed or were handed it. How should a conversation link to the
artifacts it names, and an artifact list the conversations it was part of,
without making `ws` read Claude Code's transcripts or slowing any command?

### What a solution needs

1. **Never break or slow a command for a link.** `get` on a full or locked
   store still copies the file; a link failure is a warning.
2. **Exactly-once counts.** A message that cites an artifact counts once,
   however often its transcript is scanned, by how many scanners, across
   restarts and rewrites.
3. **Links outlive what they point at.** A removed workspace, a deleted
   transcript (`claude rm`) or an artifact removed later doesn't lose the
   other side's history.
4. **`ws` stays independent of Claude Code's undocumented transcript
   format**, which only the desktop parses today.
5. **Cheap at rest.** With nothing new in any transcript, the steady-state
   cost is a `stat` per agent per sweep and no `ws` run.
6. **Stable JSON** (0016): fields are only added.

### Relevant facts

- A workspace has exactly one session and lives as long as it, so a
  conversation is identified by its workspace id; every agent revision and
  comment already records `Actor.workspace`[^actor].
- `created`, `revised`, `commented` and `resolved` are already on disk
  (`artifact.json`, `comments.json`)[^records]; `get` and citations leave no
  trace today.
- `ws claude`, `ws machine spawn` and the desktop's `ReplyBox` append a hint
  line (`run "ws machine artifact get <id>"`) to a prompt that cites
  artifacts[^hint]. Text typed into a terminal gets none.
- The desktop parses both transcript formats (Claude Code's
  `projects/<dir>/<session>.jsonl`, a runner's `state/<id>/runner/transcript.jsonl`)
  into items, reading only whole lines from an offset[^transcript]. It already
  scans transcripts for pull-request links, inside `list_entries`, which #92
  had to move off a tokio worker[^prlinks].
- Remote environments' transcripts aren't readable from the desktop, and the
  conversation view refuses them[^remote]. `claude rm` deletes the transcript
  with the workspace[^lifetime].

## Decision

### D1. What is recorded

- **Derived at read time**: `created`, `revised`, `commented`, `resolved`,
  from the existing records. No migration.
- **Stored** in `artifacts/<id>/links.json` (version 1), only for `read` and
  `cited`: one entry per (workspace, action, rev) with `name`, `root`,
  `environment`, `first_at`, `last_at` and `count`. A repeat updates
  `last_at` and `count`; for `cited`, `count` is messages, not occurrences.
  Written under the artifact's lock through `.tmp/`; a 2 s lock bound; 500
  entries, the oldest `last_at` evicted. A failure is a warning.
- **Writers**: `ws machine artifact get` (`read`, with the rev); `ws claude`
  and `ws machine spawn` whose prompt cites artifacts (`cited` for the new
  workspace, with `environment` for `--env <remote>`); the desktop's
  `ReplyBox` through the hidden `ws artifact cite <REF>... --workspace <id>`;
  the transcript scan (D2) through the hidden `ws artifact scan record`.
  `show`, `list` and `comment list` record nothing.
- **Output**: the full artifact JSON (`add`, `show`, `put`, people's and
  agents') gains `conversations`, one row per workspace (`actions`, `revs`,
  `first_at`, `last_at`, `environment`, `removed`), latest first; `list`
  gains `conversation_count`. Person actions are not listed.

### D2. The desktop scans local transcripts

- **Who**: the desktop's Rust backend, in a sweep every 30 s
  (`citations::sweep_forever`), over the **local** Claude agents and runners
  that `ws list --json` lists. It reuses the transcript parser, so the scan
  and the conversation view agree. `ws` never parses a transcript. Workspace
  Online doesn't sweep; the desktop on that machine does.
- **What**: `user` and `assistant` items, one transcript line being one
  message (key: Claude Code's `uuid`, or `r<offset>` for a runner line).
  Not thinking, tool calls or tool results (their references are `add`/`get`
  output, already `created`/`read`), notices, sidechain or meta lines. A user
  message carrying the hint line is skipped, since whoever added the hint
  recorded it. References inside fenced code blocks and inline code spans
  are blanked first; ids not in the store are skipped.
- **Dedupe**: `ws` keeps per-workspace scan state in
  `state/<id>/citations.json` under `state/<id>/citations.lock`: `seen`, the
  newest 4096 (message key, artifact id) pairs already recorded, and an
  advisory cursor (`transcript`, `offset`). `scan record` skips pairs in
  `seen`, and adds a pair only after its link write succeeded. So rescans,
  two scanners, retries and restarts never count twice, and a lost cursor
  costs only a rescan. The workspace id is checked against the id grammar
  before any path is built, and a workspace with no `workspace.toml` is
  refused, so a removal in progress isn't undone.
- **Cost**: per sweep and workspace, a `stat`; when the file grew, a read of
  at most 4 MiB of whole lines past the cursor, JSON-parsing only lines that
  contain `@artifact:`. A half-written last line waits. A shorter file or a
  new path is rescanned from 0. `ws` runs only when the new bytes held
  citations, the cursor moved 1 MiB past the stored one, or the path
  changed.

### D3. Each side shows the other

- **Conversation view**: a remark plugin links `@artifact:` references in
  assistant and thinking text (`ws-artifact:<id>[?rev=<n>]`, handled in-app,
  never `openExternal`), skipping code; user, notice and tool-result text, and
  comment bodies on the artifact page, get the same pass. Only the plugin
  mints `ws-artifact:` links (an author-written one is plain text), and the
  handler re-parses the id and revision strictly; the hint line collapses to a row of chips. A header strip,
  "Artifacts in this conversation", lists the artifacts linked to the
  workspace, from `ws artifact list --conversation <id>` (each summary gains
  that workspace's `conversation` row), refreshed every 15 s while open.
- **Artifact page**: a "Conversations (n)" section under the revision bar:
  name, environment, action chips, age, removed state; a click opens that
  agent's conversation.

### Why this option

1. Criterion 1: every link write is best effort with a bounded lock; the scan
   runs in its own task, never in `list_entries` or a command a person waits
   on.
2. Criterion 2: `seen` is keyed by message and artifact, and updated only
   after the link is written, under one lock per workspace. Weak: a
   transcript rewritten with more than 4096 citing messages could count an
   evicted pair again.
3. Criterion 3: links live in the artifact store, which outlives
   workspaces; scanned citations are copied there, so the transcript can go.
4. Criterion 4: the format stays the desktop's; `ws` stores keys and refs.
5. Criterion 5: unchanged transcripts cost a `stat`.
6. Criterion 6: only new fields and hidden commands.

## Alternatives

### `ws` scans transcripts itself

**Pros:** works without the desktop running. **Cons:** `ws` would parse an
undocumented format the desktop already parses, two parsers to keep in step;
and it still couldn't read remote transcripts.

### Scan on read, in `list_entries` or the conversation view

**Pros:** no new task. **Cons:** only conversations someone opens would be
scanned; `list_entries` already had to be fixed for transcript scanning
(#92).

### Dedupe by cursor only (compare-and-swap on the offset)

**Pros:** no `seen` list. **Cons:** a rewritten transcript or a new path
forces a choice between missing citations and counting them twice, and a
crash between the link write and the cursor write double counts.

### Record every occurrence, or a row per citation

**Pros:** full history. **Cons:** unbounded growth for a chatty agent; the
page wants who and when, not every mention.

### A separate action for agent replies (`mentioned`)

**Pros:** distinguishes who wrote it. **Cons:** another action to explain;
the transcript, one click away, says who.

### Defer the strip and the scan (the r1 recommendation)

**Pros:** smaller. **Cons:** the person reviewing the design asked for both.

## Consequences

- Agents see `conversations` in `ws machine artifact show`, so they can find
  who else worked on a document.
- `read` starts empty for existing artifacts; `cited` is backfilled from
  transcripts that still exist on the first sweep, each with its own time.
- Citations made in the last sweep interval (up to about 30 s) before a
  workspace is removed are lost; remote transcripts aren't scanned.
- Indented code blocks aren't treated as code by the scan.
- `cited` from transcripts is best effort and depends on a desktop running
  on the machine: a host with only the CLI or Workspace Online records the
  `cited` rows that `ws claude`, `spawn` and `cite` write, not scanned ones.
  `conversations` is therefore eventually and partially complete, and agents
  reading it through `ws machine artifact show` should treat it so.
- Rows are self-reported: an agent controls its own transcript and can cite
  any artifact. Counts are advisory, not an audit trail, and `name`, `root`
  and `environment` from `links.json` are rendered as text only, never as
  Markdown or a link target.
- `scan record`'s input carries `version: 1`; the desktop and `ws` change it
  together, and a Go test reads the JSON the Rust side produces (a fixture
  in `internal/cli/testdata`).
- `list --conversation` reads every `links.json`; a per-workspace index is the
  planned fix if that grows costly (design, The strip).
- The implementation follows in the stacked PRs listed in
  [tasks.md](../design/artifact-links/tasks.md); names such as
  `citations::sweep_forever` and `ws artifact scan` exist once they land.
- New hidden commands (`ws artifact cite`, `ws artifact scan cursors|record`)
  and a flag (`ws artifact list --conversation`); no new error codes; one new
  Tauri command, `artifact_cite`.
- Design, constraints and tasks:
  [docs/design/artifact-links](../design/artifact-links/README.md).

## References

[^actor]: `internal/artifact/artifact.go` at `41a30b2`, `Actor` and `Agent(workspace, name)`.
[^records]: `internal/artifact/artifact.go` at `41a30b2`, `Record.CreatedBy`, `Revision.CreatedBy`, `Comment.Author`, `Thread.ResolvedBy`.
[^hint]: `internal/artifact/ref.go` at `41a30b2`, `hintMarker` and `AppendHint`; `desktop/src/features/terminals/ReplyBox.tsx`, `appendHint(message)`.
[^transcript]: `desktop/src-tauri/src/transcript.rs` at `41a30b2`, `read_lines` ("complete = bytes.iter().rposition(|&b| b == b'\n')") and `items_of`.
[^prlinks]: `desktop/src-tauri/src/pr_links.rs` at `41a30b2`, `PrLinks::augment`, called from `list_entries`; commit "fix: stop transcript PR-link scans from burning a tokio worker in ws-online (#92)".
[^remote]: `desktop/src-tauri/src/commands.rs` at `41a30b2`, `read_transcript`: `refuse_remote(environment.as_deref(), "Showing the conversation")`.
[^lifetime]: `internal/cli/guides/ws.md`, "Sessions and lifetime".
