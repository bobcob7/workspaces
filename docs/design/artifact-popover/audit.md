# Audit: silent interactions in the artifacts UI

Scope: `desktop/src/features/artifacts/` plus `ui/uiSlice.ts` (notices), `App.tsx` (snackbar), `api.ts`. Read-only audit of
branch `feat/artifact-ux-feedback` at `629fc66`. Paths below are relative to `desktop/src/`; `A/` = `features/artifacts/`.

Goal: every interaction gives visible feedback. Feedback channels that exist today:

- `noticeShown` -> one global MUI Snackbar (`App.tsx:245-259`), bottom-left, one slot (a new notice replaces the old one),
  auto-hides after 4 s (8 s for errors). `Alert` carries `role="alert"`.
- Inline `<p role="alert">` for detail load errors (`A/ArtifactsPane.tsx:189`).
- Inline `<p role="status">` for list load errors and "Uploading X..." rows (`A/ArtifactsList.tsx:129-138`).
- Precedent for clipboard feedback: `features/repos/ReposPane.tsx:42-49` (info notice on success, error notice on failure).

Classes: **OK** / **NO FEEDBACK** / **WEAK** (feedback exists but unclear, late, or ambiguous) / **OFFSCREEN** (feedback exists but
can be out of sight or gone before it is read). Fix class: **clear-fix** (small, unambiguous, safe, <= ~30 lines, jsdom-testable)
or **decision** (needs a product/UX call).

## Interaction table

| # | Interaction | Pending | Success | Failure | Class | Fix | Where |
|---|---|---|---|---|---|---|---|
| 1 | Post new comment | Post disabled (`posting`), label unchanged | Popover closes, thread appears after reload; not scrolled to or activated, no notice | Snackbar (bottom-left, 8 s); draft kept | WEAK | clear-fix (F6) | `A/CommentComposer.tsx:31-46`, `A/ArtifactsPane.tsx:63-67` |
| 2 | Reply to thread | None; Reply not disabled, so a double click posts twice | Field clears, comment appears after reload | Snackbar; text kept | WEAK | clear-fix (F4) | `A/ThreadList.tsx:57-76,101` |
| 3 | Resolve / Reopen | None, button stays live | Thread jumps into the collapsed "Resolved (n)" `<details>`; only the count changes | Snackbar | WEAK | clear-fix (F5) | `A/ThreadList.tsx:105-112,161-165` |
| 4 | Reply/Post with empty text | Button disabled, Enter silently returns | n/a | n/a | OK (standard) | - | `A/ThreadList.tsx:69-71`, `A/CommentComposer.tsx:33` |
| 5 | Cancel / Escape on a typed draft | n/a | Popover closes, text discarded without a word | n/a | WEAK | decision (D4) | `A/CommentPopover.tsx:108-120`, `A/ArtifactsPane.tsx:56` |
| 6 | Attach (picker) in New agent / Reply box | "Uploading..." `role=status`; Send/Create disabled | Reference inserted at caret, chip shown | Snackbar naming each failed file | OK | - | `A/AttachButton.tsx:73-81`, `A/useAttachments.ts:71-91` |
| 7 | Attach: one file over 10 MiB in a batch | as above | n/a | Whole batch refused before any upload; message names only the first oversized file; valid files are dropped too | WEAK | decision (D2) | `A/upload.ts:46-50` |
| 8 | Attach: unsupported type | as above | n/a | Picker `accept` is advisory; drop/paste are unfiltered; outcome depends on the server's message (not verifiable here) | WEAK | decision (D3) | `A/ArtifactsActions.tsx:8-9` |
| 9 | Attach: drop of text/non-file onto the prompt | none | `onDrop` calls `preventDefault` then `attach([])` returns: dropped text is swallowed, nothing inserted, nothing shown | n/a | NO FEEDBACK | clear-fix (F9) | `A/useAttachments.ts:101-104` |
| 10 | Attach: paste of a non-image file | none | `imagesFromClipboard` ignores it; nothing happens | n/a | NO FEEDBACK | decision (D3) | `A/upload.ts:71-84`, `A/useAttachments.ts:105-112` |
| 11 | Attach: two overlapping uploads | `busy` is one boolean; the first to finish clears "Uploading..." and re-enables Send while the second still runs | n/a | n/a | WEAK | clear-fix (F10) | `A/useAttachments.ts:69,76,88` |
| 12 | Upload (top-bar button) | "Uploading X..." row, but in the list pane, which is hidden on a narrow screen with the detail pane showing; button not disabled | List refreshes, new artifact auto-selected | Snackbar | WEAK | decision (D5) | `A/ArtifactsActions.tsx:15-21`, `A/ArtifactsList.tsx:134-138`, `A/artifactsSlice.ts:112-121` |
| 13 | Upload: oversize | as 12 | n/a | Snackbar, whole batch refused (see 7) | WEAK | decision (D2) | `A/upload.ts:47-50` |
| 14 | Upload by drag-drop onto the list | Drop-zone banner while dragging; upload rows | as 12 | Snackbar | OK | - | `A/ArtifactsList.tsx:84-104,128` |
| 15 | Upload by paste (window-level, also while typing in the comment box or search) | upload rows | Selection jumps to the new artifact, remounting the detail and silently losing an open draft | Snackbar | WEAK | decision (D1) | `A/ArtifactsList.tsx:71-82`, `A/artifactsSlice.ts:117` |
| 16 | Revision switch | Viewer disappears (`content` is null until the new rev loads); no spinner, no "Loading" | New rev renders | Inline `role=alert` text at the top of the scroll area; select already shows the new rev while the body is blank | NO FEEDBACK (pending), WEAK (failure) | clear-fix (F3) | `A/useArtifactDetail.ts:27,51-73`, `A/ArtifactsPane.tsx:194` |
| 17 | Detail first load | Header and body blank, no "Loading" | Appears | Inline error only; no retry | NO FEEDBACK | clear-fix (F3) | `A/ArtifactsPane.tsx:138-206` |
| 18 | Older revision is read-only | n/a | Highlights, selection-comment button, "Comment on the whole artifact", Reply and Resolve all vanish with no explanation | n/a | NO FEEDBACK | clear-fix (F2) | `A/ArtifactsPane.tsx:71,97-99,85` |
| 19 | Click a thread whose anchor is detached or on an older rev | n/a | Item gets the active style, but there is no element to scroll to or highlight; nothing visibly happens in the document | n/a | NO FEEDBACK | clear-fix (F7) | `A/MarkdownViewer.tsx:112-121`, `A/anchors.ts:133` |
| 20 | Click a thread on a text/image artifact | n/a | Only the item's own active style; documents without highlights give no hint why | n/a | WEAK | decision (D6) | `A/ArtifactsPane.tsx:124-128` |
| 21 | Click a highlight in the document | n/a | Margin item scrolls into view (`block: nearest`) and gets the active style; highlight is outlined | n/a | OK | - | `A/ThreadList.tsx:51-55`, `A/MarkdownViewer.tsx:153-161` |
| 22 | Select text -> "Comment" button -> popover | n/a | Button appears under the block; popover opens beside it; "Comments on the whole paragraph" when the quote could not be matched | n/a | OK | - | `A/MarkdownViewer.tsx:123-195`, `A/CommentComposer.tsx:50-54` |
| 23 | Select text in a text/image artifact | n/a | Nothing: only whole-artifact comments exist, with no hint | n/a | WEAK | decision (D6) | `A/TextViewer.tsx`, `A/ArtifactsPane.tsx:113-128` |
| 24 | Delete: confirm dialog | Dialog closes at once; no pending state | Row disappears, pane returns to "Select an artifact"; no notice; on a narrow screen the pane stays on an empty detail view | Snackbar (dialog already closed) | WEAK | clear-fix (F8) | `A/ArtifactsPane.tsx:153-156`, `A/artifactsSlice.ts:70-82,122-125` |
| 25 | Copy reference | none | Nothing at all | Nothing; if `navigator.clipboard` is undefined the call throws synchronously and `.catch` never runs (uncaught in the click handler) | NO FEEDBACK | clear-fix (F1) | `A/ArtifactsPane.tsx:145-151` |
| 26 | Refresh / reload | Lists poll every 10 s, no manual refresh; no indicator (`loading` is never rendered) | Rows change under the user | List error text clears at the start of each poll (`error = null` on pending), so a persistent failure blinks | WEAK | decision (D7) | `A/artifactsSlice.ts:100-103`, `A/ArtifactsList.tsx:65-69` |
| 27 | New revision arrives while viewing latest | none | `shownRev` jumps to the new latest, `ArtifactBody` is re-keyed, open popover and typed draft vanish with no notice | n/a | NO FEEDBACK | decision (D1) | `A/useArtifactDetail.ts:26,29-49`, `A/ArtifactsPane.tsx:196` |
| 28 | List row selection | Pane blank until loaded (see 17) | Row gets `aria-current`; pane swaps | n/a | WEAK (pane) | covered by F3 | `A/ArtifactsList.tsx:145-150` |
| 29 | Kind chips and search | none needed | Rows filter; "No artifacts match." | n/a | OK | - | `A/ArtifactsList.tsx:139-141` |
| 30 | Empty list while the first load runs | "No artifacts yet." shown during the first load (misleading) | n/a | n/a | WEAK | clear-fix (F11) | `A/ArtifactsList.tsx:139-141` |
| 31 | Empty list, no hint how to add | n/a | "No artifacts yet." with no mention of Upload, drop or paste | n/a | WEAK | clear-fix (F11) | `A/ArtifactsList.tsx:140` |
| 32 | Pane with nothing selected | n/a | "Select an artifact" | n/a | OK | - | `A/ArtifactsPane.tsx:228` |
| 33 | Artifact with no threads | n/a | Empty list, nothing says "no comments yet" | n/a | WEAK | clear-fix (F11) | `A/ThreadList.tsx:152-154` |
| 34 | Image fails to decode/display | none | n/a | Browser broken-image glyph with alt text only | WEAK | clear-fix (F12) | `A/ImageViewer.tsx:39-46` |
| 35 | Markdown link click, `openExternal` rejects | none | n/a | Swallowed (`.catch(() => undefined)`) | NO FEEDBACK | clear-fix (F1) | `A/MarkdownViewer.tsx:23-29` |
| 36 | Markdown link with a disallowed protocol or a `#anchor` | none | `keepUrl` empties the href / `openLink` returns after `preventDefault`: click does nothing, no in-page scroll | n/a | NO FEEDBACK | decision (D8) | `A/MarkdownViewer.tsx:11-29` |
| 37 | Any error notice | n/a | n/a | Single bottom-left Snackbar, 8 s; a second error replaces the first; the window can be scrolled or unfocused when it fires | OFFSCREEN | decision (D9) | `App.tsx:245-259`, `ui/uiSlice.ts:110-115` |
| 38 | Detail load error with a long document | n/a | n/a | `<p role=alert>` sits inside the scrolling `.root` (`overflow: auto`) under a non-sticky header, so it scrolls away with the content | OFFSCREEN | clear-fix (F3) | `A/ArtifactsPane.tsx:189-193`, `A/ArtifactsPane.module.css:9-17` |
| 39 | Post failure while the popover is docked | n/a | n/a | Snackbar (z-index 1400) overlaps the docked popover (z-index 1000, bottom 8 px, full width) at the same corner, partly covering Post/Cancel | OFFSCREEN | decision (D9) | `A/CommentPopover.module.css:2,17-22`, `App.tsx:248` |
| 40 | Header controls (revision, Copy reference, Delete) on a long document | n/a | Header is part of the scrolling `.root`, so these controls scroll out of reach | n/a | WEAK | decision (D10) | `A/ArtifactsPane.tsx:160-188`, `A/ArtifactsPane.module.css:9-17` |

Tally (40 rows, mixed rows counted under the worse class): OK 7, NO FEEDBACK 10, WEAK 20, OFFSCREEN 3. 12 clear-fixes (F1-F12), 10 decisions (D1-D10).

## Clear-fixes, ranked

Ranked by how often a user hits the silence times how confusing it is. Each uses existing mechanisms.

### F1. Copy reference and external link: give success and failure notices (rows 25, 35)

- Files: `A/ArtifactsPane.tsx` (`copyReference`, lines 145-151), `A/MarkdownViewer.tsx` (`openLink`, 23-29), tests in
  `A/ArtifactsPane.test.tsx`, `A/MarkdownViewer.test.tsx`.
- Response: make `copyReference` async with try/await (so a synchronous throw from a missing `navigator.clipboard` is caught), then
  `noticeShown({severity:"info", message:"Copied @artifact:<id>/r<n>"})` on success and
  `noticeShown({severity:"error", message:"Couldn't copy the reference"})` on failure, mirroring `features/repos/ReposPane.tsx:42-49`.
  `openLink` dispatches `noticeShown({severity:"error", message:"Couldn't open the link"})` on rejection (needs `useAppDispatch`
  threaded in, e.g. via a small hook inside the `a` component).
- Test: stub `navigator.clipboard.writeText` resolved/rejected and `undefined`; assert the snackbar text via the store's `ui.notice`.

### F2. Say why an older revision is read-only (row 18)

- Files: `A/ArtifactsPane.tsx` (`ArtifactBody`, near line 71), CSS in `A/ArtifactsPane.module.css`, test in `A/ArtifactsPane.test.tsx`.
- Response: when `!onLatest`, render a `role="status"` line above the body: "Viewing r{shownRev}, not the latest. Comments are read-only." with a
  "Go to latest" `Button` calling `chooseRev(artifact.latest)` (pass a callback down). Replaces the silent disappearance of Comment,
  Reply, Resolve and highlights.

### F3. Loading and error states for the detail pane and revision switch (rows 16, 17, 28, 38)

- Files: `A/useArtifactDetail.ts` (expose `loading = artifact === null || shownContent === null` while `error === null`),
  `A/ArtifactsPane.tsx`, `A/ArtifactsPane.module.css`, tests in `A/ArtifactsPane.test.tsx`.
- Response: while loading, render `<p role="status">Loading r{shownRev}...</p>` (or "Loading artifact...") where the body goes. On error, keep
  the `role="alert"` paragraph but add a "Retry" button calling `reload()`/re-selecting the rev, and make the error `position: sticky; top: 0`
  (CSS only) so it cannot scroll out of view. On a failed revision read, reset the select to the last rev that loaded.

### F4. Reply pending state and double-submit guard (row 2)

- Files: `A/ThreadList.tsx` (`ThreadItem`, 46-115), test in `A/ThreadList.test.tsx`.
- Response: add `const [busy, setBusy] = useState(false)` set inside `perform`; disable Reply and Resolve while busy and show "Sending..." as the
  Reply button label. Prevents the duplicate post and shows progress.

### F5. Resolve/Reopen: confirm where the thread went (row 3)

- Files: `A/ThreadList.tsx`, test in `A/ThreadList.test.tsx`.
- Response: after a successful resolve, `noticeShown({severity:"info", message:"Thread resolved - see Resolved (n)"})`; after reopen,
  "Thread reopened". Reuses the snackbar; also disable the button while busy (same `busy` as F4).

### F6. Post comment: pending label and success confirmation (row 1)

- Files: `A/CommentComposer.tsx`, `A/ArtifactsPane.tsx` (`onPosted`), tests in `A/ArtifactsPane.test.tsx`.
- Response: the Post label becomes "Posting..." while `posting`; on success dispatch `noticeShown({severity:"success", message:"Comment posted"})`
  and `onActivate(<new thread id>)` using the id returned by `artifactCommentAdd` (`CommentAdded`), so the margin scrolls to and outlines the new
  thread (the existing `scrollIntoView` + `data-focused` effects do the work). Check `CommentAdded`'s shape in `api.ts` before wiring.

### F7. Clicking a thread that has no anchor to show (row 19)

- Files: `A/MarkdownViewer.tsx` (effect at 112-121), or `A/ArtifactsPane.tsx` `onActivate`; test in `A/MarkdownViewer.test.tsx`.
- Response: when the activated thread has no element in the viewer (detached, or anchored to another rev), `noticeShown({severity:"info",
  message:"This comment's text isn't in r{shownRev}"})` (detached: "The text this comment was on has changed"). Detached threads already say
  `on rN` in their headline (`A/ThreadList.tsx:34`); this gives the click a visible answer.

### F8. Delete: confirmation notice and narrow-screen return (row 24)

- Files: `A/ArtifactsPane.tsx` (`remove`, 153-156), `ui/uiSlice.ts` (`narrowBack` already exists; `pickedTypes` does not cover the remove action), tests in `A/ArtifactsPane.test.tsx`.
- Response: on success `noticeShown({severity:"success", message:"Deleted <name>"})`, and `narrowBack()` so a narrow screen is not left on an empty
  detail view. While pending, the dialog's Delete button shows "Deleting..." and the dialog stays open until the thunk settles (`await dispatch(...)` before
  `setConfirming(false)`).

### F9. Dropping non-file content on the prompt (row 9)

- Files: `A/useAttachments.ts` (`onDrop`, 101-104), test in `A/useAttachments.test.ts`.
- Response: only `preventDefault` and attach when `hasFiles(event)`; otherwise return and let the browser insert the dropped text.

### F10. Overlapping uploads clear "Uploading..." early (row 11)

- Files: `A/useAttachments.ts`, test in `A/useAttachments.test.ts`.
- Response: replace the boolean with a counter (`pending`), `busy = pending > 0`, so Send stays disabled and "Uploading..." stays until the last upload settles.

### F11. Empty and loading states in the list and thread margin (rows 30, 31, 33)

- Files: `A/ArtifactsList.tsx` (139-141), `A/ThreadList.tsx` (151-154), tests in `A/ArtifactsList.test.tsx`, `A/ThreadList.test.tsx`.
- Response: show "Loading..." (`role=status`) instead of "No artifacts yet." while `loading && items.length === 0`; change the empty text to
  "No artifacts yet. Upload a file, drop one here, or paste an image."; render "No comments yet." when a thread list has no threads at all.

### F12. Image that cannot be shown (row 34)

- Files: `A/ImageViewer.tsx`, test in `A/ImageViewer.test.tsx`.
- Response: `onError` sets a flag; render `<p role="alert">Couldn't display {name}.</p>` in place of the frame (keep the caption).

## Needs a decision

- **D1. Losing a draft when the body remounts (rows 15, 27).** A new revision (poll) or an upload-by-paste re-keys/remounts the pane and drops an
  open popover and its text silently. Options: keep the draft in `ArtifactDetail` state; hold `shownRev` until the user accepts a "r{n} is
  available - Switch" banner; or only auto-select after upload when no draft is open.
- **D2. Oversize in a batch (rows 7, 13).** Refuse the whole batch (today, naming one file) or upload the valid files and report every oversized one?
  The second is friendlier but changes `uploadFiles` semantics and its tests.
- **D3. Unsupported types (rows 8, 10).** Validate against `artifactAccept` client-side for drop/paste and say so, or leave it to the server's error?
  Pasting a non-image file is silently ignored today; decide whether to upload it, or to tell the user only images are taken.
- **D4. Discarding a typed draft (row 5).** Confirm on Escape/Cancel when text is present, or keep as is? Interacts with Escape handling in
  `CommentPopover` and its tests.
- **D5. Upload progress visibility on narrow screens (row 12).** The progress rows live in the list, which is hidden when the detail pane shows. Move
  progress into the top bar (disable Upload and show a spinner) or rely on notices?
- **D6. Text and image artifacts cannot be commented on by selection (rows 20, 23).** Product call whether to add selection anchors for text, or
  explain "Comments on this file apply to the whole file".
- **D7. Refresh affordance and list-load errors (row 26).** Add a manual Refresh and a visible loading state; stop clearing `error` on every poll so
  a failing list is not a 10 s blink.
- **D8. `#anchor` and blocked links in Markdown (row 36).** Needs heading ids (no slug plugin today) to scroll; blocked protocols could show a
  notice. Choice of plugin and message wording.
- **D9. One short-lived bottom-left Snackbar for all errors (rows 37, 39).** Errors vanish in 8 s, replace each other, and can overlap the docked
  popover. Options: errors persist until dismissed, queue notices, or render comment errors inline in the composer (`role="alert"`, which also
  keeps them next to the draft).
- **D10. Sticky header (row 40).** Make `.header` sticky inside the scrolling pane so Copy reference, revision and Delete stay reachable on long documents.

## Not verified

- Server-side messages for unsupported types and the shape of `CommentAdded` were not checked (Rust/Go side out of scope).
- Narrow-layout claims (rows 12, 24) come from reading `App.tsx` and `uiSlice.ts` (`pickedTypes` lacks the remove action); not run in a browser.
- No code was executed; findings are from reading the sources.
