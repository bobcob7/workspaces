//! The axum router, middleware, WebSocket and notifier loop (ADR 0053).

use super::auth::{self, Credentials, RateLimiter, Sessions};
use super::config::Config;
use super::dispatch::{dispatch, ChannelSink, Ctx, DispatchError};
use super::push::{self, Pusher, Subscriptions, VapidKeys};
use super::wire::{ClientMsg, PushPayload, ServerMsg};
use crate::commands::{self, AppState};
use crate::loops;
use crate::model::Entry;
use crate::pty::Terminals;
use crate::reviews::checker::{Checker, Deps};
use crate::reviews::report;
use crate::reviews::store::{self, Store};
use crate::runner::{AppError, Runner};
use crate::tray::watcher::{wanted, Watcher, WATCH_INTERVAL};
use axum::body::Bytes;
use axum::extract::ws::{Message, WebSocket, WebSocketUpgrade};
use axum::extract::{ConnectInfo, Path, Request, State};
use axum::http::header::{self, HeaderValue};
use axum::http::{HeaderMap, Method, StatusCode};
use axum::middleware::{from_fn, from_fn_with_state, Next};
use axum::response::{IntoResponse, Json, Response};
use axum::routing::{any, get, post};
use axum::Router;
use serde::Deserialize;
use serde_json::{json, Value};
use std::collections::HashMap;
use std::net::SocketAddr;
use std::path::PathBuf;
use std::sync::{Arc, Mutex, PoisonError};
use std::time::Duration;
use tokio::net::TcpListener;
use tokio::sync::mpsc::{self, UnboundedReceiver, UnboundedSender};
use tokio::time::Instant;

const VERSION: &str = env!("CARGO_PKG_VERSION");
const PING_INTERVAL: Duration = Duration::from_secs(30);
const SILENT_LIMIT: Duration = Duration::from_secs(90);
const CONNECTION_HEADER: &str = "x-ws-connection";
const CONTENT_SECURITY_POLICY: &str = "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; worker-src 'self'; manifest-src 'self'; frame-ancestors 'none'";

/// Everything the routes share.
pub struct Online {
    config: Config,
    ctx: Ctx,
    credentials: Credentials,
    sessions: Sessions,
    limiter: RateLimiter,
    pusher: Arc<Pusher>,
    hub: Arc<Hub>,
    notifier: Arc<Notifier>,
}

/// The open WebSocket connections, by id.
#[derive(Default)]
pub struct Hub {
    conns: Mutex<HashMap<String, Arc<Conn>>>,
}

impl Hub {
    fn insert(&self, conn: Arc<Conn>) {
        self.conns().insert(conn.id.clone(), conn);
    }

    fn remove(&self, id: &str) {
        self.conns().remove(id);
    }

    fn get(&self, id: &str) -> Option<Arc<Conn>> {
        self.conns().get(id).cloned()
    }

    fn broadcast(&self, msg: &ServerMsg) {
        for conn in self.conns().values() {
            conn.send(msg.clone());
        }
    }

    fn conns(&self) -> std::sync::MutexGuard<'_, HashMap<String, Arc<Conn>>> {
        self.conns.lock().unwrap_or_else(PoisonError::into_inner)
    }
}

/// One WebSocket: its outgoing frames and the terminals opened through it.
pub struct Conn {
    id: String,
    tx: UnboundedSender<ServerMsg>,
    terminals: Mutex<Vec<u32>>,
}

impl Conn {
    fn new(id: String, tx: UnboundedSender<ServerMsg>) -> Self {
        Self {
            id,
            tx,
            terminals: Mutex::new(Vec::new()),
        }
    }

    fn take_terminals(&self) -> Vec<u32> {
        let mut terminals = self
            .terminals
            .lock()
            .unwrap_or_else(PoisonError::into_inner);
        std::mem::take(&mut *terminals)
    }
}

impl ChannelSink for Conn {
    fn send(&self, msg: ServerMsg) -> bool {
        self.tx.send(msg).is_ok()
    }

    fn terminal_opened(&self, id: u32) {
        self.terminals
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .push(id);
    }
}

/// Turns each listing into notifications for the sockets and the subscriptions.
pub struct Notifier {
    watcher: Arc<Watcher>,
    store: Arc<Store>,
    pusher: Arc<Pusher>,
    hub: Arc<Hub>,
    runner: Runner,
    jobs_dir: Option<PathBuf>,
}

impl Notifier {
    fn observe(&self, entries: &[Entry]) {
        let observed = self.watcher.observe(entries, commands::now_ms());
        let events = wanted(&observed.events, &self.store.get().settings.notifications);
        for payload in push::payloads(&events) {
            let value = serde_json::to_value(&payload).unwrap_or(Value::Null);
            self.hub.broadcast(&ServerMsg::Event {
                event: "notify".into(),
                payload: value,
            });
            let pusher = Arc::clone(&self.pusher);
            tokio::spawn(async move {
                pusher.send_all(&payload).await;
            });
        }
    }

    async fn run(self: Arc<Self>) {
        let mut interval = tokio::time::interval(WATCH_INTERVAL);
        interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
        loop {
            interval.tick().await;
            if !self.watcher.is_stale(commands::now_ms()) {
                continue;
            }
            match commands::list(&self.runner, self.jobs_dir.as_deref()).await {
                Ok(listing) => self.observe(&listing.entries),
                Err(e) => eprintln!("notify: listing failed: {}: {}", e.code, e.message),
            }
        }
    }
}

/// Builds the state, starts the background tasks, and serves until SIGTERM or Ctrl-C.
pub async fn run(config: Config) -> Result<(), String> {
    let addr = config.addr;
    let online = build(config)?;
    let listener = TcpListener::bind(addr)
        .await
        .map_err(|e| format!("bind {addr}: {e}"))?;
    start_background(&online);
    eprintln!("ws-online: listening on {addr}");
    let app = router(Arc::clone(&online)).into_make_service_with_connect_info::<SocketAddr>();
    let served = axum::serve(listener, app)
        .with_graceful_shutdown(shutdown_signal())
        .await;
    online.ctx.state.terminals.close_all();
    served.map_err(|e| e.to_string())
}

/// The router, for run and tests.
pub fn router(online: Arc<Online>) -> Router {
    let public = Router::new()
        .route("/healthz", get(healthz))
        .route("/api/session", get(session))
        .route("/api/login", post(login));
    let protected = Router::new()
        .route("/api/logout", post(logout))
        .route("/api/invoke/{command}", post(invoke))
        .route("/api/ws", get(ws_upgrade))
        .route("/api/reports/{file}", get(report_file))
        .route("/api/push/vapid", get(vapid))
        .route("/api/push/subscribe", post(subscribe))
        .route("/api/push/unsubscribe", post(unsubscribe))
        .route("/api/push/test", post(push_test))
        .route_layer(from_fn_with_state(Arc::clone(&online), session_guard));
    let web_dir = online.config.web_dir.clone();
    let statics = tower_http::services::ServeDir::new(&web_dir).fallback(
        tower_http::services::ServeFile::new(web_dir.join("index.html")),
    );
    Router::new()
        .merge(public)
        .merge(protected)
        .route("/api", any(api_not_found))
        .route("/api/{*rest}", any(api_not_found))
        .fallback_service(statics)
        .layer(from_fn_with_state(Arc::clone(&online), origin_guard))
        .layer(from_fn(security_headers))
        .with_state(online)
}

/// Builds `Online` from a config without starting anything (tests use a temp data dir).
pub fn build(config: Config) -> Result<Arc<Online>, String> {
    let data_dir = config.data_dir.clone();
    let now = commands::now_ms();
    let store = Arc::new(Store::open(Some(data_dir.join(store::FILE_NAME))));
    let state = AppState {
        runner: Runner::new(crate::env::fallback_path()),
        terminals: Terminals::default(),
        jobs_dir: crate::jobs::jobs_dir(),
        store: Arc::clone(&store),
        reviews: Arc::new(Checker::default()),
        reports: report::Cache::default(),
        watcher: Arc::new(Watcher::default()),
        stats: Arc::new(crate::stats::Cache::default()),
    };
    let notifier = Arc::new(Notifier {
        watcher: Arc::clone(&state.watcher),
        store,
        pusher: Arc::new(Pusher::new(
            VapidKeys::load_or_create(&data_dir.join("vapid.json"))
                .map_err(|e| format!("vapid keys: {e}"))?,
            config.vapid_subject.clone(),
            Subscriptions::load(data_dir.join("subscriptions.json")),
        )),
        hub: Arc::new(Hub::default()),
        runner: state.runner.clone(),
        jobs_dir: state.jobs_dir.clone(),
    });
    let credentials = Credentials::new(config.username.clone(), config.password.clone());
    let sessions = Sessions::load(
        data_dir.join("sessions.json"),
        config.session_days,
        credentials.fingerprint(),
        now,
    );
    let listing_notifier = Arc::clone(&notifier);
    let ctx = Ctx {
        state,
        reports_dir: data_dir.join("reports"),
        on_listing: Box::new(move |entries: &[Entry]| listing_notifier.observe(entries)),
        online: Some(config.summary()),
    };
    Ok(Arc::new(Online {
        config,
        ctx,
        credentials,
        sessions,
        limiter: RateLimiter::default(),
        pusher: Arc::clone(&notifier.pusher),
        hub: Arc::clone(&notifier.hub),
        notifier,
    }))
}

fn start_background(online: &Arc<Online>) {
    let state = &online.ctx.state;
    let deps = Deps {
        runner: state.runner.clone(),
        store: state.store.clone(),
        jobs_dir: state.jobs_dir.clone(),
        http: Arc::new(crate::reviews::http::ReqwestHttp::new()),
    };
    tokio::spawn(state.reviews.clone().run(deps));
    tokio::spawn(loops::tick_forever(state.runner.clone()));
    tokio::spawn(Arc::clone(&online.notifier).run());
}

async fn shutdown_signal() {
    let terminate = async {
        match tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate()) {
            Ok(mut signal) => {
                signal.recv().await;
            }
            Err(_) => std::future::pending::<()>().await,
        }
    };
    tokio::select! {
        _ = tokio::signal::ctrl_c() => {}
        _ = terminate => {}
    }
}

impl Online {
    fn is_authenticated(&self, headers: &HeaderMap) -> bool {
        let token = cookie_token_of(headers);
        token.is_some_and(|token| self.sessions.is_valid(token, commands::now_ms()))
    }

    fn origin_ok(&self, headers: &HeaderMap) -> bool {
        auth::origin_allowed(
            header_str(headers, "origin"),
            header_str(headers, "host"),
            self.config.public_origin.as_deref(),
        )
    }

    fn sink_for(&self, headers: &HeaderMap) -> Option<Arc<dyn ChannelSink>> {
        let conn = self.hub.get(header_str(headers, CONNECTION_HEADER)?)?;
        let sink: Arc<dyn ChannelSink> = conn;
        Some(sink)
    }
}

fn header_str<'a>(headers: &'a HeaderMap, name: &str) -> Option<&'a str> {
    headers.get(name).and_then(|value| value.to_str().ok())
}

fn cookie_token_of(headers: &HeaderMap) -> Option<&str> {
    header_str(headers, "cookie").and_then(auth::cookie_token)
}

fn error(status: StatusCode, code: &str, message: &str) -> Response {
    (status, Json(AppError::new(code, message))).into_response()
}

fn dispatch_failure(failure: &DispatchError) -> Response {
    let status =
        StatusCode::from_u16(failure.status()).unwrap_or(StatusCode::INTERNAL_SERVER_ERROR);
    (status, Json(failure.to_app_error())).into_response()
}

async fn origin_guard(State(online): State<Arc<Online>>, request: Request, next: Next) -> Response {
    let guarded = *request.method() == Method::POST || request.uri().path() == "/api/ws";
    if !guarded || online.origin_ok(request.headers()) {
        return next.run(request).await;
    }
    error(
        StatusCode::FORBIDDEN,
        "forbidden_origin",
        "request origin isn't allowed",
    )
}

async fn session_guard(
    State(online): State<Arc<Online>>,
    request: Request,
    next: Next,
) -> Response {
    if online.is_authenticated(request.headers()) {
        return next.run(request).await;
    }
    error(StatusCode::UNAUTHORIZED, "unauthenticated", "log in first")
}

async fn security_headers(request: Request, next: Next) -> Response {
    let path = request.uri().path().to_owned();
    let mut response = next.run(request).await;
    let headers = response.headers_mut();
    headers.insert(
        header::X_CONTENT_TYPE_OPTIONS,
        HeaderValue::from_static("nosniff"),
    );
    headers.insert(
        header::REFERRER_POLICY,
        HeaderValue::from_static("same-origin"),
    );
    headers.insert(header::X_FRAME_OPTIONS, HeaderValue::from_static("DENY"));
    headers.insert(
        header::CONTENT_SECURITY_POLICY,
        HeaderValue::from_static(CONTENT_SECURITY_POLICY),
    );
    if let Some(policy) = cache_policy(&path) {
        headers.insert(header::CACHE_CONTROL, HeaderValue::from_static(policy));
    }
    response
}

fn cache_policy(path: &str) -> Option<&'static str> {
    if path == "/api" || path.starts_with("/api/") {
        return Some("no-store");
    }
    let last = path.rsplit('/').next().unwrap_or_default();
    let revalidate = last.is_empty()
        || !last.contains('.')
        || matches!(last, "index.html" | "sw.js" | "manifest.webmanifest");
    revalidate.then_some("no-cache")
}

async fn api_not_found() -> Response {
    error(StatusCode::NOT_FOUND, "not_found", "no such route")
}

async fn healthz() -> Json<Value> {
    Json(json!({"status": "ok", "version": VERSION}))
}

async fn session(State(online): State<Arc<Online>>, headers: HeaderMap) -> Json<Value> {
    let authenticated = online.is_authenticated(&headers);
    let username = if authenticated {
        Value::String(online.config.username.clone())
    } else {
        Value::Null
    };
    Json(json!({
        "authenticated": authenticated,
        "username": username,
        "version": VERSION,
    }))
}

#[derive(Deserialize)]
struct LoginBody {
    username: String,
    password: String,
}

async fn login(
    State(online): State<Arc<Online>>,
    ConnectInfo(peer): ConnectInfo<SocketAddr>,
    headers: HeaderMap,
    body: Bytes,
) -> Response {
    let now = commands::now_ms();
    let forwarded = header_str(&headers, "x-forwarded-for");
    let client = auth::client_key(peer, forwarded, online.config.trust_proxy);
    if let Err(retry_after) = online.limiter.check(&client, now) {
        let mut response = error(
            StatusCode::TOO_MANY_REQUESTS,
            "rate_limited",
            "too many failed logins; try again later",
        );
        response
            .headers_mut()
            .insert(header::RETRY_AFTER, HeaderValue::from(retry_after));
        return response;
    }
    let Ok(form) = serde_json::from_slice::<LoginBody>(&body) else {
        return error(
            StatusCode::BAD_REQUEST,
            "bad_request",
            "expected a username and password",
        );
    };
    if !online.credentials.verify(&form.username, &form.password) {
        online.limiter.fail(&client, now);
        return error(
            StatusCode::UNAUTHORIZED,
            "bad_credentials",
            "wrong username or password",
        );
    }
    online.limiter.succeed(&client);
    match online.sessions.create(now) {
        Ok(token) => {
            let cookie = auth::session_cookie(
                &token,
                online.config.session_days,
                online.config.secure_cookie,
            );
            (StatusCode::NO_CONTENT, [(header::SET_COOKIE, cookie)]).into_response()
        }
        Err(e) => error(
            StatusCode::INTERNAL_SERVER_ERROR,
            "storage_failed",
            &e.to_string(),
        ),
    }
}

async fn logout(State(online): State<Arc<Online>>, headers: HeaderMap) -> Response {
    if let Some(token) = cookie_token_of(&headers) {
        if let Err(e) = online.sessions.remove(token) {
            return error(
                StatusCode::INTERNAL_SERVER_ERROR,
                "storage_failed",
                &e.to_string(),
            );
        }
    }
    let cookie = auth::clear_cookie(online.config.secure_cookie);
    (StatusCode::NO_CONTENT, [(header::SET_COOKIE, cookie)]).into_response()
}

async fn invoke(
    State(online): State<Arc<Online>>,
    Path(command): Path<String>,
    headers: HeaderMap,
    body: Bytes,
) -> Response {
    let args = match parse_args(&body) {
        Ok(args) => args,
        Err(failure) => return dispatch_failure(&failure),
    };
    let sink = online.sink_for(&headers);
    match dispatch(&online.ctx, &command, args, sink).await {
        Ok(value) => Json(value).into_response(),
        Err(failure) => dispatch_failure(&failure),
    }
}

fn parse_args(body: &[u8]) -> Result<Value, DispatchError> {
    if body.iter().all(u8::is_ascii_whitespace) {
        return Ok(Value::Null);
    }
    serde_json::from_slice(body).map_err(|e| DispatchError::BadArgs(e.to_string()))
}

async fn ws_upgrade(State(online): State<Arc<Online>>, ws: WebSocketUpgrade) -> Response {
    ws.on_upgrade(move |socket| serve_socket(online, socket))
}

/// Runs one socket: hello, frames both ways, and cleanup when it ends.
async fn serve_socket(online: Arc<Online>, mut socket: WebSocket) {
    let (tx, mut outgoing) = mpsc::unbounded_channel();
    let conn = Arc::new(Conn::new(random_hex(), tx));
    online.hub.insert(Arc::clone(&conn));
    let hello = ServerMsg::Hello {
        connection: conn.id.clone(),
    };
    if send_frame(&mut socket, &hello).await {
        pump(&online, &conn, &mut socket, &mut outgoing).await;
    }
    online.hub.remove(&conn.id);
    for id in conn.take_terminals() {
        let _ = online.ctx.state.terminals.close(id);
    }
}

enum Step {
    Outgoing(ServerMsg),
    Frame(Option<Result<Message, axum::Error>>),
    Ping,
    Silent,
}

async fn pump(
    online: &Online,
    conn: &Arc<Conn>,
    socket: &mut WebSocket,
    outgoing: &mut UnboundedReceiver<ServerMsg>,
) {
    let mut ping = tokio::time::interval(PING_INTERVAL);
    let mut last_seen = Instant::now();
    loop {
        let step = tokio::select! {
            Some(msg) = outgoing.recv() => Step::Outgoing(msg),
            frame = socket.recv() => Step::Frame(frame),
            _ = ping.tick() => Step::Ping,
            _ = tokio::time::sleep_until(last_seen + SILENT_LIMIT) => Step::Silent,
        };
        match step {
            Step::Outgoing(msg) => {
                if !send_frame(socket, &msg).await {
                    return;
                }
            }
            Step::Ping => {
                if socket
                    .send(Message::Ping(Default::default()))
                    .await
                    .is_err()
                {
                    return;
                }
            }
            Step::Silent | Step::Frame(None) | Step::Frame(Some(Err(_))) => return,
            Step::Frame(Some(Ok(Message::Close(_)))) => return,
            Step::Frame(Some(Ok(Message::Text(text)))) => {
                last_seen = Instant::now();
                if let Some(reply) = handle_text(online, conn, text.as_str()).await {
                    if !send_frame(socket, &reply).await {
                        return;
                    }
                }
            }
            Step::Frame(Some(Ok(_))) => last_seen = Instant::now(),
        }
    }
}

async fn handle_text(online: &Online, conn: &Arc<Conn>, text: &str) -> Option<ServerMsg> {
    match serde_json::from_str::<ClientMsg>(text) {
        Ok(ClientMsg::Invoke { id, command, args }) => {
            Some(handle_invoke(online, conn, id, command, args).await)
        }
        Ok(ClientMsg::Ping) | Err(_) => None,
    }
}

/// Runs an invoke frame through the same dispatch as `/api/invoke`, bound to `conn`.
pub(crate) async fn handle_invoke(
    online: &Online,
    conn: &Arc<Conn>,
    id: u64,
    command: String,
    args: Value,
) -> ServerMsg {
    let sink: Arc<dyn ChannelSink> = conn.clone();
    match dispatch(&online.ctx, &command, args, Some(sink)).await {
        Ok(value) => ServerMsg::Result {
            id,
            ok: true,
            value: Some(value),
            error: None,
        },
        Err(failure) => ServerMsg::Result {
            id,
            ok: false,
            value: None,
            error: Some(failure.to_app_error()),
        },
    }
}

async fn send_frame(socket: &mut WebSocket, msg: &ServerMsg) -> bool {
    let Ok(text) = serde_json::to_string(msg) else {
        return false;
    };
    socket.send(Message::Text(text.into())).await.is_ok()
}

fn random_hex() -> String {
    let mut bytes = [0u8; 16];
    getrandom::fill(&mut bytes).expect("system random source failed");
    bytes.iter().map(|byte| format!("{byte:02x}")).collect()
}

async fn report_file(State(online): State<Arc<Online>>, Path(file): Path<String>) -> Response {
    if !is_report_name(&file) {
        return error(StatusCode::NOT_FOUND, "not_found", "no such report");
    }
    match std::fs::read(online.ctx.reports_dir.join(&file)) {
        Ok(bytes) => ([(header::CONTENT_TYPE, "text/html; charset=utf-8")], bytes).into_response(),
        Err(_) => error(StatusCode::NOT_FOUND, "not_found", "no such report"),
    }
}

/// `^[A-Za-z0-9._-]+\.html$`, without a regex crate.
fn is_report_name(name: &str) -> bool {
    name.strip_suffix(".html").is_some_and(|stem| {
        !stem.is_empty()
            && stem
                .chars()
                .all(|c| c.is_ascii_alphanumeric() || matches!(c, '.' | '_' | '-'))
    })
}

async fn vapid(State(online): State<Arc<Online>>) -> Json<Value> {
    Json(json!({"publicKey": online.pusher.public_key()}))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct SubscribeBody {
    subscription: Value,
    label: String,
}

async fn subscribe(State(online): State<Arc<Online>>, body: Bytes) -> Response {
    let Ok(form) = serde_json::from_slice::<SubscribeBody>(&body) else {
        return error(
            StatusCode::BAD_REQUEST,
            "bad_request",
            "expected a subscription and a label",
        );
    };
    match online
        .pusher
        .subscriptions()
        .add(form.subscription, &form.label, commands::now_ms())
    {
        Ok(id) => Json(json!({"id": id})).into_response(),
        Err(e) if e.code == "bad_subscription" => {
            (StatusCode::BAD_REQUEST, Json(e)).into_response()
        }
        Err(e) => (StatusCode::UNPROCESSABLE_ENTITY, Json(e)).into_response(),
    }
}

#[derive(Deserialize)]
struct UnsubscribeBody {
    endpoint: String,
}

async fn unsubscribe(State(online): State<Arc<Online>>, body: Bytes) -> Response {
    let Ok(form) = serde_json::from_slice::<UnsubscribeBody>(&body) else {
        return error(
            StatusCode::BAD_REQUEST,
            "bad_request",
            "expected an endpoint",
        );
    };
    match online.pusher.subscriptions().remove(&form.endpoint) {
        Ok(_) => StatusCode::NO_CONTENT.into_response(),
        Err(e) => error(
            StatusCode::INTERNAL_SERVER_ERROR,
            "storage_failed",
            &e.to_string(),
        ),
    }
}

async fn push_test(State(online): State<Arc<Online>>) -> Json<Value> {
    let payload = PushPayload {
        title: "Workspace Online".into(),
        body: "Notifications are working.".into(),
        tag: "test".into(),
        key: None,
        url: "/".into(),
    };
    let (sent, failed) = online.pusher.send_all(&payload).await;
    Json(json!({"sent": sent, "failed": failed}))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::online::config::Password;
    use std::path::Path;
    use tempfile::TempDir;

    fn test_config(dir: &Path) -> Config {
        let web_dir = dir.join("web");
        std::fs::create_dir_all(&web_dir).unwrap();
        std::fs::write(
            web_dir.join("index.html"),
            "<!doctype html><title>ws</title>",
        )
        .unwrap();
        Config {
            username: "me".into(),
            password: Password::Plain("pw".into()),
            addr: "127.0.0.1:0".parse().unwrap(),
            public_origin: None,
            secure_cookie: false,
            session_days: 30,
            trust_proxy: false,
            data_dir: dir.join("data"),
            web_dir,
            vapid_subject: "mailto:ws-online@localhost".into(),
        }
    }

    struct Test {
        base: String,
        client: reqwest::Client,
        _dir: TempDir,
    }

    fn walk(dir: &std::path::Path) -> Vec<std::path::PathBuf> {
        let mut found = Vec::new();
        for entry in std::fs::read_dir(dir).unwrap().flatten() {
            let path = entry.path();
            if path.is_dir() {
                found.extend(walk(&path));
            } else {
                found.push(path);
            }
        }
        found
    }

    impl Test {
        async fn start() -> Self {
            let dir = tempfile::tempdir().unwrap();
            let online = build(test_config(dir.path())).unwrap();
            let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
            let addr = listener.local_addr().unwrap();
            let app = router(online).into_make_service_with_connect_info::<SocketAddr>();
            tokio::spawn(async move {
                axum::serve(listener, app).await.unwrap();
            });
            Self {
                base: format!("http://{addr}"),
                client: reqwest::Client::builder()
                    .redirect(reqwest::redirect::Policy::none())
                    .build()
                    .unwrap(),
                _dir: dir,
            }
        }

        /// Counts the subscriptions persisted under the data dir.
        fn stored_subscriptions(&self) -> usize {
            let path = walk(self._dir.path())
                .into_iter()
                .find(|p| p.file_name().is_some_and(|n| n == "subscriptions.json"))
                .expect("subscriptions.json written");
            crate::online::push::Subscriptions::load(path).list().len()
        }

        fn get(&self, path: &str) -> reqwest::RequestBuilder {
            self.client.get(format!("{}{path}", self.base))
        }

        fn post(&self, path: &str, body: &str) -> reqwest::RequestBuilder {
            self.client
                .post(format!("{}{path}", self.base))
                .header("Origin", &self.base)
                .header("Content-Type", "application/json")
                .body(body.to_string())
        }

        async fn login(&self) -> String {
            let response = self
                .post("/api/login", r#"{"username":"me","password":"pw"}"#)
                .send()
                .await
                .unwrap();
            assert_eq!(response.status(), 204);
            cookie_pair(&response)
        }
    }

    trait JsonBody {
        fn json(self) -> impl std::future::Future<Output = Result<Value, String>>;
    }

    impl JsonBody for reqwest::Response {
        async fn json(self) -> Result<Value, String> {
            let text = self.text().await.map_err(|e| e.to_string())?;
            serde_json::from_str(&text).map_err(|e| e.to_string())
        }
    }

    fn cookie_pair(response: &reqwest::Response) -> String {
        response
            .headers()
            .get("set-cookie")
            .and_then(|value| value.to_str().ok())
            .and_then(|value| value.split(';').next())
            .unwrap_or_default()
            .to_string()
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn healthz_and_session_are_public() {
        let test = Test::start().await;
        let health: Value = test
            .get("/healthz")
            .send()
            .await
            .unwrap()
            .json()
            .await
            .unwrap_or_default();
        assert_eq!(health["status"], "ok");
        let session: Value = test
            .get("/api/session")
            .send()
            .await
            .unwrap()
            .json()
            .await
            .unwrap();
        assert_eq!(session["authenticated"], false);
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn login_checks_origin_and_password() {
        let test = Test::start().await;
        let no_origin = test
            .client
            .post(format!("{}/api/login", test.base))
            .body(r#"{"username":"me","password":"pw"}"#)
            .send()
            .await
            .unwrap();
        assert_eq!(no_origin.status(), 403);
        let foreign = test
            .client
            .post(format!("{}/api/login", test.base))
            .header("Origin", "http://evil.example")
            .body(r#"{"username":"me","password":"pw"}"#)
            .send()
            .await
            .unwrap();
        assert_eq!(foreign.status(), 403);
        let wrong = test
            .post("/api/login", r#"{"username":"me","password":"nope"}"#)
            .send()
            .await
            .unwrap();
        assert_eq!(wrong.status(), 401);
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn login_sets_cookie_and_rate_limits_failures() {
        let test = Test::start().await;
        let ok = test
            .post("/api/login", r#"{"username":"me","password":"pw"}"#)
            .send()
            .await
            .unwrap();
        assert_eq!(ok.status(), 204);
        let cookie = ok
            .headers()
            .get("set-cookie")
            .unwrap()
            .to_str()
            .unwrap()
            .to_string();
        assert!(cookie.contains("HttpOnly"));
        assert!(cookie.contains("SameSite=Strict"));
        assert!(cookie.contains("Path=/"));
        assert!(cookie.contains("Max-Age=2592000"));
        for _ in 0..10 {
            let bad = test
                .post("/api/login", r#"{"username":"me","password":"nope"}"#)
                .send()
                .await
                .unwrap();
            assert_eq!(bad.status(), 401);
        }
        let limited = test
            .post("/api/login", r#"{"username":"me","password":"nope"}"#)
            .send()
            .await
            .unwrap();
        assert_eq!(limited.status(), 429);
        assert!(limited.headers().contains_key("retry-after"));
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn invoke_needs_session_and_maps_errors() {
        let test = Test::start().await;
        let unauthenticated = test
            .post("/api/invoke/open_defaults", "")
            .send()
            .await
            .unwrap();
        assert_eq!(unauthenticated.status(), 401);
        let cookie = test.login().await;
        let ok = test
            .post("/api/invoke/open_defaults", "")
            .header("Cookie", &cookie)
            .send()
            .await
            .unwrap();
        assert_eq!(ok.status(), 200);
        let unknown = test
            .post("/api/invoke/nope", "")
            .header("Cookie", &cookie)
            .send()
            .await
            .unwrap();
        assert_eq!(unknown.status(), 404);
        let body: Value = unknown.json().await.unwrap();
        assert_eq!(body["code"], "unknown_command");
        let unsupported = test
            .post("/api/invoke/open_external", "{}")
            .header("Cookie", &cookie)
            .send()
            .await
            .unwrap();
        assert_eq!(unsupported.status(), 422);
        let missing = test.get("/api/nope").send().await.unwrap();
        assert_eq!(missing.status(), 404);
        let json: Value = missing.json().await.unwrap();
        assert_eq!(json["code"], "not_found");
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn static_routes_serve_index_with_headers() {
        let test = Test::start().await;
        let response = test.get("/some/route").send().await.unwrap();
        assert_eq!(response.status(), 200);
        assert_eq!(response.headers()["x-content-type-options"], "nosniff");
        assert!(response.headers().contains_key("content-security-policy"));
        assert!(response.text().await.unwrap().contains("<title>ws</title>"));
        let cookie = test.login().await;
        let api = test
            .post("/api/invoke/open_defaults", "")
            .header("Cookie", &cookie)
            .send()
            .await
            .unwrap();
        assert_eq!(api.headers()["cache-control"], "no-store");
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn logout_invalidates_the_cookie() {
        let test = Test::start().await;
        let cookie = test.login().await;
        let out = test
            .post("/api/logout", "")
            .header("Cookie", &cookie)
            .send()
            .await
            .unwrap();
        assert_eq!(out.status(), 204);
        let after = test
            .post("/api/invoke/open_defaults", "")
            .header("Cookie", &cookie)
            .send()
            .await
            .unwrap();
        assert_eq!(after.status(), 401);
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn push_routes_round_trip_a_subscription() {
        let test = Test::start().await;
        let cookie = test.login().await;
        let vapid: Value = test
            .get("/api/push/vapid")
            .header("Cookie", &cookie)
            .send()
            .await
            .unwrap()
            .json()
            .await
            .unwrap();
        assert!(vapid["publicKey"]
            .as_str()
            .is_some_and(|key| !key.is_empty()));
        let subscription = r#"{"label":"phone","subscription":{"endpoint":"https://push.example.com/abc","expirationTime":null,"keys":{"p256dh":"BLn9b-VR0ca83knDNZ32dCHGyjJp-1riX9ZTN40MqV8K_LpQmLqxC_DoHvqvFXO_nGdAB4W9dogZb_sM-uV4JbY","auth":"_ordMnz7uTCmrpBTeUV4Bw"}}}"#;
        let added: Value = test
            .post("/api/push/subscribe", subscription)
            .header("Cookie", &cookie)
            .send()
            .await
            .unwrap()
            .json()
            .await
            .unwrap();
        assert!(added["id"].as_str().is_some_and(|id| !id.is_empty()));
        let removed = test
            .post(
                "/api/push/unsubscribe",
                r#"{"endpoint":"https://push.example.com/abc"}"#,
            )
            .header("Cookie", &cookie)
            .send()
            .await
            .unwrap();
        assert_eq!(removed.status(), 204);
        assert_eq!(test.stored_subscriptions(), 0);
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn subscribe_rejects_a_short_p256dh_key() {
        let test = Test::start().await;
        let cookie = test.login().await;
        let subscription = r#"{"label":"phone","subscription":{"endpoint":"https://push.example.com/abc","expirationTime":null,"keys":{"p256dh":"BLn9b","auth":"_ordMnz7uTCmrpBTeUV4Bw"}}}"#;
        let response = test
            .post("/api/push/subscribe", subscription)
            .header("Cookie", &cookie)
            .send()
            .await
            .unwrap();
        assert_eq!(response.status(), 400);
        let body: Value = response.json().await.unwrap();
        assert_eq!(body["code"], "bad_subscription");
        assert!(body["message"].is_string());
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn ws_upgrade_without_a_cookie_is_refused() {
        let test = Test::start().await;
        let response = test
            .get("/api/ws")
            .header("Origin", &test.base)
            .send()
            .await
            .unwrap();
        assert_eq!(response.status(), 401);
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn report_names_are_checked() {
        let test = Test::start().await;
        let cookie = test.login().await;
        for name in ["/api/reports/../x.html", "/api/reports/a.txt"] {
            let response = test
                .get(name)
                .header("Cookie", &cookie)
                .send()
                .await
                .unwrap();
            assert_eq!(response.status(), 404, "{name}");
        }
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn handle_invoke_returns_result_frames() {
        let dir = tempfile::tempdir().unwrap();
        let online = build(test_config(dir.path())).unwrap();
        let (tx, _rx) = mpsc::unbounded_channel();
        let conn = Arc::new(Conn::new("test".into(), tx));
        let ok = handle_invoke(&online, &conn, 1, "open_defaults".into(), Value::Null).await;
        assert!(matches!(
            ok,
            ServerMsg::Result {
                id: 1,
                ok: true,
                ..
            }
        ));
        let unknown = handle_invoke(&online, &conn, 2, "nope".into(), Value::Null).await;
        match unknown {
            ServerMsg::Result {
                ok: false,
                error: Some(error),
                ..
            } => assert_eq!(error.code, "unknown_command"),
            other => panic!("unexpected frame: {other:?}"),
        }
    }

    #[test]
    fn report_name_accepts_only_html_files() {
        assert!(is_report_name("review-1.html"));
        assert!(!is_report_name(".html"));
        assert!(!is_report_name("a.txt"));
        assert!(!is_report_name("a b.html"));
    }

    #[test]
    fn cache_policy_covers_api_and_shell_files() {
        assert_eq!(cache_policy("/api/session"), Some("no-store"));
        assert_eq!(cache_policy("/"), Some("no-cache"));
        assert_eq!(cache_policy("/sw.js"), Some("no-cache"));
        assert_eq!(cache_policy("/assets/app.js"), None);
    }

    #[test]
    fn parse_args_treats_empty_body_as_no_args() {
        assert_eq!(parse_args(b"  ").unwrap(), Value::Null);
        assert!(matches!(parse_args(b"{"), Err(DispatchError::BadArgs(_))));
    }

    #[test]
    fn client_messages_parse_by_type() {
        let parsed: ClientMsg = serde_json::from_str(r#"{"type":"ping"}"#).unwrap();
        assert_eq!(parsed, ClientMsg::Ping);
    }
}
