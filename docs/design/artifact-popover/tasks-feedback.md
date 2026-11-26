# Artifacts UI: visible feedback fixes (phase 2)

Source of truth: `docs/design/artifact-popover/audit.md` (fix ids F1..F12 and the file:line references there). Implement
only the clear-fixes below; the decisions D1..D10 are out of scope. Branch base: `feat/artifact-ux-feedback`.

## Rules (all tasks)

- Work in `desktop/`. Node 22.12+: install the official Node 22 LTS tarball into `$CLAUDE_JOB_DIR/tmp` (SHA256 checked),
  first on PATH. If a command is refused, list it under `## Not run here`.
- Verify: `cd desktop && npm ci && npm run typecheck && npm test -- --maxWorkers=2 --testTimeout=60000`
  (known unrelated flake on main: NewAgentDialog "omits environment for local").
- TypeScript standards: named exports, no `any`, no non-null assertions, explicit exported return types, CSS Modules,
  Vitest + Testing Library, test behaviour. Reuse `noticeShown` from `features/ui/uiSlice` for notices (see
  `features/repos/ReposPane.tsx:42-49` for the clipboard precedent). Keep every existing accessible name and test.
- Each fix gets a failing-first test (write the test, see it fail, then fix). Mutate each new test's target and report survivors.
- Commits: conventional (`feat:`/`fix:`/`test:`), imperative, NO `Co-Authored-By` trailer. Commit and push after each fix. No PR.

## T4. F1, F8, F2 (pane header actions and read-only notice)  Depends on: none

Files: `ArtifactsPane.tsx` (+test), `MarkdownViewer.tsx` (+test), `ArtifactsPane.module.css`.
- F1: `copyReference` async/try-await so a missing `navigator.clipboard` is caught; info notice "Copied <reference>" on success, error
  notice "Couldn't copy the reference" on failure. `openLink` failure dispatches error notice "Couldn't open the link".
- F8: on delete success, success notice "Deleted <name>" and `narrowBack()` (check `uiSlice` for the exact action); the dialog's
  Delete shows "Deleting..." and stays open until the thunk settles (await dispatch, then close); on failure the dialog closes and the existing error notice shows.
- F2: when `!onLatest`, render a `role="status"` line above the body: "Viewing r{shownRev}, not the latest. Comments are read-only." with a
  "Go to latest" button calling `chooseRev(artifact.latest)` (pass it down).

## T5. F3, F6, F7 (loading/errors, post feedback, detached thread click)  Depends on: T4 (same files)

Files: `useArtifactDetail.ts`, `ArtifactsPane.tsx`, `CommentComposer.tsx`, `MarkdownViewer.tsx`, their tests, CSS.
- F3: while detail or revision content is loading (and no error) show `role="status"` "Loading artifact..." / "Loading r{n}..."; on error keep
  the `role="alert"` paragraph, add a "Retry" button that reloads, make the alert `position: sticky; top: 0` with a background so it cannot scroll away; a failed revision read resets the select to the last revision that loaded.
- F6: Post label "Posting..." while posting; on success success-notice "Comment posted" and activate the new thread (use the id `artifactCommentAdd` returns; check `CommentAdded` in `api.ts`) so the margin scrolls to it.
- F7: activating a thread with no element in the viewer shows an info notice: "This comment's text isn't in r{shownRev}" (other revision) or
  "The text this comment was on has changed" (detached).

## T6. F4, F5, F9, F10, F11, F12 (thread list, attachments, list, image)  Depends on: none (disjoint from T4/T5: `ThreadList.tsx`, `useAttachments.ts`, `ArtifactsList.tsx`, `ImageViewer.tsx` and tests)

- F4: reply/resolve `busy` state; Reply shows "Sending..." and Reply+Resolve are disabled while busy (no double submit).
- F5: success notices "Thread resolved - see Resolved (n)" with the real count, and "Thread reopened".
- F9: `onDrop` only prevents default and attaches when the drop carries files; otherwise do nothing.
- F10: replace the boolean `busy` with a pending counter so "Uploading..." and the disabled Send persist until the last overlapping upload settles.
- F11: list shows `role="status"` "Loading..." instead of "No artifacts yet." during the first load; empty text becomes "No artifacts yet. Upload a file, drop one here, or paste an image."; the thread list shows "No comments yet." when there are no threads at all.
- F12: image `onError` shows `role="alert"` "Couldn't display <name>." in place of the frame.
