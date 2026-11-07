# Workspace Online: implementation tasks

Implements [0053](../../adrs/0053-workspace-online.md), the
[design](README.md) and the operator guide [docs/online.md](../../online.md).
Each task builds, passes its tests, and can be merged on its own. Each
task is one commit on its own branch (conventional commit, imperative
mood, for example `feat: add ws-online auth (ADR 0053)`).

**Order and parallelism**

| Task | Title | Depends on | Size | Parallel with |
| ---- | ----- | ---------- | ---- | ------------- |
| T1 | Backend split: `desktop`/`online` features, `ipc` shim, online skeleton | nothing | large | T7 |
| T2 | `online::config` and `online::auth` | T1 | small | T3, T4, T7, T8 |
| T3 | `online::push`: VAPID keys, subscriptions, payloads, sending | T1 | small | T2, T4, T7, T8 |
| T4 | `online::dispatch`: command table | T1 | large | T2, T3, T7, T8 |
| T5 | `online::server` and the `ws-online` binary | T2, T3, T4 | large | T7, T8 |
| T6 | Docker image, entrypoint, compose, smoke test, CI job | T5 | large | T7, T8 |
| T7 | Frontend: transport, login page, web mode, web mock | nothing (the contract is in the design README) | large | T1–T6 |
| T8 | Frontend: PWA manifest, service worker, push, notification settings | T7 | large | T1–T6 |
| T9 | Frontend: narrow (phone) layout | T8 | large | T5, T6 |
| T10 | Follow-ups from review: push key validation, keepalive, docs | T6, T9 | large | none |
| T11 | Terminal copy, paste and links in the browser | T10 | large | none |

Backend files are all under `desktop/src-tauri/`; frontend files under
`desktop/src/`, `desktop/public/` and `desktop/test/`; T6 under `deploy/`,
`.dockerignore`, `.github/workflows/ci.yml` and `Makefile`. T2, T3 and T4
each own different files in `desktop/src-tauri/src/online/` (T1 creates
them as stubs and owns `Cargo.toml`, `Cargo.lock` and
`online/mod.rs`), so they run together. T1 and T6 both touch `Makefile`,
in order. T7 and T8 both touch `desktop/test/web-mock.js`,
`desktop/src/main.tsx` and `desktop/README.md`, so they run in order.

**Read before any task:** the ADR's "Decision" section and, in the
design README, the sections the task names. When this file and those
disagree, they win. Don't read the whole repo: each task lists what to
read.

## Rules for every task

- **Follow the existing code** in the files you edit: naming, comment
  style (a short doc comment on every `pub` or exported item, no other
  comments unless the logic isn't obvious), file layout and test style.
  Prefer early returns. Keep functions small.
- **Rust** (`desktop/src-tauri/`): edition 2021 as the crate is. `cargo
  fmt` clean. Clippy clean with `-D warnings` in **both** builds:
  `cargo clippy --all-targets -- -D warnings` and `cargo clippy
  --no-default-features --features online --all-targets -- -D warnings`.
  Unit tests in the file's `#[cfg(test)] mod tests` with
  snake_case-sentence names; `tempfile` for files; `#[tokio::test]` for
  async. Errors are `crate::runner::AppError` at command boundaries;
  messages lower-case, no trailing period. Don't `unwrap()` outside
  tests. Log with `eprintln!("<area>: ...")` as `tray/` does (no
  `tracing`). **Only the crates listed in T1 may be added**, and only by
  T1.
- **TypeScript** (`desktop/src/`): strict types, `readonly` fields on
  interfaces, no `any`. Components are `const X = (props): JSX.Element =>`,
  styles in a CSS module. Tests use vitest with `@testing-library/react`
  and `@testing-library/user-event`, the helpers in
  `desktop/src/test/fixtures.ts` and `desktop/src/test/render.tsx`. Query
  by role and accessible name. **No new npm dependencies.** Web-only
  functions (session, push) live in `desktop/src/web/`, not in the `api`
  object, so `fakeApi()` doesn't change.
- **The desktop app must not change behaviour.** `make desktop-test`
  passes after every task (T1 extends it to the online build).
  `go test ./...` keeps passing (no task touches Go).
- Don't touch files a task doesn't list, except to fix a compile error
  the task itself caused (say so in your report).

## Contract shared by T1–T5

`desktop/src-tauri/src/online/wire.rs` (created complete by T1):

```rust
//! Messages shared by the online server's modules (ADR 0053).

use crate::runner::AppError;
use serde::{Deserialize, Serialize};
use serde_json::Value;

/// A frame the server sends on `/api/ws`.
#[derive(Debug, Clone, Serialize, PartialEq)]
#[serde(tag = "type", rename_all = "camelCase")]
pub enum ServerMsg {
    /// The first frame: the id to send as `X-WS-Connection`.
    Hello { connection: String },
    /// A message on a channel the browser passed as `{"__channel": id}`.
    Channel { id: u64, payload: Value },
    /// A server event, such as `notify`.
    Event { event: String, payload: Value },
    /// The answer to an invoke frame.
    Result {
        id: u64,
        ok: bool,
        #[serde(skip_serializing_if = "Option::is_none")]
        value: Option<Value>,
        #[serde(skip_serializing_if = "Option::is_none")]
        error: Option<AppError>,
    },
}

/// A frame the browser sends on `/api/ws`.
#[derive(Debug, Clone, Deserialize, PartialEq)]
#[serde(tag = "type", rename_all = "camelCase")]
pub enum ClientMsg {
    /// Runs a command, as `POST /api/invoke/<command>` does.
    Invoke {
        id: u64,
        command: String,
        #[serde(default)]
        args: Value,
    },
    /// Keeps the socket alive; no answer.
    Ping,
}

/// One notification, sent by Web Push and as the `notify` event.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct PushPayload {
    pub title: String,
    pub body: String,
    /// `needsInput:<key>`, `done:<key>`, or `agents` for a summary.
    pub tag: String,
    /// The agent's entry key, or `None` for a summary.
    pub key: Option<String>,
    /// Where a click opens: `/?agent=<key>` or `/`.
    pub url: String,
}
```

`desktop/src-tauri/src/online/mod.rs` (created complete by T1):

```rust
//! Workspace Online: the desktop backend served over HTTP (ADR 0053).

pub mod auth;
pub mod config;
pub mod dispatch;
pub mod push;
pub mod server;
pub mod wire;

use std::io::Write;
use std::path::Path;

/// Writes `bytes` to `path` through a temporary file in the same
/// directory and a rename, with mode 0600.
pub(crate) fn write_private(path: &Path, bytes: &[u8]) -> std::io::Result<()>;
```

`write_private`: `std::fs::create_dir_all(parent)`, create
`<path>.tmp-<pid>` with `OpenOptions::new().write(true).create(true).truncate(true).mode(0o600)`
(`std::os::unix::fs::OpenOptionsExt`), `write_all`, `sync_all`,
`std::fs::rename` onto `path`.

---

## T1. Backend split: `desktop`/`online` features, `ipc` shim, online skeleton

- **Id:** T1. **Depends on:** nothing. **Size:** large (many files, all
  mechanical). **Parallel with:** T7.

**Read:** ADR 0053 "Decision" (Backend split); design README "Crate
layout"; `desktop/src-tauri/Cargo.toml`, `build.rs`, `src/lib.rs`,
`src/main.rs`, `src/env.rs`, `src/tray/mod.rs`, `src/tray/watcher.rs`;
the `use` lines and `#[tauri::command]` lines of `src/commands.rs`,
`src/cache.rs`, `src/loops.rs`, `src/open.rs`, `src/reviews/mod.rs`,
`src/pty.rs` (`grep -n "tauri" <file>` finds every place), and the
test `terminal_streams_output_takes_input_and_reports_exit` in
`src/pty.rs`.

**Create**

- `desktop/src-tauri/src/ipc.rs`
- `desktop/src-tauri/src/online/mod.rs` and `wire.rs` (complete, from the contract above, plus a test of `write_private`)
- `desktop/src-tauri/src/online/{auth,config,dispatch,push,server}.rs`: stubs, each only a `//!` line naming what will go there (e.g. `//! Login, sessions, rate limiting and origin checks (ADR 0053).`)
- `desktop/src-tauri/src/bin/ws-online.rs`: stub, `//! The Workspace Online server (ADR 0053).` and `fn main() {}`

**Modify**

- `desktop/src-tauri/Cargo.toml`, `Cargo.lock` (by cargo), `build.rs`, `src/lib.rs`, `src/env.rs`, `src/commands.rs`, `src/cache.rs`, `src/loops.rs`, `src/open.rs`, `src/reviews/mod.rs`, `src/pty.rs`, `src/tray/mod.rs`, `src/tray/watcher.rs`, `Makefile` (repo root).

**`Cargo.toml`.** Keep `[package]` and `[lib]` as they are. Add:

```toml
[[bin]]
name = "ws-desktop"
path = "src/main.rs"
required-features = ["desktop"]

[[bin]]
name = "ws-online"
path = "src/bin/ws-online.rs"
required-features = ["online"]

[features]
default = ["desktop"]
desktop = ["dep:tauri", "dep:tauri-plugin-opener", "dep:tauri-plugin-notification", "dep:tauri-build"]
online = [
  "dep:axum", "dep:tower-http", "dep:argon2", "dep:sha2", "dep:subtle",
  "dep:getrandom", "dep:base64ct", "dep:web-push-native", "dep:reqwest",
  "tokio/rt-multi-thread", "tokio/net", "tokio/signal",
]
```

Make `tauri-build` (build-dependency), `tauri`, `tauri-plugin-opener`
and `tauri-plugin-notification` `optional = true`, keeping their
versions and features. Add these optional dependencies (and no others):

```toml
axum = { version = "0.8", features = ["ws"], optional = true }
tower-http = { version = "0.7", features = ["fs"], optional = true }
argon2 = { version = "0.6", features = ["getrandom"], optional = true }
sha2 = { version = "0.11", optional = true }
subtle = { version = "2", optional = true }
getrandom = { version = "0.4", optional = true }
base64ct = { version = "1.8", features = ["alloc"], optional = true }
web-push-native = { version = "0.5", optional = true }
reqwest = { version = "0.13", default-features = false, features = ["rustls"], optional = true }
```

Run `cargo check --no-default-features --features online` once to
update `Cargo.lock`, and commit the lock file.

**`build.rs`:** keep the `git describe` part; call `tauri_build::build()`
only under `#[cfg(feature = "desktop")]`.

**`src/ipc.rs`:**

```rust
//! The IPC types commands take: Tauri's under `desktop`, look-alikes that
//! `online::dispatch` builds under `online` (ADR 0053).

#[cfg(feature = "desktop")]
pub use tauri::{ipc::Channel, State};

#[cfg(not(feature = "desktop"))]
pub use shim::{Channel, ChannelClosed, State};

#[cfg(not(feature = "desktop"))]
mod shim {
    // State<'r, T: Send + Sync + 'static>(&'r T) with
    //   pub fn new(inner: &'r T) -> Self, Deref<Target = T>, Clone + Copy.
    // ChannelClosed: unit struct, Debug, Clone, Copy, PartialEq, Eq,
    //   Display "channel closed", std::error::Error.
    // Channel<T>: { sink: Arc<dyn Fn(serde_json::Value) -> Result<(), ChannelClosed> + Send + Sync>,
    //   _message: PhantomData<fn(T)> }, Clone (manual impl, no T: Clone bound),
    //   impl<T: Serialize> Channel<T> {
    //     pub fn new(sink: impl Fn(Value) -> Result<(), ChannelClosed> + Send + Sync + 'static) -> Self;
    //     pub fn send(&self, message: T) -> Result<(), ChannelClosed>  // to_value, then sink; a serialize error is ChannelClosed
    //   }
}
```

Tests (under `#[cfg(all(test, not(feature = "desktop")))]`):
`state_derefs_to_inner`, `channel_sends_serialized_messages` (collect
into an `Arc<Mutex<Vec<Value>>>`), `channel_reports_closed_sink`.

**`src/lib.rs`:**

- At the top: `#![cfg_attr(not(feature = "desktop"), allow(dead_code))]`
  and
  `#[cfg(all(feature = "desktop", feature = "online"))] compile_error!("the desktop and online features can't be enabled together");`
- Add `mod ipc;` and `#[cfg(feature = "online")] pub mod online;`.
- Gate `use commands::AppState;`, `use reviews::…`, `use std::sync::Arc;`,
  `use tauri::Manager;` and `pub fn run()` with
  `#[cfg(feature = "desktop")]`. Every `mod` stays as it is.

**`src/env.rs`:** add, with a test:

```rust
/// This process's PATH followed by the usual install locations, without
/// asking a login shell: for ws-online, whose environment is the
/// container's.
pub fn fallback_path() -> String
```

Same fallbacks list as `resolve_path` (factor it into a private
`fn fallbacks(home: &str) -> [String; 7]`), merged with
`merge_paths(&[&current, &fallbacks.join(":")])`.

**Command files** (`commands.rs`, `cache.rs`, `loops.rs`, `open.rs`,
`reviews/mod.rs`):

- Replace `use tauri::State;` (and the `State` in
  `use tauri::{Manager, State};`) with `use crate::ipc::State;`; in
  `commands.rs` also `use tauri::ipc::Channel;` → `use crate::ipc::Channel;`.
- Replace every `#[tauri::command]` with
  `#[cfg_attr(feature = "desktop", tauri::command)]`.
- Replace `tauri::async_runtime::spawn_blocking` with
  `tokio::task::spawn_blocking` (in `commands.rs` and `open.rs`).
- Put `#[cfg(feature = "desktop")]` on: `list_entries`, `versions`,
  `open_external` (commands.rs), `review_open_report`
  (reviews/mod.rs), and on the imports only they use
  (`tauri_plugin_opener::OpenerExt` in both files, `tauri::Manager` in
  reviews/mod.rs).

**`src/pty.rs`:** `use tauri::ipc::Channel;` → `use crate::ipc::Channel;`.
Put `#[cfg(feature = "desktop")]` on the tests
`terminal_streams_output_takes_input_and_reports_exit` and
`attach_live`. Add `#[cfg(not(feature = "desktop"))]` test
`terminal_streams_output_through_the_shim_channel`: the same script and
assertions, with `Channel::new(move |value| { tx.send(value).map_err(|_| ChannelClosed) })`
and `value["event"]`, `value["data"]` read directly.

**`src/tray/mod.rs`:** gate `pub mod menu;`, the `use` lines for
`AppState`, `tauri`, `NotificationExt`, `Observed`, and `pub fn publish`
with `#[cfg(feature = "desktop")]`. **`src/tray/watcher.rs`:** gate
`watch_forever` and the imports only it uses (`AppState`, `AppHandle`,
`Manager`).

**`Makefile`:** in `desktop-test`, add after the existing cargo line:

```make
	cd desktop/src-tauri && cargo test --no-default-features --features online && cargo clippy --no-default-features --features online --all-targets -- -D warnings
```

**Acceptance criteria**

- `cargo build` and `cargo test` (default features) pass unchanged, and
  `npm run tauri build` would still find `ws-desktop`.
- `cargo build --no-default-features --features online --bin ws-online`
  builds without `tauri` in the dependency graph:
  `cargo tree --no-default-features --features online -e normal | grep -c tauri`
  prints `0`.
- `cargo check --features online` (both on) fails with the
  `compile_error!` message.
- The shim tests and `terminal_streams_output_through_the_shim_channel`
  pass under `--no-default-features --features online`.
- No command function body changed except the `spawn_blocking` path.

**Verify**

```
cd desktop/src-tauri
cargo fmt --check
cargo test && cargo clippy --all-targets -- -D warnings
cargo test --no-default-features --features online
cargo clippy --no-default-features --features online --all-targets -- -D warnings
test "$(cargo tree --no-default-features --features online -e normal --prefix none | grep -c '^tauri')" = 0
! cargo check --features online 2>/dev/null
cd ../.. && make desktop-test
```

---

## T2. `online::config` and `online::auth`

- **Id:** T2. **Depends on:** T1. **Size:** small (two files). **Parallel with:** T3, T4, T7, T8.

**Read:** design README "Configuration" and "Sessions and cookie" and
"Guards"; `src/online/mod.rs` (`write_private`); `src/runner.rs`
(`AppError`, first 30 lines).

**Modify** (replacing T1's stubs): `desktop/src-tauri/src/online/config.rs`,
`desktop/src-tauri/src/online/auth.rs`.

**`config.rs`**

```rust
/// How the login password is configured.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Password { Plain(String), Hash(String) }

/// A variable that is missing or invalid.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ConfigError { pub var: &'static str, pub message: String }
// Display: "{var}: {message}"; impl std::error::Error.

/// The server's settings, read from the environment.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Config {
    pub username: String,
    pub password: Password,
    pub addr: std::net::SocketAddr,
    /// The public URL's origin, e.g. "https://ws.example.com" (no default port, no slash).
    pub public_origin: Option<String>,
    pub secure_cookie: bool,
    pub session_days: u32,
    pub trust_proxy: bool,
    pub data_dir: std::path::PathBuf,
    pub web_dir: std::path::PathBuf,
    pub vapid_subject: String,
}

impl Config {
    pub fn from_env() -> Result<Self, ConfigError>;           // from_lookup(|k| std::env::var(k).ok())
    pub fn from_lookup(get: impl Fn(&str) -> Option<String>) -> Result<Self, ConfigError>;
    /// Creates the data directory (mode 0700) and checks `web_dir/index.html` exists.
    pub fn prepare(&self) -> Result<(), ConfigError>;
}

/// "scheme://host[:port]" of an http(s) URL, lower-case, default port
/// dropped; None for anything else or a URL with a path other than "/".
pub fn origin_of(url: &str) -> Option<String>;
```

Rules, all from the design README's table: empty strings count as unset;
`WS_ONLINE_USERNAME` trimmed; both or neither password variable is an
error on `WS_ONLINE_PASSWORD`; the hash is checked with
`argon2::password_hash::phc::PasswordHash::new`; `WS_ONLINE_SECURE_COOKIE`
`auto|true|false` (`auto` = public origin starts with `https://`);
`WS_ONLINE_SESSION_DAYS` 1–365; `WS_ONLINE_TRUST_PROXY` `true|false`;
`WS_ONLINE_DATA_DIR` default `$WS_CONFIG_DIR/online` or
`./ws-online-data`; `WS_ONLINE_WEB_DIR` default `./dist`;
`WS_ONLINE_VAPID_SUBJECT` default public URL as given (trailing `/`
trimmed), else `mailto:ws-online@localhost`, and must start with
`mailto:` or `https://`.

**`auth.rs`**

```rust
pub const COOKIE: &str = "ws_online_session";

/// The configured user, compared in constant time.
pub struct Credentials { /* username, password */ }
impl Credentials {
    pub fn new(username: String, password: Password) -> Self;
    /// Checks both fields fully, whatever the first one gave.
    pub fn verify(&self, username: &str, password: &str) -> bool;
    /// hex(sha256(username + "\n" + plain password or hash)), stored with sessions.
    pub fn fingerprint(&self) -> String;
}

/// An argon2id PHC string for `password`, for `ws-online hash-password`.
pub fn hash_password(password: &str) -> Result<String, String>;

/// 32 random bytes, base64url without padding.
pub fn new_token() -> String;
/// hex(sha256(token)).
pub fn token_hash(token: &str) -> String;

/// Live sessions, persisted to `sessions.json` (design README).
pub struct Sessions { /* path, ttl_ms, cred, Mutex<Vec<Record>> */ }
impl Sessions {
    pub fn load(path: PathBuf, days: u32, cred: String, now_ms: i64) -> Self; // a missing or bad file is empty
    pub fn create(&self, now_ms: i64) -> std::io::Result<String>;            // returns the token; persists
    pub fn is_valid(&self, token: &str, now_ms: i64) -> bool;
    pub fn remove(&self, token: &str) -> std::io::Result<()>;                // persists
}

/// `Set-Cookie` for a new session, and the one that ends it.
pub fn session_cookie(token: &str, days: u32, secure: bool) -> String;
pub fn clear_cookie(secure: bool) -> String;
/// The session token in a `Cookie` header, if any.
pub fn cookie_token(cookie_header: &str) -> Option<&str>;

/// Failed logins per client address in a sliding window.
#[derive(Default)]
pub struct RateLimiter { /* Mutex<HashMap<String, VecDeque<i64>>> */ }
impl RateLimiter {
    pub const WINDOW_MS: i64 = 15 * 60 * 1000;
    pub const MAX_FAILURES: usize = 10;
    /// Err(seconds to wait) when `client` has MAX_FAILURES failures in the window.
    pub fn check(&self, client: &str, now_ms: i64) -> Result<(), u64>;
    pub fn fail(&self, client: &str, now_ms: i64);
    pub fn succeed(&self, client: &str);
}

/// The Origin rule from the design README "Guards".
pub fn origin_allowed(origin: Option<&str>, host: Option<&str>, public_origin: Option<&str>) -> bool;
/// The rate-limit key: the first X-Forwarded-For entry when trusted and present, else the peer IP.
pub fn client_key(peer: std::net::SocketAddr, forwarded_for: Option<&str>, trust_proxy: bool) -> String;
```

Use `sha2::Sha256`, `subtle::ConstantTimeEq` on the two 32-byte
digests, `argon2::{Argon2, password_hash::{PasswordHasher, PasswordVerifier, phc::PasswordHash}}`,
`getrandom::fill`, `base64ct::{Base64UrlUnpadded, Encoding}`. Hex with a
private `fn hex(bytes: &[u8]) -> String` (`format!("{b:02x}")`). Persist
with `super::write_private`.

**Acceptance criteria** (tests in each file):

- config: a minimal env (username + password) gives every default;
  missing username, both passwords, neither, a bad hash, a bad addr,
  session days 0 and 366, `SECURE_COOKIE=maybe`, a public URL with a
  path, and a `VAPID_SUBJECT` of `ws@x` each fail naming the variable;
  `auto` is secure only with an https public URL; `origin_of` drops
  `:443`/`:80`, keeps `:8443`, lower-cases, accepts a trailing `/`;
  `prepare` creates the data dir and fails without `index.html`.
- auth: plain and hash credentials verify the right pair and reject a
  wrong username or password; `hash_password` output verifies;
  `fingerprint` changes with the password; sessions survive `load` of
  the same file, expire, are dropped when the fingerprint differs, and
  `remove` ends one; the file is mode 0600 and never contains the token;
  the cookie strings match the design README exactly (with and without
  `Secure`); `cookie_token` finds the cookie among others and ignores
  `ws_online_session_x=`; the 11th attempt is limited with the right
  wait, an old failure leaves the window, `succeed` clears; every
  `origin_allowed` case (missing origin, matching host, other port,
  public origin set and matched/unmatched); `client_key` with and
  without trust.

**Verify**

```
cd desktop/src-tauri
cargo fmt --check
cargo test --no-default-features --features online online::
cargo clippy --no-default-features --features online --all-targets -- -D warnings
```

---

## T3. `online::push`: VAPID keys, subscriptions, payloads, sending

- **Id:** T3. **Depends on:** T1. **Size:** small (one file). **Parallel with:** T2, T4, T7, T8.

**Read:** design README "Push"; `src/online/wire.rs`, `src/online/mod.rs`;
`src/tray/attention.rs` (`Attention`, `Event`, lines 1–20 and 115–145);
`src/tray/view.rs` (`Notice`, `notices`, `SUMMARY_OVER`).

**Modify** (replacing T1's stub): `desktop/src-tauri/src/online/push.rs`.

```rust
/// The server's VAPID key pair, kept in `vapid.json`.
pub struct VapidKeys { /* ES256KeyPair, public: String */ }
impl VapidKeys {
    /// Reads the file, or generates a pair and writes it (0600).
    pub fn load_or_create(path: &Path) -> std::io::Result<Self>;
    /// base64url of the 65-byte uncompressed point, for applicationServerKey.
    pub fn public_key(&self) -> &str;
}

/// One browser's subscription, as stored.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct StoredSubscription { pub id: String, pub label: String, pub created_ms: i64, pub subscription: Value }

/// The subscriptions, persisted to `subscriptions.json`.
pub struct Subscriptions { /* path, Mutex<Vec<StoredSubscription>> */ }
impl Subscriptions {
    pub fn load(path: PathBuf) -> Self;                      // missing or bad file = empty
    /// Validates by deserializing a WebPushBuilder; replaces one with the same endpoint.
    pub fn add(&self, subscription: Value, label: &str, now_ms: i64) -> Result<String, AppError>; // code "bad_subscription"
    pub fn remove(&self, endpoint: &str) -> std::io::Result<bool>;
    pub fn list(&self) -> Vec<StoredSubscription>;
}
/// First 16 hex chars of sha256(endpoint).
pub fn subscription_id(endpoint: &str) -> String;

/// The push payloads for one listing's wanted events (design README "Payload").
pub fn payloads(events: &[Event]) -> Vec<PushPayload>;

/// What a push service said.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Delivery { Sent, Gone, Failed(String) }
/// 200, 201, 202 → Sent; 404, 410 → Gone; else Failed("status <n>").
pub fn classify(status: u16) -> Delivery;

/// Sends payloads to every subscription.
pub struct Pusher { /* keys, subject, subscriptions, reqwest::Client (10 s timeout) */ }
impl Pusher {
    pub fn new(keys: VapidKeys, subject: String, subscriptions: Subscriptions) -> Self;
    pub fn public_key(&self) -> &str;
    pub fn subscriptions(&self) -> &Subscriptions;
    /// The encrypted, VAPID-signed request, with `Urgency: high`.
    pub fn request(&self, subscription: &Value, payload: &PushPayload) -> Result<reqwest::Request, String>;
    /// (sent, failed); Gone subscriptions are removed and not counted as failed.
    pub async fn send_all(&self, payload: &PushPayload) -> (usize, usize);
}
```

Implementation notes (verified on 2026-11-06, see ADR footnote `spike`):
`ES256KeyPair::generate()`, `.to_bytes()`, `ES256KeyPair::from_bytes(&bytes)`;
the public key is `key.public_key().public_key().to_bytes_uncompressed()`
(import `web_push_native::jwt_simple::algorithms::{ECDSAP256KeyPairLike, ECDSAP256PublicKeyLike, ES256KeyPair}`);
`let builder: WebPushBuilder = serde_json::from_value(subscription.clone())?;`
`builder.with_vapid(&key, &subject).build(serde_json::to_vec(payload)?)`
gives an `http::Request<Vec<u8>>`; `reqwest::Request::try_from(request)`
converts it; insert header `Urgency: high`. The percent-encoding for the
URL's key is a private function that keeps `A-Za-z0-9-._~` and encodes
every other byte as `%XX`.

**Acceptance criteria** (tests):

- `load_or_create` writes a 0600 file and a second call returns the same
  public key; the public key decodes to 65 bytes starting with `0x04`.
- `add` stores and persists; adding the same endpoint again replaces
  (one entry, new label); a subscription without `keys.auth` is
  `bad_subscription`; `remove` returns true then false; `load` of the
  written file gives the same list. Use this valid sample:
  `{"endpoint":"https://push.example.com/abc","expirationTime":null,"keys":{"p256dh":"BLn9b-VR0ca83knDNZ32dCHGyjJp-1riX9ZTN40MqV8K_LpQmLqxC_DoHvqvFXO_nGdAB4W9dogZb_sM-uV4JbY","auth":"_ordMnz7uTCmrpBTeUV4Bw"}}`.
- `payloads`: one NeedsInput event gives title `<name> needs input`, tag
  `needsInput:<key>`, url `/?agent=<key>`; Done gives `done:`; a key with
  a space or `/` is percent-encoded in the url only; 4 events give one
  payload with tag `agents`, key `None`, url `/`; Busy events are dropped.
- `request` targets the endpoint with `Content-Encoding: aes128gcm`, a
  `TTL`, `Urgency: high`, and `Authorization` starting `vapid t=`.
- `classify` covers 201, 404, 410, 500.
- `send_all` is not unit-tested against the network.

**Verify**

```
cd desktop/src-tauri
cargo fmt --check
cargo test --no-default-features --features online online::push
cargo clippy --no-default-features --features online --all-targets -- -D warnings
```

---

## T4. `online::dispatch`: command table

- **Id:** T4. **Depends on:** T1. **Size:** large (one long file). **Parallel with:** T2, T3, T7, T8.

**Read:** design README "Commands over /api/invoke" and "WebSocket";
`src/online/wire.rs`; `src/ipc.rs`; `src/lib.rs` (`generate_handler!`);
the signatures of every command (`grep -n -A6 "tauri::command" src/commands.rs src/cache.rs src/loops.rs src/open.rs src/reviews/mod.rs`);
`src/commands.rs` (`AppState`, `list`, tests' `fake_programs`);
`src/about.rs` lines 1–50; `src/reviews/report.rs` `write_html`.

**Modify** (replacing T1's stub): `desktop/src-tauri/src/online/dispatch.rs`.

```rust
/// Where a command's channel messages go: the socket that asked.
pub trait ChannelSink: Send + Sync {
    /// Sends one frame; false when the socket is gone.
    fn send(&self, msg: ServerMsg) -> bool;
    /// Records a terminal opened through this socket, to close with it.
    fn terminal_opened(&self, id: u32);
}

/// What commands run against.
pub struct Ctx {
    pub state: AppState,
    /// Where `review_open_report` writes (DATA/reports).
    pub reports_dir: PathBuf,
    /// Called with each listing `list_entries` returns (the notifier).
    pub on_listing: Box<dyn Fn(&[Entry]) + Send + Sync>,
}

/// Why a command didn't run, or how it failed.
#[derive(Debug, Clone, PartialEq)]
pub enum DispatchError { UnknownCommand(String), BadArgs(String), NoConnection, App(AppError) }
impl DispatchError {
    /// 404, 400, 400, 422.
    pub fn status(&self) -> u16;
    /// codes "unknown_command", "bad_args", "no_connection", or the command's own.
    pub fn to_app_error(&self) -> AppError;
}

/// Every command name, in `generate_handler!` order.
pub const COMMANDS: &[&str] = &[/* the 56 names */];

/// Runs `command` with Tauri-style camelCase `args` (`Value::Null` = `{}`).
pub async fn dispatch(ctx: &Ctx, command: &str, args: Value, sink: Option<Arc<dyn ChannelSink>>) -> Result<Value, DispatchError>;
```

Use this macro to parse each command's arguments, and helpers to turn
results into JSON:

```rust
macro_rules! args {
    ($value:expr, { $($field:ident : $ty:ty),* $(,)? }) => {{
        #[derive(serde::Deserialize)]
        #[serde(rename_all = "camelCase")]
        struct Args { $($field: $ty),* }
        let Args { $($field),* } = serde_json::from_value::<Args>($value)
            .map_err(|e| DispatchError::BadArgs(e.to_string()))?;
        ($($field),*)
    }};
}
fn json<T: Serialize>(value: T) -> Result<Value, DispatchError>;          // serialize error → App("internal")
fn result<T: Serialize>(r: Result<T, AppError>) -> Result<Value, DispatchError>;
```

Each arm calls the existing function with `State::new(&ctx.state)` and
the parsed args in signature order, for example:

```rust
"cache_refs" => {
    let id = args!(args, { id: String });
    result(cache::cache_refs(State::new(&ctx.state), id).await)
}
"remove_entry" => {
    let (workspace_id, session_id, keep_children) =
        args!(args, { workspace_id: Option<String>, session_id: Option<String>, keep_children: Option<bool> });
    result(commands::remove_entry(State::new(&ctx.state), workspace_id, session_id, keep_children).await)
}
"reviews_view" => json(reviews::reviews_view(State::new(&ctx.state))),
```

Note `workspace_open`'s argument is named `with` (JSON key `with`).
Special arms:

- `list_entries`: `commands::list(&ctx.state.runner, ctx.state.jobs_dir.as_deref()).await`;
  on success `ctx.state.reviews.set_entries(listing.entries.clone())`,
  `(ctx.on_listing)(&listing.entries)`, return the listing.
- `versions`: `about::versions(&ctx.state.runner, about::app_version(env!("CARGO_PKG_VERSION"))).await`.
- `review_open_report` (`repo`, `gitRef`): `reviews::report::write_html(&ctx.state.runner, &ctx.reports_dir, &repo, &git_ref)`,
  return `"/api/reports/<file name>"`.
- `open_external`, `workspace_open`: `App(AppError::new("unsupported", "<command> isn't available in Workspace Online"))`.
- `terminal_open` (`spec: TermSpec`, `cols: u16`, `rows: u16`,
  `on_event: Value`): `on_event["__channel"].as_u64()` or `BadArgs`; no
  sink → `NoConnection`; build
  `Channel::new(move |payload| if sink.send(ServerMsg::Channel { id, payload }) { Ok(()) } else { Err(ChannelClosed) })`,
  call `commands::terminal_open`, then `sink.terminal_opened(id)`.

**Acceptance criteria** (tests; build a `Ctx` with a helper using
`Runner::new(<tempdir with fake ws/claude scripts>)`, `Terminals::default()`,
`jobs_dir: None`, `Store::open(None)`, `Checker::default()`,
`report::Cache::default()`, `Watcher::default()`; copy `fake_programs`
from `commands.rs` tests):

- `commands_match_generate_handler`: parse `include_str!("../lib.rs")`
  between `generate_handler![` and `]`, take each line's last `::`
  segment without the comma, and assert it equals `COMMANDS`.
- Every name in `COMMANDS` is handled: dispatching each with
  `Value::Null` never returns `UnknownCommand` (it may return `BadArgs`
  or `App`); an unknown name returns `UnknownCommand`.
- `rename_workspace` with `{"workspaceId":"x-d8efb24f","name":"draft"}`
  runs the fake `ws` and returns its JSON; with `{"workspace_id":…}`
  returns `BadArgs`.
- `list_entries` calls `on_listing` once with the entries.
- `open_defaults` returns the platform defaults as JSON; `open_external`
  returns `App` with code `unsupported` and status 422.
- `terminal_open` without a sink is `NoConnection`; with a recording
  sink and a fake `ws` that echoes and exits, the sink receives
  `ServerMsg::Channel` frames with that channel id containing the output
  and an `exit` event, and `terminal_opened` got the returned id.

**Verify**

```
cd desktop/src-tauri
cargo fmt --check
cargo test --no-default-features --features online online::dispatch
cargo clippy --no-default-features --features online --all-targets -- -D warnings
```

---

## T5. `online::server` and the `ws-online` binary

- **Id:** T5. **Depends on:** T2, T3, T4. **Size:** large. **Parallel with:** T7, T8.

**Read:** the whole design README "HTTP API", "WebSocket", "Push" ›
"Notifier loop"; the `pub` items of `src/online/{config,auth,push,dispatch,wire}.rs`
(`grep -n "pub " <file>`); `src/lib.rs` `run()` (how `AppState` and
the background tasks are built); `src/tray/watcher.rs` (`Watcher`,
`wanted`, `watch_forever`).

**Modify:** `desktop/src-tauri/src/online/server.rs` (replacing the stub),
`desktop/src-tauri/src/bin/ws-online.rs`, `desktop/README.md` (a short
"Workspace Online" paragraph linking `../docs/online.md`, and a Layout
line for `src/online/`).

**`server.rs`**

```rust
/// Everything the routes share.
pub struct Online { /* config, ctx: Ctx, credentials, sessions, limiter, pusher, hub, notifier */ }

/// Builds the state, starts the background tasks, and serves until SIGTERM or Ctrl-C.
pub async fn run(config: Config) -> Result<(), String>;

/// The router, for run and tests.
pub fn router(online: Arc<Online>) -> axum::Router;

/// Builds `Online` from a config without starting anything (tests use a temp data dir).
pub fn build(config: Config) -> Result<Arc<Online>, String>;
```

- **State.** `AppState` as in `lib.rs::run`, with
  `Runner::new(env::fallback_path())`, the store at
  `data_dir/desktop.json` (`store::FILE_NAME`), and `jobs::jobs_dir()`.
  Keep clones of `state.watcher` and `state.store` for the notifier
  before moving `state` into `Ctx`. `Sessions::load(data_dir/"sessions.json", …)`,
  `VapidKeys::load_or_create(data_dir/"vapid.json")`,
  `Subscriptions::load(data_dir/"subscriptions.json")`,
  `reports_dir = data_dir/"reports"`.
- **Hub.** `Mutex<HashMap<String, Arc<Conn>>>`. `Conn { id: String, tx: tokio::sync::mpsc::UnboundedSender<ServerMsg>, terminals: Mutex<Vec<u32>> }`
  implements `ChannelSink` (`send` = `tx.send(msg).is_ok()`).
  `broadcast(msg)` sends to every conn.
- **Notifier.** `observe(&self, entries: &[Entry])`:
  `watcher.observe(entries, commands::now_ms())`, `wanted(&observed.events, &store.get().settings.notifications)`,
  `push::payloads`, then for each payload broadcast
  `ServerMsg::Event { event: "notify".into(), payload: to_value(p) }`
  and `tokio::spawn` `pusher.send_all(&p)`. `Ctx::on_listing` calls it.
  A task loops every `WATCH_INTERVAL`, and when `watcher.is_stale(now)`
  runs `commands::list` and `observe`s (errors: `eprintln!("notify: listing failed: {code}: {message}")`).
- **Other tasks.** `state.reviews.clone().run(deps)` and
  `loops::tick_forever(runner)`, as `lib.rs` does, with `tokio::spawn`.
- **Routes** exactly as the design README table. `/api` is a nested
  router with a JSON 404 fallback `{"code":"not_found","message":"no such route"}`;
  everything else is `tower_http::services::ServeDir::new(web_dir).fallback(ServeFile::new(web_dir.join("index.html")))`.
- **Middleware** (`axum::middleware::from_fn_with_state`): security
  headers and `Cache-Control` on every response; the origin guard on
  `POST` and `/api/ws`; the session guard on protected routes. The
  client address comes from `ConnectInfo<SocketAddr>` (serve with
  `into_make_service_with_connect_info::<SocketAddr>()`).
- **Login** checks the limiter first (429 + `Retry-After`), then
  `Credentials::verify`; failure → `limiter.fail`, 401
  `{"code":"bad_credentials","message":"wrong username or password"}`;
  success → `limiter.succeed`, `sessions.create`, 204 + `Set-Cookie`.
- **Invoke** reads the body as JSON (empty = `{}`), looks up
  `X-WS-Connection` in the hub for the sink, and maps
  `DispatchError::status()` and `to_app_error()` to the response.
- **WebSocket** (`axum::extract::ws`): on upgrade create a `Conn` with a
  16-byte random hex id, register it, send `Hello`, then loop with
  `tokio::select!` over: the conn's receiver (send as text), incoming
  frames (parse `ClientMsg`; `Invoke` → `handle_invoke` below; bad JSON
  ignored), and a 30 s ping interval; stop when the socket closes or is
  silent for 90 s. On exit remove the conn and
  `state.terminals.close(id)` each of its terminals. Put the frame
  handling in `pub(crate) async fn handle_invoke(online: &Online, conn: &Arc<Conn>, id: u64, command: String, args: Value) -> ServerMsg`
  so it is unit-testable without a socket.
- **Reports** route validates the file name with the README regex
  (without a regex crate: check each char and the `.html` suffix) and
  serves it as `text/html; charset=utf-8`.
- **Shutdown:** `axum::serve(...).with_graceful_shutdown(signal)` where
  `signal` waits for `tokio::signal::ctrl_c()` or SIGTERM
  (`tokio::signal::unix`), then `state.terminals.close_all()`.

**`bin/ws-online.rs`**

```
ws-online [serve]        serve until stopped (the default)
ws-online hash-password  read a password from stdin, print its argon2 hash
```

`#[tokio::main]`; `serve`: `Config::from_env()`, `prepare()`,
`server::run`; any error printed as `ws-online: <error>` with exit code
1. `hash-password`: one line from stdin with the trailing newline
removed; empty → `ws-online: empty password`, exit 2. Unknown argument
→ the usage above on stderr, exit 2.

**Acceptance criteria** (`#[tokio::test(flavor = "multi_thread")]` tests
in `server.rs` that `build` a config with a temp data dir and a temp web
dir holding `index.html`, bind `127.0.0.1:0` with
`tokio::net::TcpListener`, serve the router with connect info in a
spawned task, and use `reqwest` with explicit `Origin` and `Cookie`
headers):

- `/healthz` is 200 `{"status":"ok",…}` without a cookie; `/api/session`
  says `authenticated: false`.
- Login without `Origin` is 403; with a foreign `Origin` 403; wrong
  password 401; right password 204 with a `Set-Cookie` holding
  `HttpOnly`, `SameSite=Strict`, `Path=/` and the configured `Max-Age`;
  the 11th bad login is 429 with `Retry-After`.
- `POST /api/invoke/open_defaults` is 401 without the cookie, 200 with
  it; `/api/invoke/nope` is 404 `unknown_command`; `open_external` is
  422 `unsupported`; `/api/nope` is 404 JSON.
- `GET /some/route` serves `index.html`; every response carries
  `X-Content-Type-Options: nosniff` and the CSP; `/api/*` has
  `Cache-Control: no-store`.
- After logout the old cookie gets 401.
- `/api/push/vapid` returns the key from `vapid.json`;
  `/api/push/subscribe` with the T3 sample returns an id and
  `/api/push/unsubscribe` removes it (204).
- `handle_invoke` for `open_defaults` returns `Result { ok: true, … }`
  and for an unknown command `Result { ok: false, error: unknown_command }`.
- `/api/reports/../x.html` and `/api/reports/a.txt` are 404.
- Manual: `WS_ONLINE_USERNAME=me WS_ONLINE_PASSWORD=pw WS_ONLINE_WEB_DIR=../dist WS_ONLINE_DATA_DIR=$(mktemp -d) cargo run --no-default-features --features online --bin ws-online`
  serves the built frontend at <http://localhost:8080> (say in your
  report whether you ran it).

**Verify**

```
cd desktop/src-tauri
cargo fmt --check
cargo test --no-default-features --features online
cargo clippy --no-default-features --features online --all-targets -- -D warnings
cargo build --release --no-default-features --features online --bin ws-online
printf 'pw\n' | ./target/release/ws-online hash-password | grep -q '^\$argon2id\$'
cd ../.. && make desktop-test
```

---

## T6. Docker image, entrypoint, compose, smoke test, CI job

- **Id:** T6. **Depends on:** T5. **Size:** large. **Parallel with:** T7, T8.

**Read:** ADR 0053 "Decision" (Image); `docs/online.md` (Environment,
Volumes, Docker Compose); design README "Paths in the container";
`Makefile`; `.github/workflows/ci.yml`.

**Create**

- `deploy/docker/Dockerfile`
- `deploy/docker/entrypoint.sh` (mode 755)
- `deploy/docker/docker-compose.yml` (exactly the one in `docs/online.md`)
- `deploy/docker/smoke-test.sh` (mode 755)
- `.dockerignore` (repo root): `.git`, `**/node_modules`, `desktop/dist`,
  `desktop/src-tauri/target`, `bin`, `dist`, `deploy/docker/.env`
- `.gitignore`: add `/deploy/docker/.env`

**Modify:** `Makefile` (add to `.PHONY` and):

```make
# Workspace Online (ADR 0053): the server binary and the frontend, or the image.
online:
	cd desktop && npm ci && npm run build
	cd desktop/src-tauri && cargo build --release --no-default-features --features online --bin ws-online

online-image:
	docker build -f deploy/docker/Dockerfile --build-arg VERSION=$(VERSION) -t ws-online .

online-smoke: online-image
	deploy/docker/smoke-test.sh ws-online
```

and `.github/workflows/ci.yml`, a new job:

```yaml
  # ADR 0053: the Workspace Online image builds and starts; nothing is pushed.
  docker:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - uses: docker/setup-buildx-action@v3
      - uses: docker/build-push-action@v6
        with:
          context: .
          file: deploy/docker/Dockerfile
          push: false
          load: true
          tags: ws-online:ci
          cache-from: type=gha
          cache-to: type=gha,mode=max
      - run: deploy/docker/smoke-test.sh ws-online:ci
```

**Dockerfile** (BuildKit; `# syntax=docker/dockerfile:1.7`), stages:

1. `FROM golang:1.27-trixie AS ws`: copy `go.mod go.sum`, `go mod download`
   (cache mount `/go/pkg/mod`), copy `cmd` and `internal`, `ARG VERSION=dev`,
   `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/ws ./cmd/ws`.
2. `FROM node:22-trixie-slim AS web`: `WORKDIR /src/desktop`, copy
   `desktop/package.json desktop/package-lock.json`, `npm ci` (cache
   mount `/root/.npm`), `COPY desktop/ ./`, `npm run build` →
   `/src/desktop/dist`.
3. `FROM rust:1.94-trixie AS server`: `WORKDIR /src/desktop/src-tauri`,
   `COPY desktop/src-tauri ./`, then with cache mounts on
   `/usr/local/cargo/registry` and `/src/desktop/src-tauri/target`:
   `cargo build --release --locked --no-default-features --features online --bin ws-online && mkdir -p /out && cp target/release/ws-online /out/`.
4. `FROM debian:trixie-slim`:
   - `apt-get install -y --no-install-recommends ca-certificates curl git openssh-client jq ripgrep make nodejs npm tini procps less bash gnupg`,
     then GitHub CLI from its apt repo (keyring
     `https://cli.github.com/packages/githubcli-archive-keyring.gpg` into
     `/etc/apt/keyrings/`, source
     `deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main`),
     `apt-get install -y gh`, `rm -rf /var/lib/apt/lists/*`.
   - `useradd --create-home --uid 1000 --shell /bin/bash ws`;
     `mkdir -p /data /opt/ws-online/seed/home && chown ws:ws /data /opt/ws-online/seed/home`.
   - `COPY --from=ws /out/ws /usr/local/bin/ws`,
     `COPY --from=server /out/ws-online /usr/local/bin/ws-online`,
     `COPY --from=web /src/desktop/dist /opt/ws-online/web`,
     `COPY --chmod=755 deploy/docker/entrypoint.sh /usr/local/bin/ws-online-entrypoint`.
   - `USER ws`, `ENV HOME=/home/ws`, then
     `RUN curl -fsSL https://claude.ai/install.sh | bash && /home/ws/.local/bin/claude --version && mv /home/ws/.local /opt/ws-online/seed/home/.local`.
   - `ENV PATH=/home/ws/.local/bin:/usr/local/bin:/usr/bin:/bin SHELL=/bin/bash NPM_CONFIG_PREFIX=/home/ws/.local WS_CONFIG_DIR=/data/ws WS_ONLINE_DATA_DIR=/data/online WS_ONLINE_WEB_DIR=/opt/ws-online/web WS_ONLINE_ADDR=0.0.0.0:8080`
   - `WORKDIR /home/ws`, `VOLUME ["/home/ws", "/data"]`, `EXPOSE 8080`,
     `HEALTHCHECK --interval=30s --timeout=5s --start-period=20s CMD curl -fsS http://127.0.0.1:8080/healthz >/dev/null || exit 1`,
     `ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/ws-online-entrypoint"]`.

**entrypoint.sh** (`#!/usr/bin/env bash`, `set -euo pipefail`):

1. With arguments other than `serve`: `exec "$@"` (so
   `docker run … ws-online hash-password` and `bash` work without the checks).
2. Fail with `ws-online-entrypoint: <message>` and exit 64 when
   `WS_ONLINE_USERNAME` is empty, both or neither password variable is
   set, or `CLAUDE_CODE_OAUTH_TOKEN` is empty (message says to run
   `claude setup-token`).
3. `mkdir -p "$HOME/.local/bin" "$HOME/.claude" "$WS_CONFIG_DIR" "$WS_ONLINE_DATA_DIR"`.
4. If `$HOME/.local/bin/claude` isn't executable: `cp -a /opt/ws-online/seed/home/. "$HOME/"`.
5. `~/.claude.json`: create `{}` if missing or empty; with `jq --arg dir "$WS_CONFIG_DIR/workspaces"`
   set `.hasCompletedOnboarding = true | .projects[$dir].hasTrustDialogAccepted = true`,
   via a temp file and `mv`; `chmod 600`.
6. `git config --global user.name/user.email` from `WS_GIT_NAME` /
   `WS_GIT_EMAIL` when set; when `GH_TOKEN` is set, `gh auth setup-git`
   (a failure prints a warning and continues).
7. `exec ws-online serve`.

**smoke-test.sh** `[image] [port]` (defaults `ws-online`, `18080`):
runs the image detached as `ws-online-smoke-$$` with
`-p 127.0.0.1:$port:8080 -e WS_ONLINE_USERNAME=admin -e WS_ONLINE_PASSWORD=smoke-secret -e CLAUDE_CODE_OAUTH_TOKEN=smoke-token`,
removes it and a temp cookie jar on exit (`trap`), and checks, failing
with a message on the first miss:

1. `curl -fsS --retry 30 --retry-delay 1 --retry-all-errors $base/healthz | jq -e '.status == "ok"'`
2. Login with a wrong password (with `Origin: http://127.0.0.1:$port`) is 401.
3. Login without `Origin` is 403.
4. Login with the right password is 204 and the jar holds `ws_online_session`.
5. `/api/session` with the jar: `.authenticated == true and .username == "admin"`.
6. `POST /api/invoke/open_defaults` without the cookie is 401.
7. `POST /api/invoke/versions` with the jar: `jq -e '.app.version'`.
8. `GET /` contains `<div id="root">`.
9. `docker exec`: `claude --version`, `gh --version`, `ws --version`,
   `jq -e '.hasCompletedOnboarding == true' /home/ws/.claude.json`,
   `test -x /home/ws/.local/bin/claude`, and `id -u` prints `1000`.
10. A second container with no `CLAUDE_CODE_OAUTH_TOKEN` exits non-zero
    and its logs mention `claude setup-token` (`docker run --rm` without `-d`).

Prints `smoke test passed`.

**Acceptance criteria**

- The image builds from a clean checkout and the smoke test passes.
- `docker run --rm -i ws-online ws-online hash-password <<< pw` prints an
  `$argon2id$` hash (entrypoint passthrough).
- Restarting the container with the same volumes keeps
  `~/.local/bin/claude`, `~/.claude.json` and `/data/online/vapid.json`
  (check by hand once, say so in the report).
- `make desktop-test` and `go test ./...` still pass.

**Verify**

```
docker build -f deploy/docker/Dockerfile -t ws-online:test .
deploy/docker/smoke-test.sh ws-online:test 18080
docker run --rm -i ws-online:test ws-online hash-password <<< pw | grep -q '^\$argon2id\$'
bash -n deploy/docker/entrypoint.sh deploy/docker/smoke-test.sh
touch deploy/docker/.env && docker compose -f deploy/docker/docker-compose.yml config -q && rm deploy/docker/.env
docker ps -a --filter name=ws-online-smoke --format '{{.Names}}' | grep -c . | grep -qx 0
go test ./...
```

---

## T7. Frontend: transport, login page, web mode, web mock

- **Id:** T7. **Depends on:** nothing (the contract is in the design README). **Size:** large. **Parallel with:** T1–T6.

**Read:** design README "HTTP API" (routes table only), "WebSocket",
"Frontend" › "Transport" and "Web mode differences", "Screenshots";
`desktop/src/api.ts` lines 1–3 and 670–795; `desktop/src/main.tsx`;
`desktop/src/test/setup.ts`; `desktop/src/App.test.tsx` lines 1–15;
`desktop/test/tauri-mock.js` lines 1–10 and 380–427;
`desktop/src/features/agents/EntryPane.tsx` lines 150–290;
`desktop/src/features/settings/SettingsList.tsx`;
`desktop/src/features/settings/AboutGroup.tsx`; `desktop/README.md`
"Screenshots without the backend".

**Create**

- `desktop/src/transport/transport.ts`: `isTauri()` and

  ```ts
  export interface ChannelHandle { readonly arg: unknown }
  export interface Transport {
    invoke<T>(command: string, args?: Record<string, unknown>): Promise<T>;
    channel<T>(onMessage: (message: T) => void): ChannelHandle;
    listen<T>(event: string, handler: (payload: T) => void): Promise<() => void>;
  }
  ```
- `desktop/src/transport/tauri.ts`: `tauriTransport` over `invoke`,
  `Channel` (`arg` is the `Channel` instance) and `listen`.
- `desktop/src/transport/socket.ts`: `class Socket` — `connect()`,
  `connectionId(): Promise<string>` (resolves after `hello`),
  `openChannel(onMessage): number` (ids from 1), `invoke(command, args): Promise<unknown>`
  (invoke frames, rejects with the frame's `AppError`), `isOpen()`,
  `on(event, handler): () => void`; on close: every channel gets
  `{event:"exit",code:null}` and is forgotten, pending invokes reject
  with `{code:"disconnected"}`, reconnect after 1 s doubling to 30 s.
  The `WebSocket` constructor is injectable for tests:
  `new Socket(url: string, create: (url: string) => WebSocket = (u) => new WebSocket(u))`.
- `desktop/src/transport/web.ts`: `webTransport(socket: Socket): Transport`
  — `invoke` uses invoke frames for `terminal_write` and
  `terminal_resize` when `socket.isOpen()`, else `fetch` as the README
  says; when `args` contains a value made by `channel()` (`{__channel:n}`)
  it adds `X-WS-Connection: await socket.connectionId()`; non-2xx →
  throws `toAppError(await res.json())` (or `{code:"http_<status>"}`
  when the body isn't JSON); 401 also dispatches
  `new Event("ws-online:unauthenticated")` on `window`. `listen` uses
  `socket.on`.
- `desktop/src/transport/index.ts`: `export const transport = (): Transport`
  — `tauriTransport` when `isTauri()`, else one lazily created
  `webTransport(new Socket(<ws(s)://host/api/ws>))`. Decided per call.
- `desktop/src/web/session.ts`: `getSession(): Promise<{authenticated: boolean; username: string | null; version: string}>`,
  `login(username, password): Promise<"ok" | "bad_credentials" | "rate_limited" | "error">`,
  `logout(): Promise<void>` (then `location.reload()`).
- `desktop/src/web/WebRoot.tsx`: loads the session; spinner while
  loading; `LoginPage` when unauthenticated or after the
  `ws-online:unauthenticated` event; otherwise its children.
- `desktop/src/web/LoginPage.tsx` + `LoginPage.module.css`: centered card
  with the app icon (`/icons/icon-192.png`, `alt=""`; it 404s until T8,
  which is fine), heading "Workspaces", fields **Username**
  (`autocomplete="username"`) and **Password** (`type="password"`,
  `autocomplete="current-password"`), button **Log in**, disabled while
  submitting; errors in an `Alert`: "Wrong username or password.",
  "Too many attempts. Try again later.", "Couldn't reach the server.";
  calls `onLoggedIn()` on success. Works at 390 px wide.
- `desktop/src/platform.ts`: `export const isWeb = (): boolean => !isTauri();`
- Tests: `transport/socket.test.ts` (with a fake WebSocket class: hello
  resolves `connectionId`; channel frames reach the right handler;
  result frames resolve/reject invokes; close sends exit to channels and
  rejects pending invokes), `transport/web.test.ts` (mock `fetch`: URL,
  method, headers, body; 422 rejects with the `AppError`; 401 dispatches
  the event; `terminal_write` goes to the socket when open),
  `web/LoginPage.test.tsx` (submits, shows each error, calls
  `onLoggedIn`), `web/WebRoot.test.tsx` (login page when
  unauthenticated, children when authenticated).
- `desktop/test/web-mock.js`: load **after** `tauri-mock.js`. Saves
  `window.__TAURI_INTERNALS__.invoke`, deletes `window.__TAURI_INTERNALS__`,
  sets `window.__WEB_MOCK__ = { authenticated: true, username: "me", calls: [] }`
  (later init scripts may change it), and replaces `window.fetch` for
  `/api/session`, `/api/login` (accepts `me` / `secret`, else 401
  `bad_credentials`), `/api/logout`, `/api/invoke/<command>` (calls the
  saved invoke with the JSON body; a `{__channel}` arg is ignored),
  `/api/push/*` (`vapid` → a fixed key, others → `{}`/204), passing
  other URLs to the real fetch; and `window.WebSocket` with a fake that
  sends `{"type":"hello","connection":"mock"}` and answers invoke
  frames with `{"type":"result","id":…,"ok":true,"value":null}`.

**Modify**

- `desktop/src/api.ts`: drop the `@tauri-apps/api` imports; `call` uses
  `transport().invoke`; `terminalOpen` uses `transport().channel` and
  passes `.arg` as `onEvent`; `onTraySelect` uses `transport().listen`
  in Tauri and returns a no-op unsubscribe in web (T8 replaces it);
  `openExternal` in web mode opens `http(s)` URLs with
  `window.open(url, "_blank", "noopener")` and ignores anything else;
  `reviewOpenReport` in web mode opens `about:blank` first, then sets
  the window's location to the returned path. Nothing else in `api`
  changes.
- `desktop/src/test/setup.ts`: define `window.__TAURI_INTERNALS__ = {}`
  (configurable) before each test, so existing tests stay in Tauri mode;
  web tests delete it.
- `desktop/src/main.tsx`: in web mode wrap `<App />` in `<WebRoot>`.
- `desktop/src/features/agents/EntryPane.tsx`: don't render the Open
  folder / Open in editor buttons when `isWeb()`.
- `desktop/src/features/settings/SettingsList.tsx`: leave out
  `workspaces` when `isWeb()`.
- `desktop/src/features/settings/AboutGroup.tsx`: in web mode a
  **Log out** button calling `logout()`.
- `desktop/README.md`: under "Screenshots without the backend", the web
  mode recipe from the design README "Screenshots".

**Acceptance criteria**

- All existing tests pass unchanged (they run in Tauri mode).
- The new tests above pass.
- With the mocks, the screenshot agent can take: the login page
  (`__WEB_MOCK__.authenticated = false`), the same after a wrong
  password, and the Agents view at 1280×820 and 390×844 (no horizontal
  scroll on the login page at 390).
- `grep -rn "@tauri-apps/api" desktop/src --include=*.ts --include=*.tsx | grep -v test | grep -v src/transport/tauri.ts`
  prints nothing.

**Verify**

```
cd desktop
npx tsc --noEmit
npx vitest run
node --check test/web-mock.js
npm run build
grep -rn "@tauri-apps/api" src --include=*.ts --include=*.tsx | grep -v '\.test\.' | grep -v 'src/transport/tauri.ts' | grep -c . | grep -qx 0
```

---

## T8. Frontend: PWA manifest, service worker, push, notification settings

- **Id:** T8. **Depends on:** T7. **Size:** large. **Parallel with:** T1–T6.

**Read:** design README "Push" › "Payload", "Frontend" › "PWA files" and
"Web mode differences", "Screenshots"; `docs/online.md`
"Notifications"; `desktop/src/transport/socket.ts` and `index.ts`
(from T7); `desktop/src/main.tsx`; `desktop/src/api.ts` `onTraySelect`;
`desktop/src/features/ui/tray.ts`;
`desktop/src/features/settings/SettingsPane.tsx` lines 190–240;
`desktop/src/theme.ts`; `desktop/index.html`; `desktop/test/web-mock.js`.

**Create**

- `desktop/public/manifest.webmanifest` (design README).
- `desktop/public/icons/icon-192.png`
  (`sips -z 192 192 src-tauri/icons/icon.png --out public/icons/icon-192.png`,
  or `magick src-tauri/icons/icon.png -resize 192x192 public/icons/icon-192.png`),
  `icon-256.png` (copy of `src-tauri/icons/128x128@2x.png`),
  `icon-512.png` (copy of `src-tauri/icons/icon.png`).
- `desktop/public/sw.js` (plain JS, design README).
- `desktop/src/web/push.ts`:

  ```ts
  export type PushState = "unsupported" | "insecure" | "denied" | "default" | "granted" | "subscribed";
  export const pushState = async (): Promise<PushState>;     // insecure when !isSecureContext
  export const enablePush = async (): Promise<PushState>;    // permission, GET /api/push/vapid, subscribe, POST /api/push/subscribe {subscription: sub.toJSON(), label}
  export const disablePush = async (): Promise<PushState>;   // POST /api/push/unsubscribe {endpoint}, then unsubscribe()
  export const sendTestPush = async (): Promise<{ sent: number; failed: number }>;
  export const registerServiceWorker = async (): Promise<void>; // navigator.serviceWorker.register("/sw.js"); no-op when unsupported
  export const showInPage = async (payload: PushPayload): Promise<void>; // only when permission granted and not subscribed; registration.showNotification with the same tag
  ```

  `label` is a short browser/OS description from `navigator.userAgent`
  (e.g. "Chrome on macOS"). The VAPID key is decoded from base64url to a
  `Uint8Array` for `applicationServerKey`.
- `desktop/src/web/BrowserPushRow.tsx`: the **This browser** row for the
  Notifications group, using the existing `Row` layout: help text by
  state ("Notifications reach this browser even when the app is closed."
  when subscribed; "Turn on to get notifications here when the app is
  closed." when default/granted; "Blocked in the browser's site
  settings." when denied; "This browser can't receive push
  notifications." when unsupported; "Needs HTTPS (or localhost). See
  docs/online.md." when insecure), buttons **Turn on** / **Turn off** /
  **Send a test** as the state allows, a result line after a test
  ("Sent to 2 devices."), errors in the row's error slot.
- Tests: `web/push.test.ts` (stub `Notification`, `navigator.serviceWorker`
  and `fetch`: each state; enable posts the subscription; disable posts
  the endpoint), `web/BrowserPushRow.test.tsx` (buttons per state, test
  result line).

**Modify**

- `desktop/index.html`: `<link rel="manifest" href="/manifest.webmanifest" />`,
  `<meta name="theme-color" content="<theme background>" />`,
  `<link rel="apple-touch-icon" href="/icons/icon-192.png" />`,
  `<meta name="apple-mobile-web-app-capable" content="yes" />`.
- `desktop/src/main.tsx`: in web mode call `registerServiceWorker()`.
- `desktop/src/api.ts`: web `onTraySelect(handler)`: if the URL has
  `?agent=<key>`, call `handler(key)` once and remove the parameter with
  `history.replaceState`; listen for service worker `message` events
  `{type:"select", key}`; resolve to an unsubscribe.
- `desktop/src/transport/index.ts` (or `main.tsx`): in web mode
  subscribe to the socket's `notify` event and call `showInPage`.
- `desktop/src/features/settings/SettingsPane.tsx`: in web mode render
  `<BrowserPushRow />` as the last row of the Notifications group; the
  help text there says "When one of your agents needs input or is done.
  These switches apply to every browser." in web mode.
- `desktop/test/web-mock.js`: stub `Notification`,
  `Navigator.prototype.serviceWorker` (a fake registration whose
  `pushManager.getSubscription()` and `subscribe()` follow
  `__WEB_MOCK__.push`) and `window.PushManager` according to
  `window.__WEB_MOCK__.push` (`"subscribed"` default, `"default"`,
  `"denied"`, `"unsupported"` deletes them), read at call time.
- `desktop/README.md`: the push states for screenshots.

**Acceptance criteria**

- `npm run build` puts `manifest.webmanifest`, `sw.js` and the three
  icons in `dist/`; the manifest is valid JSON with icons 192, 256, 512.
- `sw.js` never calls `cache.put` for a URL whose path starts with
  `/api` (check by reading it; it's under 120 lines).
- All existing tests and the new ones pass; the Tauri build is unchanged
  (no service worker registered when `isTauri()`).
- With the mocks, the screenshot agent can take Settings ›
  Notifications with the This browser row in the `default` and
  `subscribed` states, at 1280×820 and 390×844.

**Verify**

```
cd desktop
npx tsc --noEmit
npx vitest run
node --check public/sw.js && node --check test/web-mock.js
npm run build
for f in manifest.webmanifest sw.js icons/icon-192.png icons/icon-256.png icons/icon-512.png; do test -f "dist/$f"; done
jq -e '[.icons[].sizes] | index("192x192") and index("512x512")' dist/manifest.webmanifest
file dist/icons/icon-192.png | grep -q '192 x 192'
cd .. && make desktop-test
```

## T9. Frontend: narrow (phone) layout

Added by the orchestrator after the web-mode screenshots: at 390x844 the
Agents view is 479px wide, the top bar's "New agent" button is clipped,
the "N waiting for you" badge wraps onto three lines, and the detail pane
is off-screen. A PWA is used on phones, so the app needs a one-pane layout
below 720px. It applies in both web and Tauri mode (the desktop window can
be narrow too); above 720px nothing changes.

**Depends on:** T8. **Parallel with:** T5, T6 (disjoint files).

**Read:** `desktop/src/App.tsx`, `desktop/src/App.module.css`,
`desktop/src/features/ui/uiSlice.ts`, `desktop/src/features/rail/Rail.module.css`,
and the list components of each section (`Sidebar`, `ReposList`,
`ReviewsList`, `LoopsList`, `SettingsList`) only as far as needed to find
the action each one dispatches when the user picks an item.

**Files:** modify `desktop/src/features/ui/uiSlice.ts` (+ its test),
`desktop/src/App.tsx`, `desktop/src/App.module.css`,
`desktop/src/App.test.tsx`, and at most `desktop/src/features/rail/Rail.module.css`.
No new dependencies.

**Behavior:**

1. `uiSlice` gains `narrowPane: "list" | "detail"` (initial `"list"`) and a
   `narrowBack` action that sets it to `"list"`. `sectionChosen` sets it to
   `"list"`. Picking an item sets it to `"detail"`: `selected` with a
   non-null key, `traySelected` (or whatever the tray/notification select
   action is called), and, via `extraReducers` with `addMatcher`/`addCase`,
   `repoSelected`, `loopSelected`, `creatingStarted` (new loop), the
   reviews/PR selection action, and the settings section choice. Selecting
   `null` sets `"list"`.
2. `App.tsx` puts `data-narrow-pane={narrowPane}` on `.body`, and renders,
   as the first child of `<main>`, a back bar (`<div className={styles.backBar}>`
   holding an MUI `Button` with `startIcon` arrow-back and text "Back",
   accessible name "Back") that dispatches `narrowBack()`.
3. CSS: `.backBar` is `display: none` by default. Under
   `@media (max-width: 720px)`:
   - `.topBar` gets `flex-wrap: wrap; row-gap: 6px; padding: 6px 10px`;
     `.brand` and `.badge` get `white-space: nowrap`; the actions wrap onto
     a second row instead of overflowing.
   - `.body[data-narrow-pane="list"] .main { display: none }` and the
     sidebar becomes `flex: 1; width: auto; min-width: 0` (also for
     `data-section="settings"`).
   - `.body[data-narrow-pane="detail"] .sidebar { display: none }` and
     `.backBar { display: flex }` with a bottom border.
   - Nothing may overflow horizontally: `.root`, `.body` get
     `min-width: 0; max-width: 100vw; overflow-x: hidden` if needed.
4. The terminal (xterm) in the detail pane must refit when it becomes
   visible; if the existing terminal code already refits on resize via a
   ResizeObserver, nothing is needed, otherwise note it in the report.

**Acceptance:**

- uiSlice tests: `narrowPane` transitions for `selected("k")`,
  `selected(null)`, `sectionChosen`, `narrowBack`, `repoSelected`,
  `loopSelected`.
- `App.test.tsx`: the body has `data-narrow-pane="list"` initially and
  `"detail"` after clicking an agent in the list; clicking "Back" returns
  it to `"list"`.
- In a real browser (Playwright with chromium, the web mock as in
  `desktop/README.md`, `npx vite --port 1420`), at 390x844 for Agents,
  Repos, Loops and Settings, in both list and detail state:
  `document.documentElement.scrollWidth <= document.documentElement.clientWidth`,
  every visible button's `getBoundingClientRect()` lies inside the
  viewport, and the badge is one line tall (height < 30px). At 1280x820
  the layout is unchanged (Back bar not displayed, sidebar 300px wide per
  `getComputedStyle`). Put the check results in the report; do not
  commit the script.

**Verify:**

```sh
cd desktop && npx tsc --noEmit && npx vitest run src/App.test.tsx src/features/ui && npm run build
```

## T10. Follow-ups from review

Added by the orchestrator: the reviewers' and implementers' notes from
T1-T9, gathered into one task. Each item is small; do them all.

**Depends on:** T6, T9. **Parallel with:** none.

**Files:** `desktop/src-tauri/src/online/{push,config,auth,server}.rs`,
`desktop/src/transport/socket.ts` (+ `socket.test.ts`),
`desktop/src/main.tsx`, `desktop/src/web/{session.ts,LoginPage.tsx,LoginPage.test.tsx}`,
`desktop/src/platform.ts`, `docs/design/workspace-online/README.md`,
`docs/adrs/0053-workspace-online.md`, this file (T1's verify line only),
`docs/online.md`.

1. **Push key validation (bug).** `Subscriptions::add` in `online/push.rs`
   deserializes a `WebPushBuilder`, which panics on keys of the wrong
   length (generic-array "Slice must be the same length as the array"),
   so `POST /api/push/subscribe` with a bad key drops the connection.
   Before deserializing, decode `keys.p256dh` and `keys.auth` as base64url
   (no padding; also accept padded) and require 65 bytes starting with
   `0x04` and 16 bytes respectively, else return `bad_subscription`. Also
   require `endpoint` to start with `https://`. Tests: short p256dh, short
   auth, bad base64, http endpoint each give `bad_subscription`; and a
   server test in `server.rs` that subscribing with a short p256dh returns
   400 `{"code":"bad_subscription",...}` (the existing error JSON shape).
2. **VAPID subject default.** In `online/config.rs` `vapid_subject`, use
   the public URL as the default only when it starts with `https://`;
   otherwise use `DEFAULT_VAPID_SUBJECT`. Test both cases. Update
   `docs/online.md`'s env table text for `WS_ONLINE_VAPID_SUBJECT`.
3. **Client keepalive.** The server drops a socket silent for 90 s. In
   `transport/socket.ts`, while the socket is open, send `{"type":"ping"}`
   every 30 s; clear the timer on close. Test with fake timers.
4. **No socket before login.** `main.tsx` calls `transport().listen(...)`
   at startup, which opens `/api/ws` before the user has logged in. In web
   mode, register that listener only after the session is authenticated
   (e.g. from `WebRoot` once logged in, or make the socket connect lazily
   only after `getSession()` reports a session). Keep Tauri mode unchanged.
   Add or adjust a test proving no WebSocket is constructed while on the
   login page.
5. **LoginPage** clears the previous error when a new attempt is
   submitted (test it).
6. **Doc comments** on every exported/`pub` item that lacks one:
   `web/session.ts` (`Session`, `LoginResult`, `getSession`, `login`,
   `logout`), `platform.ts` (`isWeb`), `online/auth.rs`
   (`Credentials::new`, `Password` variants), `online/push.rs`
   (`Subscriptions::list`, `Pusher::new`, `Pusher::public_key`,
   `Pusher::subscriptions`, `Delivery` variants).
7. **WebSocket test.** In `online/server.rs` tests, add one test that
   logs in, opens `/api/ws` with the cookie and a matching Origin
   (`tokio-tungstenite` only if it is already a dependency; otherwise use
   reqwest's upgrade or axum's test utilities that are already available;
   do not add a dependency), reads the hello frame, sends an invoke frame
   for a harmless command (one the dispatch table answers without the
   network, e.g. `settings_get` against the temp config), and checks the
   reply frame's id and ok flag. Also check that an upgrade without the
   cookie is refused with 401. If no WebSocket client is available
   without a new dependency, write the 401 check only and say so.
8. **Docs.** Fix "56" to "55" command names in the design README and the
   ADR. Fix T1's verify line in this file to
   `cargo tree --no-default-features --features online -e normal --prefix none | grep -c '^tauri'`
   (expects 0).

**Verify:**

```sh
cd desktop/src-tauri && cargo fmt --check && cargo clippy --no-default-features --features online --all-targets -- -D warnings && cargo clippy --all-targets -- -D warnings && cargo test --no-default-features --features online online::
cd desktop && npx tsc --noEmit && npx vitest run src/transport src/web src/App.test.tsx && npm run build
```

## T11. Terminal copy, paste and links in the browser

Added by the orchestrator after the first deployment: in the web app the
user can't copy text out of a terminal or paste into it, so they can't
complete an MCP server's OAuth sign-in (`/mcp` prints a URL to open, and
some flows want a code or URL pasted back). The terminal is plain xterm.js
(`desktop/src/features/terminals/registry.ts`) with no clipboard or link
handling. Desktop browsers half-work (Cmd+C/Cmd+V via xterm's hidden
textarea); phones and tablets don't work at all: there is no way to select
terminal text or to reach a paste menu. It must work on iOS Safari (an
installed PWA) and Android Chrome as well as desktop browsers, and keep
working in the Tauri app.

**Depends on:** T10. **Parallel with:** none.

**Read:** `desktop/src/features/terminals/{registry.ts,TerminalView.tsx,TerminalView.module.css,ReplyBox.tsx,terminalsSlice.ts}`,
the components that render `TerminalView` (find them with grep), `desktop/src/api.ts`
(`openExternal`, `terminalWrite`), `desktop/src/platform.ts`.

**Files:** modify the files above as needed, plus their tests; create
`desktop/src/features/terminals/TerminalTools.tsx` (+ test) and, if
useful, `desktop/src/features/terminals/clipboard.ts` (+ test). New npm
dependency allowed: `@xterm/addon-web-links` (the stable version that
matches the installed `@xterm/xterm` 6.x; check its peer range). No
other new dependencies.

**Behavior:**

1. **Links.** Load `WebLinksAddon` in `registry.ts`. Activating a link
   calls `api.openExternal(url)` (which opens a new tab in web mode and
   the system browser in Tauri). URLs that wrap across lines must still
   work as far as the addon supports.
2. **Registry API.** Add `registry.selection(key): string` (xterm
   `getSelection()`), `registry.text(key, lines?: number): string`
   (the last `lines` lines of the buffer, default 200, trailing spaces
   trimmed, wrapped lines joined without a newline, using
   `buffer.active.getLine(i).translateToString(true)` and `isWrapped`),
   and `registry.paste(key, text)`, which sends text to the pty the way
   xterm's own paste does: `term.paste(text)` (it applies bracketed
   paste when the app enabled it).
3. **Toolbar** (`TerminalTools`): a small row of icon buttons with
   accessible names, shown above each terminal in both web and Tauri
   mode:
   - **Copy** (enabled when there is a selection; listen to
     `term.onSelectionChange`): `navigator.clipboard.writeText(selection)`,
     then a short "Copied" notice (the existing notice/snackbar
     mechanism). On failure, fall back to the text dialog below with the
     selection preselected.
   - **Paste**: `navigator.clipboard.readText()` and `registry.paste`.
     If the API is missing, or throws (iOS and Firefox often deny it),
     open a **Paste dialog**: an MUI Dialog with a multiline TextField
     (autofocused, so the native long-press / Cmd+V paste menu works on
     every device) and a "Send" button that calls `registry.paste` with
     its contents. "Send" must not add a newline of its own.
   - **Select text**: opens a **Text dialog** showing `registry.text(key)`
     in a read-only multiline TextField (monospace, full width, scrolled
     to the bottom), where native selection and the OS copy menu work on
     phones. It also lists every http(s) URL found in that text as
     clickable links (each calling `api.openExternal`) with a copy
     button per URL. This is what makes the OAuth link usable on a phone.
4. **Keyboard.** With `term.attachCustomKeyEventHandler`, let the browser
   handle Cmd+V / Ctrl+Shift+V (paste) and Cmd+C / Ctrl+Shift+C when
   there is a selection (copy) instead of sending them to the pty: return
   false for those keydown events, and for copy write the selection with
   `navigator.clipboard.writeText`. Plain Ctrl+C must still send SIGINT
   (`\x03`) to the pty.
5. **Phone layout.** Under the existing 720px media query the toolbar
   buttons stay at least 40px tall, and the dialogs are full-screen
   (`fullScreen` prop driven by `useMediaQuery("(max-width:720px)")`).

**Acceptance:**

- Unit tests: `registry.text` joins wrapped lines and trims (using a
  real xterm `Terminal` written to in jsdom, or the registry's existing
  test approach); URL extraction finds `https://` URLs including ones
  with query strings and stops at whitespace and closing brackets/quotes;
  the Paste button falls back to the dialog when `readText` rejects, and
  "Send" calls `terminalWrite` with the pasted text; Copy is disabled
  without a selection; the custom key handler returns false for Cmd+V
  and Ctrl+Shift+V and true for plain Ctrl+C.
- In a real browser (Playwright chromium, vite on 1420 with the web mock
  as in `desktop/README.md`; extend the mock's `terminal_open` to emit
  a line containing `https://claude.ai/oauth/authorize?code=true&client_id=abc&redirect_uri=http%3A%2F%2Flocalhost%3A54545%2Fcallback`
  if it doesn't already emit data): at 1280x820 and 390x844, open a
  terminal, open Select text, check the URL is listed as a link, the
  Paste dialog opens when clipboard read is denied and sending
  "hello" writes it, and no control is outside the viewport. Report the
  results; don't commit the script. Commit the mock change.

**Verify:**

```sh
cd desktop && npx tsc --noEmit && npx vitest run src/features/terminals && npm run build
```
