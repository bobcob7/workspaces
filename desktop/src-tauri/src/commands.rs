//! The commands the frontend invokes.

use crate::jobs::{self, JobState};
use crate::model::{self, ClaudeAgent, Listing, WsList};
use crate::pty::{TermEvent, TermSpec, Terminals};
use crate::reviews::checker::Checker;
use crate::reviews::report;
use crate::reviews::store::Store;
use crate::runner::{self, AppError, Runner};
use crate::transcript::{self, Chunk};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::{SystemTime, UNIX_EPOCH};
use tauri::ipc::Channel;
use tauri::State;
use tauri_plugin_opener::OpenerExt;

pub struct AppState {
    pub runner: Runner,
    pub terminals: Terminals,
    pub jobs_dir: Option<PathBuf>,
    pub store: Arc<Store>,
    pub reviews: Arc<Checker>,
    /// Review reports read this session (ADR 0040).
    pub reports: report::Cache,
}

type CmdResult<T> = Result<T, AppError>;

fn strings(args: &[&str]) -> Vec<String> {
    args.iter().map(|s| (*s).to_string()).collect()
}

fn now_ms() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |d| i64::try_from(d.as_millis()).unwrap_or(i64::MAX))
}

/// Lists workspaces and background sessions.
#[tauri::command]
pub async fn list_entries(state: State<'_, AppState>) -> CmdResult<Listing> {
    let listing = list(&state.runner, state.jobs_dir.as_deref()).await?;
    state.reviews.set_entries(listing.entries.clone());
    Ok(listing)
}

/// Runs `ws list --all` and `claude agents` together; hidden workspaces
/// are listed too, and the frontend keeps them out of the default views
/// (ADR 0043). Either failing becomes a
/// warning as long as the other one answers.
pub async fn list(runner: &Runner, jobs_dir: Option<&Path>) -> CmdResult<Listing> {
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
    let job_for = |id: &str| -> Option<JobState> { jobs::read_job(jobs_dir?, id) };
    let entries = model::merge(ws, &agents.unwrap_or_default(), &job_for, now_ms());
    Ok(Listing { entries, warnings })
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
#[tauri::command]
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
pub fn new_agent_args(req: &NewAgent) -> Vec<String> {
    let mut args = vec!["claude".to_string()];
    if let Some(name) = non_empty(req.name.as_ref()) {
        args.extend(["--name".to_string(), name.to_string()]);
    }
    if req.offline {
        args.push("--offline".to_string());
    }
    args.extend(
        req.specs
            .iter()
            .map(|s| s.trim())
            .filter(|s| !s.is_empty())
            .map(str::to_string),
    );
    let mut claude_args = Vec::new();
    if let Some(model) = non_empty(req.model.as_ref()) {
        claude_args.extend(["--model".to_string(), model.to_string()]);
    }
    if let Some(mode) = non_empty(req.permission_mode.as_ref()) {
        claude_args.extend(["--permission-mode".to_string(), mode.to_string()]);
    }
    if let Some(prompt) = non_empty(req.prompt.as_ref()) {
        claude_args.push(prompt.to_string());
    }
    if !claude_args.is_empty() {
        args.push("--".to_string());
        args.extend(claude_args);
    }
    args
}

/// Creates a workspace with a background agent in it: `ws claude`.
#[tauri::command]
pub async fn create_agent(state: State<'_, AppState>, request: NewAgent) -> CmdResult<Created> {
    state.runner.ws_json(&new_agent_args(&request)).await
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
async fn ws_items(runner: &Runner, args: &[&str]) -> CmdResult<Vec<String>> {
    let mut args = strings(args);
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
pub fn rm_args(workspace_id: &str, keep_children: bool) -> Vec<&str> {
    let mut args = vec!["rm"];
    if keep_children {
        args.push("--keep-children");
    }
    args.extend(["--", workspace_id]);
    args
}

/// Removes a workspace, its session, and the workspaces it started
/// (`ws rm`), or, for a session outside any workspace, deletes the session
/// (`claude rm`).
#[tauri::command]
pub async fn remove_entry(
    state: State<'_, AppState>,
    workspace_id: Option<String>,
    session_id: Option<String>,
    keep_children: Option<bool>,
) -> CmdResult<()> {
    if let Some(id) = workspace_id {
        let args = rm_args(&id, keep_children.unwrap_or(false));
        return ws_items(&state.runner, &args).await.map(|_| ());
    }
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
#[tauri::command]
pub async fn rename_workspace(
    state: State<'_, AppState>,
    workspace_id: String,
    name: String,
) -> CmdResult<Renamed> {
    state
        .runner
        .ws_json(&strings(&["rename", "--", &workspace_id, &name]))
        .await
}

#[derive(Debug, Deserialize, Serialize)]
pub struct HiddenSet {
    pub id: String,
    pub hidden: bool,
}

/// The arguments that hide or show a workspace: `ws hide` or `ws unhide`
/// (ADR 0043).
pub fn set_hidden_args(workspace_id: &str, hidden: bool) -> Vec<String> {
    let op = if hidden { "hide" } else { "unhide" };
    strings(&[op, "--", workspace_id])
}

/// Hides or shows a workspace.
#[tauri::command]
pub async fn set_workspace_hidden(
    state: State<'_, AppState>,
    workspace_id: String,
    hidden: bool,
) -> CmdResult<HiddenSet> {
    state
        .runner
        .ws_json(&set_hidden_args(&workspace_id, hidden))
        .await
}

#[tauri::command]
pub async fn stop_agent(state: State<'_, AppState>, session_id: String) -> CmdResult<()> {
    state
        .runner
        .claude(&strings(&["stop", &session_id]))
        .await
        .map(|_| ())
}

/// Respawns every stopped agent workspace: `ws restore`. Returns the ids
/// restored.
#[tauri::command]
pub async fn restore_agents(state: State<'_, AppState>) -> CmdResult<Vec<String>> {
    ws_items(&state.runner, &["restore"]).await
}

/// Opens a URL in the browser, or a directory in the file manager.
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

/// Reads a background session's conversation from offset on, so the
/// frontend can follow it by asking again from the `next` it returns.
#[tauri::command]
pub async fn read_transcript(
    state: State<'_, AppState>,
    session_id: String,
    offset: u64,
) -> CmdResult<Chunk> {
    let jobs_dir = state
        .jobs_dir
        .clone()
        .ok_or_else(|| AppError::new("transcript_missing", "no Claude Code config directory"))?;
    tauri::async_runtime::spawn_blocking(move || conversation(&jobs_dir, &session_id, offset))
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

#[tauri::command]
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

#[tauri::command]
pub fn terminal_write(state: State<'_, AppState>, id: u32, data: String) -> CmdResult<()> {
    state.terminals.write(id, &data)
}

#[tauri::command]
pub fn terminal_resize(state: State<'_, AppState>, id: u32, cols: u16, rows: u16) -> CmdResult<()> {
    state.terminals.resize(id, cols, rows)
}

#[tauri::command]
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
        }
        dir
    }

    #[tokio::test]
    async fn list_merges_ws_and_claude() {
        let dir = fake_programs(
            r#"echo '{"workspaces":[{"id":"w","path":"/w","created_at":null,"session":{"kind":"agent","id":"s1","state":"working","pid":1,"stale":false},"repos":[]}],"warnings":["careful"]}'"#,
            r#"echo '[{"id":"s2","sessionId":"u","cwd":"/x","kind":"background","status":"busy","pid":2,"startedAt":1}]'"#,
        );
        let runner = Runner::new(dir.path().display().to_string());
        let listing = list(&runner, None).await.unwrap();
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
        let listing = list(&runner, None).await.unwrap();
        assert_eq!(listing.warnings, ["claude agents: daemon down"]);
    }

    #[tokio::test]
    async fn list_fails_with_ws_error_when_both_fail() {
        let dir = fake_programs(
            r#"echo '{"error":{"code":"config","message":"bad config"}}' >&2; exit 2"#,
            "exit 1",
        );
        let runner = Runner::new(dir.path().display().to_string());
        let err = list(&runner, None).await.unwrap_err();
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
    fn rm_args_keep_children_or_not() {
        assert_eq!(rm_args("x", false), ["rm", "--", "x"]);
        assert_eq!(rm_args("-x", true), ["rm", "--keep-children", "--", "-x"]);
    }

    #[test]
    fn set_hidden_args_hide_or_unhide() {
        assert_eq!(
            set_hidden_args("x-d8efb24f", true),
            ["hide", "--", "x-d8efb24f"]
        );
        assert_eq!(set_hidden_args("-x", false), ["unhide", "--", "-x"]);
    }

    #[tokio::test]
    async fn set_workspace_hidden_runs_ws_hide() {
        let dir = fake_programs(
            r#"printf '{"id":"%s","name":null,"path":"/w","hidden":%s,"warnings":[]}' "$4" "$([ "$1" = hide ] && echo true || echo false)""#,
            "exit 0",
        );
        let runner = Runner::new(dir.path().display().to_string());
        let set: HiddenSet = runner
            .ws_json(&set_hidden_args("x-d8efb24f", true))
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
        let listing = list(&runner, jobs::jobs_dir().as_deref()).await.unwrap();
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
        }
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
    fn new_agent_args_ignores_blank_name_and_prompt() {
        let req = NewAgent {
            name: Some(" ".into()),
            prompt: Some("\n".into()),
            ..request()
        };
        assert_eq!(new_agent_args(&req), ["claude"]);
    }
}
