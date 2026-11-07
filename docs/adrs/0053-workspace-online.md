# 0053. Workspace Online: the desktop app's backend runs headless as `ws-online` in a Docker image, serving the same frontend as an installable web app behind a login, with Web Push for the tray's notifications

- Status: Proposed
- Date: 2026-11-06

## Context

### Problem

The desktop app ([0029](0029-desktop-app.md)) only runs on the machine
where the agents run, and only while someone sits at it. The user wants
to run agents on a server and drive them from any browser or phone: the
same app, behind a username and password, installable as a PWA that
notifies like the tray ([0050](0050-desktop-tray-notifications.md)),
with Claude Code using their Pro/Max subscription and keeping its
settings, MCP configs and binaries across container restarts.

How do we serve the desktop app over HTTP without a second
implementation of its backend, and package it so one `docker run` gives
a working, authenticated, persistent server?

### What a solution needs

1. **One backend.** The commands the frontend calls stay one
   implementation; `ws` stays the only code that touches workspaces
   ([0029](0029-desktop-app.md) criterion 1).
2. **One frontend.** The same React app runs in Tauri and in a browser;
   only the transport and a few local-machine features differ.
3. **Only the user gets in.** Every API and WebSocket route is behind a
   login; credentials come from the environment; no cross-site request
   can act for a logged-in browser.
4. **Notifications like the tray**: same triggers, same three settings,
   also when no tab is open.
5. **Persistent state on volumes**: Claude Code's settings, MCP config,
   credentials file and self-updating binary; `ws`'s cache and
   workspaces; the server's own keys and subscriptions.
6. **Small**: few new dependencies, no new language, CI stays cheap.

### Relevant facts

- Every frontend call goes through one function, `call()` in
  `desktop/src/api.ts`, which wraps Tauri's `invoke`; the only stream is
  the `Channel<TermEvent>` passed to `terminal_open`, and the only event
  is `tray-select`[^read-api].
- 55 commands are registered in `desktop/src-tauri/src/lib.rs`. Four
  take a `tauri::AppHandle` (`list_entries`, `versions`, `open_external`,
  `review_open_report`); the rest take only `State<'_, AppState>` and
  JSON-able arguments. Outside `lib.rs`, `tauri` is used in `commands.rs`,
  `cache.rs`, `loops.rs`, `open.rs`, `pty.rs`, `reviews/mod.rs` and
  `tray/`[^read-api].
- Tauri serialises a command's arguments as a JSON object whose keys are
  the parameter names in camelCase (`workspace_id` → `workspaceId`)[^tauri-args].
- `tauri` on Linux links WebKitGTK and GTK, so a crate that depends on
  it can't be built or run in a slim container without them[^tauri-prereq].
- The tray's decision logic is pure and Tauri-free: `tray::attention`,
  `tray::view` and `tray::watcher::Watcher` (`observe`, `wanted`);
  only `tray::menu`, `tray::publish` and `watch_forever` call Tauri[^read-api].
- Web Push needs a service worker, a VAPID key pair, the browser's
  subscription (endpoint, `p256dh`, `auth`), and an `aes128gcm`-encrypted
  POST to the endpoint. Browsers allow service workers and push only in a
  secure context: HTTPS, or `http://localhost`[^mdn-push][^mdn-secure].
  iOS/iPadOS deliver Web Push only to a web app added to the Home
  Screen (16.4+)[^webkit-push].
- `web-push-native` 0.5 builds the encrypted, VAPID-signed request as an
  `http::Request` from the subscription JSON, in pure Rust; axum 0.8,
  tower-http 0.7, argon2 0.6, reqwest 0.13 and it compile together on
  Rust 1.94[^spike].
- `ws` and the desktop app find Claude Code's files from
  `CLAUDE_CONFIG_DIR`, else `$HOME/.claude` and `$HOME/.claude.json`, and
  `ws`'s own from `WS_CONFIG_DIR`[^read-paths]. `ws claude` starts
  Claude Code's background service itself (`claude daemon run`, with
  `HOME` as its directory) and passes its environment on[^read-paths].
- `claude setup-token` prints a long-lived OAuth token for a Pro or Max
  subscription, used through `CLAUDE_CODE_OAUTH_TOKEN`[^claude-token].
  The native installer puts `claude` in `~/.local/bin`, its versions in
  `~/.local/share/claude`, and updates itself there[^claude-setup].
- `debian:trixie-slim` packages `gh` 2.46.0 (2024), Node.js 20.19 and
  `tini` 0.19[^apt].

## Decision

We will add **Workspace Online**: a headless server, `ws-online`, built
from the desktop crate without Tauri, serving the desktop frontend to a
browser, packaged as a Docker image.

**Backend split.** `desktop/src-tauri` gets two cargo features:
`desktop` (default: Tauri, tray, notifications plugin, the `ws-desktop`
binary) and `online` (axum server, the `ws-online` binary). They are
mutually exclusive. A new `ipc` module re-exports `tauri::State` and
`tauri::ipc::Channel` under `desktop`, and under `online` provides
look-alike `State` and `Channel` types, so the existing command
functions compile unchanged in both builds; `#[tauri::command]` becomes
`#[cfg_attr(feature = "desktop", tauri::command)]`. The four
`AppHandle` commands and the tray's native layer are desktop-only;
`ws-online` has its own versions of the first two and of
`review_open_report`, and answers `open_external` and `workspace_open`
with `unsupported`.

**Transport.** One RPC route, `POST /api/invoke/<command>`, takes the
same JSON arguments as Tauri's `invoke` and returns the same JSON result,
or an `AppError` with status 422. One WebSocket, `/api/ws`, carries
channel messages (terminal output), server events (`notify`), and
invoke frames, which the browser uses for `terminal_write` and
`terminal_resize` so keystrokes stay in order. A channel argument is sent
as `{"__channel": n}` with the request header `X-WS-Connection` naming
the socket; terminals opened through a socket are closed when it
closes. `api.ts` picks Tauri when `window.__TAURI_INTERNALS__` exists,
else HTTP and WebSocket. The exact routes and shapes are in the
[design](../design/workspace-online/README.md).

**Auth.** `WS_ONLINE_USERNAME` plus `WS_ONLINE_PASSWORD` or
`WS_ONLINE_PASSWORD_HASH` (an argon2 PHC string; `ws-online
hash-password` makes one). Exactly one of the two is set. Comparisons
are constant-time. A login sets a random 256-bit session token in an
`HttpOnly`, `SameSite=Strict` cookie (`Secure` when the public URL is
HTTPS, or as configured), valid 30 days; the server keeps only its
SHA-256, in a file on the data volume, so restarts keep sessions and a
credentials change ends them. Failed logins are limited to 10 per client
address per 15 minutes. Every `/api` route but `/api/session` and
`/api/login`, and `/api/ws`, needs a session; every `POST` and the
WebSocket upgrade also need an `Origin` that matches the server. Static
files, the manifest, the service worker and `/healthz` are public.

**PWA and notifications.** The frontend ships a web manifest with the
desktop icons and a hand-written service worker that caches only the
app shell, never `/api`. `ws-online` runs the tray's watcher every 4 s
(`Watcher::observe`, then `wanted` with the same `notifications`
settings in its own `desktop.json`), and turns each notice into a Web
Push message to every stored subscription and a `notify` event on every
open socket. Both use the tag `<kind>:<agent>`, so a browser that gets
both shows one. VAPID keys are generated on first start and kept on the
data volume with the subscriptions; a subscription the push service
reports gone (404/410) is dropped. Clicking a notification focuses the
app and selects the agent, which the desktop can't do.

**Image.** A multi-stage `deploy/docker/Dockerfile` builds `ws` (static
Go), the frontend (`vite build`) and `ws-online` (Rust, `--features
online`), on `debian:trixie-slim` with git, `gh` (GitHub's apt repo),
openssh-client, curl, jq, ripgrep, make, Node.js/npm, procps and tini,
as user `ws` (uid 1000). Claude Code is installed with the native
installer at build time into a seed directory; the entrypoint copies it
into the home volume when `~/.local/bin/claude` is missing, so later
self-updates persist. The entrypoint checks the required variables,
marks onboarding done and trusts `$WS_CONFIG_DIR/workspaces` in
`~/.claude.json`, sets up git from `WS_GIT_NAME`, `WS_GIT_EMAIL` and
`GH_TOKEN`, and execs `ws-online` under tini. Two volumes: `/home/ws`
(the whole home: `~/.claude`, `~/.claude.json`, `~/.local`, `~/.config/gh`,
`~/.ssh`, npm globals) and `/data` (`ws` at `/data/ws`, the server at
`/data/online`).

**CI.** `ci.yml` gains a job that builds the image without pushing.
Publishing an image is out of scope.

### Why this option

1. **One backend**: the command functions are reused as they are; the
   server's dispatch table only names them. The cost is a shim module
   and `cfg_attr` on each command, and two features that can't be on
   together, so `--all-features` is unusable.
2. **One frontend**: only `api.ts`'s transport, the login page, the
   push settings row, and hiding the open-locally buttons differ.
3. **Auth** is a single user, as asked, with the usual cookie
   protections; Origin checks plus `SameSite=Strict` cover CSRF without
   tokens in the frontend. It relies on a reverse proxy for TLS.
4. **Notifications** reuse the tray's pure logic and settings, so the
   rules are the same tested ones. Web Push works with no tab open;
   it needs HTTPS (or localhost) and, on iOS, a Home Screen install.
5. **Persistence**: mounting the whole home keeps everything Claude
   Code, `gh` and npm write there without listing paths, at the cost of
   also keeping things nobody asked to keep.
6. **Small**: Rust and TypeScript already exist in the repo; new crates
   are axum, tower-http, argon2, sha2, subtle, getrandom, base64ct,
   web-push-native and reqwest, behind the `online` feature only; no new
   npm packages.

## Alternatives

### A separate crate in a cargo workspace for the shared logic

Move the domain modules out of `desktop/src-tauri` into a Tauri-free
library crate used by both binaries.

**Pros**

- No feature juggling; both binaries can be checked together.

**Cons**

- Commands sit in the domain modules (`cache.rs`, `loops.rs`, `open.rs`,
  `reviews/mod.rs`), so every module would be split into logic and
  adapter, and ~13 000 lines moved: too large to review and to hand to
  implementers in small steps. The feature split can become this later.

### Build the server with Tauri linked in

**Pros**

- No changes to existing files.

**Cons**

- Needs WebKitGTK in the image and a display-less GTK at runtime, and
  `tauri::State` can't be built outside a Tauri app anyway.

### A Go server in `ws`

**Pros**

- One language for the server and `ws`.

**Cons**

- Re-implements the desktop backend (merging, transcripts, reviews,
  loops, terminals), which criterion 1 rules out.

### Remote desktop or a VNC/noVNC image running the Tauri app

**Pros**

- No code changes.

**Cons**

- No PWA, no Web Push, poor on phones, and a much bigger image.

### Server-sent events and HTTP-only input instead of a WebSocket

**Pros**

- Simpler proxying.

**Cons**

- Keystrokes as separate POSTs can arrive out of order, and SSE is one
  way, so terminals would need two transports.

### Mount only `~/.claude`, `~/.claude.json` and `~/.local` as volumes

**Pros**

- Only what was asked for persists.

**Cons**

- `~/.claude.json` is a file, so it can't be its own named volume; `gh`
  and SSH credentials would be lost on restart.

## Consequences

- `desktop/src-tauri` builds two ways. `make desktop-test` also runs
  clippy and the tests with `--no-default-features --features online`;
  under `online`, unused desktop helpers are allowed dead code.
- Commands added later must be added to the online dispatch table, or
  the web app answers `unknown_command`; a test lists the table against
  the names in `lib.rs`.
- Closing or reloading a tab closes its terminals; a `ws shell` started
  there ends with it, as it does when the desktop app quits.
- Push needs HTTPS through a reverse proxy, except on localhost; on
  iOS it needs a Home Screen install. Without push, an open tab still
  notifies from the `notify` event.
- The auto-review checker and the loop ticker run in the server as they
  do in the desktop app, so agents and reviews keep going with no
  browser open.
- **Unverified**: Claude Code's background service and `claude attach`
  on Linux in a container, and `CLAUDE_CODE_OAUTH_TOKEN` reaching
  background sessions started by `ws claude`. The image's smoke test
  checks only `claude --version`; the first real run will tell. If the
  token doesn't reach them, the fallback is to run `claude /login` once
  in the container, which writes `~/.claude/.credentials.json` to the
  home volume.
- How we'll know the code follows this: the tasks' unit tests for auth,
  push payloads and dispatch, the image smoke test, and Playwright
  screenshots of the login page, the main view and the notification
  settings in a browser.

## References

[^read-api]: Read 2026-11-06 at commit 1443a87: `desktop/src/api.ts` (`call`, `terminalOpen`, `onTraySelect`), `desktop/src-tauri/src/lib.rs` (`generate_handler!`), and `grep -rn tauri desktop/src-tauri/src`.
[^tauri-args]: Tauri, "Calling Rust from the Frontend." <https://v2.tauri.app/develop/calling-rust/> (accessed 2026-11-06). "Arguments should be passed as a JSON object with camelCase keys."
[^tauri-prereq]: Tauri, "Prerequisites." <https://v2.tauri.app/start/prerequisites/> (accessed 2026-11-06). Linux needs `libwebkit2gtk-4.1-dev` among other system packages.
[^mdn-push]: MDN, "Push API." <https://developer.mozilla.org/en-US/docs/Web/API/Push_API> (accessed 2026-11-06). "For an app to receive push messages, it has to have an active service worker."
[^mdn-secure]: MDN, "Secure contexts." <https://developer.mozilla.org/en-US/docs/Web/Security/Secure_Contexts> (accessed 2026-11-06). "Locally-delivered resources such as those with http://127.0.0.1 URLs, http://localhost and http://*.localhost URLs … are also considered to have been delivered securely."
[^webkit-push]: WebKit, "Web Push for Web Apps on iOS and iPadOS." <https://webkit.org/blog/13878/web-push-for-web-apps-on-ios-and-ipados/> (accessed 2026-11-06). "A web app that has been added to the Home Screen can request permission to receive push notifications."
[^spike]: Measured 2026-11-06 on an Apple M4, macOS 26 (Darwin 25.6.0), rustc 1.94.1: a scratch crate depending on `axum 0.8` (`ws`), `tower-http 0.7` (`fs`), `argon2 0.6` (`getrandom`), `web-push-native 0.5`, `base64ct 1.8`, `reqwest 0.13` (`rustls`, no defaults), `getrandom 0.4` built, generated a VAPID key pair (`ES256KeyPair::generate`) and verified an argon2id hash. 292 packages in its lock file. One run.
[^read-paths]: Read 2026-11-06: `internal/claude/client.go` (`jobsDir`), `internal/claude/trust.go`, `internal/claude/daemon.go` (`RunDaemon`, `daemonDir`), `internal/claude/start.go` (`--bg`), `desktop/src-tauri/src/jobs.rs` (`jobs_dir`), `desktop/src-tauri/src/env.rs`, and README "Locations and configuration".
[^claude-token]: Anthropic, "Claude Code: Identity and Access Management" and `claude setup-token --help`. <https://docs.anthropic.com/en/docs/claude-code/iam> (accessed 2026-11-06). `setup-token`: "Set up a long-lived authentication token (requires Claude subscription)".
[^claude-setup]: Anthropic, "Set up Claude Code." <https://docs.anthropic.com/en/docs/claude-code/setup> (accessed 2026-11-06). The native install script `curl -fsSL https://claude.ai/install.sh | bash`.
[^apt]: Measured 2026-11-06: `docker run --rm debian:trixie-slim sh -c "apt-get update && apt-cache policy gh nodejs npm tini ripgrep"`: candidates gh 2.46.0-3, nodejs 20.19.2, npm 9.2.0, tini 0.19.0, ripgrep 14.1.1.
