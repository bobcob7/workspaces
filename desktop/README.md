# Workspaces desktop

A window onto your `ws` workspaces and their Claude Code agents, like
`claude agents` with workspaces in it ([ADR 0029](../docs/adrs/0029-desktop-app.md)).

- **See every agent**: the ones that need input in a group at the top,
  then every root workspace in a tree, with what each started nested
  beneath it, most recent first. A search box, a status filter (needs
  input, working, done, stopped, shells, other) and a Show hidden switch
  narrow the tree; a match keeps the workspaces above it in view, dimmed
  if they don't match themselves ([ADR 0058](../docs/adrs/0058-workspace-tree-and-filters.md),
  [tasks](../docs/design/tree-list/tasks.md)). Each entry shows what it's
  doing now, its task, its result, and links to the PRs it opened. The
  window title counts agents waiting for you.
- **Read the conversation**: an agent's messages, replies, and tool calls
  as a page that scrolls like any other, followed live from its transcript
  ([ADR 0030](../docs/adrs/0030-desktop-conversation-from-transcript.md)).
  Tool calls open to show their input and result. A stopped agent's
  conversation can be read without resuming it.
- **Talk to an agent**: selecting a running agent attaches to it
  (`claude attach`) behind the conversation; the Terminal toggle shows it,
  so permission prompts, slash commands, and everything else work. The
  message box below types into the same session. Detach leaves it running;
  Resume restarts a stopped one.
- **Start work**: New agent runs `ws claude` with repo specs (suggested
  from the cache), a task, a model, and a permission mode. New shell opens
  `ws shell` in a terminal; that workspace is removed when the shell exits
  or the app quits.
- **Manage**: Rename (`ws rename`: changes the name shown, never the
  workspace's directory), Stop (`claude stop`), Remove (`ws rm`, after
  confirming), and Restore stopped (`ws restore`). A workspace's name is
  the one it was created with until renamed; its id, and directory, end
  with a random suffix ([ADR 0036](../docs/adrs/0036-workspace-id-suffix-and-rename.md)).
- **Environments**: Settings › Environments enrolls other machines (a Linux
  server, say) that run `ws env agent run`; New agent and New shell have an
  Environment picker, and the Agents list shows each remote workspace with
  its environment's badge, a filter chip per environment and a dimmed
  offline state. A remote workspace's Terminal is its agent's terminal over
  the daemon, and Stop, Rename, Hide and Remove run through `ws stop` and
  `--env` ([ADR 0072](../docs/adrs/0072-remote-environments.md), see
  [Environments](#environments)).
- **Open a workspace in your tools**: Open folder shows the workspace in
  Finder (Explorer on Windows, the default file manager on Linux), and
  Open in editor opens it in VS Code (`code`): the repo when the workspace
  has exactly one, otherwise the workspace folder. Both commands are set
  in Settings, Workspaces. A command like `open -a "Visual Studio Code"`
  or `zed` works, and `{path}` puts the folder somewhere other than the
  end. If a command fails, the app shows why
  ([ADR 0051](../docs/adrs/0051-desktop-open-workspace-and-ask-about-pr.md),
  [design](../docs/design/ws-open/README.md)).
- **Hidden workspaces**: reviews, agents started with `ws claude
  --hidden`, and anything you Hide (`ws hide`) stay out of the Agents
  list and don't count as waiting for you. Turn on Show hidden above the
  list to see them in the tree; Unhide (`ws unhide`) puts one back
  ([ADR 0043](../docs/adrs/0043-hidden-workspaces.md),
  [design](../docs/design/hidden-workspaces/README.md)).
- **Workspace trees**: what an agent starts from its workspace, helpers and
  reviews, is its child, listed beneath it in the Agents list: expand or
  collapse each one, and selecting a child opens the way to it. In Needs
  input a child's row says `↳ <parent>`; a parent's pane lists the
  workspaces it started, with Open and Remove. Removing a parent
  lists everything that goes with it, and can keep its children instead
  (`ws rm --keep-children`)
  ([ADR 0044](../docs/adrs/0044-workspace-trees.md),
  [design](../docs/design/workspace-trees/README.md)).
- **Menu-bar tray and notifications**: the menu bar shows how many of
  your top-level agents need input, and its menu lists them, and the
  finished ones, to open with a click. A native notification says when
  one starts needing input or finishes. Children and hidden workspaces
  never count or notify. Closing the window leaves the app in the tray;
  ⌘Q or Quit ends it. Settings, Notifications turns them off
  ([ADR 0050](../docs/adrs/0050-desktop-tray-notifications.md),
  [design](../docs/design/tray/README.md)).
- **Finished reviews clean up after themselves**: once every reviewer has
  reported, `ws` removes the review agent and its workspace. The app reads
  `ws review list`, so the pull request stays in Reviewed with its report,
  marked "agent removed after finishing", and isn't auto-reviewed again
  ([ADR 0045](../docs/adrs/0045-finished-reviews-are-removed.md)).
- **Repos**: the cached repos, grouped by host and owner, with each one's
  branches and tags, the last commit on each, and the workspaces on it
  (`ws cache list`, `ws cache refs`). Fetch one or all (`ws cache update`),
  add one with a live preview of what the spec resolves to (`ws cache
  resolve`, `ws cache add`), remove one after confirming (`ws cache
  remove`), and start an agent or shell on any repo, branch, or tag.
- **Check versions** (Settings, About): the app's version and the commit
  it was built from, and the `ws` and `claude` it runs, with their versions
  and where they are on its PATH. A warning says when the app and `ws`
  were built from different commits, so one needs rebuilding.
- **Forge credentials** (Settings, Forges, "What access do the credentials need?"): GitHub uses `gh`'s login (classic `repo`, or fine-grained Metadata, Contents, Pull requests, Commit statuses and Checks, all read). A Forgejo or Gitea token needs `read:repository` and `read:user`, and is read from `WS_FORGEJO_TOKEN_<HOST>` or saved in the app. The same text is shown in the app.
- **Repos settings** (Settings, Repos): the default owners in `config.toml`
  (`ws config`), the hosts it lists, and a box that shows what a spec
  resolves to with those settings (`ws cache resolve`, `ws cache search`).
- **Forges settings** (Settings, Forges): the ordered host list in
  `config.toml` (`[[hosts]]`), with each host's forge kind, token and
  account; add, remove, reorder and re-kind hosts there.
- **Agent roles** (Settings, Agent roles): the built-in and custom roles
  that `ws machine spawn --role` starts, edited or removed in the app
  (`ws machine roles`).
- **About › Locations** (Settings, About): where the `ws` config file,
  the app's settings file and the `WS_CONFIG_DIR`, `CLAUDE_CONFIG_DIR` and
  `PATH` the app runs with are, each with a Copy button.
- **Review pull requests** (the Reviews section): watch cached GitHub repos
  and the app checks them for open pull requests with `gh`. Review starts a
  `ws claude` agent on the PR's head commit
  ([ADR 0032](../docs/adrs/0032-review-agent-per-pr-head.md)); its result
  shows under the pull request, which moves from To review to Reviewing and
  Reviewed, and is flagged when new commits arrive. Auto-review starts
  reviews by itself, within the rules and limit in Settings. With
  [environments](../docs/tools/ws-env.md), the Start review dialog has an
  Environment field and a watched repo names the one auto-review uses; a run
  on a remote shows its environment, and is dimmed and read-only while that
  environment is offline. The review runs on that machine with its own `gh`
  sign-in ([ADR 0080](../docs/adrs/0080-reviews-and-loops-on-environments.md)).
  ![Start review with an Environment field](docs/reviews-start-environment.png)
  ![Reviews with remote runs](docs/reviews-remote-runs.png)
  ![A remote review's findings, one truncated](docs/reviews-remote-report.png)
- **Loops, per environment** (the Loops section): the list is grouped by
  environment, each with its online dot, and an offline group is dimmed with
  "missed runs are skipped, not made up". New loop has an Environment field.
  Saving a script or `bypassPermissions` on a remote opens a dialog that shows
  the script before it runs as the agent's user there.
  ![Loops grouped by environment](docs/loops-environments.png)
  ![New loop with an Environment field](docs/loop-new-environment.png)
  ![Confirming a script on a remote](docs/loop-confirm-script.png)
- **Ask about a pull request**: Ask Claude, on any pull request in
  Reviews, starts an agent in a new workspace at the PR's head commit
  (`ask-<repo>-<n>-<sha7>`). It reads the PR's description, discussion
  and diff with `gh`, and the code around the changes. Then it replies
  with a summary and waits for your questions. It can't edit files,
  commit, push, or post to GitHub. Asking again about the same commit
  opens the same agent
  ([ADR 0051](../docs/adrs/0051-desktop-open-workspace-and-ask-about-pr.md)).
- **Stats**: a bar with this machine's CPU, memory and disk and the Claude
  Code usage in the window, and a Stats section with the same, plus usage
  by model, tokens by kind, lines written by language, and productivity by
  model and language, read from the transcripts and estimated at API rates
  ([ADR 0059](../docs/adrs/0059-stats.md)).

The sections are chosen from the rail on the left, or with the shortcuts shown (⌘1–⌘7 for the seven sections), and are
designed in
[docs/design/desktop-sections](../docs/design/desktop-sections/README.md)
([ADR 0031](../docs/adrs/0031-desktop-sections-rail.md),
[ADR 0032](../docs/adrs/0032-review-agent-per-pr-head.md),
[ADR 0033](../docs/adrs/0033-ws-cache-refs.md)).

Everything goes through the `ws`, `claude`, and `gh` CLIs. The app's only
state is Reviews': watched repos, dismissals, auto-review records, and
settings, in `desktop.json` in its config directory
(`~/Library/Application Support/com.github.bobcob7.workspaces/`,
[ADR 0034](../docs/adrs/0034-desktop-state-file.md)). Checking runs in the
backend every 5 minutes by default
([ADR 0035](../docs/adrs/0035-desktop-review-checker.md)).

![An agent waiting on a permission prompt, attached in the app](docs/needs-input.png)

| A finished agent, with its result and PRs | Starting an agent |
| --- | --- |
| ![](docs/done.png) | ![](docs/new-agent.png) |

![A cached repo's branches, with the workspaces on each](docs/repos.png)

| Adding a repo | Removing a repo |
| --- | --- |
| ![](docs/repos-add.png) | ![](docs/repos-remove.png) |

![A pull request waiting for review](docs/reviews.png)

| Merge status badges and filters | Filtered by merge status |
| --- | --- |
| ![](docs/reviews-filters.png) | ![](docs/reviews-filters-merge.png) |
| **Settings, Forges (GitHub and Forgejo hosts)** | **Phone width** |
| ![](docs/reviews-forges-settings.png) | ![](docs/mobile-reviews-filters.png) |
| **Adding a host in Forges** | **Forges at phone width** |
| ![](docs/settings-forges-add.png) | ![](docs/mobile-settings-forges.png) |

| A review out of date after new commits | Editing a review before starting it |
| --- | --- |
| ![](docs/reviews-reviewed.png) | ![](docs/reviews-start.png) |
| **Choosing watched repos** | **Settings, Reviews group** |
| ![](docs/reviews-watched.png) | ![](docs/settings.png) |
| **A review agent in Agents** | **Nothing watched yet, gh signed out** |
| ![](docs/review-agent.png) | ![](docs/reviews-empty.png) |

| Open folder and Open in editor on a workspace | Settings, Workspaces group |
| --- | --- |
| ![](docs/workspace-open.png) | ![](docs/settings-workspaces.png) |

| The Open... menu, closed | The Open... menu, expanded |
| --- | --- |
| ![](docs/open-menu-closed.png) | ![](docs/open-menu-expanded.png) |

| The Open actions in More actions on a phone |
| --- |
| ![](docs/open-menu-phone.png) |
| **Ask Claude on a pull request** | **The Q&A agent it starts** |
| ![](docs/reviews-ask.png) | ![](docs/reviews-ask-started.png) |
| **A repo's Pull request reviews card, in Repos** | **Renaming a workspace** |
| ![](docs/repos-reviews.png) | ![](docs/rename.png) |

![A loop's detail view: Schedule, Prompt, Repos, Agent, Gate script and Variables cards](docs/loops.png)

| Settings, Repos, with a cached repo resolved | Settings, Repos: the ambiguous candidates |
| --- | --- |
| ![](docs/settings-repos.png) | ![](docs/settings-repos-ambiguous.png) |
| **Settings, Reviews, with the move banner** | **Settings, Agent roles** |
| ![](docs/settings-reviews-config.png) | ![](docs/settings-roles.png) |
| **Settings, editing a role** | **Settings, About › Locations** |
| ![](docs/settings-role-edit.png) | ![](docs/settings-about-locations.png) |
| **Repos on a phone** | **Agent roles on a phone** |
| ![](docs/online-settings-repos-mobile.png) | ![](docs/online-settings-roles-mobile.png) |
| **About on a phone** | |
| ![](docs/online-settings-about-mobile.png) | |

A loop's name can be any text, such as "Nightly deps bump"; its ID (`nightly-deps-bump`) is derived from it when the loop is added and names its workspaces. A loop is edited with **Edit** in its header, in the same form that adds it.
The prompt and the gate script show in scrollable monospace blocks.

| **Editing a loop** | **Choosing the period** |
| --- | --- |
| ![](docs/loops-edit.png) | ![](docs/loops-edit-period.png) |
| **The gate script and variables, in the form** | **Adding a loop: the ID comes from its name** |
| ![](docs/loops-edit-script.png) | ![](docs/loops-new.png) |
| **Repos suggested from the cache** | |
| ![](docs/loops-new-repos.png) | |

| **A loop's gate script, last check and variables** | **A paused loop with no gate script** |
| --- | --- |
| ![](docs/loops-script.png) | ![](docs/loops-paused.png) |

The loop view also has a **History** card, with the loop's recent runs:
each shows its status, duration and verdict, and opens the run's report.
The **Memory** card lists the keys the loop's runs share, and can add,
edit, delete and clear them. On a phone, the memory table stacks into
rows, and its buttons are at least 40 px tall.

| **Loop history** | **A run's report** |
| --- | --- |
| ![](docs/loops-history.png) | ![](docs/loops-history-run.png) |
| **Loop memory** | **Editing a memory value** |
| ![](docs/loops-memory.png) | ![](docs/loops-memory-edit.png) |
| **History on a phone** | **Memory on a phone** |
| ![](docs/mobile-loops-history.png) | ![](docs/mobile-loops-memory.png) |
| **A blocked loop run in Agents** | **Loop history with a blocked run** |
| ![](docs/agents-loop-blocked.png) | ![](docs/loops-history-blocked.png) |
| **Hiding a loop's runs, in its form** | **A blocked loop run on a phone** |
| ![](docs/loops-edit-hidden.png) | ![](docs/mobile-agents-loop-blocked.png) |

The screenshots use sample data.

## Environments

A remote environment is another machine that runs `ws env agent run` and
connects to this one ([`ws env`](../docs/tools/ws-env.md)); this app needs
the daemon running on the controller. Settings › Environments lists them with
their status, and adds, rotates and removes them:

![Settings, Environments](docs/settings-environments.png)

Add environment shows the one-time code and the two commands to run on the
machine. The code stays in the dialog and goes nowhere else; Rotate cancels an
unused one.

![Add environment](docs/settings-environments-add.png)

New agent and New shell pick the environment (it remembers the last choice);
an offline environment is disabled, and a child of a workspace runs where its
parent does:

![New agent with the Environment picker](docs/new-agent-environment.png)

New agent also sets the scope: the environments the agent may start children
on (`ws claude --env-scope`). Only you grant it, and four rules apply: the
agent's own environment is always in it and can't be unchecked; the field
starts from `environments.default_scope` in `config.toml`, leaving out names
that aren't enrolled; a widened scope is never remembered between openings;
and an offline environment can be checked, since a child can start there once
it's back online. The list is deduplicated and at most 64 names are kept, so 63
besides the agent's own environment are preselected; the dialog refuses to start
above 64, as the Rust validator does. If `default_scope` can't be read, the dialog
warns and the agent starts with its own environment only; the CLI then skips
`default_scope`, as an own-environment-only `--env-scope` does.

| Screen | Image |
| --- | --- |
| The default scope, from `default_scope`, with a name that isn't enrolled left out | ![](docs/scope-new-agent-default.png) |
| A remote environment chosen, with the scope it may start children on | ![](docs/scope-new-agent-remote.png) |
| An offline environment checked, with the note that its children start once it's back | ![](docs/scope-new-agent-offline.png) |
| An error: a scope name that was removed or never enrolled | ![](docs/scope-new-agent-error.png) |
| On a phone (360 px) | ![](docs/scope-new-agent-mobile.png) |

The Agents list marks each remote row with its environment, filters by
environment, and dims an offline environment's rows while keeping their last
status; their actions are disabled with the reason:

![Agents with environment badges](docs/agents-environments.png)

On a phone, the same list through Workspace Online:

![Agents with environment badges on a phone](docs/agents-environments-mobile.png)

A remote agent's pane opens on its Terminal, with a note: the conversation
view reads local transcripts, so a remote agent's conversation isn't shown
yet. Terminals are one connection per environment, don't survive a reconnect,
and a terminal closed by an offline environment says so:

![A remote terminal](docs/remote-terminal.png)

Open folder and Open in editor are disabled on a remote workspace (the files
are on the other machine).

Not yet: a remote agent's conversation view, stopping a `ws` runner agent on a
remote, and Windows. Reviews and loops run on remote environments; see
[`ws review`](../docs/tools/ws-review.md) and [`ws loop`](../docs/tools/ws-loop.md).

### Environment scopes

A remote child of `ws machine spawn --env` sits under its parent and says where
it runs. An agent's pane shows its scope, with each environment's state.

| Screen | Desktop (1100 px) | Phone (360 px) |
| --- | --- | --- |
| `plan-scope` expanded, with its children on other environments | ![Agents tree](docs/scope-tree.png) | ![Agents tree on a phone](docs/scope-tree-mobile.png) |
| `plan-scope`'s pane: the Scope line and the children card | ![plan-scope pane](docs/scope-pane.png) | ![plan-scope pane on a phone](docs/scope-pane-mobile.png) |
| A child's pane, naming the parent it was started from | ![Child pane](docs/scope-child-pane.png) | ![Child pane on a phone](docs/scope-child-pane-mobile.png) |
| The Remove dialog with its note | ![Remove dialog](docs/scope-remove.png) | ![Remove dialog on a phone](docs/scope-remove-mobile.png) |

| Screen | Desktop (1100 px) | Phone (360 px) |
| --- | --- | --- |
| `plan-scope`'s root bar: the merged rollup, with the latest note | ![Rollup on the root bar](docs/rollup-plan-scope.png) | ![Rollup on the root bar on a phone](docs/rollup-plan-scope-mobile.png) |

Not shown: a refusal for an environment out of scope; it goes to the agent that
ran `ws machine spawn --env`.

## Artifacts

The Artifacts section ([ADR 0078](../docs/adrs/0078-artifacts.md), [ADR 0079](../docs/adrs/0079-artifact-comments-and-anchors.md)) holds files that people and agents upload: screenshots as evidence for a bug, or a Markdown design pitch. Upload with the button, drag and drop, or paste. Attach an artifact to a new agent or a reply; the prompt gets an `@artifact:<id>` reference. Select text in a Markdown artifact to comment on it; the comment stays attached to that passage across revisions.

| **List and upload** | **Image viewer** |
| --- | --- |
| ![](docs/artifacts-list.png) | ![](docs/artifacts-image.png) |
| **Markdown with comment threads** | **Detached and resolved threads** |
| ![](docs/artifacts-markdown-threads.png) | ![](docs/artifacts-markdown-threads-resolved.png) |
| **Attaching to a new agent** | **A Markdown artifact on a phone** |
| ![](docs/artifacts-new-agent.png) | ![](docs/artifacts-phone.png) |

Artifacts and conversations link both ways ([ADR 0084](../docs/adrs/0084-bidirectional-artifact-links.md)). In an agent's conversation, each `@artifact:` reference outside code opens the artifact in the app at the revision it names, the hint line `ws` appends becomes a row of artifacts, and a strip at the top lists every artifact the conversation created, read, discussed or was handed. An artifact's page lists, under "Conversations", the agents that worked with it; a row opens that agent's conversation. Every 30 s the app scans local agents' transcripts for references in your messages and their replies, and records them with `ws artifact scan record`, once per message.

| **Links and the strip in a conversation** | **Conversations on an artifact's page** |
| --- | --- |
| ![](docs/artifact-links-conversation.png) | ![](docs/artifact-links-artifact-page.png) |
| **The strip on a phone** | **Conversations on a phone** |
| ![](docs/artifact-links-conversation-phone.png) | ![](docs/artifact-links-artifact-page-phone.png) |

The screenshots use sample data.

## Workspace Online

The same backend can run on a server and serve the frontend to a browser
behind a username and password (`ws-online`, feature `online`). See
[the operator guide](../docs/online.md).

## Requirements

- `ws` and Claude Code (2.1.302 or newer) on the PATH your login shell
  sets; the Repos section needs a `ws` with `ws cache refs`. The app asks your login shell for its PATH, then adds
  `~/.local/bin`, `~/go/bin`, `/opt/homebrew/bin`, and `/usr/local/bin`.
- For Reviews, the GitHub CLI (`gh`) signed in (`gh auth login`); the
  Reviews section says what to run if it isn't.
- The workspace root trusted in Claude Code once (see
  [`ws claude`](../docs/tools/ws-claude.md#trust)); the New agent dialog
  says how if it isn't.
- To build: Rust 1.90+, Node.js 22.12+ (the tests need it), and Tauri's
  [system dependencies](https://tauri.app/start/prerequisites/).

## Build and run

The app hosts the `ws` daemon (`ws daemon run --origin app`) while it runs.
Quitting leaves runners running under it until they finish.

```console
$ make desktop        # from the repo root: bundle in desktop/src-tauri/target/release/bundle/
$ make desktop-dev    # run with hot reload
$ make desktop-test   # typecheck, vitest, cargo test, clippy
```

`cargo test -- --ignored` in `src-tauri` also runs checks against your
real setup: `list_live` prints the listing, `attach_live` attaches to
the session in `WS_DESKTOP_ATTACH` for 4 s and detaches,
`conversation_live` prints that session's conversation, `cache_live`
prints the cache and the first repo's refs (it needs a `ws` with
`ws cache refs`), and `check_live` checks the cached repo in
`WS_DESKTOP_WATCH` with `gh` and prints its pull requests (without
auto-review).

## Layout

```
src-tauri/src/
  commands.rs   # the commands the frontend invokes
  cache.rs      # the Repos section's commands: ws cache list, refs, resolve, update, add, remove
  model.rs      # merging ws list, claude agents, and job state into entries and statuses
  jobs.rs       # Claude Code's jobs/<id>/state.json, read best-effort
  transcript.rs # a session's transcript, read into conversation items
  pty.rs        # claude attach / ws shell in a pseudo-terminal
  runner.rs     # running ws and claude, and reading ws's JSON errors
  env.rs        # the login shell's PATH
  open.rs       # Open folder and Open in editor: the commands from Settings, launched on that PATH
  reviews/      # Reviews: desktop.json, gh, review names, the checker, auto-review, Ask Claude (ask.rs)
  loops.rs      # the Loops section's commands: ws loop
  roles.rs      # Settings' roles: ws machine roles
  ws_config.rs  # Settings: ws config
  about.rs      # Settings' About group
  stats/        # the stats bar and Stats section: transcripts read incrementally, host usage (ADR 0059)
  tray/         # the menu-bar tray and its notifications (ADR 0050)
  online/       # Workspace Online (feature `online`): config, auth, push, dispatch, wire, and the server (ADR 0053)
src/
  api.ts                    # typed invoke wrappers
  features/rail/            # the section rail and ⌘1–⌘7
  features/agents/          # list, statuses, entry pane, agents slice, top-bar actions
  features/repos/           # repo list, detail with branches and tags, add and remove dialogs, repos slice
  features/conversation/    # conversation view, tool call pairing, conversations slice
  features/terminals/       # xterm.js registry, terminal view, message box, terminals slice
  features/create/          # new agent and new shell dialogs
  features/reviews/         # pull request list and detail, dialogs, the Repos slots, reviews slice
  features/loops/           # the Loops section: list, detail, history, memory, add and edit forms
  features/stats/           # the stats bar and Stats section
  features/settings/        # settings groups
  features/ui/              # section, agent selection, dialogs, notices
```

## Screenshots without the backend

`test/tauri-mock.js` stands in for the Rust backend in a browser, with
sample data for every command the app calls. It logs `tauri-mock:
unhandled command` for anything it doesn't know. Run `npx vite --port
1420` here, then load the mock before the app in Playwright:

```js
await page.addInitScript({ path: "test/tauri-mock.js" });
await page.goto("http://localhost:1420");
```

`window.__TAURI_MOCK__` holds the calls made, the sample data and the
handlers, which a later init script can change.

The sample listing is a tree for the Agents list ([ADR 0058](../docs/adrs/0058-workspace-tree-and-filters.md)):
`fix-login` has two generations of children, one of which needs input,
and a hidden review child; `lint-sweep` is hidden and has a visible
child, so it shows as a dimmed context row until Show hidden is on.
Three loop runs sit beside them: a blocked and a needs-attention run in
Needs input (shown with a Blocked or Needs attention prefix), and one done
run that is hidden.

The sample `environments.default_scope` names `linux-server` and `old-rig` (not enrolled), and the pending `lab-pi` is never offered; an agent named `fail-scope` fails with `environment_unknown`.

The sample `plan-scope` has an environment scope (local, linux-server,
win-rig offline, old-rig removed) and three children on other environments
from `ws machine spawn --env`: `ip-linux` on linux-server (online, done),
`ip-win` on win-rig (offline), and `ip-probe` on win-rig, started by
`ip-linux`. Its root bar shows the merged rollup, `4/8 steps`; the note
`Waiting for win-rig` is `ip-win`'s, the most recently updated step record
below `plan-scope`.

For web mode (Workspace Online), load `test/web-mock.js` after the Tauri
mock. It serves `/api` and a WebSocket from the same handlers, and
`window.__WEB_MOCK__.authenticated` decides whether the login page shows:

```js
await page.addInitScript({ path: "test/tauri-mock.js" });
await page.addInitScript({ path: "test/web-mock.js" });
await page.addInitScript(() => { window.__WEB_MOCK__.authenticated = false; });
await page.goto("http://localhost:1420");
```

Login accepts `me` / `secret`. `window.__WEB_MOCK__.push` sets the push
state of This browser in Settings › Notifications: `"subscribed"` (the
default), `"granted"`, `"default"`, `"denied"` or `"unsupported"`.

## Agents list tree

The Agents list as a tree ([ADR 0058](../docs/adrs/0058-workspace-tree-and-filters.md)), from the mock data:

- [Tree at 1100px](docs/tree-list.png): Needs input on top (five entries, including the blocked and needs-attention loop runs), `fix-login` expanded to show its children.
- [Search](docs/tree-list-search.png): `lint` typed; the hidden `lint-sweep` stays as a dimmed context row, and the blocked loop run matches on `eslint`.
- [Show hidden](docs/tree-list-hidden.png): the three hidden entries in the tree.
- [Status filter](docs/tree-list-status.png): Working only.
- [At 360px](docs/tree-list-mobile.png): the filter bar wrapped, search on its own row.

## Progress bars

A workspace that reports steps with `ws machine progress` (ADR 0077) shows a thin bar with `done/total` beside it: in its row, on each child in the Started from this workspace card, and, for a top-level workspace, one overall bar under the header with the latest step note. The bar counts the workspace's own steps plus its descendants', as `ws list --json` gives them. A bar whose last verdict failed (`FAIL` or `CHANGES_REQUESTED`) is drawn in the error colour. Rows without a rollup show no bar.

| **Agents list** | **Orchestrator pane** |
| --- | --- |
| ![](docs/progress-list.png) | ![](docs/progress-pane.png) |

| **Started from this workspace** | **Pane at 360px** |
| --- | --- |
| ![](docs/progress-children.png) | ![](docs/progress-phone.png) |

## Status

| Shown        | When                                                            |
| ------------ | --------------------------------------------------------------- |
| Needs input  | idle, and Claude Code's job state is `blocked`                  |
| Waiting      | idle, with no clearer job state                                 |
| Working      | in the middle of a turn                                         |
| Done         | idle, and the job state is `done`                               |
| Starting     | no process yet, started under 10 s ago                          |
| Stopped      | no process; Resume or Restore stopped brings it back            |

Working, starting, and stopped come from `ws list`. The job state is
undocumented, so if Claude Code changes it, idle agents show as Waiting.

| **Settings, Notifications group** | **Notifications turned off** |
| --- | --- |
| ![](docs/settings-notifications.png) | ![](docs/settings-notifications-off.png) |
