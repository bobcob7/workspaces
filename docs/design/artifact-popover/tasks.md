# Artifact comment popover: tasks

Problem: in `desktop/src/features/artifacts/ArtifactsPane.tsx` the `CommentComposer` renders at the top of the right-hand
`<aside>` (markdown) or below the whole document (text/image). On a long document, commenting on text lower in the page
opens the composer out of sight.

Fix: the composer opens as a popover anchored to the selection / trigger, always inside the viewport.

## Rules (all tasks)

- Work in `desktop/`. Node 22.12+ is required (pod has Node 20): install the official Node 22 LTS tarball into
  `$CLAUDE_JOB_DIR/tmp`, check its SHA256, put it first on PATH. If a command is refused, list it under `## Not run here`.
- Verify: `cd desktop && npm ci && npm run typecheck && npm test -- --maxWorkers=2 --testTimeout=60000`.
- TypeScript standards: named exports, no `any`, no non-null assertions, explicit exported return types, CSS Modules,
  Vitest + Testing Library, test behaviour.
- Commits: conventional prefix (`feat:`, `fix:`, `test:`), imperative mood, NO `Co-Authored-By` trailer (repo rule; it
  overrides the role prompt). Do not open a PR.
- jsdom has no layout: unit-test the pure position function with numbers; for the component mock
  `getBoundingClientRect` and `window.innerWidth/innerHeight`.
- Keep existing behaviour, tests and a11y. `CommentComposer` keeps its props and its `aria-label="Comment"` textbox,
  `Post` and `Cancel` buttons.

## T1. CommentPopover and position function (new files only)  [small-ish]

Depends on: none. Files: `desktop/src/features/artifacts/popoverPosition.ts` (+ `.test.ts`),
`CommentPopover.tsx` (+ `.test.tsx`), `CommentPopover.module.css`.

`popoverPosition.ts`:
```ts
export interface Box { readonly top: number; readonly left: number; readonly width: number; readonly height: number }
export interface Viewport { readonly width: number; readonly height: number }
export type Placement =
  | { readonly mode: "anchored"; readonly top: number; readonly left: number; readonly maxHeight: number }
  | { readonly mode: "docked" };
export const PHONE_MAX_WIDTH = 720;
export const MARGIN = 8;
/** below the anchor if it fits, else above, else docked; left-aligned to the anchor then clamped inside the viewport.
 *  docked when viewport.width <= PHONE_MAX_WIDTH, when the anchor is not visible (entirely above/below/left/right of the
 *  viewport), or when neither below nor above has room for `size.height` (min 160). */
export const placePopover = (anchor: Box, size: { width: number; height: number }, viewport: Viewport): Placement
```
`CommentPopover.tsx`: `CommentPopover({ anchorEl: HTMLElement | null, onDismiss, children })`:
- renders `role="dialog"` `aria-label="New comment"` in a portal-free `position: fixed` box (`z-index` above the pane).
  Anchored: `top/left/maxHeight` from `placePopover`, `overflow: auto`, width `min(360px, 100vw - 16px)`.
  Docked: fixed to the bottom, full width minus 8px margins, `max-height: 60vh`, safe-area bottom padding.
- recomputes in a layout effect and on `resize` and on `scroll` (capture phase on `window`, so the pane's scroll container
  counts) via `requestAnimationFrame`-free direct setState; removes listeners on unmount. If `anchorEl` is null or detached
  from the document: docked.
- on open moves focus to the first `textarea` inside (`aria-label="Comment"`); on `Escape` keydown inside it calls
  `onDismiss` and returns focus to `anchorEl` if it is still connected and focusable, else leaves it.
Tests: every branch of `placePopover` (fits below, flips above, clamps left/right, phone dock, anchor off-screen dock,
no room dock); component: focus lands in textarea, Escape calls onDismiss, scroll event repositions (mock rect change),
docked at 500px width.

## T2. Wire the popover into the artifacts pane  

Depends on: T1. Files: `ArtifactsPane.tsx`, `MarkdownViewer.tsx`, their tests, `ArtifactsPane.module.css` if needed.

- `MarkdownViewer`: `onComment` gets a third arg `anchorEl: HTMLElement | null`, the selected block element (kept from
  `captureSelection`; the selection collapses on click). Update the existing tests that assert `toHaveBeenCalledWith`.
- `ArtifactsPane.ArtifactBody`: `Draft` gains `anchorEl: HTMLElement | null`. When `draft !== null` render the
  `CommentComposer` inside `<CommentPopover anchorEl={draft.anchorEl} onDismiss={() => setDraft(null)}>`, for every kind;
  no composer is rendered in the margin or below the document any more.
- "Comment on the whole artifact": the button's `event.currentTarget` is the `anchorEl`. For text and image kinds add the
  same button (class `wholeComment`) above the thread list when `onLatest`, replacing the always-visible composer.
  The per-block comment affordance and the whole-artifact button both end in the popover.
- Dismiss on Cancel, Escape, successful post, and when the shown revision changes or the selected artifact changes
  (already remounted per selection). Do not lose the typed text when only repositioning.
- Update existing tests in `ArtifactsPane.test.tsx` that look for the composer without a draft (text/image): they first
  click "Comment on the whole artifact". Add tests: select-and-Comment on a markdown artifact shows a
  `dialog` named "New comment" containing the "Comment" textbox, focused; Escape closes it; whole-artifact button opens it
  for text and image kinds; posting closes it and calls `artifactCommentAdd` with the anchor.

## T3. Screenshot verification (Sonnet child, not an implementer)

Depends on: T2 merged. Files: `desktop/test/tauri-mock.js` (add a long markdown artifact, ~150 paragraphs with headings),
`desktop/docs/artifacts-popover-desktop.png`, `artifacts-popover-bottom-desktop.png`, `artifacts-popover-phone.png`,
`artifacts-popover-whole.png`. Method: handbook page "screenshots of the desktop app" (Playwright against `npx vite
--port 1420` with the committed mock). Scroll the pane near the bottom, select text in a paragraph there, click Comment,
and capture. Check at 1280x800 and 390x844: popover fully inside the viewport (assert with `getBoundingClientRect`),
textbox focused, no horizontal scroll, buttons >= 40px tall at phone width, docked at the bottom on phone; then scroll the
anchor out of view and assert the popover is still visible (docked).
