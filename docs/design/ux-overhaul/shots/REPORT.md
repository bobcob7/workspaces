# UX overhaul screenshot report (verdict: FAIL, 1 layout bug + 2 minor)

Method: Vite dev server (port 1420) + headless Chromium via Playwright, `desktop/test/tauri-mock.js` plus a small
patch giving `fix-login-d8efb24f` 8 children and ~40-line `detail`/`result`. Branch `feat/ux-overhaul` @ 81222a6.
Raw numbers: `metrics-1.json`, `metrics-2.json`. Tofu squares in PNGs are missing emoji/arrow fonts in the pod, not app bugs.

## 1. Header expanded / collapsed (1400, 390)
- 1400: toggle `aria-label` flips "Collapse details" -> "Expand details", `aria-expanded` true -> false; details hidden, conversation takes the freed height. PASS.
- 390: no "Collapse details" button (phone uses the "Details" icon button, `aria-expanded`); compact header 41px. PASS (by design).
- Files: `1-1400-header-*.png`, `1-390-header-*.png`.

## 2. Long detail/result + 8 children
- 1400: `._details_shj7k_115` (EntryPane.module.css `.details`) `max-height: 360px` = 40.0vh, `overflow-y: auto`, scrollHeight 685 > clientHeight 359 (scrolls). Conversation 294px (32.6vh) + composer visible. `.detail` max-height 120px, `.result` max-height 160px, both scroll. ChildrenCard list `max-height: 220px`, scrollHeight 349. PASS.
- 390: `.details` max-height 337.6px = 40vh, scrolls (scrollH 811). Conversation 307px (36.4vh), composer visible. ChildrenCard list `max-height: 320px` (ChildrenCard.module.css `.list`) scrollHeight 871. PASS.
- Note (UX, minor): with detail, result and children open, the 40vh area scrolls and contains nested scrollers (detail 120, result 160, children list 220/320); the child rows are mostly reached only by scrolling the outer area first. Not a clip or overflow.
- Files: `2-*-long-detail-children.png`, `2-*-children-expanded.png`.

## 3. Sidebar collapsed (1400)
- Before: Rail x=0 w=72, `._sidebar_1yi28_46` w=300, section w=1028. After toggle: sidebar w=0, Rail w=72, section x=72 w=1328 (full remaining width). `scrollWidth == innerWidth` (no horizontal scroll), body `overflow-x: hidden`. Ctrl+B toggles it back. Toggle is 32x32, title "Expand sidebar (⌘B)". PASS.
- Side effect (good): the header action row, which wraps with the sidebar open, fits on one line when collapsed (header 109px -> 66px).
- File: `3-1400-sidebar-collapsed.png`.

## 4. Phone (390x844)
- Sidebar toggle `display: none` (0x0). PASS. `3-390-no-sidebar-toggle.png`.
- Entry view: all interactive controls >= 40px (Back 65x40, Conversation/Terminal 40, More actions 40x40, Details 40x40, children toggle 352x41, Send 56x40, bottom nav 60x40). No horizontal scroll. PASS.
- **BUG (FAIL): list view top bar buttons are 30.75px tall** (< 40): "Restore stopped" 113x31, "New shell" 80x31, "New agent" 85x31. Computed: `font-size 13px; padding 4px 5px / 3px 9px / 4px 10px; min-height auto`. Source: `desktop/src/features/agents/AgentsActions.tsx` (MUI `size="small"`), container `.topBar` in `desktop/src/App.module.css`. Needs a phone min-height 40px. File: `4-390-phone-list.png`.

## Other layout findings (not blocking)
- 1400, sidebar open: EntryPane `<header>` action row wraps ("Remove" alone on a second row), header 109px tall (`EntryPane.module.css` `.titleRow` has `flex-wrap: wrap`). Header collapse does not shrink this (collapsed header is still 109px), only the details area is removed.
- 1400: "Collapse details"/"Expand details" toggle is 24x31 (desktop, fine) .
- Mock gap: the Tauri mock lacks `__TAURI_INTERNALS__.unregisterListener`, causing console `pageerror`s on unmount. Not an app bug.

Not run: vitest/typecheck (out of scope). Node v20.19.2 sufficed for Vite 8.3.8 (EBADENGINE warning only); no Node 22 tarball needed.
