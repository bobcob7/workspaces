# Open a workspace, and ask about a pull request

Design for [ADR 0051](../../adrs/0051-desktop-open-workspace-and-ask-about-pr.md).
This change is desktop-only: `ws` itself doesn't change. The tasks are
in [tasks.md](tasks.md).

Three features:

- **A. Open folder**: opens a workspace in the file manager (Finder,
  Explorer, or the default on Linux).
- **B. Open in editor**: opens a workspace in an editor (VS Code by
  default).
- **C. Ask Claude**: on a pull request in Reviews, starts a read-only
  agent at the PR's head that reads the PR and waits for your questions.

## UX

### A and B: the workspace pane

In `EntryPane`'s title row, right after the spacer (`<span
className={styles.spacer} />`) and before the Conversation/Terminal
toggle:

```
fix-login  ● Working                     [📁 Open folder] [</> Open in editor] [Conversation|Terminal] [Detach] [Stop] [Rename] [Hide] [Remove]
/Users/me/…/workspaces/fix-login-d8efb24f   session e8cdf5d8   buff-bear@main
```

| | Open folder | Open in editor |
|---|---|---|
| Component | MUI `Button size="small"` with `startIcon` | same |
| Icon | inline SVG folder, 14×14, `stroke="currentColor"`, `aria-hidden` (same style as `EyeOffIcon`) | inline SVG `</>` (two chevrons), same style |
| Label (accessible name) | `Open folder` | `Open in editor` |
| `title` tooltip | `Open <entry.path> in the file manager (Settings › Workspaces)` | `Open <editorDir(entry)> in the editor (Settings › Workspaces)` |
| Shown for | every entry kind | every entry kind |
| Disabled when | `entry.kind === "pending"`, the entry is busy (`s.agents.busy` has its key), or this button's open is in flight | same |
| Calls | `openWorkspace({ entry, with: "fileManager" })` | `openWorkspace({ entry, with: "editor" })` |
| Directory | `entry.path` | the repo's directory `entry.path + "/" + entry.repos[0].path` when `entry.repos.length === 1`, otherwise `entry.path` |

The path link under the title (`<button className={styles.path}>`) used
`api.openExternal(entry.path)`. Now it dispatches the same
`openWorkspace({ entry, with: "fileManager" })`, and its `title` becomes
`Open in the file manager`.

**Why the editor opens the repo when there's exactly one.** A one-repo
workspace's agent already works in that repo ([ADR
0028](../../adrs/0028-single-repo-session-directory.md)). Opening it as
the editor's root gives VS Code what it expects: the git repo at the
root, so Source Control works, and the repo's `.vscode/` settings and
tasks load. Opened one level up, the editor shows one extra folder and
ignores `.vscode/`. With several repos, or none, the workspace directory
is the only root that holds everything. VS Code still finds nested git
repos, since `git.autoRepositoryDetection` defaults to scanning
subfolders. The file manager always opens the workspace directory, the
same as the path link, so you see everything the workspace holds.

**Success**: nothing is shown; the other app comes to the front.
**Failure**: an error notice (`noticeShown({ severity: "error", message
})`) reads `Couldn't open <entry.name>: <error.message>`, for example:

- `` Couldn't open fix-login: `code` isn't on the app's PATH. In VS Code run "Shell Command: Install 'code' command in PATH", or set the editor in Settings › Workspaces. ``
- `` Couldn't open fix-login: /w/fix-login-d8efb24f/buff-bear doesn't exist ``
- `` Couldn't open fix-login: `zed` exited with status 1: zed: unknown flag --bogus ``

### A and B: Settings › Workspaces

A new Settings group, **Workspaces**. In `SettingsList` it comes after
Notifications and before About. Its id is `"workspaces"` and its section
`aria-label` is `"Workspaces"`. The initial group stays `"reviews"`.

```
Workspaces
 File manager   Opens a workspace's folder. Empty uses     [ open                  ]  ← placeholder = default
                the system's. The folder is added at the
                end, or where {path} is.
 Editor         Opens a workspace in your editor: its      [ code                  ]
                repo when it has exactly one, otherwise
                the workspace folder. Empty uses VS Code's
                `code`. Try `open -a "Visual Studio Code"`,
                `cursor` or `zed`; use full paths, since ~
                and $VARS aren't expanded.
```

| Row label (`role="group"` name) | Input `aria-label` | Value | Placeholder | Saved |
|---|---|---|---|---|
| `File manager` | `File manager command` | `settings.open.fileManager` | `openDefaults.fileManager` (`open` on macOS) | on blur, if changed, trimmed |
| `Editor` | `Editor command` | `settings.open.editor` | `openDefaults.editor` (`code`) | on blur, if changed, trimmed |

- The rows use the existing `Row` component and the `useDraft` hook
  from `SettingsPane.tsx`, like "At most". `TextField size="small"`,
  full width of the control column, font `var(--font-mono)`.
- When a save is rejected (`setting_invalid`, e.g. `the editor command
  has an unclosed quote`), the message shows under that field
  (`role="alert"`) and the field goes back to the saved value, the same
  as "At most" does.
- The group shows a spinner until `settings` loads. `openDefaults` is
  fetched once when the group is first shown. Until it arrives, the
  placeholders are empty.

### C: Ask Claude on a pull request

In `ReviewsPane`'s `PrHeader` title row, right after **Open on GitHub**:

```
Add rate limiting  ● To review           [Open on GitHub] [💬 Ask Claude] [Dismiss] [Review]
```

| | |
|---|---|
| Component | `Button size="small"` with an inline SVG speech-bubble `startIcon` (same style as the other icons) |
| Label | `Ask Claude`; `Starting…` while its request is in flight |
| `title` | `Start a read-only Claude Code agent at this pull request's head that reads it and answers your questions. It can't change code or post to GitHub.` |
| Shown for | every pull request, in every group (To review, Reviewing, Reviewed, Dismissed) |
| Disabled when | `s.reviews.asking` contains `pr.key` |
| Calls | `askAboutPr(pr)` |

`askAboutPr` does this:

1. `api.reviewsAsk(pr.repo, pr.number)` returns `AskStarted`.
2. `await dispatch(fetchEntries())`
3. `dispatch(sectionChosen("agents"))`, then `dispatch(selected(started.id))`.
   This is what New agent does. Selecting a live agent attaches to it
   (`EntryPane`'s effect), and one that's still `starting` shows its
   conversation until it's attached.
4. A notice. When it started: success, `Started <id> to answer questions
   about <owner/name>#<n>`. When `already`: info, `<id> already answers
   questions about <owner/name>#<n>`.
5. On an error: an error notice with `error.message`, and the thunk
   rejects.

The agent is an ordinary, visible, top-level workspace named
`ask-<repo>-<n>-<sha7>`. It shows in Agents as Working while it reads,
then Waiting or Needs input. You remove it like any other agent.

## Backend

### Settings schema (`desktop.json`)

`settings` gains `open`:

```json
{
  "version": 1,
  "settings": {
    "reviews": { "...": "unchanged" },
    "notifications": { "enabled": true, "needsInput": true, "done": true },
    "open": { "fileManager": "", "editor": "" }
  },
  "reviews": { "...": "unchanged" }
}
```

- `""` means the platform default. A file without `open`, or with only
  one of its keys, loads with `""` for whatever is missing (`#[serde(default)]`).
  The version stays `1`, and the file isn't rewritten on load. This is
  the same migration as `notifications` in ADR 0050.
- Values are stored trimmed. `settings_set_open` rejects a value that
  doesn't split (see below) with `setting_invalid`. An empty value is
  always valid.

### Rust: `store.rs`

```rust
/// The commands that open a workspace (ADR 0051). "" uses the platform's
/// default.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct OpenSettings {
    pub file_manager: String,
    pub editor: String,
}

#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Settings {
    pub reviews: ReviewSettings,
    pub notifications: NotificationSettings,
    pub open: OpenSettings,          // new
}
```

### Rust: `open.rs` (new module, `mod open;` in `lib.rs`)

```rust
//! Opening a workspace in the file manager or an editor (ADR 0051).

use crate::commands::AppState;
use crate::reviews::store::{OpenSettings, Settings};
use crate::runner::AppError;
use serde::{Deserialize, Serialize};
use std::path::{Path, PathBuf};
use std::time::Duration;
use tauri::State;

/// Which program opens the workspace. JSON: "fileManager" | "editor".
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub enum OpenWith { FileManager, Editor }

pub const DEFAULT_EDITOR: &str = "code";

/// `open` on macOS, `explorer` on Windows, `xdg-open` elsewhere.
pub fn default_file_manager() -> &'static str;

/// The defaults, as the Settings placeholders show them.
pub fn defaults() -> OpenSettings;

/// The command to run: the trimmed setting, or the default when it's empty.
pub fn command_for(with: OpenWith, settings: &OpenSettings) -> String;

/// Splits a command into words (rules below). Err(`setting_invalid`) on an
/// unclosed quote or when there are no words.
pub fn split_command(command: &str) -> Result<Vec<String>, AppError>;

/// Rejects a non-empty setting that `split_command` rejects; the message
/// names the field: "the editor command has an unclosed quote".
pub fn validate(settings: &OpenSettings) -> Result<(), AppError>;

/// The directory to open: for Editor with exactly one repo,
/// `Path::new(path).join(&repos[0])`; otherwise `path`.
pub fn target_dir(with: OpenWith, path: &str, repos: &[String]) -> PathBuf;

/// The program and arguments: every `{path}` inside a word is replaced by
/// `dir`; when no word contains `{path}`, `dir` is appended as a last word.
pub fn build_argv(command: &str, dir: &Path) -> Result<Vec<String>, AppError>;

/// Starts a program. The seam tests replace.
pub trait Launch {
    /// `argv[0]` is the program, found on `path_env`.
    fn launch(&self, argv: &[String], path_env: &str) -> Result<(), AppError>;
}

/// Launches real processes; waits `grace` for an early failure.
pub struct SystemLauncher { pub grace: Duration }
impl Default for SystemLauncher { /* grace: 3 s */ }
impl Launch for SystemLauncher { /* below */ }

/// What was opened, for tests and the frontend.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Opened {
    pub dir: String,
    pub command: Vec<String>,
}

/// Opens the workspace: checks the target is a directory, builds the
/// argv from the setting, and launches it.
pub fn open(
    launcher: &dyn Launch,
    path_env: &str,
    settings: &OpenSettings,
    with: OpenWith,
    path: &str,
    repos: &[String],
) -> Result<Opened, AppError>;

#[tauri::command]
pub async fn workspace_open(
    state: State<'_, AppState>,
    with: OpenWith,
    path: String,
    repos: Vec<String>,
) -> Result<Opened, AppError>;   // spawn_blocking(open(&SystemLauncher::default(), …))

#[tauri::command]
pub fn open_defaults() -> OpenSettings;

#[tauri::command]
pub fn settings_set_open(state: State<'_, AppState>, open: OpenSettings) -> Result<Settings, AppError>;
```

**Splitting rules (`split_command`).** These are POSIX-shell-like, with
no expansion:

- Unquoted whitespace separates words. Runs of whitespace count as one.
- `'…'` is literal. An unclosed `'` is an error.
- `"…"` is literal, except that `\"` and `\\` become `"` and `\`. Any
  other backslash stays, so `"C:\Program Files\x.exe"` keeps its
  backslashes. An unclosed `"` is an error.
- Outside quotes, `\` followed by whitespace, `'`, `"` or `\` makes that
  character literal. Any other backslash stays, so `C:\Windows\explorer.exe`
  works.
- `""` and `''` make an empty word only when they're the whole word
  (`a "" b` gives `["a", "", "b"]`).
- No words at all is an error: `no program in the command`.

| Input | Output |
|---|---|
| `code` | `["code"]` |
| `code -n` | `["code", "-n"]` |
| `open -a "Visual Studio Code"` | `["open", "-a", "Visual Studio Code"]` |
| `open -a 'Visual Studio Code'` | same |
| `/Applications/My\ Editor.app/bin/ed --wait` | `["/Applications/My Editor.app/bin/ed", "--wait"]` |
| `idea --line 1 {path}` | `["idea", "--line", "1", "{path}"]` |
| `open -a "Visual Studio Code` | Err `setting_invalid`: `unclosed " in the command` |
| `   ` | Err `setting_invalid`: `no program in the command` |

`build_argv("open -a Code", "/w/x y")` gives
`["open", "-a", "Code", "/w/x y"]`.
`build_argv("idea --dir={path}", "/w")` gives `["idea", "--dir=/w"]`.

**`open` errors** (each an `AppError { code, message }`):

| Situation | code | message |
|---|---|---|
| target isn't an existing directory (`!dir.is_dir()`) | `open_dir_missing` | `<dir> doesn't exist` |
| stored command doesn't split | `open_command_invalid` | `the <editor\|file manager> command in Settings › Workspaces is invalid: <split error>` |
| program not found (`io::ErrorKind::NotFound`) | `open_program_missing` | `` `<program>` isn't on the app's PATH. `` plus, when the program is `code`: `In VS Code run "Shell Command: Install 'code' command in PATH", or set the editor in Settings › Workspaces.`; otherwise: `Set its full path in Settings › Workspaces.` |
| spawn failed otherwise | `open_failed` | `` couldn't start `<program>`: <io error> `` |
| exited non-zero within `grace` | `open_failed` | `` `<program>` exited with status <code>: <stderr, trimmed, last 500 chars> `` (no `: …` if stderr is empty) |

The launcher isn't called when the directory is missing or the command
doesn't split.

**`SystemLauncher::launch`.** `std::process::Command::new(&argv[0])`
with `.args(&argv[1..])`, `.env("PATH", path_env)`, `stdin` and `stdout`
null, and `stderr` piped. Setting `PATH` on the command makes Rust look
up the program on that PATH, which is what `Runner` relies on. Then it
calls `try_wait` every 25 ms until `grace` passes:

- Exited with success: `Ok(())`.
- Exited with failure: read stderr. If the program's file stem
  (`Path::new(&argv[0]).file_stem()`) is `explorer`, ignoring case,
  return `Ok(())`, since Explorer exits 1 even when it works. Otherwise
  return `Err(open_failed …)`.
- Still running after `grace`: start a `std::thread` that reads stderr to
  the end (so a chatty program never blocks on a full pipe), then calls
  `child.wait()`. Return `Ok(())`.

`workspace_open` reads `state.store.get().settings.open` and
`state.runner.path()`, and runs `open` in
`tauri::async_runtime::spawn_blocking`. A `JoinError` maps to
`AppError::new("internal", …)`.

`settings_set_open` trims both fields, runs `validate`, then
`state.store.update(|d| { d.settings.open = open; d.settings.clone() })`.

### Rust: `reviews/names.rs`

```rust
const ASK_PREFIX: &str = "ask-";

/// `ask-<repo>-<number>-<sha7>`: the `--name` of a pull request Q&A agent
/// (ADR 0051), which `ws` makes the id from by adding a random suffix.
pub fn ask_name(repo_id: &str, number: u64, sha: &str) -> String;
// format!("{ASK_PREFIX}{}-{number}-{}", short_repo(ASK_PREFIX, repo_id, number), sanitize(short_sha(sha)))
```

### Rust: `reviews/ask.rs` (new, `pub mod ask;` in `reviews/mod.rs`)

```rust
//! Ask about a pull request (ADR 0051): a read-only Claude Code agent in a
//! new workspace at the PR's head, primed with the PR, that waits for
//! questions.

/// Pre-approved, so reading the PR never waits for permission.
pub const ALLOWED_TOOLS: &[&str] = &[
    "Read", "Grep", "Glob",
    "Bash(gh pr view *)", "Bash(gh pr diff *)", "Bash(gh pr checks *)",
    "Bash(git log *)", "Bash(git show *)", "Bash(git diff *)",
    "Bash(git blame *)", "Bash(git grep *)",
];

/// Denied for the whole session: no edits, commits, pushes, or posts.
pub const DISALLOWED_TOOLS: &[&str] = &[
    "Edit", "Write", "NotebookEdit",
    "Bash(git commit *)", "Bash(git push *)",
    "Bash(gh pr comment *)", "Bash(gh pr review *)", "Bash(gh pr merge *)",
    "Bash(gh pr close *)", "Bash(gh pr edit *)", "Bash(gh pr ready *)",
    "Bash(gh issue *)", "Bash(gh api *)",
];

/// The pull request the agent is about.
pub struct About<'a> {
    pub repo: &'a str,          // cache id, "github.com/bobcob7/buff-bear"
    pub number: u64,
    pub title: &'a str,
    pub url: &'a str,
    pub head: &'a str,          // full head commit sha
    pub head_branch: &'a str,
    pub base_branch: &'a str,
}

/// The agent's first message (exact text below).
pub fn prompt(about: &About<'_>) -> String;

/// `ws claude` arguments without `--json` (the runner adds it):
/// ["claude", "--name", ask_name, "<repo>@<head>", "--", prompt,
///  ("--model", model) only when model is non-empty after trim,
///  "--permission-mode", "default",
///  "--allowedTools", ALLOWED_TOOLS…, "--disallowedTools", DISALLOWED_TOOLS…]
/// The prompt comes right after "--", because both tool flags take
/// several values and would take a prompt that followed them as a tool.
pub fn ask_args(about: &About<'_>, model: &str) -> Vec<String>;

/// An agent already asking about this pull request at this head: a
/// listed entry whose workspace id starts with `ask_name(..) + "-"`.
pub fn existing<'e>(entries: &'e [Entry], repo: &str, number: u64, head: &str) -> Option<&'e Entry>;

#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct AskStarted {
    pub id: String,
    pub session: Option<String>,
    pub path: String,
    /// It was already running; nothing new was started.
    pub already: bool,
}

/// Returns the existing agent, or runs `ws claude` and returns the new one.
pub async fn start(
    runner: &Runner,
    about: &About<'_>,
    model: &str,
    entries: &[Entry],
) -> Result<AskStarted, AppError>;
```

`existing` → `AskStarted { id: workspace_id, session: entry.session_id, path: entry.path, already: true }`.
New → `runner.ws_json::<Created>(&ask_args(..))` →
`AskStarted { id, session: Some(session), path, already: false }`.

The command, in `reviews/mod.rs`:

```rust
/// Starts, or finds, a read-only agent that answers questions about the
/// pull request (ADR 0051).
#[tauri::command]
pub async fn reviews_ask(state: State<'_, AppState>, repo: String, number: u64) -> CmdResult<ask::AskStarted>;
```

It looks up `state.reviews.pull_request(&repo, number)`. If that's
`None`, it returns the same `pr_not_found` error as `reviews_plan`. It
uses `model = state.store.get().settings.reviews.model` (the Review
agent's model in Settings) and `entries = state.reviews.entries()`.

### The prompt (exact text)

Placeholders: `{pr}` is `owner/name#number` (from `github::split_repo`;
fall back to `{repo}#{number}`), `{repo}` is the cache id, and `{sha7}`
is `names::short_sha(head)`. Lines are joined with `\n`. There is one
empty line between paragraphs and no trailing newline.

```text
You answer questions about pull request {pr}, "{title}" ({url}). This workspace has {repo} checked out at the pull request's head commit, {sha7}: branch {head_branch} into {base_branch}.

First get to know it, without changing anything:
1. Run `gh pr view {number} --repo {repo} --comments` for the description and the discussion.
2. Run `gh pr diff {number} --repo {repo}` for the changes.
3. Read the changed files, and the code around them that calls them or that they call, until you can explain how the change fits in. Use `git log`, `git show` and `git blame` for history when it helps.

Rules: you are read-only. Don't edit, create or delete files, don't commit or push, and don't post anything to GitHub: no comments, reviews, approvals or labels. If I ask for a change, describe it with file and line, and say that a fix agent can make it.

When you've read it, reply with a short summary: what the pull request changes and why, the files that matter most, and anything that looks risky or unclear. Then wait for my questions, and answer them with file and line references.
```

Example, for `github.com/bobcob7/buff-bear` #42 "Add rate limiting" at
`3f2a9c1e…`, `feat/rate-limit` into `main`: the first line is ``You
answer questions about pull request bobcob7/buff-bear#42, "Add rate
limiting" (https://github.com/bobcob7/buff-bear/pull/42). This workspace
has github.com/bobcob7/buff-bear checked out at the pull request's head
commit, 3f2a9c1: branch feat/rate-limit into main.``

`gh` accepts `--repo HOST/OWNER/REPO`, so the cache id works for GitHub
Enterprise hosts too.

### Tauri commands (all registered in `lib.rs`'s `generate_handler!`)

| Command | Args (JS keys) | Returns |
|---|---|---|
| `workspace_open` | `{ with: "fileManager" \| "editor", path: string, repos: string[] }` | `Opened` |
| `open_defaults` | none | `OpenSettings` |
| `settings_set_open` | `{ open: OpenSettings }` | `Settings` |
| `reviews_ask` | `{ repo: string, number: number }` | `AskStarted` |

JSON shapes:

```json
// Opened
{ "dir": "/w/fix-login-d8efb24f/buff-bear", "command": ["code", "/w/fix-login-d8efb24f/buff-bear"] }
// OpenSettings
{ "fileManager": "", "editor": "" }
// open_defaults on macOS
{ "fileManager": "open", "editor": "code" }
// AskStarted
{ "id": "ask-buff-bear-42-3f2a9c1-0a1b2c3d", "session": "5f6e7d8c", "path": "/w/ask-buff-bear-42-3f2a9c1-0a1b2c3d", "already": false }
// Error (rejected invoke)
{ "code": "open_program_missing", "message": "`code` isn't on the app's PATH. In VS Code run …" }
```

## Frontend

### `api.ts`

```ts
/** Which program opens a workspace (ADR 0051). */
export type OpenWith = "fileManager" | "editor";

/** The commands that open a workspace; "" uses the platform default. */
export interface OpenSettings {
  readonly fileManager: string;
  readonly editor: string;
}

/** The directory opened and the command run. */
export interface Opened {
  readonly dir: string;
  readonly command: readonly string[];
}

/** A pull request Q&A agent, started or found (ADR 0051). */
export interface AskStarted {
  readonly id: string;
  readonly session: string | null;
  readonly path: string;
  readonly already: boolean;
}

export interface Settings {
  readonly reviews: ReviewSettings;
  readonly notifications: NotificationSettings;
  readonly open: OpenSettings;          // new
}

// in `api`:
workspaceOpen: async (openWith: OpenWith, path: string, repos: readonly string[]): Promise<Opened> =>
  call<Opened>("workspace_open", { with: openWith, path, repos }),
openDefaults: async (): Promise<OpenSettings> => call<OpenSettings>("open_defaults"),
settingsSetOpen: async (open: OpenSettings): Promise<Settings> => call<Settings>("settings_set_open", { open }),
reviewsAsk: async (repo: string, number: number): Promise<AskStarted> =>
  call<AskStarted>("reviews_ask", { repo, number }),
```

### Slices

- `settingsSlice.ts`: `SettingsGroup` adds `"workspaces"`. State adds
  `openDefaults: OpenSettings | null` and `openErrors:
  Readonly<Partial<Record<keyof OpenSettings, string>>>`. Thunks:
  `fetchOpenDefaults` (void → `OpenSettings`) and
  `saveOpenSettings(change: Partial<OpenSettings>)`. The second merges
  into `current.open`, like `saveNotificationSettings`, then calls
  `api.settingsSetOpen`. When it's fulfilled, it sets `settings` and
  clears the changed keys' `openErrors`. When it's rejected, it sets
  `openErrors[key]` to the message for each changed key.
- `agentsSlice.ts`: the thunk `openWorkspace({ entry: Entry, with:
  OpenWith })` returns `Opened`. It calls `api.workspaceOpen(with,
  entry.path, entry.repos.map((r) => r.path))`. On an error it dispatches
  `noticeShown({ severity: "error", message: \`Couldn't open
  ${entry.name}: ${error.message}\` })` and rejects with the `AppError`.
  It also exports a pure helper `editorDir(entry: Entry): string`, which
  gives `` `${entry.path}/${entry.repos[0].path}` `` for exactly one repo,
  else `entry.path`, for the tooltip.
- `reviewsSlice.ts`: state adds `asking: readonly string[]` (pr keys).
  The thunk `askAboutPr(pr: PullRequest)` returns `AskStarted`, as in
  the UX section. pending adds `pr.key`; fulfilled and rejected remove
  it.

### Test fixtures (`src/test/fixtures.ts`)

- `openSettings(overrides)` → `{ fileManager: "", editor: "", ...overrides }`.
- Every `Settings` object in `fakeApi` gets `open: openSettings()`.
- `fakeApi` adds:
  - `workspaceOpen: vi.fn(async (_with, path) => ({ dir: path, command: ["code", path] }))`
  - `openDefaults: vi.fn(async () => ({ fileManager: "open", editor: "code" }))`
  - `settingsSetOpen: vi.fn(async (open) => ({ reviews: reviewSettings(), notifications: notificationSettings(), open }))`
  - `reviewsAsk: vi.fn(async () => ({ id: "ask-buff-bear-42-3f2a9c1-0a1b2c3d", session: "5f6e7d8c", path: "/w/ask-buff-bear-42-3f2a9c1-0a1b2c3d", already: false }))`

### Tauri mock for screenshots (`desktop/test/tauri-mock.js`)

This is a plain browser script (no imports or exports, ES2020), loaded
with Playwright's `page.addInitScript({ path })` before the app's
scripts. It isn't a test, and Vitest and `tsc` don't pick it up (it's
outside `src/`, and its name doesn't end in `.test.*`).

- It defines `window.__TAURI_INTERNALS__ = { invoke, transformCallback,
  unregisterCallback, convertFileSrc, metadata }`:
  - `metadata` is `{ currentWindow: { label: "main" }, currentWebview: {
    windowLabel: "main", label: "main" } }`.
  - `transformCallback(cb, once)` stores `cb` under a new numeric id as
    `window["_" + id]` and returns the id. `unregisterCallback(id)`
    deletes it.
  - `convertFileSrc(p)` returns `p`.
  - `invoke(cmd, args)` returns a `Promise`. It pushes `{ cmd, args }`
    onto `window.__TAURI_MOCK__.calls`. When the handler throws, it
    rejects with the thrown `{ code, message }`.
- It exposes `window.__TAURI_MOCK__ = { calls: [], data, handlers }`, so a
  screenshot script can change sample data or override a handler before
  the app loads (by adding another init script after this one).
- It handles every command the app invokes at startup or when a section
  opens:

  | Commands | Returns |
  |---|---|
  | `list_entries` | `{ entries: [...], warnings: [] }` with 4 entries: an agent `fix-login-d8efb24f` (status `needsInput`, one repo `github.com/bobcob7/buff-bear` at path `buff-bear`, ref `main`); an agent `ask-buff-bear-42-3f2a9c1-0a1b2c3d` (status `idle`, one repo at the PR's sha, `refKind: "commit"`); an agent `docs-sweep-1a2b3c4d` with two repos (status `working`); and a shell workspace `scratch-91b3f0c4` |
  | `cache_repos` | `["github.com/bobcob7/buff-bear", "github.com/bobcob7/api"]` |
  | `cache_list` | `{ repos: [2 CachedRepo], warnings: [] }` |
  | `cache_refs` | `{ id, defaultBranch: "main", refs: [main, feat/rate-limit] }` |
  | `reviews_view`, `reviews_check_now`, `reviews_load_repos`, `reviews_set_watch`, `reviews_dismiss`, `reviews_restore` | a `ReviewsView` with `buff-bear` watched and one PR #42 "Add rate limiting" in `toReview` (head `3f2a9c1e0b6d4a2f9e8c7b6a5d4c3b2a1f0e9d8c`, `feat/rate-limit` → `main`, `reviews: []`) |
  | `reviews_plan` | a `ReviewPlan` for #42 |
  | `reviews_profiles` | 3 `ListedProfile`s (`efficiency` always, `go` auto `["**/*.go"]`, `security` always) |
  | `review_report` | `{ repo, ref, review: null, agent: null, profiles: [], comments: [], superseded: [], warnings: [] }` |
  | `settings_get` | `{ reviews: {defaults as in fixtures.reviewSettings}, notifications: {enabled, needsInput, done all true}, open: { fileManager: "", editor: "" } }` |
  | `settings_set_reviews`, `settings_set_notifications`, `settings_set_open` | the stored settings with the given group replaced (kept in `data.settings`) |
  | `open_defaults` | `{ fileManager: "open", editor: "code" }` |
  | `versions` | as `fakeApi().versions` |
  | `loops_list` | `{ loops: [], warnings: [] }` |
  | `read_transcript` | `{ items: [one user and one assistant item], next: 2, reset: false, dropped: 0, found: true }` |
  | `terminal_open` | `1` (sends no events) |
  | `terminal_write`, `terminal_resize`, `terminal_close`, `stop_agent`, `remove_entry`, `open_external` | `null` |
  | `workspace_open` | `{ dir, command }` computed like the backend (`editor` with one repo → repo dir) |
  | `reviews_ask` | `AskStarted` for `ask-buff-bear-42-3f2a9c1-0a1b2c3d`, `already: false` |
  | `plugin:event\|listen` | a new numeric id |
  | `plugin:event\|unlisten` | `null` |

- For any other command it calls `console.warn("tauri-mock: unhandled
  command", cmd, args)` and resolves `null`.

To use it for screenshots (in `desktop/README.md`): run `npx vite --port
1420` in `desktop/`, then in Playwright call `await
page.addInitScript({ path: "test/tauri-mock.js" })` and `await
page.goto("http://localhost:1420")`.

## Edge cases

- **Path with spaces** (`~/Library/Application Support/…`): the path is
  one argv word and is never parsed by a shell.
- **Workspace removed while the pane is open**: `open_dir_missing`.
- **Entry `pending`**: buttons disabled; the directory may not exist yet.
- **A session outside any workspace** (`workspaceId === null`): `path` is
  its `cwd` and `repos` is empty, so both buttons open the `cwd`.
- **An editor that blocks** (`code --wait`, `vim` with no terminal): it
  counts as started after 3 s, and the app never kills it.
- **Ask Claude twice**: the second click finds the first agent through
  `existing` and selects it (`already: true`). After new commits, the
  head differs, so a new agent starts. The old one stays until removed.
- **Pull request no longer open** (closed between checks):
  `pr_not_found`, shown as an error notice.
- **`gh` signed out**: the agent's `gh pr view` fails. It says so in the
  conversation, and you can run `gh auth login` and tell it to retry.
- **Claude Code not trusting the workspace root**: `ws claude` fails with
  `claude_untrusted`, shown as an error notice, as New agent does.
