//! The commands the frontend invokes.

use crate::ipc::Channel;
use crate::ipc::State;
use crate::jobs::{self, JobState};
use crate::model::{self, ClaudeAgent, Listing, WsList};
use crate::pty::{remote_env, TermEvent, TermSpec, Terminals};
use crate::reviews::checker::Checker;
use crate::reviews::report;
use crate::reviews::store::Store;
use crate::runner::{self, AppError, Runner};
use crate::transcript::{self, Chunk};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::collections::HashSet;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::{SystemTime, UNIX_EPOCH};
#[cfg(feature = "desktop")]
use tauri_plugin_opener::OpenerExt;

pub struct AppState {
    pub runner: Runner,
    pub terminals: Terminals,
    pub jobs_dir: Option<PathBuf>,
    /// PR links seen per session, kept after Claude Code drops them.
    pub pr_links: Arc<crate::pr_links::PrLinks>,
    pub store: Arc<Store>,
    pub reviews: Arc<Checker>,
    /// Review reports read this session (ADR 0040).
    pub reports: report::Cache,
    /// Tracks agents for the tray's notifications (ADR 0050).
    pub watcher: Arc<crate::tray::watcher::Watcher>,
    /// Transcript and host stats (ADR 0059).
    pub stats: Arc<crate::stats::Cache>,
}

type CmdResult<T> = Result<T, AppError>;

fn strings(args: &[&str]) -> Vec<String> {
    args.iter().map(|s| (*s).to_string()).collect()
}

pub(crate) fn now_ms() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |d| i64::try_from(d.as_millis()).unwrap_or(i64::MAX))
}

/// Lists workspaces and background sessions, and hands the listing to the
/// tray's watcher (ADR 0050).
#[cfg(feature = "desktop")]
#[tauri::command]
pub async fn list_entries(app: tauri::AppHandle, state: State<'_, AppState>) -> CmdResult<Listing> {
    let listing = list(&state.runner, state.jobs_dir.as_deref(), &state.pr_links).await?;
    state.reviews.set_entries(listing.entries.clone());
    let observed = state.watcher.observe(&listing.entries, now_ms());
    crate::tray::publish(&app, &observed);
    Ok(listing)
}

/// Runs `ws list --all` and `claude agents` together; hidden workspaces
/// are listed too, and the frontend keeps them out of the default views
/// (ADR 0043). Either failing becomes a
/// warning as long as the other one answers.
pub async fn list(
    runner: &Runner,
    jobs_dir: Option<&Path>,
    pr_links: &crate::pr_links::PrLinks,
) -> CmdResult<Listing> {
    let ws_args = strings(&["list", "--all"]);
    let claude_args = strings(&["agents", "--json", "--all"]);
    let (ws, agents) = tokio::join!(
        runner.ws_json::<WsList>(&ws_args),
        runner.claude(&claude_args)
    );
    let agents =
        agents.and_then(|out| runner::parse_json::<Vec<ClaudeAgent>>(&out, "claude agents"));
    if let (Err(ws_err), Err(_)) = (&ws, &agents) {
        return Err(ws_err.clone());
    }
    let mut warnings = Vec::new();
    if let Err(err) = &ws {
        warnings.push(format!("ws list: {}", err.message));
    }
    if let Err(err) = &agents {
        warnings.push(format!("claude agents: {}", err.message));
    }
    let ws = ws.ok();
    if let Some(list) = &ws {
        warnings.extend(list.warnings.iter().map(model::warning_text));
    }
    // A session without its state file still gets the PR links kept for it.
    let job_for = |id: &str| -> Option<JobState> {
        let job = jobs_dir
            .and_then(|dir| jobs::read_job(dir, id))
            .unwrap_or_default();
        Some(pr_links.augment(id, job))
    };
    let agents = agents.unwrap_or_default();
    let entries = off_worker(|| model::merge(ws, &agents, &job_for, now_ms()));
    Ok(Listing { entries, warnings })
}

/// Runs blocking work (file reads and scans) without stalling the runtime's
/// other tasks: on a multi-thread runtime the worker hands its queue off;
/// on a current-thread one, which has nothing to hand to, it runs inline.
fn off_worker<T>(work: impl FnOnce() -> T) -> T {
    use tokio::runtime::{Handle, RuntimeFlavor};
    match Handle::try_current().map(|h| h.runtime_flavor()) {
        Ok(RuntimeFlavor::MultiThread) => tokio::task::block_in_place(work),
        _ => work(),
    }
}

#[derive(Deserialize)]
struct CacheList {
    repos: Vec<CacheRepo>,
}

#[derive(Deserialize)]
struct CacheRepo {
    id: String,
}

/// The cached repos' ids, for suggesting repo specs.
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn cache_repos(state: State<'_, AppState>) -> CmdResult<Vec<String>> {
    let list: CacheList = state.runner.ws_json(&strings(&["cache", "list"])).await?;
    Ok(list.repos.into_iter().map(|r| r.id).collect())
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct NewAgent {
    pub name: Option<String>,
    pub specs: Vec<String>,
    pub prompt: Option<String>,
    pub model: Option<String>,
    pub permission_mode: Option<String>,
    #[serde(default)]
    pub offline: bool,
    /// `claude` or `ws`; blank or None is `claude` (ADR 0070).
    #[serde(default)]
    pub runner: Option<String>,
    #[serde(default)]
    pub endpoint: Option<String>,
    /// The environment to create the agent in; blank or `local` is this machine.
    #[serde(default)]
    pub environment: Option<String>,
    /// Environments the agent may start children on (`--env-scope`); None sends no flag (ADR 0083).
    #[serde(default)]
    pub env_scope: Option<Vec<String>>,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct Created {
    pub id: String,
    pub session: String,
    pub path: String,
}

fn non_empty(value: Option<&String>) -> Option<&str> {
    value.map(|s| s.trim()).filter(|s| !s.is_empty())
}

/// The arguments for `ws claude`, without `--json`, which the runner adds.
/// A `ws` runner agent gets `--runner=ws` and `--endpoint=<e>` before the
/// specs, and after `--` only the prompt and `--model <m>` (ADR 0070).
pub fn new_agent_args(req: &NewAgent) -> Vec<String> {
    let ws = non_empty(req.runner.as_ref()) == Some("ws");
    let mut args = vec!["claude".to_string()];
    if let Some(env) = remote_env(req.environment.as_deref()) {
        args.push(format!("--env={env}"));
    }
    if let Some(names) = &req.env_scope {
        args.push(format!("--env-scope={}", names.join(",")));
    }
    if let Some(name) = non_empty(req.name.as_ref()) {
        args.extend(["--name".to_string(), name.to_string()]);
    }
    if req.offline {
        args.push("--offline".to_string());
    }
    if ws {
        args.push("--runner=ws".to_string());
        if let Some(endpoint) = non_empty(req.endpoint.as_ref()) {
            args.push(format!("--endpoint={endpoint}"));
        }
    }
    args.extend(
        req.specs
            .iter()
            .map(|s| s.trim())
            .filter(|s| !s.is_empty())
            .map(str::to_string),
    );
    let model_args: Vec<String> = non_empty(req.model.as_ref()).map_or_else(Vec::new, |model| {
        vec!["--model".to_string(), model.to_string()]
    });
    let prompt_args: Vec<String> = non_empty(req.prompt.as_ref())
        .map(str::to_string)
        .into_iter()
        .collect();
    let claude_args = if ws {
        [prompt_args, model_args].concat()
    } else {
        let mode_args: Vec<String> = non_empty(req.permission_mode.as_ref())
            .map_or_else(Vec::new, |mode| {
                vec!["--permission-mode".to_string(), mode.to_string()]
            });
        [model_args, mode_args, prompt_args].concat()
    };
    if !claude_args.is_empty() {
        args.push("--".to_string());
        args.extend(claude_args);
    }
    args
}

/// Most names in a scope (envscope.MaxEntries).
const MAX_SCOPE_NAMES: usize = 64;
/// Longest environment name (config's default_scope rule).
const MAX_SCOPE_NAME_BYTES: usize = 64;

/// Checks a requested environment scope (ADR 0083): 1-64 names, each 1-64
/// bytes with no whitespace, comma, control character or leading '-', and
/// none repeated.
pub fn validate_env_scope(names: &[String]) -> Result<(), AppError> {
    if names.is_empty() || names.len() > MAX_SCOPE_NAMES {
        return Err(AppError::new(
            "usage",
            format!("an environment scope needs 1 to {MAX_SCOPE_NAMES} names"),
        ));
    }
    let mut seen = HashSet::new();
    for name in names {
        if !valid_scope_name(name) {
            return Err(AppError::new(
                "usage",
                format!("{name:?} isn't a valid environment name"),
            ));
        }
        if !seen.insert(name.as_str()) {
            return Err(AppError::new(
                "usage",
                format!("environment {name:?} is listed twice"),
            ));
        }
    }
    Ok(())
}

fn valid_scope_name(name: &str) -> bool {
    (1..=MAX_SCOPE_NAME_BYTES).contains(&name.len())
        && !name.starts_with('-')
        && !name
            .chars()
            .any(|c| c == ',' || c.is_whitespace() || c.is_control())
}

/// Creates a workspace with a background agent in it: `ws claude`.
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn create_agent(state: State<'_, AppState>, request: NewAgent) -> CmdResult<Created> {
    start_agent(&state.runner, request).await
}

/// Checks the request's environment scope, then runs `ws claude` for it.
async fn start_agent(runner: &Runner, request: NewAgent) -> CmdResult<Created> {
    if let Some(names) = &request.env_scope {
        validate_env_scope(names)?;
    }
    runner.ws_json(&new_agent_args(&request)).await
}

#[derive(Debug, Deserialize)]
struct ItemResults {
    workspaces: Vec<ItemResult>,
}

#[derive(Debug, Deserialize)]
struct ItemResult {
    id: Option<String>,
    error: Option<Value>,
}

/// Runs a `ws` command that reports per-workspace results, which it prints
/// even when some fail, and returns the first item's error, if any.
async fn ws_items(runner: &Runner, args: &[impl AsRef<str>]) -> CmdResult<Vec<String>> {
    let mut args: Vec<String> = args.iter().map(|a| a.as_ref().to_string()).collect();
    args.insert(1, "--json".to_string());
    let outcome = runner.run("ws", &args).await?;
    let Ok(results) = serde_json::from_slice::<ItemResults>(&outcome.stdout) else {
        return Err(runner::ws_error(&outcome.stderr));
    };
    if let Some((id, error)) = results
        .workspaces
        .iter()
        .find_map(|r| r.error.as_ref().map(|e| (r.id.as_deref(), e)))
    {
        let message = error["message"].as_str().unwrap_or("ws failed");
        return Err(AppError {
            code: error["code"].as_str().unwrap_or("ws_failed").to_string(),
            message: id.map_or_else(|| message.to_string(), |id| format!("{id}: {message}")),
            details: None,
        });
    }
    Ok(results
        .workspaces
        .into_iter()
        .filter_map(|r| r.id)
        .collect())
}

/// The arguments of `ws rm` for a workspace, which removes the workspaces
/// it started too, unless they're kept (ADR 0044).
/// `--env <name>` goes before `--` when the workspace is remote.
pub fn rm_args(workspace_id: &str, keep_children: bool, environment: Option<&str>) -> Vec<String> {
    let mut args = strings(&["rm"]);
    args.extend(env_args(environment));
    if keep_children {
        args.push("--keep-children".to_string());
    }
    args.extend(strings(&["--", workspace_id]));
    args
}

/// `--env=<name>`, one argument so a name that starts with `-` can't become
/// a flag, for a remote environment; nothing for local or none.
pub fn env_args(environment: Option<&str>) -> Vec<String> {
    remote_env(environment)
        .map(|env| vec![format!("--env={env}")])
        .unwrap_or_default()
}

/// Refuses a command that only works on this machine when the entry is
/// remote (ADR 0076, D3).
pub fn refuse_remote(environment: Option<&str>, what: &str) -> CmdResult<()> {
    match remote_env(environment) {
        Some(env) => Err(AppError::new(
            "local_only",
            format!("{what} isn't supported for an agent on {env}"),
        )),
        None => Ok(()),
    }
}

/// Removes a workspace, its session, and the workspaces it started
/// (`ws rm`), or, for a session outside any workspace, deletes the session
/// (`claude rm`), which is local only.
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn remove_entry(
    state: State<'_, AppState>,
    workspace_id: Option<String>,
    session_id: Option<String>,
    keep_children: Option<bool>,
    environment: Option<String>,
) -> CmdResult<()> {
    if let Some(id) = workspace_id {
        let args = rm_args(&id, keep_children.unwrap_or(false), environment.as_deref());
        return ws_items(&state.runner, &args).await.map(|_| ());
    }
    refuse_remote(environment.as_deref(), "Deleting a bare session")?;
    let id = session_id.ok_or_else(|| AppError::new("usage", "nothing to remove"))?;
    state
        .runner
        .claude(&strings(&["rm", &id]))
        .await
        .map(|_| ())
}

#[derive(Debug, Deserialize, Serialize)]
pub struct Renamed {
    pub id: String,
    pub name: String,
}

/// Changes a workspace's display name: `ws rename`. Its id and directory
/// stay as they are (ADR 0036).
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn rename_workspace(
    state: State<'_, AppState>,
    workspace_id: String,
    name: String,
    environment: Option<String>,
) -> CmdResult<Renamed> {
    state
        .runner
        .ws_json(&rename_args(&workspace_id, &name, environment.as_deref()))
        .await
}

/// The arguments of `ws rename` for a workspace.
pub fn rename_args(workspace_id: &str, name: &str, environment: Option<&str>) -> Vec<String> {
    let mut args = strings(&["rename"]);
    args.extend(env_args(environment));
    args.extend(strings(&["--", workspace_id, name]));
    args
}

#[derive(Debug, Deserialize, Serialize)]
pub struct HiddenSet {
    pub id: String,
    pub hidden: bool,
}

/// The arguments that hide or show a workspace: `ws hide` or `ws unhide`
/// (ADR 0043).
pub fn set_hidden_args(workspace_id: &str, hidden: bool, environment: Option<&str>) -> Vec<String> {
    let op = if hidden { "hide" } else { "unhide" };
    let mut args = strings(&[op]);
    args.extend(env_args(environment));
    args.extend(strings(&["--", workspace_id]));
    args
}

/// Hides or shows a workspace.
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn set_workspace_hidden(
    state: State<'_, AppState>,
    workspace_id: String,
    hidden: bool,
    environment: Option<String>,
) -> CmdResult<HiddenSet> {
    state
        .runner
        .ws_json(&set_hidden_args(
            &workspace_id,
            hidden,
            environment.as_deref(),
        ))
        .await
}

/// The app's version, and the `ws` and `claude` it runs, for Settings >
/// About.
#[cfg(feature = "desktop")]
#[tauri::command]
pub async fn versions(
    app: tauri::AppHandle,
    state: State<'_, AppState>,
) -> CmdResult<crate::about::Versions> {
    let version = app.package_info().version.to_string();
    Ok(crate::about::versions(&state.runner, crate::about::app_version(&version)).await)
}

/// The program and arguments that stop an agent: `ws stop --env=<name> --
/// <id>` for a remote one, `claude stop <session>` otherwise.
pub fn stop_args(
    session_id: &str,
    workspace_id: Option<&str>,
    environment: Option<&str>,
) -> CmdResult<(&'static str, Vec<String>)> {
    let Some(env) = remote_env(environment) else {
        return Ok(("claude", strings(&["stop", session_id])));
    };
    let id = workspace_id.filter(|id| !id.is_empty()).ok_or_else(|| {
        AppError::new("bad_args", "stopping a remote agent needs its workspace id")
    })?;
    Ok((
        "ws",
        vec![
            "stop".to_string(),
            format!("--env={env}"),
            "--".to_string(),
            id.to_string(),
        ],
    ))
}

/// Stops an agent: `claude stop`, or `ws stop` on a remote environment,
/// which answers `not_stopped` when the agent had already ended.
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn stop_agent(
    state: State<'_, AppState>,
    session_id: String,
    workspace_id: Option<String>,
    environment: Option<String>,
) -> CmdResult<()> {
    let (program, args) = stop_args(&session_id, workspace_id.as_deref(), environment.as_deref())?;
    if program == "claude" {
        return state.runner.claude(&args).await.map(|_| ());
    }
    let result: StopResult = state.runner.ws_json(&args).await?;
    if result.stopped.iter().all(|item| item.stopped) {
        return Ok(());
    }
    Err(AppError::new(
        "not_stopped",
        "The agent had already ended, so there was nothing to stop",
    ))
}

/// What `ws stop --json` answers.
#[derive(Debug, Deserialize)]
struct StopResult {
    stopped: Vec<StopItem>,
}

#[derive(Debug, Deserialize)]
struct StopItem {
    stopped: bool,
}

/// Respawns every stopped agent workspace: `ws restore`. Returns the ids
/// restored.
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn restore_agents(state: State<'_, AppState>) -> CmdResult<Vec<String>> {
    ws_items(&state.runner, &["restore"]).await
}

/// Opens a URL in the browser, or a directory in the file manager.
#[cfg(feature = "desktop")]
#[tauri::command]
pub fn open_external(app: tauri::AppHandle, target: String) -> CmdResult<()> {
    let opener = app.opener();
    let result = if target.starts_with("https://") || target.starts_with("http://") {
        opener.open_url(&target, None::<&str>)
    } else {
        opener.open_path(&target, None::<&str>)
    };
    result.map_err(|e| AppError::new("open_failed", e.to_string()))
}

/// Reads an agent's conversation from offset on, so the frontend can follow
/// it by asking again from the `next` it returns. A `ws` runner's agent
/// passes its workspace id; a Claude agent passes none and reads its
/// background session.
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn read_transcript(
    state: State<'_, AppState>,
    session_id: String,
    offset: u64,
    workspace_id: Option<String>,
    environment: Option<String>,
) -> CmdResult<Chunk> {
    refuse_remote(environment.as_deref(), "Showing the conversation")?;
    if let Some(id) = workspace_id {
        return runner_conversation(id, offset).await;
    }
    let jobs_dir = state
        .jobs_dir
        .clone()
        .ok_or_else(|| AppError::new("transcript_missing", "no Claude Code config directory"))?;
    tokio::task::spawn_blocking(move || conversation(&jobs_dir, &session_id, offset))
        .await
        .map_err(|e| AppError::new("internal", e.to_string()))?
}

fn conversation(jobs_dir: &Path, session_id: &str, offset: u64) -> CmdResult<Chunk> {
    let missing = || {
        AppError::new(
            "transcript_missing",
            "Claude Code has no transcript for this session",
        )
    };
    let job = jobs::read_job(jobs_dir, session_id).ok_or_else(missing)?;
    let config_dir = jobs_dir.parent().ok_or_else(missing)?;
    let path = transcript::transcript_path(config_dir, &job).ok_or_else(missing)?;
    transcript::read_from(&path, offset).map_err(|e| {
        AppError::new(
            "transcript_unreadable",
            format!("reading {}: {e}", path.display()),
        )
    })
}

/// Reads a `ws` runner's transcript from offset on, from ws's state directory.
async fn runner_conversation(workspace_id: String, offset: u64) -> CmdResult<Chunk> {
    let state_dir = ws_state_dir(|name| std::env::var(name).ok())
        .ok_or_else(|| AppError::new("transcript_missing", "no ws config directory"))?;
    let read = tokio::task::spawn_blocking(move || {
        transcript::runner_transcript(&state_dir, &workspace_id, offset)
    })
    .await
    .map_err(|e| AppError::new("internal", e.to_string()))?;
    read.map_err(|e| {
        AppError::new(
            "transcript_unreadable",
            format!("reading the runner transcript: {e}"),
        )
    })
}

/// ws's config directory (CONFIG_DIR): `WS_CONFIG_DIR`, else the platform
/// config directory's `workspaces` (the same root `ws` uses).
pub fn ws_config_dir(get: impl Fn(&str) -> Option<String>) -> Option<PathBuf> {
    let non_empty = |name: &str| get(name).filter(|value| !value.is_empty());
    match non_empty("WS_CONFIG_DIR") {
        Some(dir) => Some(PathBuf::from(dir)),
        None => Some(user_config_dir(&non_empty)?.join("workspaces")),
    }
}

/// ws's state directory, where runners keep their files: `state` under the
/// config directory.
pub fn ws_state_dir(get: impl Fn(&str) -> Option<String>) -> Option<PathBuf> {
    Some(ws_config_dir(get)?.join("state"))
}

fn user_config_dir(non_empty: &dyn Fn(&str) -> Option<String>) -> Option<PathBuf> {
    let home = PathBuf::from(non_empty("HOME")?);
    if cfg!(target_os = "macos") {
        return Some(home.join("Library").join("Application Support"));
    }
    match non_empty("XDG_CONFIG_HOME") {
        Some(dir) => Some(PathBuf::from(dir)),
        None => Some(home.join(".config")),
    }
}

/// `ws runner stop`'s object: whether the runner was still running.
#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
pub struct RunnerStopped {
    pub id: String,
    pub session: String,
    pub stopped: bool,
    #[serde(default)]
    pub warnings: Vec<Value>,
}

pub fn runner_stop_args(workspace_id: &str) -> Vec<String> {
    strings(&["runner", "stop", "--json", workspace_id])
}

/// Stops a `ws` runner without removing its workspace: `ws runner stop`
/// through the daemon.
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn runner_stop(
    state: State<'_, AppState>,
    id: String,
    environment: Option<String>,
) -> CmdResult<RunnerStopped> {
    refuse_remote(environment.as_deref(), "Stopping a ws runner")?;
    state.runner.ws_json_as_given(&runner_stop_args(&id)).await
}

#[cfg_attr(feature = "desktop", tauri::command)]
pub fn terminal_open(
    state: State<'_, AppState>,
    spec: TermSpec,
    cols: u16,
    rows: u16,
    on_event: Channel<TermEvent>,
) -> CmdResult<u32> {
    state
        .terminals
        .open(&spec, state.runner.path(), cols, rows, on_event)
}

#[cfg_attr(feature = "desktop", tauri::command)]
pub fn terminal_write(state: State<'_, AppState>, id: u32, data: String) -> CmdResult<()> {
    state.terminals.write(id, &data)
}

#[cfg_attr(feature = "desktop", tauri::command)]
pub fn terminal_resize(state: State<'_, AppState>, id: u32, cols: u16, rows: u16) -> CmdResult<()> {
    state.terminals.resize(id, cols, rows)
}

#[cfg_attr(feature = "desktop", tauri::command)]
pub fn terminal_close(state: State<'_, AppState>, id: u32) -> CmdResult<()> {
    state.terminals.close(id)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::PermissionsExt;

    /// A directory holding fake `ws` and `claude` scripts, for PATH.
    fn fake_programs(ws: &str, claude: &str) -> tempfile::TempDir {
        let dir = tempfile::tempdir().unwrap();
        for (name, body) in [("ws", ws), ("claude", claude)] {
            let path = dir.path().join(name);
            std::fs::write(&path, format!("#!/bin/sh\n{body}\n")).unwrap();
            std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o755)).unwrap();
            crate::test_support::wait_until_executable(&path);
        }
        dir
    }

    #[tokio::test(flavor = "multi_thread", worker_threads = 2)]
    async fn list_does_not_stall_the_runtime_while_scanning() {
        use std::sync::atomic::Ordering;
        let dir = fake_programs(
            r#"echo '{"workspaces":[],"warnings":[]}'"#,
            r#"echo '[{"id":"s2","sessionId":"u","cwd":"/x","kind":"background","status":"busy","pid":2,"startedAt":1}]'"#,
        );
        let jobs = tempfile::tempdir().unwrap();
        let transcript = jobs.path().join("t.jsonl");
        std::fs::write(&transcript, "https://github.com/o/r/pull/4\n").unwrap();
        std::fs::create_dir(jobs.path().join("s2")).unwrap();
        std::fs::write(
            jobs.path().join("s2/state.json"),
            format!(r#"{{"linkScanPath":"{}"}}"#, transcript.display()),
        )
        .unwrap();
        let path = dir.path().display().to_string();
        let jobs_path = jobs.path().to_path_buf();
        // One listing per worker thread, so both are busy scanning.
        let all: Vec<_> = (0..2)
            .map(|_| {
                let links = Arc::new(crate::pr_links::PrLinks::default());
                links.scan_delay_ms.store(500, Ordering::SeqCst);
                let (runner, jobs_path, task_links) =
                    (Runner::new(path.clone()), jobs_path.clone(), links.clone());
                let handle = tokio::spawn(async move {
                    list(&runner, Some(&jobs_path), &task_links).await.unwrap()
                });
                (links, handle)
            })
            .collect();
        // Let the listings reach their scans (a plain sleep: the timer needs a free worker), then see how soon another task runs.
        std::thread::sleep(std::time::Duration::from_millis(250));
        let started = std::time::Instant::now();
        tokio::spawn(async {}).await.unwrap();
        assert!(
            started.elapsed() < std::time::Duration::from_millis(100),
            "a spawned task waited {:?} behind the scans",
            started.elapsed()
        );
        for (links, handle) in all {
            let listing = handle.await.unwrap();
            assert_eq!(links.scan_count.load(Ordering::SeqCst), 1);
            assert_eq!(listing.entries.len(), 1);
        }
    }

    #[tokio::test]
    async fn list_merges_ws_and_claude() {
        let dir = fake_programs(
            r#"echo '{"workspaces":[{"id":"w","path":"/w","created_at":null,"session":{"kind":"agent","id":"s1","state":"working","pid":1,"stale":false},"repos":[]}],"warnings":["careful"]}'"#,
            r#"echo '[{"id":"s2","sessionId":"u","cwd":"/x","kind":"background","status":"busy","pid":2,"startedAt":1}]'"#,
        );
        let runner = Runner::new(dir.path().display().to_string());
        let listing = list(&runner, None, &crate::pr_links::PrLinks::default())
            .await
            .unwrap();
        let keys: Vec<_> = listing.entries.iter().map(|e| e.key.as_str()).collect();
        assert_eq!(keys, ["w", "session:s2"]);
        assert_eq!(listing.warnings, ["careful"]);
    }

    #[tokio::test]
    async fn list_warns_when_claude_fails() {
        let dir = fake_programs(
            r#"echo '{"workspaces":[],"warnings":[]}'"#,
            "echo 'daemon down' >&2; exit 1",
        );
        let runner = Runner::new(dir.path().display().to_string());
        let listing = list(&runner, None, &crate::pr_links::PrLinks::default())
            .await
            .unwrap();
        assert_eq!(listing.warnings, ["claude agents: daemon down"]);
    }

    #[tokio::test]
    async fn list_fails_with_ws_error_when_both_fail() {
        let dir = fake_programs(
            r#"echo '{"error":{"code":"config","message":"bad config"}}' >&2; exit 2"#,
            "exit 1",
        );
        let runner = Runner::new(dir.path().display().to_string());
        let err = list(&runner, None, &crate::pr_links::PrLinks::default())
            .await
            .unwrap_err();
        assert_eq!(err.code, "config");
    }

    #[tokio::test]
    async fn ws_items_reports_first_item_error() {
        let dir = fake_programs(
            r#"echo '{"workspaces":[{"id":"x","removed":false,"error":{"code":"workspace_not_found","message":"not found"}}],"warnings":[]}'; exit 3"#,
            "exit 0",
        );
        let runner = Runner::new(dir.path().display().to_string());
        let err = ws_items(&runner, &["rm", "x"]).await.unwrap_err();
        assert_eq!(err.code, "workspace_not_found");
    }

    #[test]
    fn runner_stop_args_stop_one_workspace_as_json() {
        assert_eq!(
            runner_stop_args("t1-impl-d8efb24f"),
            vec!["runner", "stop", "--json", "t1-impl-d8efb24f"]
        );
    }

    #[test]
    fn ws_state_dir_prefers_ws_config_dir() {
        let env = |name: &str| (name == "WS_CONFIG_DIR").then(|| "/c".to_string());
        assert_eq!(ws_state_dir(env), Some(PathBuf::from("/c/state")));
    }

    #[test]
    fn ws_state_dir_ignores_an_empty_ws_config_dir() {
        let env = |name: &str| match name {
            "WS_CONFIG_DIR" => Some(String::new()),
            "HOME" => Some("/h".to_string()),
            _ => None,
        };
        assert!(ws_state_dir(env).is_some_and(|p| p.ends_with("state")));
    }

    #[cfg(not(target_os = "macos"))]
    #[test]
    fn ws_state_dir_defaults_under_the_xdg_config_home() {
        let env = |name: &str| match name {
            "XDG_CONFIG_HOME" => Some("/x".to_string()),
            "HOME" => Some("/h".to_string()),
            _ => None,
        };
        assert_eq!(
            ws_state_dir(env),
            Some(PathBuf::from("/x/workspaces/state"))
        );
    }

    #[test]
    fn ws_state_dir_needs_a_home_without_ws_config_dir() {
        assert_eq!(ws_state_dir(|_| None), None);
    }

    #[test]
    fn rm_args_keep_children_or_not() {
        assert_eq!(rm_args("x", false, None), ["rm", "--", "x"]);
        assert_eq!(
            rm_args("-x", true, Some("local")),
            ["rm", "--keep-children", "--", "-x"]
        );
    }

    #[test]
    fn rm_args_with_env() {
        assert_eq!(
            rm_args("x", true, Some("linux-server")),
            ["rm", "--env=linux-server", "--keep-children", "--", "x"]
        );
    }

    #[test]
    fn set_hidden_args_with_env() {
        assert_eq!(
            set_hidden_args("x", true, Some("win-rig")),
            ["hide", "--env=win-rig", "--", "x"]
        );
    }

    #[test]
    fn set_hidden_args_hide_or_unhide() {
        assert_eq!(
            set_hidden_args("x-d8efb24f", true, None),
            ["hide", "--", "x-d8efb24f"]
        );
        assert_eq!(set_hidden_args("-x", false, None), ["unhide", "--", "-x"]);
    }

    #[tokio::test]
    async fn set_workspace_hidden_runs_ws_hide() {
        let dir = fake_programs(
            r#"printf '{"id":"%s","name":null,"path":"/w","hidden":%s,"warnings":[]}' "$4" "$([ "$1" = hide ] && echo true || echo false)""#,
            "exit 0",
        );
        let runner = Runner::new(dir.path().display().to_string());
        let set: HiddenSet = runner
            .ws_json(&set_hidden_args("x-d8efb24f", true, None))
            .await
            .unwrap();
        assert_eq!(set.id, "x-d8efb24f");
        assert!(set.hidden);
    }

    #[tokio::test]
    async fn rename_workspace_runs_ws_rename() {
        let dir = fake_programs(
            r#"printf '{"id":"%s","name":"%s","path":"/w","warnings":[]}' "$4" "$5""#,
            "exit 0",
        );
        let runner = Runner::new(dir.path().display().to_string());
        let renamed: Renamed = runner
            .ws_json(&strings(&["rename", "--", "x-d8efb24f", "-draft"]))
            .await
            .unwrap();
        assert_eq!(renamed.id, "x-d8efb24f");
        assert_eq!(renamed.name, "-draft");
    }

    /// Lists the real workspaces and sessions: `cargo test -- --ignored`.
    #[tokio::test]
    #[ignore = "runs the installed ws and claude"]
    async fn list_live() {
        let runner = Runner::new(crate::env::resolve_path());
        let listing = list(
            &runner,
            jobs::jobs_dir().as_deref(),
            &crate::pr_links::PrLinks::default(),
        )
        .await
        .unwrap();
        for entry in &listing.entries {
            println!(
                "{:<28} {:?} {:?} {:?}",
                entry.key, entry.kind, entry.status, entry.detail
            );
        }
        println!("warnings: {:?}", listing.warnings);
    }

    /// Prints the conversation of the session in `WS_DESKTOP_ATTACH`:
    /// `cargo test -- --ignored`.
    #[test]
    #[ignore = "reads a real Claude Code transcript"]
    fn conversation_live() {
        let id = std::env::var("WS_DESKTOP_ATTACH").expect("WS_DESKTOP_ATTACH");
        let chunk = conversation(&jobs::jobs_dir().unwrap(), &id, 0).unwrap();
        for item in &chunk.items {
            println!(
                "{}",
                serde_json::to_string(item)
                    .unwrap()
                    .chars()
                    .take(160)
                    .collect::<String>()
            );
        }
        println!(
            "{} items, {} dropped, next {}",
            chunk.items.len(),
            chunk.dropped,
            chunk.next
        );
    }

    fn request() -> NewAgent {
        NewAgent {
            name: None,
            specs: vec![],
            prompt: None,
            model: None,
            permission_mode: None,
            offline: false,
            runner: None,
            endpoint: None,
            environment: None,
            env_scope: None,
        }
    }

    #[test]
    fn new_agent_args_adds_env() {
        let req = NewAgent {
            environment: Some("linux-server".into()),
            ..request()
        };
        assert_eq!(new_agent_args(&req), ["claude", "--env=linux-server"]);
        let local = NewAgent {
            environment: Some("local".into()),
            ..request()
        };
        assert_eq!(new_agent_args(&local), ["claude"]);
    }

    #[test]
    fn rename_args_with_env() {
        assert_eq!(
            rename_args("x", "-n", Some("win-rig")),
            ["rename", "--env=win-rig", "--", "x", "-n"]
        );
        assert_eq!(rename_args("x", "n", None), ["rename", "--", "x", "n"]);
    }

    #[test]
    fn stop_args_remote_uses_ws_stop() {
        let (program, args) = stop_args("s1", Some("fix-1"), Some("linux-server")).unwrap();
        assert_eq!(program, "ws");
        assert_eq!(args, ["stop", "--env=linux-server", "--", "fix-1"]);
        let (program, args) = stop_args("s1", Some("fix-1"), None).unwrap();
        assert_eq!((program, args), ("claude", strings(&["stop", "s1"])));
        assert_eq!(
            stop_args("s1", None, Some("linux-server"))
                .unwrap_err()
                .code,
            "bad_args"
        );
    }

    #[test]
    fn stop_args_with_a_blank_workspace_id_is_bad_args() {
        let err = stop_args("s1", Some(""), Some("linux-server")).unwrap_err();
        assert_eq!(err.code, "bad_args");
        assert_eq!(
            stop_args("s1", None, Some("linux-server"))
                .unwrap_err()
                .code,
            "bad_args"
        );
    }

    #[test]
    fn stop_args_for_a_local_agent_ignore_a_blank_environment() {
        let (program, args) = stop_args("s1", None, Some("")).unwrap();
        assert_eq!((program, args), ("claude", strings(&["stop", "s1"])));
    }

    #[test]
    fn a_blank_environment_adds_no_env_flag() {
        assert_eq!(rm_args("x", false, Some("")), ["rm", "--", "x"]);
        assert_eq!(rename_args("x", "n", Some("")), ["rename", "--", "x", "n"]);
        assert_eq!(set_hidden_args("x", true, Some("")), ["hide", "--", "x"]);
        let req = NewAgent {
            environment: Some(String::new()),
            ..request()
        };
        assert_eq!(new_agent_args(&req), ["claude"]);
    }

    #[test]
    fn an_environment_that_starts_with_a_dash_stays_one_argument() {
        assert_eq!(
            rm_args("x", false, Some("-v")),
            ["rm", "--env=-v", "--", "x"]
        );
    }

    #[test]
    fn local_only_commands_refuse_a_remote_environment() {
        let err = refuse_remote(Some("linux-server"), "Stopping a ws runner").unwrap_err();
        assert_eq!(err.code, "local_only");
        assert!(err.message.contains("linux-server"), "{}", err.message);
        assert!(refuse_remote(Some("local"), "x").is_ok());
        assert!(refuse_remote(None, "x").is_ok());
    }

    #[test]
    fn new_agent_args_without_options_is_empty_workspace_without_prompt() {
        assert_eq!(new_agent_args(&request()), ["claude"]);
    }

    #[test]
    fn new_agent_args_puts_claude_options_after_separator() {
        let req = NewAgent {
            name: Some("Fix Login".into()),
            specs: vec!["buff-bear@main".into(), "  ".into()],
            prompt: Some("  Fix it  ".into()),
            model: Some("opus".into()),
            permission_mode: Some("acceptEdits".into()),
            offline: true,
            runner: None,
            endpoint: None,
            environment: None,
            env_scope: None,
        };
        assert_eq!(
            new_agent_args(&req),
            [
                "claude",
                "--name",
                "Fix Login",
                "--offline",
                "buff-bear@main",
                "--",
                "--model",
                "opus",
                "--permission-mode",
                "acceptEdits",
                "Fix it",
            ]
        );
    }

    #[test]
    fn new_agent_args_for_ws_agent_puts_runner_before_specs_and_model_after_prompt() {
        let req = NewAgent {
            name: Some("Fix Login".into()),
            specs: vec!["buff-bear@main".into()],
            prompt: Some("Fix it".into()),
            model: Some("qwen9b".into()),
            permission_mode: Some("acceptEdits".into()),
            runner: Some("ws".into()),
            endpoint: Some("gpu".into()),
            ..request()
        };
        assert_eq!(
            new_agent_args(&req),
            [
                "claude",
                "--name",
                "Fix Login",
                "--runner=ws",
                "--endpoint=gpu",
                "buff-bear@main",
                "--",
                "Fix it",
                "--model",
                "qwen9b",
            ]
        );
    }

    #[test]
    fn new_agent_args_for_ws_agent_without_endpoint_or_model_sends_only_the_runner() {
        let req = NewAgent {
            prompt: Some("Fix it".into()),
            runner: Some("ws".into()),
            endpoint: Some(" ".into()),
            ..request()
        };
        assert_eq!(
            new_agent_args(&req),
            ["claude", "--runner=ws", "--", "Fix it"]
        );
    }

    #[test]
    fn new_agent_args_ignores_blank_name_and_prompt() {
        let req = NewAgent {
            name: Some(" ".into()),
            prompt: Some("\n".into()),
            ..request()
        };
        assert_eq!(new_agent_args(&req), ["claude"]);
    }

    fn names(list: &[&str]) -> Vec<String> {
        list.iter().map(|n| n.to_string()).collect()
    }

    #[test]
    fn new_agent_args_without_env_scope_sends_no_env_scope_flag() {
        let req = NewAgent {
            environment: Some("linux-server".into()),
            ..request()
        };
        assert_eq!(new_agent_args(&req), ["claude", "--env=linux-server"]);
    }

    #[test]
    fn new_agent_args_sends_env_scope_as_one_argument() {
        let req = NewAgent {
            env_scope: Some(names(&["local", "linux-server"])),
            ..request()
        };
        assert_eq!(
            new_agent_args(&req),
            ["claude", "--env-scope=local,linux-server"]
        );
    }

    #[test]
    fn new_agent_args_puts_env_scope_after_env() {
        let req = NewAgent {
            environment: Some("linux-server".into()),
            env_scope: Some(names(&["linux-server"])),
            ..request()
        };
        assert_eq!(
            new_agent_args(&req),
            ["claude", "--env=linux-server", "--env-scope=linux-server"]
        );
    }

    #[test]
    fn new_agent_args_with_an_empty_env_scope_sends_no_env_scope_flag() {
        let req = NewAgent {
            env_scope: Some(vec![]),
            ..request()
        };
        assert_eq!(new_agent_args(&req), ["claude"]);
    }

    #[test]
    fn validate_env_scope_accepts_enrolled_style_names() {
        assert!(validate_env_scope(&names(&["local", "linux-server", "win-rig.lab_2"])).is_ok());
    }

    #[test]
    fn validate_env_scope_refuses_an_empty_list() {
        let err = validate_env_scope(&[]).unwrap_err();
        assert_eq!(err.code, "usage");
    }

    #[test]
    fn validate_env_scope_refuses_unsafe_names() {
        for name in [
            "a,b",
            "a b",
            "-x",
            "a\nb",
            "",
            &"x".repeat(65),
            "a\u{0}b",
            "a\u{7f}b",
        ] {
            let err = validate_env_scope(&names(&["local", name])).unwrap_err();
            assert_eq!(err.code, "usage", "{name:?}");
            assert_eq!(
                err.message,
                format!("{name:?} isn't a valid environment name"),
                "{name:?}"
            );
        }
    }

    #[test]
    fn validate_env_scope_refuses_more_than_64() {
        let many: Vec<String> = (0..65).map(|i| format!("env-{i}")).collect();
        assert_eq!(validate_env_scope(&many).unwrap_err().code, "usage");
        assert!(validate_env_scope(&many[..64]).is_ok());
    }

    #[test]
    fn validate_env_scope_refuses_duplicates() {
        let err = validate_env_scope(&names(&["local", "local"])).unwrap_err();
        assert_eq!(err.code, "usage");
    }

    /// Runs start_agent with a fake ws, and returns its error, asserting ws never ran.
    async fn refused_before_ws(env_scope: Vec<String>) -> AppError {
        let dir = fake_programs(
            r#"[ "$1" = probe ] || touch "$(dirname "$0")/ws-ran"; echo '{}'"#,
            "exit 1",
        );
        let runner = Runner::new(dir.path().display().to_string());
        let request = NewAgent {
            env_scope: Some(env_scope),
            ..request()
        };
        let err = start_agent(&runner, request).await.unwrap_err();
        assert!(!dir.path().join("ws-ran").exists(), "ws must not run");
        err
    }

    #[tokio::test]
    async fn start_agent_refuses_an_empty_env_scope_before_ws_runs() {
        let err = refused_before_ws(vec![]).await;
        assert_eq!(err.code, "usage");
    }

    #[tokio::test]
    async fn start_agent_refuses_more_than_64_env_scope_names_before_ws_runs() {
        let many: Vec<String> = (0..65).map(|i| format!("env-{i}")).collect();
        let err = refused_before_ws(many).await;
        assert_eq!(err.code, "usage");
    }
}
