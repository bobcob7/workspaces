# Workspace Online: design

Implements [0053](../../adrs/0053-workspace-online.md). The operator guide
is [docs/online.md](../../online.md); the implementation tasks are in
[tasks.md](tasks.md). When this file and the ADR disagree, the ADR wins.

## Architecture

```
browser (React app from desktop/src, built by vite)
  │  HTTPS (reverse proxy) or http://localhost
  ▼
ws-online  (desktop/src-tauri, --no-default-features --features online)
  ├─ static files: WS_ONLINE_WEB_DIR (index.html fallback)        public
  ├─ /healthz, /api/session, /api/login                           public
  ├─ /api/invoke/<command>  ──► online::dispatch ──► existing command fns
  ├─ /api/ws  (channels, events, invoke frames)
  ├─ /api/push/*, /api/reports/<file>
  ├─ background: reviews::checker, loops::tick_forever, online notifier
  └─ runs ws and claude (Runner) with PATH = env::fallback_path()
        │
        ▼
   ws  ──► claude --bg / claude daemon run / claude attach (pty)
```

### Crate layout (`desktop/src-tauri`)

| Path | Feature | What |
| ---- | ------- | ---- |
| `src/ipc.rs` | both | `State` and `Channel`: Tauri's under `desktop`, look-alikes under `online` |
| `src/lib.rs` | both | modules; `run()` and `tray::menu` only under `desktop`; `pub mod online` under `online` |
| `src/main.rs` | `desktop` | `ws-desktop` binary (unchanged) |
| `src/bin/ws-online.rs` | `online` | `ws-online` binary: `serve` (default) and `hash-password` |
| `src/online/mod.rs` | `online` | declares the modules below |
| `src/online/wire.rs` | `online` | message types shared by the modules (below) |
| `src/online/config.rs` | `online` | `Config::from_env` |
| `src/online/auth.rs` | `online` | password check, sessions, rate limit, origin check |
| `src/online/push.rs` | `online` | VAPID keys, subscriptions, payloads, sending |
| `src/online/dispatch.rs` | `online` | command name + JSON args → existing command fn |
| `src/online/server.rs` | `online` | axum router, middleware, WebSocket, notifier loop |

`desktop` and `online` are mutually exclusive (`compile_error!` when both
are on). The default feature is `desktop`, so `npm run tauri build`,
`cargo test` and `make desktop-test` keep working as they do.

### Paths in the container

| Path | Volume | Holds |
| ---- | ------ | ----- |
| `/home/ws` (`HOME`) | `ws-home` | `~/.claude/` (settings.json, agents, jobs, projects, credentials), `~/.claude.json` (MCP servers, trust, onboarding), `~/.local/bin/claude` and `~/.local/share/claude/` (self-updating), npm globals (`NPM_CONFIG_PREFIX=~/.local`), `~/.config/gh/`, `~/.ssh/`, `~/.gitconfig` |
| `/data/ws` (`WS_CONFIG_DIR`) | `ws-data` | `ws`'s config.toml, cache, workspaces, state, reviews, loops |
| `/data/online` (`WS_ONLINE_DATA_DIR`) | `ws-data` | `desktop.json` (settings, watched repos), `vapid.json`, `subscriptions.json`, `sessions.json`, `reports/` |
| `/opt/ws-online/web` (`WS_ONLINE_WEB_DIR`) | image | the built frontend |
| `/opt/ws-online/seed/home` | image | Claude Code as installed at build time, copied into `/home/ws` when missing |

## Configuration (`online::config`)

Read once at start by `Config::from_env`. Errors name the variable.

| Variable | Default | Rule |
| -------- | ------- | ---- |
| `WS_ONLINE_USERNAME` | (required) | non-empty after trimming |
| `WS_ONLINE_PASSWORD` | | exactly one of this and `_HASH`; non-empty |
| `WS_ONLINE_PASSWORD_HASH` | | an argon2 PHC string (`$argon2id$…`), parsed at start |
| `WS_ONLINE_ADDR` | `0.0.0.0:8080` | a `SocketAddr` |
| `WS_ONLINE_PUBLIC_URL` | unset | `http://` or `https://` URL, no path other than `/`; its origin is the only allowed `Origin` when set |
| `WS_ONLINE_SECURE_COOKIE` | `auto` | `auto` (Secure iff public URL is https), `true`, `false` |
| `WS_ONLINE_SESSION_DAYS` | `30` | 1–365 |
| `WS_ONLINE_TRUST_PROXY` | `false` | `true`: the client address for rate limiting is the first `X-Forwarded-For` entry |
| `WS_ONLINE_DATA_DIR` | `$WS_CONFIG_DIR/online`, else `./ws-online-data` | created if missing |
| `WS_ONLINE_WEB_DIR` | `./dist` | must contain `index.html` |
| `WS_ONLINE_VAPID_SUBJECT` | public URL if it starts with `https://`, else `mailto:ws-online@localhost` | must start with `mailto:` or `https://` |

## HTTP API

All bodies are JSON (`Content-Type: application/json`) unless noted.
Errors are an `AppError`: `{"code": "...", "message": "...", "details"?: {...}}`.

### Guards (in this order)

1. **Public** routes skip 2–3: `GET /healthz`, `GET /api/session`,
   `POST /api/login` (still origin-checked), and every non-`/api` path.
2. **Origin**: every `POST` and the `/api/ws` upgrade need an `Origin`
   header. With `WS_ONLINE_PUBLIC_URL` set it must equal that URL's
   origin (`scheme://host[:port]`, default ports omitted). Otherwise its
   `host[:port]` must equal the request's `Host` header. Missing or
   different: **403** `{"code":"forbidden_origin"}`.
3. **Session**: the `ws_online_session` cookie must name a live session.
   Otherwise **401** `{"code":"unauthenticated","message":"log in first"}`.

### Routes

| Method, path | Auth | Request | Response |
| ------------ | ---- | ------- | -------- |
| `GET /healthz` | no | | 200 `{"status":"ok","version":"0.1.0"}` |
| `GET /api/session` | no | | 200 `{"authenticated":bool,"username":string\|null,"version":string}` |
| `POST /api/login` | no | `{"username":string,"password":string}` | 204 + `Set-Cookie`; 401 `{"code":"bad_credentials"}`; 429 `{"code":"rate_limited"}` + `Retry-After: <s>`; 400 `{"code":"bad_request"}` |
| `POST /api/logout` | yes | (none) | 204 + expiring `Set-Cookie` |
| `POST /api/invoke/:command` | yes | the command's args object; empty body = `{}` | 200 result JSON (`null` for `()`); 404 `unknown_command`; 400 `bad_args`; 422 the command's `AppError`; 400 `no_connection` when a channel arg has no socket |
| `GET /api/ws` | yes | WebSocket upgrade | see below |
| `GET /api/reports/:file` | yes | `file` matches `^[A-Za-z0-9._-]+\.html$` | 200 `text/html` from `DATA/reports/`; 404 otherwise |
| `GET /api/push/vapid` | yes | | 200 `{"publicKey":"<base64url uncompressed P-256 point>"}` |
| `POST /api/push/subscribe` | yes | `{"subscription":PushSubscriptionJSON,"label":string}` | 200 `{"id":string}` |
| `POST /api/push/unsubscribe` | yes | `{"endpoint":string}` | 204 (also when unknown) |
| `POST /api/push/test` | yes | (none) | 200 `{"sent":n,"failed":n}` |
| anything else | no | | static file from `WS_ONLINE_WEB_DIR`; unknown paths serve `index.html` |

`PushSubscriptionJSON` is what the browser's `subscription.toJSON()`
returns: `{"endpoint":string,"expirationTime":number|null,"keys":{"p256dh":string,"auth":string}}`.

**Headers on every response:** `X-Content-Type-Options: nosniff`,
`Referrer-Policy: same-origin`, `X-Frame-Options: DENY`, and
`Content-Security-Policy: default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; worker-src 'self'; manifest-src 'self'; frame-ancestors 'none'`.
`index.html`, `sw.js` and `manifest.webmanifest` also get
`Cache-Control: no-cache`; `/api/*` gets `Cache-Control: no-store`.

### Commands over `/api/invoke`

The 55 command names in `lib.rs`'s `generate_handler!` are the names
here, with the same camelCase argument keys Tauri uses (`workspaceId`,
`sessionId`, `keepChildren`, `gitRef`, `autoReview`, `onEvent`, …) and the
same results. Online differences:

| Command | Online behaviour |
| ------- | ---------------- |
| `list_entries` | as desktop, then hands the entries to the notifier instead of the tray |
| `versions` | app version is `CARGO_PKG_VERSION` |
| `terminal_open` | `onEvent` is `{"__channel": <u64>}`; needs `X-WS-Connection` (or an invoke frame); the terminal is closed when that socket closes |
| `review_open_report` | writes the HTML into `DATA/reports/` and returns `"/api/reports/<file>"`; the browser opens it |
| `open_external`, `workspace_open` | 422 `{"code":"unsupported","message":"<command> isn't available in Workspace Online"}` (the web frontend never calls them) |

### Sessions and cookie

- Token: 32 random bytes (`getrandom`), base64url without padding.
- Cookie: `ws_online_session=<token>; Path=/; HttpOnly; SameSite=Strict; Max-Age=<days*86400>` plus `; Secure` when secure. Logout sends the same with `Max-Age=0`.
- Stored: `DATA/sessions.json`, `{"version":1,"sessions":[{"hash":"<hex sha256(token)>","expiresMs":i64,"cred":"<hex sha256(username + \"\\n\" + password-or-hash)>"}]}`, written atomically (temp file + rename), mode 0600. Loading drops expired sessions and those whose `cred` differs from the current credentials.
- Password check: with a hash, `Argon2::default().verify_password`; with a plain password, `sha256(input).ct_eq(sha256(password))`. The username is compared the same constant-time way. Both are always checked (no early return on a wrong username).
- Rate limit: per client address, failed logins in the last 15 minutes; the 11th attempt inside the window gets 429 with `Retry-After` = seconds until the oldest failure leaves the window. A successful login clears that address. In memory only.

## WebSocket `/api/ws`

Text frames, one JSON object each, tagged by `type`.

Server → browser (`wire::ServerMsg`):

```json
{"type":"hello","connection":"<id>"}
{"type":"channel","id":7,"payload":{"event":"data","data":"..."}}
{"type":"event","event":"notify","payload":<PushPayload>}
{"type":"result","id":12,"ok":true,"value":null}
{"type":"result","id":13,"ok":false,"error":{"code":"terminal_closed","message":"terminal 3 isn't open"}}
```

Browser → server (`wire::ClientMsg`):

```json
{"type":"invoke","id":12,"command":"terminal_write","args":{"id":3,"data":"ls\r"}}
{"type":"ping"}
```

- `hello` is the first frame. `<id>` is 16 random bytes in hex; the
  browser sends it as `X-WS-Connection` on `/api/invoke` calls whose
  args contain a channel.
- Channel ids are chosen by the browser, unique per connection. A
  channel's `payload` is exactly what Tauri's channel would deliver
  (`TermEvent`: `{"event":"data","data":string}` or `{"event":"exit","code":number|null}`).
- Invoke frames go through the same dispatch as `/api/invoke`; the
  result frame echoes the `id`. A channel arg in an invoke frame binds to
  that socket.
- `ping` gets no answer; the server sends a WebSocket ping every 30 s
  and drops a socket silent for 90 s.
- When a socket closes, the server closes every terminal opened through
  it. The browser, on close, delivers `{"event":"exit","code":null}` to
  each open channel, forgets them, and reconnects with backoff (1 s,
  doubling, max 30 s).

## Push

### Keys and subscriptions

- `DATA/vapid.json`: `{"version":1,"private":"<base64url 32-byte scalar>","public":"<base64url 65-byte uncompressed point>"}`, mode 0600, created on first start with `ES256KeyPair::generate()`.
- `DATA/subscriptions.json`: `{"version":1,"subscriptions":[{"id":string,"label":string,"createdMs":i64,"subscription":PushSubscriptionJSON}]}`; `id` is the first 16 hex chars of `sha256(endpoint)`, so subscribing again replaces.
- Sending: `WebPushBuilder` from the subscription JSON, `.with_vapid(&key, subject)`, `.build(payload_json)`, POST with `reqwest` (10 s timeout), plus header `Urgency: high`. 201/200/202 = sent; 404/410 = remove the subscription; anything else = failed, logged as `eprintln!("push: <status or error>")` like the tray's messages.

### Payload (`wire::PushPayload`)

```json
{"title":"fix-login needs input","body":"Which branch should I use?","tag":"needsInput:fix-login-d8efb24f","key":"fix-login-d8efb24f","url":"/?agent=fix-login-d8efb24f"}
```

- One payload per event when there are at most `SUMMARY_OVER` (3)
  events: `title`/`body` from `tray::view::notices(&[event])`, `tag`
  `needsInput:<key>` or `done:<key>`, `key` the event key, `url`
  `/?agent=<percent-encoded key>`.
- More than 3: one payload from `notices(events)`, `tag` `"agents"`,
  `key` `null`, `url` `"/"`.
- Only events that `tray::watcher::wanted` keeps under the current
  `settings.notifications` are sent, exactly as the tray does.

### Notifier loop

Every 4 s (`tray::watcher::WATCH_INTERVAL`), when `Watcher::is_stale`,
run `commands::list` and observe it; `list_entries` observes too. For each
observation: `wanted` → payloads → broadcast `{"type":"event","event":"notify","payload":p}`
on every socket, and send `p` to every subscription.

## Frontend

### Transport (`desktop/src/transport/`)

- `isTauri()`: `"__TAURI_INTERNALS__" in window`.
- `Transport` interface: `invoke<T>(command, args?)`, `channel<T>(onMessage)` → `{ arg: unknown }`, `listen<T>(event, handler)` → unsubscribe.
- `tauriTransport` wraps `@tauri-apps/api`; `webTransport` uses `fetch("/api/invoke/<command>", {method:"POST", credentials:"same-origin", headers:{"Content-Type":"application/json"[, "X-WS-Connection"]}, body})` and the socket client. `terminal_write` and `terminal_resize` go as invoke frames when the socket is open, else over HTTP.
- A 401 from any call fires `window.dispatchEvent(new Event("ws-online:unauthenticated"))`, which the web root turns into the login page.

### Web mode differences

| Feature | Web mode |
| ------- | -------- |
| Login | `LoginPage` until `/api/session` says authenticated; a **Log out** button in Settings › About |
| Open folder / Open in editor (agent pane) | hidden |
| Settings › Workspaces (open commands) | hidden from the settings list |
| `openExternal(url)` | `window.open(url, "_blank", "noopener")` for `http(s)://`; anything else is ignored |
| `reviewOpenReport` | opens a blank tab before the call, then points it at the returned `/api/reports/...` |
| Tray select | service worker `message` `{type:"select",key}` and `?agent=<key>` on load |
| Notifications settings | adds a **This browser** row: state, Turn on / Turn off, Send a test, and the HTTPS hint when `!isSecureContext` |

### PWA files (`desktop/public/`)

- `manifest.webmanifest`: `name` "Workspaces", `short_name` "Workspaces", `start_url` "/", `scope` "/", `display` "standalone", `background_color` and `theme_color` from `theme.ts`'s background, icons 192, 256, 512 (`purpose` "any").
- `icons/icon-192.png` (resized from `src-tauri/icons/icon.png`), `icons/icon-256.png` (copy of `128x128@2x.png`), `icons/icon-512.png` (copy of `icon.png`).
- `sw.js`: cache `ws-shell-v1`; install caches `/` and `/manifest.webmanifest`; fetch: ignore non-GET and `/api/*` and `/healthz`; navigations network-first, falling back to cached `/`; `/assets/*` and `/icons/*` cache-first. `push` shows `payload.title` with `{body, tag, data:{url,key}, icon:"/icons/icon-192.png", badge:"/icons/icon-192.png"}`. `notificationclick` focuses an open client and posts `{type:"select",key}`, else `clients.openWindow(url)`.

### Screenshots

Web mode is screenshotted against **mocks**, not the real server:
`npx vite --port 1420` in `desktop/`, then in Playwright

```js
await page.addInitScript({ path: "test/tauri-mock.js" }); // sample data and handlers
await page.addInitScript({ path: "test/web-mock.js" });   // turns them into /api + WebSocket
await page.addInitScript(() => { window.__WEB_MOCK__.authenticated = false; }); // login page
await page.goto("http://localhost:1420");
```

`test/web-mock.js` removes `__TAURI_INTERNALS__` (so the app is in web
mode), serves `/api/invoke/<command>` from `__TAURI_MOCK__.handlers`,
fakes `/api/session`, `/api/login`, `/api/logout`, `/api/push/*` and the
WebSocket, and (from T8) stubs `Notification`, `navigator.serviceWorker`
and `PushManager` according to `window.__WEB_MOCK__.push`
(`"subscribed"`, `"default"`, `"denied"`, `"unsupported"`).

Shots wanted: the login page (empty, and with the "wrong username or
password" error), the Agents view in a 1280×820 browser, the same at
390×844 (phone), and Settings › Notifications with the This browser row
in the `default` and `subscribed` states.
