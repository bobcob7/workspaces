//! Command names and JSON arguments mapped to the command functions (ADR 0053).

use super::wire::ServerMsg;
use crate::about;
use crate::commands::{self, AppState};
use crate::ipc::{Channel, ChannelClosed, State};
use crate::model::Entry;
use crate::open;
use crate::pty::TermSpec;
use crate::reviews;
use crate::runner::AppError;
use crate::{cache, environments, loops, roles, runner_service, secrets, stats, ws_config};
use serde::Serialize;
use serde_json::{Map, Value};
use std::path::PathBuf;
use std::sync::Arc;

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

/// Where a command's channel messages go: the socket that asked.
pub trait ChannelSink: Send + Sync {
    /// Sends one frame; false when the socket is gone.
    fn send(&self, msg: ServerMsg) -> bool;
    /// Records a terminal opened through this socket, to close with it.
    fn terminal_opened(&self, id: u32);
}

/// Receives the entries of each listing.
pub type OnListing = Box<dyn Fn(&[Entry]) + Send + Sync>;

/// What commands run against.
pub struct Ctx {
    /// The app state the commands read.
    pub state: AppState,
    /// Where `review_open_report` writes (DATA/reports).
    pub reports_dir: PathBuf,
    /// Called with each listing `list_entries` returns (the notifier).
    pub on_listing: OnListing,
    /// Workspace Online's settings for Settings › About.
    pub online: Option<about::OnlineSettings>,
}

/// Why a command didn't run, or how it failed.
#[derive(Debug, Clone, PartialEq)]
pub enum DispatchError {
    UnknownCommand(String),
    BadArgs(String),
    NoConnection,
    App(AppError),
}

impl DispatchError {
    /// 404, 400, 400, 422.
    pub fn status(&self) -> u16 {
        match self {
            Self::UnknownCommand(_) => 404,
            Self::BadArgs(_) | Self::NoConnection => 400,
            Self::App(_) => 422,
        }
    }

    /// The error as the frontend shows it.
    pub fn to_app_error(&self) -> AppError {
        match self {
            Self::UnknownCommand(name) => {
                AppError::new("unknown_command", format!("no command named {name}"))
            }
            Self::BadArgs(message) => AppError::new("bad_args", message.clone()),
            Self::NoConnection => {
                AppError::new("no_connection", "this command needs a websocket connection")
            }
            Self::App(error) => error.clone(),
        }
    }
}

/// Every command name, in `generate_handler!` order.
pub const COMMANDS: &[&str] = &[
    "list_entries",
    "cache_repos",
    "cache_list",
    "cache_refs",
    "cache_resolve",
    "cache_fetch",
    "cache_add",
    "cache_remove",
    "create_agent",
    "remove_entry",
    "rename_workspace",
    "versions",
    "set_workspace_hidden",
    "stop_agent",
    "restore_agents",
    "open_external",
    "read_transcript",
    "runner_stop",
    "terminal_open",
    "terminal_write",
    "terminal_resize",
    "terminal_close",
    "reviews_view",
    "reviews_check_now",
    "reviews_load_repos",
    "reviews_set_watch",
    "reviews_set_hosts",
    "reviews_set_token",
    "reviews_dismiss",
    "reviews_restore",
    "reviews_plan",
    "reviews_profiles",
    "reviews_profile_show",
    "reviews_profile_add",
    "reviews_profile_edit",
    "reviews_profile_remove",
    "reviews_start",
    "reviews_ask",
    "review_report",
    "review_decide",
    "review_open_report",
    "review_comment_plan",
    "review_comment_post",
    "review_fix_plan",
    "review_fix_start",
    "review_fix_sent",
    "settings_get",
    "settings_set_reviews",
    "settings_set_notifications",
    "workspace_open",
    "open_defaults",
    "settings_set_open",
    "config_list",
    "config_set",
    "config_unset",
    "runner_settings_get",
    "daemon_status",
    "daemon_restart",
    "runner_check",
    "endpoint_token_set",
    "cache_search",
    "settings_environment",
    "roles_list",
    "roles_show",
    "roles_add",
    "roles_edit",
    "roles_remove",
    "loops_list",
    "loops_add",
    "loops_edit",
    "loops_set_paused",
    "loops_remove",
    "loops_run_now",
    "loops_history",
    "loops_history_run",
    "loops_history_clear",
    "loops_memory",
    "loops_memory_set",
    "loops_memory_remove",
    "loops_memory_clear",
    "stats_get",
    "environments_list",
    "environment_add",
    "environment_rotate",
    "environment_remove",
    "environments_ca_status",
    "environments_ca_rotate",
];

/// Runs `command` with Tauri-style camelCase `args` (`Value::Null` = `{}`).
pub async fn dispatch(
    ctx: &Ctx,
    command: &str,
    args: Value,
    sink: Option<Arc<dyn ChannelSink>>,
) -> Result<Value, DispatchError> {
    let args = if args.is_null() {
        Value::Object(Map::new())
    } else {
        args
    };
    match command {
        "list_entries" => list_entries(ctx).await,
        "cache_repos" => result(commands::cache_repos(State::new(&ctx.state)).await),
        "cache_list" => result(cache::cache_list(State::new(&ctx.state)).await),
        "cache_refs" => {
            let id = args!(args, { id: String });
            result(cache::cache_refs(State::new(&ctx.state), id).await)
        }
        "cache_resolve" => {
            let spec = args!(args, { spec: String });
            result(cache::cache_resolve(State::new(&ctx.state), spec).await)
        }
        "cache_fetch" => {
            let (ids, all) = args!(args, { ids: Vec<String>, all: bool });
            result(cache::cache_fetch(State::new(&ctx.state), ids, all).await)
        }
        "cache_add" => {
            let spec = args!(args, { spec: String });
            result(cache::cache_add(State::new(&ctx.state), spec).await)
        }
        "cache_remove" => {
            let id = args!(args, { id: String });
            result(cache::cache_remove(State::new(&ctx.state), id).await)
        }
        "create_agent" => {
            let request = args!(args, { request: commands::NewAgent });
            result(commands::create_agent(State::new(&ctx.state), request).await)
        }
        "remove_entry" => {
            let (workspace_id, session_id, keep_children, environment) = args!(args, {
                workspace_id: Option<String>,
                session_id: Option<String>,
                keep_children: Option<bool>,
                environment: Option<String>,
            });
            result(
                commands::remove_entry(
                    State::new(&ctx.state),
                    workspace_id,
                    session_id,
                    keep_children,
                    environment,
                )
                .await,
            )
        }
        "rename_workspace" => {
            let (workspace_id, name, environment) = args!(args, {
                workspace_id: String,
                name: String,
                environment: Option<String>,
            });
            result(
                commands::rename_workspace(State::new(&ctx.state), workspace_id, name, environment)
                    .await,
            )
        }
        "versions" => versions(ctx).await,
        "set_workspace_hidden" => {
            let (workspace_id, hidden, environment) = args!(args, {
                workspace_id: String,
                hidden: bool,
                environment: Option<String>,
            });
            result(
                commands::set_workspace_hidden(
                    State::new(&ctx.state),
                    workspace_id,
                    hidden,
                    environment,
                )
                .await,
            )
        }
        "stop_agent" => {
            let (session_id, workspace_id, environment) = args!(args, {
                session_id: String,
                workspace_id: Option<String>,
                environment: Option<String>,
            });
            result(
                commands::stop_agent(
                    State::new(&ctx.state),
                    session_id,
                    workspace_id,
                    environment,
                )
                .await,
            )
        }
        "restore_agents" => result(commands::restore_agents(State::new(&ctx.state)).await),
        "open_external" => unsupported("open_external"),
        "read_transcript" => {
            let (session_id, offset, workspace_id, environment) = args!(args, {
                session_id: String,
                offset: u64,
                workspace_id: Option<String>,
                environment: Option<String>,
            });
            result(
                commands::read_transcript(
                    State::new(&ctx.state),
                    session_id,
                    offset,
                    workspace_id,
                    environment,
                )
                .await,
            )
        }
        "runner_stop" => {
            let (id, environment) = args!(args, { id: String, environment: Option<String> });
            result(commands::runner_stop(State::new(&ctx.state), id, environment).await)
        }
        "terminal_open" => {
            let (spec, cols, rows, on_event) = args!(args, {
                spec: TermSpec,
                cols: u16,
                rows: u16,
                on_event: Value,
            });
            open_terminal(ctx, sink, spec, cols, rows, on_event)
        }
        "terminal_write" => {
            let (id, data) = args!(args, { id: u32, data: String });
            result(commands::terminal_write(State::new(&ctx.state), id, data))
        }
        "terminal_resize" => {
            let (id, cols, rows) = args!(args, { id: u32, cols: u16, rows: u16 });
            result(commands::terminal_resize(
                State::new(&ctx.state),
                id,
                cols,
                rows,
            ))
        }
        "terminal_close" => {
            let id = args!(args, { id: u32 });
            result(commands::terminal_close(State::new(&ctx.state), id))
        }
        "reviews_view" => json(reviews::reviews_view(State::new(&ctx.state))),
        "reviews_check_now" => json(reviews::reviews_check_now(State::new(&ctx.state))),
        "reviews_load_repos" => result(reviews::reviews_load_repos(State::new(&ctx.state)).await),
        "reviews_set_watch" => {
            let (repo, watch, auto_review, environment) = args!(args, {
                repo: String,
                watch: bool,
                auto_review: bool,
                environment: Option<String>,
            });
            result(reviews::reviews_set_watch(
                State::new(&ctx.state),
                repo,
                watch,
                auto_review,
                environment,
            ))
        }
        "reviews_set_hosts" => {
            let hosts = args!(args, { hosts: Vec<reviews::hosts::ConfiguredHost> });
            result(reviews::reviews_set_hosts(State::new(&ctx.state), hosts).await)
        }
        "reviews_set_token" => {
            let (host, token) = args!(args, { host: String, token: String });
            result(reviews::reviews_set_token(
                State::new(&ctx.state),
                host,
                token,
            ))
        }
        "reviews_dismiss" => {
            let (repo, number, head) = args!(args, { repo: String, number: u64, head: String });
            result(reviews::reviews_dismiss(
                State::new(&ctx.state),
                repo,
                number,
                head,
            ))
        }
        "reviews_restore" => {
            let (repo, number) = args!(args, { repo: String, number: u64 });
            result(reviews::reviews_restore(
                State::new(&ctx.state),
                repo,
                number,
            ))
        }
        "reviews_plan" => {
            let (repo, number, environment) =
                args!(args, { repo: String, number: u64, environment: Option<String> });
            result(reviews::reviews_plan(State::new(&ctx.state), repo, number, environment).await)
        }
        "reviews_ask" => {
            let (repo, number) = args!(args, { repo: String, number: u64 });
            result(reviews::reviews_ask(State::new(&ctx.state), repo, number).await)
        }
        "reviews_profiles" => {
            let environment = args!(args, { environment: Option<String> });
            result(reviews::reviews_profiles(State::new(&ctx.state), environment).await)
        }
        "reviews_profile_show" => {
            let name = args!(args, { name: String });
            result(reviews::reviews_profile_show(State::new(&ctx.state), name).await)
        }
        "reviews_profile_add" => {
            let (name, draft) =
                args!(args, { name: String, draft: reviews::profiles::ProfileDraft });
            result(reviews::reviews_profile_add(State::new(&ctx.state), name, draft).await)
        }
        "reviews_profile_edit" => {
            let (name, draft) =
                args!(args, { name: String, draft: reviews::profiles::ProfileDraft });
            result(reviews::reviews_profile_edit(State::new(&ctx.state), name, draft).await)
        }
        "reviews_profile_remove" => {
            let name = args!(args, { name: String });
            result(reviews::reviews_profile_remove(State::new(&ctx.state), name).await)
        }
        "reviews_start" => {
            let plan = args!(args, { plan: reviews::plan::ReviewPlan });
            result(reviews::reviews_start(State::new(&ctx.state), plan).await)
        }
        "review_report" => {
            let (repo, git_ref, refresh, environment, review_id) = args!(args, {
                repo: String,
                git_ref: String,
                refresh: bool,
                environment: Option<String>,
                review_id: Option<String>,
            });
            result(
                reviews::review_report(
                    State::new(&ctx.state),
                    repo,
                    git_ref,
                    refresh,
                    environment,
                    review_id,
                )
                .await,
            )
        }
        "review_decide" => {
            let (repo, git_ref, request, environment, review_id) = args!(args, {
                repo: String,
                git_ref: String,
                request: reviews::report::DecideRequest,
                environment: Option<String>,
                review_id: Option<String>,
            });
            result(
                reviews::review_decide(
                    State::new(&ctx.state),
                    repo,
                    git_ref,
                    request,
                    environment,
                    review_id,
                )
                .await,
            )
        }
        "review_open_report" => {
            let (repo, git_ref, environment, review_id) = args!(args, {
                repo: String,
                git_ref: String,
                environment: Option<String>,
                review_id: Option<String>,
            });
            open_report(
                ctx,
                &repo,
                &git_ref,
                environment.as_deref(),
                review_id.as_deref(),
            )
            .await
        }
        "review_comment_plan" => {
            let (repo, git_ref, number, environment, review_id) = args!(args, {
                repo: String,
                git_ref: String,
                number: u64,
                environment: Option<String>,
                review_id: Option<String>,
            });
            result(
                reviews::review_comment_plan(
                    State::new(&ctx.state),
                    repo,
                    git_ref,
                    number,
                    environment,
                    review_id,
                )
                .await,
            )
        }
        "review_comment_post" => {
            let (repo, git_ref, request, environment, review_id) = args!(args, {
                repo: String,
                git_ref: String,
                request: reviews::comment::PostRequest,
                environment: Option<String>,
                review_id: Option<String>,
            });
            result(
                reviews::review_comment_post(
                    State::new(&ctx.state),
                    repo,
                    git_ref,
                    request,
                    environment,
                    review_id,
                )
                .await,
            )
        }
        "review_fix_plan" => {
            let (repo, git_ref, review, findings, environment, review_id) = args!(args, {
                repo: String,
                git_ref: String,
                review: String,
                findings: Vec<reviews::fix::FixFinding>,
                environment: Option<String>,
                review_id: Option<String>,
            });
            result(
                reviews::review_fix_plan(
                    State::new(&ctx.state),
                    repo,
                    git_ref,
                    review,
                    findings,
                    environment,
                    review_id,
                )
                .await,
            )
        }
        "review_fix_start" => {
            let (repo, git_ref, request, environment, review_id) = args!(args, {
                repo: String,
                git_ref: String,
                request: reviews::fix::StartFix,
                environment: Option<String>,
                review_id: Option<String>,
            });
            result(
                reviews::review_fix_start(
                    State::new(&ctx.state),
                    repo,
                    git_ref,
                    request,
                    environment,
                    review_id,
                )
                .await,
            )
        }
        "review_fix_sent" => {
            let (repo, git_ref, findings, agent, environment, review_id) = args!(args, {
                repo: String,
                git_ref: String,
                findings: Vec<String>,
                agent: String,
                environment: Option<String>,
                review_id: Option<String>,
            });
            result(
                reviews::review_fix_sent(
                    State::new(&ctx.state),
                    repo,
                    git_ref,
                    findings,
                    agent,
                    environment,
                    review_id,
                )
                .await,
            )
        }
        "settings_get" => json(reviews::settings_get(State::new(&ctx.state))),
        "settings_set_reviews" => {
            let reviews = args!(args, { reviews: reviews::store::ReviewSettings });
            result(reviews::settings_set_reviews(
                State::new(&ctx.state),
                reviews,
            ))
        }
        "settings_set_notifications" => {
            let notifications =
                args!(args, { notifications: reviews::store::NotificationSettings });
            result(reviews::settings_set_notifications(
                State::new(&ctx.state),
                notifications,
            ))
        }
        "workspace_open" => unsupported("workspace_open"),
        "open_defaults" => json(open::open_defaults()),
        "settings_set_open" => {
            let settings = args!(args, { open: reviews::store::OpenSettings });
            result(open::settings_set_open(State::new(&ctx.state), settings))
        }
        "config_list" => result(ws_config::config_list(State::new(&ctx.state)).await),
        "config_set" => {
            let (key, values) = args!(args, { key: String, values: Vec<String> });
            result(ws_config::config_set(State::new(&ctx.state), key, values).await)
        }
        "config_unset" => {
            let key = args!(args, { key: String });
            result(ws_config::config_unset(State::new(&ctx.state), key).await)
        }
        "runner_settings_get" => {
            result(ws_config::runner_settings_get(State::new(&ctx.state)).await)
        }
        "daemon_status" => result(runner_service::daemon_status(State::new(&ctx.state)).await),
        "daemon_restart" => result(runner_service::daemon_restart(State::new(&ctx.state)).await),
        "runner_check" => {
            let (endpoint, model) = args!(args, { endpoint: String, model: String });
            result(runner_service::runner_check(State::new(&ctx.state), endpoint, model).await)
        }
        "endpoint_token_set" => {
            let (name, token) = args!(args, { name: String, token: String });
            result(secrets::endpoint_token_set(name, token).await)
        }
        "cache_search" => {
            let (query, refs, limit) = args!(args, { query: String, refs: bool, limit: u32 });
            result(cache::cache_search(State::new(&ctx.state), query, refs, limit).await)
        }
        "settings_environment" => json(about::environment(&ctx.state, ctx.online.clone())),
        "roles_list" => result(roles::roles_list(State::new(&ctx.state)).await),
        "roles_show" => {
            let name = args!(args, { name: String });
            result(roles::roles_show(State::new(&ctx.state), name).await)
        }
        "roles_add" => {
            let (name, draft) = args!(args, { name: String, draft: roles::RoleDraft });
            result(roles::roles_add(State::new(&ctx.state), name, draft).await)
        }
        "roles_edit" => {
            let (name, draft) = args!(args, { name: String, draft: roles::RoleDraft });
            result(roles::roles_edit(State::new(&ctx.state), name, draft).await)
        }
        "roles_remove" => {
            let name = args!(args, { name: String });
            result(roles::roles_remove(State::new(&ctx.state), name).await)
        }
        "loops_list" => result(loops::loops_list(State::new(&ctx.state)).await),
        "loops_add" => {
            let request = args!(args, { request: loops::NewLoop });
            result(loops::loops_add(State::new(&ctx.state), request).await)
        }
        "loops_edit" => {
            let request = args!(args, { request: loops::NewLoop });
            result(loops::loops_edit(State::new(&ctx.state), request).await)
        }
        "loops_set_paused" => {
            let (name, paused) = args!(args, { name: String, paused: bool });
            result(loops::loops_set_paused(State::new(&ctx.state), name, paused).await)
        }
        "loops_remove" => {
            let name = args!(args, { name: String });
            result(loops::loops_remove(State::new(&ctx.state), name).await)
        }
        "loops_run_now" => {
            let name = args!(args, { name: String });
            result(loops::loops_run_now(State::new(&ctx.state), name).await)
        }
        "loops_history" => {
            let (name, limit) = args!(args, { name: String, limit: Option<u32> });
            result(loops::loops_history(State::new(&ctx.state), name, limit).await)
        }
        "loops_history_run" => {
            let (name, seq) = args!(args, { name: String, seq: u32 });
            result(loops::loops_history_run(State::new(&ctx.state), name, seq).await)
        }
        "loops_history_clear" => {
            let name = args!(args, { name: String });
            result(loops::loops_history_clear(State::new(&ctx.state), name).await)
        }
        "loops_memory" => {
            let name = args!(args, { name: String });
            result(loops::loops_memory(State::new(&ctx.state), name).await)
        }
        "loops_memory_set" => {
            let (name, key, value, expect_rev) = args!(args, {
                name: String,
                key: String,
                value: String,
                expect_rev: Option<i64>,
            });
            result(
                loops::loops_memory_set(State::new(&ctx.state), name, key, value, expect_rev).await,
            )
        }
        "loops_memory_remove" => {
            let (name, key) = args!(args, { name: String, key: String });
            result(loops::loops_memory_remove(State::new(&ctx.state), name, key).await)
        }
        "loops_memory_clear" => {
            let name = args!(args, { name: String });
            result(loops::loops_memory_clear(State::new(&ctx.state), name).await)
        }
        "stats_get" => {
            let since_ms = args!(args, { since_ms: Option<i64> });
            result(stats::stats_get(State::new(&ctx.state), since_ms).await)
        }
        "environments_list" => {
            result(environments::environments_list(State::new(&ctx.state)).await)
        }
        "environment_add" => {
            let name = args!(args, { name: String });
            result(environments::environment_add(State::new(&ctx.state), name).await)
        }
        "environment_rotate" => {
            let name = args!(args, { name: String });
            result(environments::environment_rotate(State::new(&ctx.state), name).await)
        }
        "environment_remove" => {
            let name = args!(args, { name: String });
            result(environments::environment_remove(State::new(&ctx.state), name).await)
        }
        "environments_ca_status" => {
            result(environments::environments_ca_status(State::new(&ctx.state)).await)
        }
        "environments_ca_rotate" => {
            let (finish, force) = args!(args, { finish: bool, force: bool });
            result(
                environments::environments_ca_rotate(State::new(&ctx.state), finish, force).await,
            )
        }
        _ => Err(DispatchError::UnknownCommand(command.to_string())),
    }
}

async fn list_entries(ctx: &Ctx) -> Result<Value, DispatchError> {
    let listing = commands::list(&ctx.state.runner, ctx.state.jobs_dir.as_deref())
        .await
        .map_err(DispatchError::App)?;
    ctx.state.reviews.set_entries(listing.entries.clone());
    (ctx.on_listing)(&listing.entries);
    json(listing)
}

async fn versions(ctx: &Ctx) -> Result<Value, DispatchError> {
    let app = about::app_version(env!("CARGO_PKG_VERSION"));
    json(about::versions(&ctx.state.runner, app).await)
}

async fn open_report(
    ctx: &Ctx,
    repo: &str,
    git_ref: &str,
    environment: Option<&str>,
    review_id: Option<&str>,
) -> Result<Value, DispatchError> {
    let origin =
        reviews::report::Origin::new(environment, review_id).map_err(DispatchError::App)?;
    let path =
        reviews::report::write_html(&ctx.state.runner, &ctx.reports_dir, repo, git_ref, origin)
            .await
            .map_err(DispatchError::App)?;
    let name = path
        .file_name()
        .and_then(|n| n.to_str())
        .unwrap_or_default();
    json(format!("/api/reports/{name}"))
}

fn open_terminal(
    ctx: &Ctx,
    sink: Option<Arc<dyn ChannelSink>>,
    spec: TermSpec,
    cols: u16,
    rows: u16,
    on_event: Value,
) -> Result<Value, DispatchError> {
    let id = on_event["__channel"]
        .as_u64()
        .ok_or_else(|| DispatchError::BadArgs("onEvent needs a __channel id".to_string()))?;
    let sink = sink.ok_or(DispatchError::NoConnection)?;
    let frames = Arc::clone(&sink);
    let events = Channel::new(move |payload| {
        if frames.send(ServerMsg::Channel { id, payload }) {
            Ok(())
        } else {
            Err(ChannelClosed)
        }
    });
    let terminal = commands::terminal_open(State::new(&ctx.state), spec, cols, rows, events)
        .map_err(DispatchError::App)?;
    sink.terminal_opened(terminal);
    json(terminal)
}

fn unsupported(command: &str) -> Result<Value, DispatchError> {
    Err(DispatchError::App(AppError::new(
        "unsupported",
        format!("{command} isn't available in Workspace Online"),
    )))
}

fn json<T: Serialize>(value: T) -> Result<Value, DispatchError> {
    serde_json::to_value(value)
        .map_err(|e| DispatchError::App(AppError::new("internal", e.to_string())))
}

fn result<T: Serialize>(outcome: Result<T, AppError>) -> Result<Value, DispatchError> {
    json(outcome.map_err(DispatchError::App)?)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::pty::Terminals;
    use crate::reviews::checker::Checker;
    use crate::reviews::report;
    use crate::reviews::store::Store;
    use crate::tray::watcher::Watcher;
    use std::os::unix::fs::PermissionsExt;
    use std::sync::Mutex;

    /// A directory holding fake `ws` and `claude` scripts, for PATH.
    fn fake_programs(ws: &str, claude: &str) -> tempfile::TempDir {
        let dir = tempfile::tempdir().unwrap();
        for (name, body) in [("ws", ws), ("claude", claude)] {
            let path = dir.path().join(name);
            std::fs::write(&path, format!("#!/bin/sh\n{body}\n")).unwrap();
            std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o755)).unwrap();
        }
        dir
    }

    fn ctx_with(ws: &str, claude: &str, on_listing: OnListing) -> (tempfile::TempDir, Ctx) {
        let programs = fake_programs(ws, claude);
        let reports = tempfile::tempdir().unwrap();
        let state = AppState {
            runner: crate::runner::Runner::new(programs.path().display().to_string()),
            terminals: Terminals::default(),
            jobs_dir: None,
            store: Arc::new(Store::open(None)),
            reviews: Arc::new(Checker::default()),
            reports: report::Cache::default(),
            watcher: Arc::new(Watcher::default()),
            stats: Arc::new(stats::Cache::default()),
        };
        let ctx = Ctx {
            state,
            reports_dir: reports.path().to_path_buf(),
            on_listing,
            online: None,
        };
        (programs, ctx)
    }

    #[derive(Default)]
    struct Recorder {
        frames: Mutex<Vec<ServerMsg>>,
        opened: Mutex<Vec<u32>>,
    }

    impl ChannelSink for Recorder {
        fn send(&self, msg: ServerMsg) -> bool {
            self.frames.lock().unwrap().push(msg);
            true
        }

        fn terminal_opened(&self, id: u32) {
            self.opened.lock().unwrap().push(id);
        }
    }

    #[test]
    fn commands_match_generate_handler() {
        let source = include_str!("../lib.rs");
        let start = source.find("generate_handler![").unwrap() + "generate_handler![".len();
        let end = start + source[start..].find(']').unwrap();
        let names: Vec<&str> = source[start..end]
            .lines()
            .map(str::trim)
            .filter(|line| !line.is_empty())
            .map(|line| line.trim_end_matches(',').rsplit("::").next().unwrap())
            .collect();
        assert_eq!(names, COMMANDS);
    }

    #[tokio::test]
    async fn every_command_is_handled() {
        let (_dir, ctx) = ctx_with("exit 1", "exit 1", Box::new(|_| {}));
        for command in COMMANDS {
            let outcome = dispatch(&ctx, command, Value::Null, None).await;
            assert!(
                !matches!(outcome, Err(DispatchError::UnknownCommand(_))),
                "{command} isn't handled"
            );
        }
    }

    #[tokio::test]
    async fn unknown_command_is_rejected() {
        let (_dir, ctx) = ctx_with("exit 1", "exit 1", Box::new(|_| {}));
        let err = dispatch(&ctx, "nope", Value::Null, None).await.unwrap_err();
        assert_eq!(err, DispatchError::UnknownCommand("nope".to_string()));
        assert_eq!(err.status(), 404);
    }

    #[tokio::test]
    async fn rename_workspace_runs_ws_with_camel_case_args() {
        let (_dir, ctx) = ctx_with(
            r#"echo '{"id":"x-d8efb24f","name":"draft"}'"#,
            "exit 1",
            Box::new(|_| {}),
        );
        let args = serde_json::json!({"workspaceId": "x-d8efb24f", "name": "draft"});
        let value = dispatch(&ctx, "rename_workspace", args, None)
            .await
            .unwrap();
        assert_eq!(
            value,
            serde_json::json!({"id": "x-d8efb24f", "name": "draft"})
        );
    }

    #[tokio::test]
    async fn remove_entry_accepts_environment() {
        let (_dir, ctx) = ctx_with(r#"echo "$@" >&2; exit 1"#, "exit 1", Box::new(|_| {}));
        let args = serde_json::json!({"workspaceId": "fix-1", "environment": "linux-server"});
        let err = dispatch(&ctx, "remove_entry", args, None).await;
        let Err(DispatchError::App(err)) = err else {
            panic!("expected the fake ws's failure, got {err:?}");
        };
        assert_eq!(err.message, "rm --json --env=linux-server -- fix-1");
    }

    /// A context whose fake `ws` and `claude` log that they ran, and the log.
    fn logging_ctx() -> (tempfile::TempDir, Ctx, PathBuf) {
        let log = tempfile::tempdir().unwrap().keep().join("ran.log");
        let body = format!("echo ran >> {}", log.display());
        let (dir, ctx) = ctx_with(&body, &body, Box::new(|_| {}));
        (dir, ctx, log)
    }

    async fn app_error(ctx: &Ctx, command: &str, args: Value) -> AppError {
        match dispatch(ctx, command, args, None).await {
            Err(DispatchError::App(err)) => err,
            other => panic!("expected an app error, got {other:?}"),
        }
    }

    #[tokio::test]
    async fn local_only_commands_refuse_a_remote_environment_without_running_anything() {
        let (_dir, ctx, log) = logging_ctx();
        let calls = [
            (
                "read_transcript",
                serde_json::json!({"sessionId": "s", "offset": 0, "workspaceId": "w", "environment": "linux-server"}),
            ),
            (
                "runner_stop",
                serde_json::json!({"id": "w", "environment": "linux-server"}),
            ),
            (
                "remove_entry",
                serde_json::json!({"sessionId": "s", "environment": "linux-server"}),
            ),
        ];
        for (command, args) in calls {
            let err = app_error(&ctx, command, args).await;
            assert_eq!(err.code, "local_only", "{command}");
        }
        assert!(!log.exists());
    }

    #[tokio::test]
    async fn set_workspace_hidden_passes_the_environment() {
        let (_dir, ctx) = ctx_with(r#"echo "$@" >&2; exit 1"#, "exit 1", Box::new(|_| {}));
        let args = serde_json::json!({"workspaceId": "fix-1", "hidden": true, "environment": "linux-server"});
        let err = app_error(&ctx, "set_workspace_hidden", args).await;
        assert_eq!(err.message, "hide --json --env=linux-server -- fix-1");
    }

    #[tokio::test]
    async fn rename_workspace_passes_the_environment() {
        let (_dir, ctx) = ctx_with(r#"echo "$@" >&2; exit 1"#, "exit 1", Box::new(|_| {}));
        let args =
            serde_json::json!({"workspaceId": "fix-1", "name": "n", "environment": "linux-server"});
        let err = app_error(&ctx, "rename_workspace", args).await;
        assert_eq!(err.message, "rename --json --env=linux-server -- fix-1 n");
    }

    #[tokio::test]
    async fn stop_agent_passes_the_environment_and_workspace() {
        let (_dir, ctx) = ctx_with(r#"echo "$@" >&2; exit 1"#, "exit 1", Box::new(|_| {}));
        let args = serde_json::json!({"sessionId": "s", "workspaceId": "fix-1", "environment": "linux-server"});
        let err = app_error(&ctx, "stop_agent", args).await;
        assert_eq!(err.message, "stop --json --env=linux-server -- fix-1");
    }

    #[tokio::test]
    async fn stop_agent_reports_an_agent_that_had_already_ended() {
        let ended = r#"echo '{"stopped":[{"workspace":"fix-1","session":"s","stopped":false,"environment":"linux-server"}]}'"#;
        let (_dir, ctx) = ctx_with(ended, "exit 1", Box::new(|_| {}));
        let args = serde_json::json!({"sessionId": "s", "workspaceId": "fix-1", "environment": "linux-server"});
        assert_eq!(
            app_error(&ctx, "stop_agent", args).await.code,
            "not_stopped"
        );
    }

    #[tokio::test]
    async fn stop_agent_succeeds_when_the_remote_agent_stopped() {
        let stopped = r#"echo '{"stopped":[{"workspace":"fix-1","session":"s","stopped":true,"environment":"linux-server"}]}'"#;
        let (_dir, ctx) = ctx_with(stopped, "exit 1", Box::new(|_| {}));
        let args = serde_json::json!({"sessionId": "s", "workspaceId": "fix-1", "environment": "linux-server"});
        assert!(dispatch(&ctx, "stop_agent", args, None).await.is_ok());
    }

    #[tokio::test]
    async fn stop_agent_with_a_blank_workspace_id_is_bad_args() {
        let (_dir, ctx, log) = logging_ctx();
        let args =
            serde_json::json!({"sessionId": "s", "workspaceId": "", "environment": "linux-server"});
        assert_eq!(app_error(&ctx, "stop_agent", args).await.code, "bad_args");
        assert!(!log.exists());
    }

    #[tokio::test]
    async fn reviews_set_watch_accepts_environment() {
        let (_dir, ctx) = ctx_with("exit 1", "exit 1", Box::new(|_| {}));
        let repo = "github.com/o/r";
        let args = serde_json::json!({"repo": repo, "watch": true, "autoReview": true, "environment": "linux-server"});
        assert!(dispatch(&ctx, "reviews_set_watch", args, None)
            .await
            .is_ok());
        let watch = ctx.state.store.get().reviews.watch(repo);
        assert_eq!(watch.environment.as_deref(), Some("linux-server"));
        assert!(watch.auto_review);
        let old = serde_json::json!({"repo": repo, "watch": true, "autoReview": false});
        assert!(dispatch(&ctx, "reviews_set_watch", old, None).await.is_ok());
        assert_eq!(ctx.state.store.get().reviews.watch(repo).environment, None);
        let local = serde_json::json!({"repo": repo, "watch": true, "autoReview": false, "environment": "local"});
        assert!(dispatch(&ctx, "reviews_set_watch", local, None)
            .await
            .is_ok());
        assert_eq!(ctx.state.store.get().reviews.watch(repo).environment, None);
    }

    #[tokio::test]
    async fn moving_a_watched_repo_to_another_environment_requests_a_check() {
        let (_dir, ctx) = ctx_with("exit 1", "exit 1", Box::new(|_| {}));
        let repo = "github.com/o/r";
        let watch = |environment: serde_json::Value| serde_json::json!({"repo": repo, "watch": true, "autoReview": false, "environment": environment});
        dispatch(
            &ctx,
            "reviews_set_watch",
            watch("linux-server".into()),
            None,
        )
        .await
        .unwrap();
        assert!(ctx.state.reviews.take_check_request());
        dispatch(
            &ctx,
            "reviews_set_watch",
            watch("linux-server".into()),
            None,
        )
        .await
        .unwrap();
        assert!(!ctx.state.reviews.take_check_request());
        dispatch(&ctx, "reviews_set_watch", watch("other-box".into()), None)
            .await
            .unwrap();
        assert!(ctx.state.reviews.take_check_request());
    }

    #[tokio::test]
    async fn reviews_set_watch_refuses_an_environment_name_ws_would_misread() {
        let (_dir, ctx, log) = logging_ctx();
        let repo = "github.com/o/r";
        for name in ["-x", "--env=a", "a b"] {
            let args = serde_json::json!({"repo": repo, "watch": true, "autoReview": false, "environment": name});
            let err = app_error(&ctx, "reviews_set_watch", args).await;
            assert_eq!(err.code, "bad_args", "{name:?}");
        }
        assert!(!ctx.state.store.get().reviews.watch(repo).watch);
        assert!(!log.exists());
    }

    #[tokio::test]
    async fn reviews_plan_fills_the_environment_the_repo_is_watched_on() {
        let (_dir, ctx) = ctx_with("exit 1", "exit 1", Box::new(|_| {}));
        let repo = "github.com/bobcob7/buff-bear";
        ctx.state
            .reviews
            .set_pull_requests(repo, vec![crate::reviews::view::tests::pr(42)]);
        let watch = serde_json::json!({"repo": repo, "watch": true, "autoReview": false, "environment": "linux-server"});
        dispatch(&ctx, "reviews_set_watch", watch, None)
            .await
            .unwrap();
        let args = serde_json::json!({"repo": repo, "number": 42});
        let plan = dispatch(&ctx, "reviews_plan", args, None).await.unwrap();
        assert_eq!(plan["environment"], "linux-server");
        let named = serde_json::json!({"repo": repo, "number": 42, "environment": "local"});
        let plan = dispatch(&ctx, "reviews_plan", named, None).await.unwrap();
        assert!(plan["environment"].is_null());
    }

    #[tokio::test]
    async fn review_report_without_environment_is_local() {
        let (_dir, ctx) = ctx_with(r#"echo "$@" >&2; exit 1"#, "exit 1", Box::new(|_| {}));
        let args = serde_json::json!({"repo": "github.com/o/r", "gitRef": "abc", "refresh": true});
        let err = app_error(&ctx, "review_report", args).await;
        assert_eq!(
            err.message,
            "review report --json --repo github.com/o/r --ref abc"
        );
    }

    #[tokio::test]
    async fn review_report_with_environment_names_the_review() {
        let (_dir, ctx) = ctx_with(r#"echo "$@" >&2; exit 1"#, "exit 1", Box::new(|_| {}));
        let args = serde_json::json!({"repo": "github.com/o/r", "gitRef": "abc", "refresh": true, "environment": "linux-server", "reviewId": "review-1"});
        let err = app_error(&ctx, "review_report", args).await;
        assert_eq!(
            err.message,
            "review report --json --env=linux-server -- review-1"
        );
    }

    #[tokio::test]
    async fn a_remote_review_call_without_a_review_id_is_bad_args() {
        let (_dir, ctx, log) = logging_ctx();
        let calls = [
            (
                "review_report",
                serde_json::json!({"repo": "r", "gitRef": "a", "refresh": false, "environment": "linux-server"}),
            ),
            (
                "review_decide",
                serde_json::json!({"repo": "r", "gitRef": "a", "request": {"findings": ["a"], "decision": "skip"}, "environment": "linux-server"}),
            ),
            (
                "review_open_report",
                serde_json::json!({"repo": "r", "gitRef": "a", "environment": "linux-server"}),
            ),
            (
                "review_fix_sent",
                serde_json::json!({"repo": "r", "gitRef": "a", "findings": ["a"], "agent": "x", "environment": "linux-server"}),
            ),
            (
                "review_comment_plan",
                serde_json::json!({"repo": "r", "gitRef": "a", "number": 1, "environment": "linux-server"}),
            ),
            (
                "review_comment_post",
                serde_json::json!({"repo": "r", "gitRef": "a", "environment": "linux-server", "request": {"number": 1, "commitId": "a", "body": "", "event": "COMMENT", "comments": [], "findings": ["a"]}}),
            ),
            (
                "review_fix_plan",
                serde_json::json!({"repo": "r", "gitRef": "a", "review": "r-1", "findings": [{"id": "a", "title": "t"}], "environment": "linux-server"}),
            ),
            (
                "review_fix_start",
                serde_json::json!({"repo": "r", "gitRef": "a", "environment": "linux-server", "request": {"findings": ["a"], "agent": {"name": "n", "spec": "s", "branch": null, "command": "c"}, "message": "m"}}),
            ),
        ];
        assert_eq!(calls.len(), 8);
        for (command, args) in calls {
            let err = app_error(&ctx, command, args).await;
            assert_eq!(err.code, "bad_args", "{command}");
        }
        assert!(!log.exists());
    }

    #[tokio::test]
    async fn remove_entry_without_environment_is_local() {
        let (_dir, ctx) = ctx_with(r#"echo "$@" >&2; exit 1"#, "exit 1", Box::new(|_| {}));
        let args = serde_json::json!({"workspaceId": "fix-1"});
        let err = dispatch(&ctx, "remove_entry", args, None).await;
        let Err(DispatchError::App(err)) = err else {
            panic!("expected the fake ws's failure, got {err:?}");
        };
        assert_eq!(err.message, "rm --json -- fix-1");
    }

    #[tokio::test]
    async fn runner_check_with_no_model_answers_the_served_models() {
        let (_dir, ctx) = ctx_with(
            r#"echo '{"endpoint":"gpu","url":"http://gpu:1234","api":"openai","probe":"auto","model":null,"version":null,"capabilities":[],"context_tokens":null,"models":["a"],"token_source":null,"warnings":[]}'"#,
            "exit 1",
            Box::new(|_| {}),
        );
        let args = serde_json::json!({"endpoint": "gpu", "model": ""});
        let value = dispatch(&ctx, "runner_check", args, None).await.unwrap();
        assert_eq!(value["model"], Value::Null);
        assert_eq!(value["context_tokens"], Value::Null);
        assert_eq!(value["models"], serde_json::json!(["a"]));
    }

    #[tokio::test]
    async fn rename_workspace_rejects_snake_case_args() {
        let (_dir, ctx) = ctx_with("exit 1", "exit 1", Box::new(|_| {}));
        let args = serde_json::json!({"workspace_id": "x-d8efb24f", "name": "draft"});
        let err = dispatch(&ctx, "rename_workspace", args, None)
            .await
            .unwrap_err();
        assert!(matches!(err, DispatchError::BadArgs(_)));
        assert_eq!(err.status(), 400);
    }

    #[tokio::test]
    async fn list_entries_hands_listing_to_notifier() {
        let calls = Arc::new(Mutex::new(Vec::new()));
        let seen = Arc::clone(&calls);
        let (_dir, ctx) = ctx_with(
            r#"echo '{"workspaces":[{"id":"w","path":"/w","created_at":null,"session":null,"repos":[]}],"warnings":[]}'"#,
            "echo '[]'",
            Box::new(move |entries| seen.lock().unwrap().push(entries.len())),
        );
        let value = dispatch(&ctx, "list_entries", Value::Null, None)
            .await
            .unwrap();
        assert_eq!(*calls.lock().unwrap(), vec![1]);
        assert_eq!(value["entries"][0]["key"], "w");
    }

    #[tokio::test]
    async fn open_defaults_returns_platform_defaults() {
        let (_dir, ctx) = ctx_with("exit 1", "exit 1", Box::new(|_| {}));
        let value = dispatch(&ctx, "open_defaults", Value::Null, None)
            .await
            .unwrap();
        assert_eq!(value, serde_json::to_value(open::open_defaults()).unwrap());
    }

    #[tokio::test]
    async fn settings_environment_includes_online() {
        let (_dir, mut ctx) = ctx_with("exit 1", "exit 1", Box::new(|_| {}));
        ctx.online = Some(about::OnlineSettings {
            username: "me".into(),
            addr: "0.0.0.0:8080".into(),
            public_url: None,
            secure_cookie: false,
            session_days: 30,
            trust_proxy: false,
            data_dir: "/data".into(),
            web_dir: "/app".into(),
            vapid_subject: "mailto:me@example.com".into(),
            password: "hash",
        });
        let value = dispatch(&ctx, "settings_environment", Value::Null, None)
            .await
            .unwrap();
        assert_eq!(value["online"]["username"], "me");
        assert_eq!(value["online"]["password"], "hash");
    }

    #[tokio::test]
    async fn open_external_is_unsupported() {
        let (_dir, ctx) = ctx_with("exit 1", "exit 1", Box::new(|_| {}));
        let args = serde_json::json!({"target": "https://example.com"});
        let err = dispatch(&ctx, "open_external", args, None)
            .await
            .unwrap_err();
        assert_eq!(err.status(), 422);
        assert_eq!(err.to_app_error().code, "unsupported");
    }

    #[tokio::test]
    async fn stats_get_returns_camel_case_stats() {
        let (_dir, ctx) = ctx_with("exit 1", "exit 1", Box::new(|_| {}));
        let args = serde_json::json!({"sinceMs": null});
        let value = dispatch(&ctx, "stats_get", args, None).await.unwrap();
        for key in [
            "generatedAtMs",
            "system",
            "usage",
            "productivity",
            "scan",
            "warnings",
        ] {
            assert!(value.get(key).is_some(), "stats_get has no {key}");
        }
    }

    #[tokio::test]
    async fn stats_get_treats_missing_since_ms_as_all_time() {
        let (_dir, ctx) = ctx_with("exit 1", "exit 1", Box::new(|_| {}));
        let value = dispatch(&ctx, "stats_get", serde_json::json!({}), None)
            .await
            .unwrap();
        assert_eq!(value["sinceMs"], Value::Null);
    }

    #[tokio::test]
    async fn stats_get_rejects_a_bad_since_ms() {
        let (_dir, ctx) = ctx_with("exit 1", "exit 1", Box::new(|_| {}));
        let args = serde_json::json!({"sinceMs": "yesterday"});
        let err = dispatch(&ctx, "stats_get", args, None).await.unwrap_err();
        assert!(matches!(err, DispatchError::BadArgs(_)));
    }

    #[tokio::test]
    async fn terminal_open_needs_a_socket() {
        let (_dir, ctx) = ctx_with("exit 1", "exit 1", Box::new(|_| {}));
        let args = serde_json::json!({
            "spec": {"kind": "shell", "specs": []},
            "cols": 80,
            "rows": 24,
            "onEvent": {"__channel": 7},
        });
        let err = dispatch(&ctx, "terminal_open", args, None)
            .await
            .unwrap_err();
        assert_eq!(err, DispatchError::NoConnection);
    }

    #[tokio::test]
    async fn terminal_open_streams_to_the_socket() {
        let (_dir, ctx) = ctx_with("echo hello; exit 0", "exit 1", Box::new(|_| {}));
        let recorder = Arc::new(Recorder::default());
        let sink: Arc<dyn ChannelSink> = recorder.clone();
        let args = serde_json::json!({
            "spec": {"kind": "shell", "specs": []},
            "cols": 80,
            "rows": 24,
            "onEvent": {"__channel": 7},
        });
        let value = dispatch(&ctx, "terminal_open", args, Some(sink))
            .await
            .unwrap();
        let id = u32::try_from(value.as_u64().unwrap()).unwrap();
        assert_eq!(*recorder.opened.lock().unwrap(), vec![id]);
        for _ in 0..100 {
            let exited = recorder.frames.lock().unwrap().iter().any(|msg| {
                matches!(msg, ServerMsg::Channel { payload, .. } if payload["event"] == "exit")
            });
            if exited {
                break;
            }
            tokio::time::sleep(std::time::Duration::from_millis(50)).await;
        }
        let frames = recorder.frames.lock().unwrap();
        let output: String = frames
            .iter()
            .filter_map(|msg| match msg {
                ServerMsg::Channel { id: 7, payload } if payload["event"] == "data" => {
                    payload["data"].as_str().map(str::to_string)
                }
                _ => None,
            })
            .collect();
        assert!(output.contains("hello"), "output was {output:?}");
        assert!(frames.iter().any(|msg| matches!(
            msg,
            ServerMsg::Channel { id: 7, payload } if payload["event"] == "exit"
        )));
    }
}
