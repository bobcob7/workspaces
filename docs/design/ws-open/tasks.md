# Open a workspace, and ask about a pull request: implementation tasks

These tasks implement
[0051](../../adrs/0051-desktop-open-workspace-and-ask-about-pr.md), as
designed in [README.md](README.md). This change is desktop-only: it
doesn't touch `ws` (Go) at all.

**Order and parallelism**

| Task | Title | Depends on | Parallel with | Size |
| ---- | ----- | ---------- | ------------- | ---- |
| T1 | Rust: open settings, launcher and `workspace_open` | nothing | nothing | large |
| T2 | Rust: `reviews_ask`, the pull request Q&A agent | T1 (both edit `lib.rs`) | T3 | large |
| T3 | Frontend: API types, fixtures, Tauri mock, Settings › Workspaces | T1 | T2 | large |
| T4 | Frontend: Open folder and Open in editor on the workspace pane | T3 | T5 | small |
| T5 | Frontend: Ask Claude on a pull request | T3 | T4 | small |

T1 → (T2 ∥ T3) → (T4 ∥ T5). The parallel pairs touch disjoint files.

## Rules for every task

- **Read first:** this file's rules and your task's section, then
  [README.md](README.md) (the design). Where this file and the README
  disagree, the README wins. Read only the source and test files your
  task names; that's enough. Don't explore the repo.
- **Implement only your task**, with the exact names, signatures, JSON
  shapes and texts given. Don't change any existing command, field, event
  or behaviour that your task doesn't name.
- `main` (and the feature branch) must build and pass every check after
  each task. Never leave dead code that fails `clippy -D warnings`.
- **Rust** (`desktop/src-tauri/src`): follow the neighbouring code. Put
  `///` or `//!` doc comments on every public item. Unit tests go in a
  `#[cfg(test)] mod tests` at the bottom of the file they test, each named
  as a sentence about the behaviour (`split_keeps_double_quoted_spaces`),
  using `assert_eq!`/`assert!`. No new crates. No `unwrap()`/`expect()`
  outside tests. Errors are `AppError::new(code, message)`.
- **TypeScript** (`desktop/src`): follow the neighbouring code. Use
  `readonly` fields and arrow-function components, and test with Vitest
  and Testing Library like the neighbouring `*.test.tsx`: query by role
  or label, interact with `userEvent`, and use `fakeApi`/`testStore` from
  `src/test/fixtures.ts`. Component files export only components.
- **No test may launch Finder, an editor, or `claude`.** Rust tests use a
  fake `Launch` or `sh` scripts in a `tempfile::tempdir()`. Frontend tests
  use `fakeApi`.
- JSON to and from the frontend is camelCase.
- Commit message: conventional, imperative, e.g.
  `feat: add workspace open launcher (ADR 0051)`.

**Checks.** Run the ones for the languages you touched, until they all
pass:

```sh
# Rust tasks, from desktop/src-tauri (run `cargo fmt` first if --check fails)
cargo fmt --check && cargo clippy --all-targets -- -D warnings && cargo test <filter>
# Frontend tasks, from desktop
npm run typecheck && npm test -- <files>
# every task, from the repo root (Go: must still pass; this change doesn't touch Go)
make fmt && make lint
```

`make fmt` mustn't leave a diff in files you didn't change. If it does,
stop and say so in your report; don't commit those changes.

---

## T1. Rust: open settings, launcher and `workspace_open`

**Depends on:** nothing. **Parallel with:** nothing. **Size:** large.

**Read:** README.md sections "Settings schema", "Rust: `store.rs`",
"Rust: `open.rs`" and "Tauri commands". The source files are
`desktop/src-tauri/src/reviews/store.rs` (the settings structs and the
tests at the bottom, especially `file_without_notifications_opens_with_them_on`),
`desktop/src-tauri/src/lib.rs`, and `desktop/src-tauri/src/runner.rs`
(`AppError` and how `run_with_input` maps `NotFound`). From
`desktop/src-tauri/src/commands.rs`, read only `AppState` and the test
helper `fake_programs`.

**Modify**

- `desktop/src-tauri/src/reviews/store.rs`: add `OpenSettings`, exactly
  as in the README, after `NotificationSettings`. Add `pub open:
  OpenSettings` to `Settings`. Tests to add:
  - `file_without_open_settings_opens_with_empty_commands`: the file
    `{"version":1,"settings":{"reviews":{"skipDrafts":false}}}` gives
    `settings.open == OpenSettings::default()`, and the file is not
    rewritten (its bytes are unchanged).
  - `open_settings_serialize_in_camel_case_and_round_trip`:
    `{"fileManager":"open -R","editor":"zed"}`.
  - `partial_open_settings_keep_the_other_key_empty`: `{"open":{"editor":"zed"}}`
    gives `file_manager == ""`.
- `desktop/src-tauri/src/lib.rs`: add `mod open;` (alphabetical, after
  `mod model;`). Register `open::workspace_open`, `open::open_defaults`
  and `open::settings_set_open` in `generate_handler!`, right after
  `reviews::settings_set_notifications,`.

**Create** `desktop/src-tauri/src/open.rs` with every item in the README's
"Rust: `open.rs`" block, with those exact names and signatures. Follow
the splitting rules, the error table (codes and messages, word for word)
and the `SystemLauncher::launch` behaviour. `default_file_manager` uses
`cfg!(target_os = "macos")` → `"open"` and `cfg!(windows)` → `"explorer"`,
else `"xdg-open"`. `validate` names the field in its message: `the editor
command …` or `the file manager command …`, followed by the split error
(e.g. `the editor command has an unclosed "`). Return code
`setting_invalid`.

**Tests in `open.rs`** (a `FakeLauncher` in `mod tests` records each
`argv` in a `std::sync::Mutex<Vec<Vec<String>>>`, and returns `Ok(())`
or a configured error):

- `split_command`: every row of the README's table, one test per
  behaviour (plain words, double quotes, single quotes, backslash-escaped
  space, backslashes kept in `C:\Windows\explorer.exe` and in
  `"C:\Program Files\x.exe"`, empty quoted word, unclosed `"`, unclosed
  `'`, blank input).
- `build_argv`: appends the dir when there's no `{path}`, replaces
  `{path}` inside a word (`--dir={path}`), and keeps a dir with spaces as
  one word.
- `target_dir`: the editor with one repo gives the repo dir, the editor
  with two repos or none gives the workspace, and the file manager with
  one repo gives the workspace.
- `command_for`: an empty or blank setting gives the default, and a set
  value is trimmed.
- `open` (with a `tempfile::tempdir()` as the workspace and a `repo`
  subdirectory created in it):
  - the editor with one repo launches `["code", "<tmp>/repo"]` and
    returns that `Opened`;
  - a custom `open -a "Visual Studio Code"` with the file manager
    launches `["open", "-a", "Visual Studio Code", "<tmp>"]`;
  - a missing directory gives `open_dir_missing` and the launcher isn't
    called;
  - a stored command with an unclosed quote gives `open_command_invalid`
    and the launcher isn't called;
  - an error from the launcher is returned unchanged.
- `validate`: `""` is OK, `code -n` is OK, and an unclosed quote in
  `editor` gives `setting_invalid` with a message starting `the editor
  command`.
- `SystemLauncher` (unix only: put these tests under `#[cfg(unix)]`):
  write `sh` scripts into a tempdir, mark them executable, and pass that
  dir as `path_env`. Use `grace: Duration::from_millis(300)`.
  - `exit 0` gives `Ok`;
  - `echo boom >&2; exit 3` gives `open_failed`, and the message contains
    `exited with status 3` and `boom`;
  - `sleep 5` gives `Ok` within 2 s (assert it with `Instant`);
  - a program name that isn't in the dir gives `open_program_missing`;
    for the name `code`, the message contains `Shell Command: Install
    'code' command in PATH`;
  - a script named `explorer` that runs `exit 1` gives `Ok`.

**Acceptance**

- The four functions and three commands exist with the README's
  signatures and are registered. `settings_get` now returns
  `"open":{"fileManager":"","editor":""}`.
- `settings_set_open` trims both fields, rejects invalid commands with
  `setting_invalid`, and writes `desktop.json`.
- No test spawns anything except `sh` scripts in a tempdir.

**Verify**

```sh
cd desktop/src-tauri && cargo fmt --check && cargo clippy --all-targets -- -D warnings && cargo test open:: && cargo test store::
cd ../.. && make fmt && make lint
```

---

## T2. Rust: `reviews_ask`, the pull request Q&A agent

**Depends on:** T1 (both edit `lib.rs`). **Parallel with:** T3.
**Size:** large.

**Read:** README.md sections "Rust: `reviews/names.rs`", "Rust:
`reviews/ask.rs`", "The prompt (exact text)" and "Tauri commands". The
source files are `desktop/src-tauri/src/reviews/names.rs` and
`desktop/src-tauri/src/reviews/mod.rs` (`reviews_plan` shows the
`pr_not_found` lookup). From `desktop/src-tauri/src/reviews/fix.rs`, read
only `start_agent` and, in its tests, `fake_ws`, `calls` and
`start_agent_runs_ws_claude_then_records_the_agent`. From
`desktop/src-tauri/src/reviews/github.rs`, read only `PullRequest` and
`split_repo`. From `desktop/src-tauri/src/model.rs`, read only `Entry`
and `Status`.

**Modify**

- `desktop/src-tauri/src/reviews/names.rs`: add `ASK_PREFIX` and
  `ask_name`, as in the README. Tests:
  - `ask_name_has_repo_number_and_short_sha`: for `github.com/bobcob7/buff-bear`,
    42 and `3f2a9c1e0b…` it's `ask-buff-bear-42-3f2a9c1`;
  - `ask_name_fits_in_55_characters` for a 60-character repo name.
- `desktop/src-tauri/src/reviews/mod.rs`: add `pub mod ask;` and the
  `reviews_ask` command from the README. It builds `ask::About` from the
  `github::PullRequest` (`title`, `url`, `head_ref_oid`,
  `head_ref_name`, `base_ref_name`) and calls `ask::start`.
- `desktop/src-tauri/src/lib.rs`: register `reviews::reviews_ask` right
  after `reviews::reviews_start,`.

**Create** `desktop/src-tauri/src/reviews/ask.rs`, with every item in the
README's block, using the constants exactly as listed. `prompt` returns
the README's text exactly, with the placeholders filled in.

**Tests in `ask.rs`**

- `prompt_matches_the_design_text`: for the README's example PR, assert
  the whole string. Copy the expected text from README.md, fill in the
  placeholders by hand, and join the lines with `\n`.
- `prompt_falls_back_to_the_repo_id_without_an_owner`: for repo
  `"weird"`, `{pr}` is `weird#42`.
- `ask_args_put_the_prompt_before_the_tool_flags`: the full vector for
  model `""`. Index 4 is `"--"`, index 5 is the prompt, then
  `--permission-mode default`, `--allowedTools` plus the 11 allowed
  tools, then `--disallowedTools` plus the 13 denied tools.
- `ask_args_pass_a_model`: with `"opus"`, `--model opus` comes right
  after the prompt; `"  "` adds no `--model`.
- `ask_args_check_out_the_head_commit`: index 3 is `<repo>@<full sha>`.
- `disallowed_tools_cover_edits_and_github_posts`: contains `Edit`,
  `Write`, `NotebookEdit`, `Bash(git push *)`, `Bash(gh pr comment *)`,
  `Bash(gh pr review *)` and `Bash(gh api *)`.
- `existing_finds_an_agent_for_the_same_head_only`: entries
  `ask-buff-bear-42-3f2a9c1-0a1b2c3d` (found), `ask-buff-bear-42-9999999-…`
  (another head, not found), `ask-buff-bear-421-3f2a9c1-…` (another PR, not
  found), and `review-buff-bear-42-3f2a9c1-…` (not found). Build the
  `Entry` values in a helper like `fix.rs`'s `agent`.
- `start_returns_an_existing_agent_without_running_ws`: with a `fake_ws`
  that would `exit 1`, `start` returns `already: true` and the log is
  empty.
- `start_runs_ws_claude_and_returns_the_new_agent`: the `fake_ws` prints
  `{"id":"ask-buff-bear-42-3f2a9c1-0a1b2c3d","session":"5f6e7d8c","path":"/w/a"}`.
  The result has `already: false` and `session == Some("5f6e7d8c")`.
  The first log line starts with
  `claude --json --name ask-buff-bear-42-3f2a9c1 github.com/bobcob7/buff-bear@<sha> -- You answer questions about pull request`
  (the log is `$*`, so the prompt's newlines split it across lines;
  check only that prefix).
- `start_returns_the_ws_error`: with `fake_ws` running
  `echo '{"error":{"code":"claude_untrusted","message":"not trusted"}}' >&2; exit 1`,
  the result is `Err` with code `claude_untrusted`.

Copy `fake_ws` and `calls` into `ask.rs`'s test module; don't make
`fix.rs`'s public.

**Acceptance**

- `reviews_ask` is registered and returns `AskStarted` as camelCase JSON:
  `{"id","session","path","already"}`.
- A second call for the same PR and head returns the first agent with
  `already: true`.
- No test runs `claude` or the real `ws`.

**Verify**

```sh
cd desktop/src-tauri && cargo fmt --check && cargo clippy --all-targets -- -D warnings && cargo test ask && cargo test names::
cd ../.. && make fmt && make lint
```

---

## T3. Frontend: API types, fixtures, Tauri mock, Settings › Workspaces

**Depends on:** T1. **Parallel with:** T2 (T3 touches only TypeScript
and `desktop/test/`). **Size:** large.

**Read:** README.md sections "A and B: Settings › Workspaces", "Tauri
commands", "Frontend" (all of it, including "Tauri mock for
screenshots"). Source files: `desktop/src/api.ts`,
`desktop/src/test/fixtures.ts`,
`desktop/src/features/settings/settingsSlice.ts`, `SettingsList.tsx`,
`SettingsPane.tsx` and `SettingsPane.test.tsx`. Read only what you need
from `desktop/src/features/settings/ReviewerProfiles.test.tsx`: the
`Settings` literals at lines ~69 and ~85.

**Modify**

- `desktop/src/api.ts`: add `OpenWith`, `OpenSettings`, `Opened` and
  `AskStarted`. Add `open` to `Settings`. Add `workspaceOpen`,
  `openDefaults`, `settingsSetOpen` and `reviewsAsk` to `api`, exactly as
  in the README. (`reviews_ask` is added to the backend by T2. The
  wrapper is only typed here, and T5 calls it.)
- `desktop/src/test/fixtures.ts`: add `openSettings()` and the four
  `fakeApi` functions, as in the README. Add `open: openSettings()` to
  every `Settings` object.
- `desktop/src/features/settings/ReviewerProfiles.test.tsx` and
  `SettingsPane.test.tsx`: add `open: openSettings()` to each `Settings`
  literal so `npm run typecheck` passes. Change nothing else in them.
- `desktop/src/features/settings/settingsSlice.ts`: add the `"workspaces"`
  group, `openDefaults`, `openErrors`, `fetchOpenDefaults` and
  `saveOpenSettings`, as in the README's "Slices".
- `desktop/src/features/settings/SettingsList.tsx`: add `{ id:
  "workspaces", title: "Workspaces" }` between Notifications and About.
- `desktop/src/features/settings/SettingsPane.tsx`: add a
  `WorkspacesGroup` (`<section aria-label="Workspaces">`, `<h1>`
  "Workspaces"). It has the two `Row`s from the README's table, with this
  help text, word for word:
  - File manager: `Opens a workspace's folder. Empty uses the system's. The folder is added at the end, or where {path} is.`
  - Editor (a `ReactNode` with `<code>`): ``Opens a workspace in your editor: its repo when it has exactly one, otherwise the workspace folder. Empty uses VS Code's `code`. Try `open -a "Visual Studio Code"`, `cursor` or `zed`; use full paths, since ~ and $VARS aren't expanded.``

  It saves on blur with `useDraft`, reverting on failure as "At most"
  does. It dispatches `fetchOpenDefaults()` once on mount when
  `openDefaults` is null. Render it when `group === "workspaces"`.

**Create** `desktop/test/tauri-mock.js`, exactly as specified in the
README's "Tauri mock for screenshots": every command in that table, the
`__TAURI_MOCK__` object, and `console.warn` for unhandled commands. Build
the sample data from the shapes in `api.ts`, with the values from
`fixtures.ts` where the README says so. It must be valid in a browser as
is: no `import`/`export` and no TypeScript syntax. Check that it parses
with `node --check desktop/test/tauri-mock.js`.

**Tests to add to `SettingsPane.test.tsx`** (open the group first with
`store.dispatch(groupChosen("workspaces"))`):

- `shows the default commands as placeholders`: the inputs labelled
  `File manager command` and `Editor command` have placeholders `open`
  and `code` (from `fakeApi().openDefaults`).
- `saves the editor command on blur, trimmed`: type `  zed  `, tab;
  `api.settingsSetOpen` is called with `{ fileManager: "", editor: "zed" }`.
- `shows a rejected command under its field and reverts it`:
  `settingsSetOpen` rejects with `{ code: "setting_invalid", message:
  "the editor command has an unclosed \"" }`. The alert shows that
  message, and the input's value is back to `""`.
- `lists Workspaces between Notifications and About`: render
  `SettingsList` and check the button order.

**Acceptance**

- `npm run typecheck` passes with `open` required on `Settings`.
- `node --check desktop/test/tauri-mock.js` passes.
- No other component changes behaviour.

**Verify**

```sh
cd desktop && npm run typecheck && npm test -- src/features/settings && node --check test/tauri-mock.js
cd .. && make fmt && make lint
```

---

## T4. Frontend: Open folder and Open in editor on the workspace pane

**Depends on:** T3. **Parallel with:** T5. **Size:** small.

**Read:** README.md sections "A and B: the workspace pane" and "Slices"
(the `agentsSlice.ts` bullet). Source files:
`desktop/src/features/agents/agentsSlice.ts`, `agentsSlice.test.ts`,
`EntryPane.tsx` and `EntryPane.test.tsx`. The `Entry` and `OpenWith`
types are in `desktop/src/api.ts` (search for them; don't read the whole
file).

**Modify**

- `desktop/src/features/agents/agentsSlice.ts`: add `openWorkspace` and
  `editorDir`, as in the README.
- `desktop/src/features/agents/EntryPane.tsx`: add the two buttons, with
  the icons, labels, titles, placement and disabled rules from the
  README's table. Track the in-flight button with `useState<OpenWith |
  null>`, cleared when the dispatched thunk settles. Make the path link
  dispatch `openWorkspace({ entry, with: "fileManager" })` with the title
  `Open in the file manager`. The `api` import may become unused; remove
  it if so.

**Tests**

- `agentsSlice.test.ts`:
  - `editorDir opens the only repo, else the workspace`: 0, 1 and 2
    repos.
  - `openWorkspace passes the workspace path and repo paths`: an entry
    with repos `buff-bear` and `api`, `with: "editor"`.
    `api.workspaceOpen` is called with `("editor", "/w/fix-login", ["buff-bear", "api"])`.
  - `openWorkspace shows a failure as an error notice`: `workspaceOpen`
    rejects `{ code: "open_program_missing", message: "`code` isn't on the app's PATH." }`.
    `store.getState().ui.notice` is `{ severity: "error", message:
    "Couldn't open fix-login: `code` isn't on the app's PATH." }`.
- `EntryPane.test.tsx`:
  - `opens the workspace folder in the file manager`: click the button
    named `Open folder`. `workspaceOpen` is called with
    `("fileManager", "/w/fix-login", [])`.
  - `opens a one-repo workspace's repo in the editor`: an entry with one
    repo `{ id: "github.com/bobcob7/buff-bear", path: "buff-bear", ref: "main", refKind: "branch" }`.
    The `Open in editor` button's `title` contains
    `/w/fix-login/buff-bear`, and clicking it calls `workspaceOpen` with
    `("editor", "/w/fix-login", ["buff-bear"])`.
  - `disables the open buttons while the workspace is pending`:
    `entry({ kind: "pending" })`.
  - `the path link opens through the file manager setting`: clicking
    the path text calls `workspaceOpen("fileManager", …)` and not
    `openExternal`.

**Acceptance**

- Both buttons are on every entry's pane and are disabled while pending,
  busy or opening.
- Failures show the README's notice text.

**Verify**

```sh
cd desktop && npm run typecheck && npm test -- src/features/agents
cd .. && make fmt && make lint
```

---

## T5. Frontend: Ask Claude on a pull request

**Depends on:** T3. **Parallel with:** T4. **Size:** small.

**Read:** README.md section "C: Ask Claude on a pull request" and the
`reviewsSlice.ts` bullet of "Slices". Source files:
`desktop/src/features/reviews/reviewsSlice.ts`, `reviewsSlice.test.ts`,
`ReviewsPane.tsx` (the `PrHeader` component) and `ReviewsPane.test.tsx`.
`ownerRepo` is in `desktop/src/features/reviews/groups.ts`; read only its
signature.

**Modify**

- `desktop/src/features/reviews/reviewsSlice.ts`: add `asking` to the
  state (initially `[]`) and the `askAboutPr` thunk, following the
  README's steps 1 to 5 exactly, with the notice texts word for word.
  Import `selected` and `sectionChosen` from `../ui/uiSlice` if they
  aren't imported yet.
- `desktop/src/features/reviews/ReviewsPane.tsx`: in `PrHeader`, add the
  `Ask Claude` button right after `Open on GitHub`, with the icon, title,
  in-flight label and disabled rule from the README.

**Tests**

- `reviewsSlice.test.ts`:
  - `askAboutPr selects the new agent in Agents`: after it's dispatched,
    `api.reviewsAsk` was called with `(pr.repo, pr.number)`,
    `ui.section` is `"agents"`, `ui.selectedKey` is
    `"ask-buff-bear-42-3f2a9c1-0a1b2c3d"`, `api.listEntries` was called,
    and the notice is a success starting `Started ask-buff-bear-42-3f2a9c1-0a1b2c3d`.
  - `askAboutPr says when the agent already exists`: `reviewsAsk`
    resolves `already: true`; the notice is `info`.
  - `askAboutPr shows a failure`: `reviewsAsk` rejects `{ code:
    "pr_not_found", message: "gone" }`; the notice is `{ severity:
    "error", message: "gone" }`; `asking` is empty afterwards.
  - `asking holds the pull request while it starts`: the reducer with
    `askAboutPr.pending` contains `pr.key`.
- `ReviewsPane.test.tsx`:
  - `asks Claude about the pull request`: render a selected PR the way
    the existing tests do, click the button named `Ask Claude`, and
    expect `api.reviewsAsk` to be called with `("github.com/bobcob7/buff-bear", 42)`.
  - `offers Ask Claude on a dismissed pull request too`.

**Acceptance**

- The button appears on every pull request, and shows `Starting…` and is
  disabled while asking.
- After success the Agents section shows the new agent selected.

**Verify**

```sh
cd desktop && npm run typecheck && npm test -- src/features/reviews
cd .. && make fmt && make lint
```

---

## Verification

Whole-branch checks, after the last task merges (all from the repo root):

```sh
make lint
make test
make desktop-test      # typecheck, vitest, cargo test, clippy
node --check desktop/test/tauri-mock.js
```

The CLI doesn't change, so there's no CLI smoke test. The checks need no
real `claude`, editor or file manager.

**Screenshots** (the orchestrator): `cd desktop && npx vite --port 1420`,
then Playwright with `page.addInitScript({ path: "test/tauri-mock.js" })`.
Take these shots:

1. Agents → `fix-login-d8efb24f` → the header with **Open folder** and
   **Open in editor** (`desktop/docs/open-buttons.png`).
2. Settings → Workspaces (`desktop/docs/settings-workspaces.png`).
3. Reviews → #42 → the header with **Ask Claude** (`desktop/docs/reviews-ask.png`).
4. After clicking Ask Claude: Agents with `ask-buff-bear-42-3f2a9c1-…`
   selected (`desktop/docs/ask-agent.png`).

The console must show no `tauri-mock: unhandled command` warnings.

**Manual test plan** (leave these unchecked in the PR unless done on a
real install):

- [ ] Open in editor on a one-repo workspace opens that repo in VS Code,
      when the app is launched from Finder (not from a terminal).
- [ ] Open folder opens Finder at the workspace.
- [ ] Editor set to `open -a "Visual Studio Code"` works; set to a
      missing program, it shows the PATH error notice.
- [ ] Ask Claude: the agent reads the PR without stopping for
      permission (if it does stop, `--allowedTools` isn't honoured in
      `--bg` sessions: note it in the PR). It summarizes the PR and
      waits. Asked to edit a file or comment on the PR, it refuses or is
      denied.
- [ ] Ask Claude again on the same head selects the same agent.
