# 0051. The desktop app opens a workspace in the file manager or an editor, and starts a read-only agent to ask about a pull request

- Status: Proposed
- Date: 2026-11-06

## Context

### Problem

Someone reading a workspace in the desktop app often wants to see its
files in VS Code or in Finder. They also want to ask questions about a
pull request in Reviews without starting a review. They don't want to copy
paths into a terminal for either. How does the app launch an external
program for a workspace, and how does it start a pull request Q&A agent,
using the machinery it already has?

### What a solution needs

1. One click from the workspace's pane, using the program the user chose,
   with a default that works on a fresh install (VS Code's `code`).
2. It works when the app is opened from Finder, where the PATH is
   launchd's minimal one.
3. Failures, such as a missing program or a missing directory, show in the
   app. They never pass silently.
4. The Q&A agent reads the pull request and the code around it. It can't
   change code or post to GitHub, and it waits for questions.
5. It reuses `ws claude`, the attach terminal and the agent list, and
   changes the `ws` CLI only if it has to.
6. Tests never launch Finder, VS Code or `claude`.

### Relevant facts

- An app opened from Finder or the Dock gets launchd's PATH. `env.rs`
  already asks the login shell for its PATH and adds the usual install
  directories. Every `ws` and `claude` run uses that PATH[^env].
- VS Code puts `code` on the PATH through a command-palette action. On
  macOS the label is "Shell Command: Install 'code' command in PATH", and
  afterwards you "Run `code .` in any folder to start editing files in
  that folder"[^vscode]. Some installs don't have `code`.
- A workspace with exactly one repo starts its agent in that repo's
  directory ([0028](0028-single-repo-session-directory.md)). `ws list`
  gives each repo's `path` relative to the workspace[^model].
- `ws claude` passes everything after `--` to `claude` unchanged, except
  `--settings`[^wsclaude].
- Claude Code's `--allowedTools` takes "Tools that execute without
  prompting for permission". `--disallowedTools` takes deny rules, where
  "A bare tool name removes the matching tools from Claude's context:
  `"Edit"` removes Edit". `--permission-mode` accepts `default`[^cli].
  Both tool flags take several values. In the CLI reference examples
  they come after any prompt, so a prompt placed after them could be read
  as a tool.
- The review checker keeps every open pull request it checked, with its
  head commit, branches, title and URL (`Checker::pull_request`). The
  cache fetches `refs/pull/*`, so `<repo>@<head sha>` resolves even for a
  fork's pull request[^pullrefs].
- The app already opens a path with the opener plugin (`open_external`).
  That always uses the system default handler, and it can't open a
  directory in an editor.

## Decision

**Open in file manager and Open in editor.** Each workspace's pane gets
two buttons. They run a command from the new Settings group
**Workspaces** in `desktop.json` (`settings.open.fileManager` and
`settings.open.editor`). An empty value means the platform default: the
file manager is `open` on macOS, `explorer` on Windows and `xdg-open`
elsewhere, and the editor is `code` everywhere.

- The command is split into words like a shell would split it, with
  single quotes, double quotes and backslash escapes, but no variables,
  globs or pipes. So `open -a "Visual Studio Code"` works. If a word
  contains `{path}`, the directory replaces it; otherwise the directory
  is appended as the last argument. The program runs directly (no shell)
  with the app's resolved PATH.
- The editor opens the repo's directory when the workspace has exactly
  one repo, and the workspace directory otherwise. The file manager
  always opens the workspace directory.
- The app waits up to 3 s. If the program exits non-zero in that time,
  its stderr becomes the error, except for `explorer`, which exits 1 even
  when it works. A program still running after 3 s is left to run, and a
  background thread reaps it. A missing program, a missing directory and
  a bad command each have their own error code, which the pane shows as
  an error notice.
- Launching goes through a `Launch` trait. Tests use a fake, or `sh`
  scripts in a temporary directory.

**Ask about a pull request.** The pull request's header in Reviews gets
an **Ask Claude** button. It runs `ws claude` desktop-side, with no CLI
change:

```
ws claude --json --name ask-<repo>-<n>-<sha7> <repo>@<head sha> -- \
  "<prompt>" [--model <Reviews model>] --permission-mode default \
  --allowedTools <read-only tools…> --disallowedTools <writes and GitHub posts…>
```

- The prompt (exact text in the design) tells the agent to read the pull
  request with `gh pr view --comments` and `gh pr diff`, then read the
  changed files and the code around them. It replies with a short
  summary and waits for questions.
- Edits are impossible: `Edit`, `Write` and `NotebookEdit` are removed
  from its tools. So are commits, pushes and any `gh` command that posts
  (`gh pr comment`, `gh pr review`, `gh api`, and others). Reading the PR
  and git history is pre-approved, so priming doesn't stop for
  permission. Anything else asks, as in `default` mode.
- If an `ask-…` agent for the same pull request and head already exists,
  the app opens it instead of starting another.
- The app then refreshes the agents, switches to Agents and selects the
  new workspace, the same way New agent does. The agent is a normal
  top-level, visible workspace that the user removes when done.

### Why this option

- **One click and a sensible default (1):** the default is `code`. A
  command, rather than a fixed list of editors, covers `cursor`, `zed`,
  `idea`, `open -a …` and `subl` with no code per editor.
- **Finder launch (2):** the command uses the same resolved PATH as `ws`
  and `claude`, which includes `/usr/local/bin`, where VS Code installs
  `code`.
- **Visible errors (3):** the program runs directly with a short wait, so
  a failure to start or an early exit is reported. The opener plugin
  would hide both.
- **Read-only agent (4):** deny rules can't be changed from inside the
  session. Even if the agent is told to edit, it can't, and it can't post
  to GitHub. It can still run other shell commands after asking, such as
  tests, which is useful when explaining code. A command it runs could
  still write to the working tree. That is the remaining gap, and the
  prompt forbids it.
- **Reuse (5):** the agent is created by `ws claude` and attached through
  the existing terminal, and it shows in the agent list. Nothing changes
  in `ws`.
- **Weak spots:** split-by-shell-rules isn't a shell, so `~` and `$HOME`
  aren't expanded; the help text says to use full paths. Whether Claude
  Code honours `--allowedTools` in a `--bg` session started by `ws`
  hasn't been tested here. If it doesn't, priming waits for approval and
  the agent shows as Needs input, which is safe.

## Alternatives

### The opener plugin with a configured app name

`tauri-plugin-opener`'s `open_path(path, Some(app))`.

**Pros**

- Already a dependency. No process handling.

**Cons**

- It takes an app name, not a command, so `code -n` and `{path}` can't
  be used. Its errors are opaque, and it behaves differently on each
  platform.

### Spawn through the login shell (`$SHELL -lc "<command> <path>"`)

**Pros**

- Full shell syntax, and `~` and variables expand.

**Cons**

- The path has to be quoted for the shell (it contains spaces:
  `Application Support`). It starts a login shell on every click, about
  0.1 to 1 s with nvm and similar setups. It runs whatever the setting
  contains as shell code.

### A `ws review ask` subcommand

`ws` starts the Q&A agent and owns the prompt.

**Pros**

- Agents and scripts could use it too.

**Cons**

- It adds a CLI command, docs and a record for something only the
  desktop needs today. `ws claude` already does all of it. It can be
  added later if agents need it.

### Plan mode for the Q&A agent

`--permission-mode plan`.

**Pros**

- One flag, and no edits.

**Cons**

- Plan mode steers the agent toward writing a plan and asking to leave
  plan mode, which is the wrong shape for a Q&A session. It can be left
  from inside the session, while deny rules can't.

## Consequences

- Settings gains a Workspaces group, and `desktop.json` gains
  `settings.open`. Files without it load with the defaults; the version
  stays 1.
- The path link under a workspace's title now opens through the file
  manager setting too, so both ways of opening behave the same.
- `ask-` joins `review-` and `fix-` as a name prefix the app chooses.
  It's matched only to find an existing agent for the same pull request
  and head.
- **Verified by:** unit tests for command splitting, `{path}`, target
  directories, the launcher (with `sh` scripts), the ask arguments and
  prompt; component tests for the buttons and the Settings group.
- **Unverified:** that `--allowedTools` pre-approves `gh pr view` in a
  `claude --bg` session, and launching with `explorer` and `xdg-open`.
  These are manual test-plan items.

## References

[^env]: `desktop/src-tauri/src/env.rs`, `resolve_path`: "Returns the user's login-shell PATH, followed by this process's PATH and the usual install locations of `ws` and `claude`". Read 2026-11-06 at commit 599c8ba.
[^vscode]: Microsoft, "Visual Studio Code on macOS." <https://code.visualstudio.com/docs/setup/mac> (accessed 2026-11-06). "Shell Command: Install 'code' command in PATH"; "Run `code .` in any folder to start editing files in that folder."
[^model]: `desktop/src-tauri/src/model.rs` test fixture: `"repos":[{"id":"github.com/o/r","path":"r",…}]`. Read 2026-11-06 at commit 599c8ba.
[^wsclaude]: [`docs/tools/ws-claude.md`](../tools/ws-claude.md): "Everything after `--` goes to `claude` unchanged". Read 2026-11-06.
[^cli]: Anthropic, "CLI reference." <https://code.claude.com/docs/en/cli-reference> (accessed 2026-11-06). `--allowedTools`: "Tools that execute without prompting for permission, apart from reads from network paths." `--disallowedTools`: "A bare tool name removes the matching tools from Claude's context: `"Edit"` removes Edit". `--permission-mode`: "Accepts `default`, `acceptEdits`, `plan`, `auto`, `dontAsk`, `bypassPermissions`, or `manual` as an alias for `default`."
[^pullrefs]: `internal/cache/ref_test.go` and `internal/cache/search_test.go` resolve commits on `refs/pull/1/head` from the cache. Read 2026-11-06 at commit 599c8ba.
