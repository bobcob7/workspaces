# Open menu with integrated terminal: tasks

Goal: in the desktop app's workspace header (`desktop/src/features/agents/EntryPane.tsx`), replace the
separate "Open folder" / "Open in editor" buttons with one "Open..." button that expands a menu with
"Open in Editor", "Open folder" and "Open terminal". "Open terminal" opens an integrated terminal
(the user's shell) in the workspace directory, shown in the pane.

Design: a new `TermSpec` kind `dir` = `{ kind: "dir", cwd: string }` runs `$SHELL` (fallback `/bin/sh`)
in `cwd` on a pty. Terminal key: `dirKey(entry.key)` = `dir:<entry.key>`. A new `AgentView` value `"shell"`
shows it.

## T1: backend `dir` terminal spec  (parallel with T2, `small`)
Files: `desktop/src-tauri/src/pty.rs` only (plus its tests module).
- Add `TermSpec::Dir { cwd: String }` (serde camelCase tag `kind`, so JSON is `{"kind":"dir","cwd":"/x"}`).
- `command_for` returns a program for it: the `SHELL` env var if set and non-empty, else `sh`. Its
  return type currently is `(&'static str, ...)`; change to an owned `String` program. Args: `["-l"]` is
  NOT wanted; use no args. cwd = `Some(cwd)`.
- `find_program` takes a bare name searched on PATH; if the program is an absolute path (starts with `/`)
  and is a file, use it directly instead.
- Unit tests: `command_for` for Dir with SHELL set/unset (test the pure helper, do not mutate env in
  parallel tests: factor `shell_program(shell: Option<String>) -> String` and test that), and serde
  round trip of the JSON above.
Verify: `cd desktop/src-tauri && cargo fmt --check && cargo clippy --all-targets -- -D warnings && cargo test pty`.
(If the toolchain is unavailable offline, report that, run what you can, and say so.)

## T2: header "Open..." menu and terminal view  (parallel with T1)
Files: `desktop/src/api.ts` (`TermSpec` adds `| { readonly kind: "dir"; readonly cwd: string }`),
`desktop/src/features/terminals/terminalsSlice.ts` (add `export const dirKey = (entryKey: string) => \`dir:${entryKey}\``),
`desktop/src/features/ui/uiSlice.ts` (`AgentView` adds `"shell"`),
`desktop/src/features/agents/EntryPane.tsx`, `EntryPane.module.css` if needed,
`desktop/src/features/agents/EntryPane.test.tsx`, other tests that break.
- Desktop (non-phone) header: replace the "Open folder" and "Open in editor" buttons with ONE button
  "Open..." (with a small down-arrow, `aria-haspopup="menu"`, `aria-expanded`). Click opens a MUI `Menu`
  with items, in order: "Open in Editor", "Open folder", "Open terminal". The first two keep today's
  behaviour, disabled conditions and `title` text, and are hidden when `isWeb()`. "Open terminal" is
  shown on web too, disabled when `entry.kind === "pending"`. The whole button is hidden if no item shows.
- Phone layout: the existing "More actions" menu gets the same three items in that order (editor, folder,
  terminal) in place of the two it has now.
- "Open terminal" dispatches `openTerminal({ key: dirKey(entry.key), title: entry.name, spec: { kind: "dir", cwd: entry.path } })`
  then `agentViewChosen("shell")`. If a dir terminal for the entry already exists and isn't exited, just
  switch the view to "shell" without reopening.
- Pane body: `dirTerminal = items.find(t => t.key === dirKey(entry.key))`. When `view === "shell"` and
  `dirTerminal` exists, render `<TerminalView terminalKey={dirTerminal.key} generation={dirTerminal.generation} />`
  instead of the conversation/attach terminal/placeholder (no ReplyBox). When `view === "shell"` but
  there's no dir terminal for this entry, behave as if view were "conversation" for agents (and show
  the existing placeholder for others).
- The existing Conversation/Terminal toggle: show it for agents as today and add a third toggle "Shell"
  (value `"shell"`) only when a dir terminal exists for the entry; for non-agent entries with a dir
  terminal show a toggle group with just "Shell"... simpler: for non-agents show nothing extra, the
  terminal simply replaces the placeholder.
- The inner path button ("Open in the file manager") stays.
- Update `EntryPane.test.tsx`: the old tests that click "Open folder"/"Open in editor" buttons now click
  "Open..." first. Add tests: menu lists the three items in order; "Open terminal" calls the mocked
  terminals host `open` with a `dir` spec for the entry path (follow how the existing tests mock
  `terminals`; see `desktop/src/test/fixtures.ts`); disabled for pending entries; on web only
  "Open terminal" appears.
Verify: `cd desktop && npx tsc --noEmit && npx vitest run src/features/agents src/features/terminals src/features/ui && npm run lint --if-present`.
