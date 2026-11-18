//! The runner's service and endpoint checks for Settings › Models (ADR 0068):
//! `ws daemon status`, `ws daemon stop --keep-runners` and `ws runner check`.
//! Their objects pass through as ws prints them.

use crate::commands::AppState;
use crate::ipc::State;
use crate::runner::{AppError, Runner};
use serde::{Deserialize, Serialize};

type CmdResult<T> = Result<T, AppError>;

/// `ws daemon status`: whether the daemon runs, and who started it.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct DaemonStatus {
    pub running: bool,
    pub pid: Option<u32>,
    pub origin: Option<String>,
    pub started_at: Option<String>,
    pub ws_version: Option<String>,
    pub socket: Option<String>,
    pub config_dir: Option<String>,
    pub runners: Vec<DaemonRunner>,
    pub warnings: Vec<String>,
}

/// One runner the daemon supervises.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct DaemonRunner {
    pub workspace: String,
    pub session: String,
    pub pid: Option<u32>,
    pub state: String,
    pub restarts: u32,
    pub adopted: bool,
}

/// `ws daemon stop --keep-runners`: what the daemon stopped and kept.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct DaemonStopped {
    pub stopped: bool,
    pub pid: Option<u32>,
    pub runners_stopped: Vec<String>,
    pub runners_kept: Vec<String>,
    pub warnings: Vec<String>,
}

/// `ws runner check`: what an endpoint serves, and whether a model is ready.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct RunnerCheck {
    pub endpoint: String,
    pub url: String,
    pub api: String,
    pub probe: String,
    pub model: String,
    pub version: Option<String>,
    pub capabilities: Option<Vec<String>>,
    pub context_tokens: i64,
    pub models: Vec<String>,
    pub token_source: Option<String>,
    pub warnings: Vec<String>,
}

fn args(parts: &[&str]) -> Vec<String> {
    parts.iter().map(|part| part.to_string()).collect()
}

/// The daemon's status (`ws daemon status --json`). It never starts one.
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn daemon_status(state: State<'_, AppState>) -> CmdResult<DaemonStatus> {
    status(&state.runner).await
}

pub(crate) async fn status(runner: &Runner) -> CmdResult<DaemonStatus> {
    runner
        .ws_json_as_given(&args(&["daemon", "status", "--json"]))
        .await
}

/// Stops the daemon and keeps its runners (`ws daemon stop --keep-runners
/// --json`); the desktop app's host starts it again.
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn daemon_restart(state: State<'_, AppState>) -> CmdResult<DaemonStopped> {
    restart(&state.runner).await
}

pub(crate) async fn restart(runner: &Runner) -> CmdResult<DaemonStopped> {
    runner
        .ws_json_as_given(&args(&["daemon", "stop", "--keep-runners", "--json"]))
        .await
}

/// Runs the spawn preflight for an endpoint, and for a model on it
/// (`ws runner check --json [--model <m>] [-- <endpoint>]`). Empty strings leave
/// the default to ws.
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn runner_check(
    state: State<'_, AppState>,
    endpoint: String,
    model: String,
) -> CmdResult<RunnerCheck> {
    check(&state.runner, &endpoint, &model).await
}

pub(crate) async fn check(runner: &Runner, endpoint: &str, model: &str) -> CmdResult<RunnerCheck> {
    let mut args = args(&["runner", "check", "--json"]);
    if !model.is_empty() {
        args.extend(["--model".to_string(), model.to_string()]);
    }
    if !endpoint.is_empty() {
        args.extend(["--".to_string(), endpoint.to_string()]);
    }
    runner.ws_json_as_given(&args).await
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::ws_config::tests::{args as recorded, fake_ws};

    const STATUS: &str = r#"{"running":true,"pid":40211,"origin":"app","started_at":"2026-11-14T08:00:03Z","ws_version":"v0.31.0","socket":"/run/ws.sock","config_dir":"/c","runners":[{"workspace":"x-t1","session":"rn-5d2e8b10","pid":51234,"state":"working","restarts":0,"adopted":false}],"warnings":[]}"#;

    #[tokio::test]
    async fn status_asks_for_the_daemon_status_as_json() {
        let (dir, runner) = fake_ws(&format!("echo '{STATUS}'"));
        let daemon = status(&runner).await.unwrap();
        assert_eq!(recorded(&dir), "daemon status --json");
        assert_eq!(daemon.origin.as_deref(), Some("app"));
        assert_eq!(daemon.runners[0].session, "rn-5d2e8b10");
    }

    #[tokio::test]
    async fn status_reads_a_daemon_that_is_not_running() {
        let (_dir, runner) = fake_ws(
            r#"echo '{"running":false,"pid":null,"origin":null,"started_at":null,"ws_version":null,"socket":"/s.sock","config_dir":"/c","runners":[],"warnings":[]}'"#,
        );
        let daemon = status(&runner).await.unwrap();
        assert!(!daemon.running);
        assert_eq!(daemon.origin, None);
    }

    #[tokio::test]
    async fn restart_stops_the_daemon_and_keeps_its_runners() {
        let (dir, runner) = fake_ws(
            r#"echo '{"stopped":true,"pid":40211,"runners_stopped":[],"runners_kept":["rn-5d2e8b10"],"warnings":[]}'"#,
        );
        let stopped = restart(&runner).await.unwrap();
        assert_eq!(recorded(&dir), "daemon stop --keep-runners --json");
        assert!(stopped.stopped);
        assert_eq!(stopped.runners_kept, ["rn-5d2e8b10"]);
    }

    #[tokio::test]
    async fn check_passes_the_endpoint_and_model() {
        let (dir, runner) = fake_ws(
            r#"echo '{"endpoint":"gpu","url":"http://gpu:1234","api":"openai","probe":"none","model":"qwen/qwen3.5-9b","version":null,"capabilities":null,"context_tokens":65536,"models":["qwen/qwen3.5-9b","x"],"token_source":"secrets.env","warnings":[]}'"#,
        );
        let result = check(&runner, "gpu", "qwen9b").await.unwrap();
        assert_eq!(recorded(&dir), "runner check --json --model qwen9b -- gpu");
        assert_eq!(result.models.len(), 2);
        assert_eq!(result.token_source.as_deref(), Some("secrets.env"));
        assert_eq!(result.capabilities, None);
    }

    #[tokio::test]
    async fn check_without_an_endpoint_leaves_the_default_to_ws() {
        let (dir, runner) = fake_ws(
            r#"echo '{"endpoint":"gpu","url":"http://gpu:1234","api":"openai","probe":"ollama","model":"qwen3.5:9b","version":"0.40.16","capabilities":["completion","tools"],"context_tokens":65536,"models":[],"token_source":null,"warnings":[]}'"#,
        );
        let result = check(&runner, "", "").await.unwrap();
        assert_eq!(recorded(&dir), "runner check --json");
        assert_eq!(result.capabilities.unwrap(), ["completion", "tools"]);
    }

    #[tokio::test]
    async fn check_does_not_let_an_endpoint_be_read_as_a_flag() {
        let (dir, runner) = fake_ws(
            "echo '{\"error\":{\"code\":\"endpoint_unknown\",\"message\":\"no\"}}' >&2; exit 1",
        );
        check(&runner, "--model=x", "").await.unwrap_err();
        assert_eq!(recorded(&dir), "runner check --json -- --model=x");
    }

    #[tokio::test]
    async fn check_keeps_the_preflight_error_code() {
        let (_dir, runner) = fake_ws(
            r#"echo '{"error":{"code":"endpoint_unreachable","message":"gpu did not answer"}}' >&2; exit 1"#,
        );
        let err = check(&runner, "gpu", "").await.unwrap_err();
        assert_eq!(err.code, "endpoint_unreachable");
    }
}
