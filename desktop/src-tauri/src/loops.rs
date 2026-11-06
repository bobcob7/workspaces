//! Agent loops (ADR 0048): `ws loop … --json`, and a `ws loop tick` every
//! minute while the app is open.

use crate::commands::AppState;
use crate::runner::{AppError, Runner};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::time::Duration;
use tauri::State;

type CmdResult<T> = Result<T, AppError>;

/// How often the app runs `ws loop tick`.
pub const TICK_INTERVAL: Duration = Duration::from_secs(60);

#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
#[serde(rename_all(deserialize = "snake_case", serialize = "camelCase"))]
pub struct LoopError {
    pub code: String,
    pub message: String,
}

#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
#[serde(rename_all(deserialize = "snake_case", serialize = "camelCase"))]
pub struct LoopRun {
    pub started_at: String,
    pub workspace: Option<String>,
    pub session: Option<String>,
    pub error: Option<LoopError>,
}

#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
#[serde(rename_all(deserialize = "snake_case", serialize = "camelCase"))]
pub struct Loop {
    pub name: String,
    pub every: String,
    pub prompt: String,
    pub specs: Vec<String>,
    pub model: Option<String>,
    pub permission_mode: Option<String>,
    pub keep: u32,
    pub paused: bool,
    pub created_at: String,
    pub next_run_at: Option<String>,
    pub last_run: Option<LoopRun>,
}

#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
pub struct Loops {
    pub loops: Vec<Loop>,
    pub warnings: Vec<String>,
}

#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
#[serde(rename_all(deserialize = "snake_case", serialize = "camelCase"))]
pub struct LoopStarted {
    pub name: String,
    pub workspace: String,
    pub session: String,
    pub path: String,
}

/// What the Loops form sends.
#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct NewLoop {
    pub name: String,
    pub every: String,
    pub prompt: String,
    #[serde(default)]
    pub specs: Vec<String>,
    pub model: Option<String>,
    pub permission_mode: Option<String>,
    pub keep: Option<u32>,
}

fn non_blank(value: Option<&str>) -> Option<&str> {
    value.map(str::trim).filter(|s| !s.is_empty())
}

/// `ws loop add` without `--json`, which the runner adds:
/// ["loop", "add", "--every=<every>", "--prompt=<prompt>", ("--keep=<n>"),
///  ("--model=<m>"), ("--permission-mode=<p>"), "--", <name>, <specs...>].
/// Blank optional values, and blank specs, are left out; values are trimmed.
pub fn add_args(req: &NewLoop) -> Vec<String> {
    let mut args = vec![
        "loop".to_string(),
        "add".to_string(),
        format!("--every={}", req.every.trim()),
        format!("--prompt={}", req.prompt.trim()),
    ];
    if let Some(keep) = req.keep {
        args.push(format!("--keep={keep}"));
    }
    if let Some(model) = non_blank(req.model.as_deref()) {
        args.push(format!("--model={model}"));
    }
    if let Some(mode) = non_blank(req.permission_mode.as_deref()) {
        args.push(format!("--permission-mode={mode}"));
    }
    args.push("--".to_string());
    args.push(req.name.trim().to_string());
    args.extend(
        req.specs
            .iter()
            .filter_map(|s| non_blank(Some(s)))
            .map(str::to_string),
    );
    args
}

/// ["loop", "pause" | "resume", "--", <name>]
pub fn pause_args(name: &str, paused: bool) -> Vec<String> {
    let action = if paused { "pause" } else { "resume" };
    vec![
        "loop".to_string(),
        action.to_string(),
        "--".to_string(),
        name.to_string(),
    ]
}

/// ["loop", "rm", "--", <name>]
fn rm_args(name: &str) -> Vec<String> {
    vec![
        "loop".to_string(),
        "rm".to_string(),
        "--".to_string(),
        name.to_string(),
    ]
}

/// ["loop", "run", "--", <name>]
fn run_args(name: &str) -> Vec<String> {
    vec![
        "loop".to_string(),
        "run".to_string(),
        "--".to_string(),
        name.to_string(),
    ]
}

#[tauri::command]
pub async fn loops_list(state: State<'_, AppState>) -> CmdResult<Loops> {
    state
        .runner
        .ws_json(&["loop".to_string(), "list".to_string()])
        .await
}

#[tauri::command]
pub async fn loops_add(state: State<'_, AppState>, request: NewLoop) -> CmdResult<Loop> {
    state.runner.ws_json(&add_args(&request)).await
}

#[tauri::command]
pub async fn loops_set_paused(
    state: State<'_, AppState>,
    name: String,
    paused: bool,
) -> CmdResult<Loop> {
    state.runner.ws_json(&pause_args(&name, paused)).await
}

#[tauri::command]
pub async fn loops_remove(state: State<'_, AppState>, name: String) -> CmdResult<Loop> {
    state.runner.ws_json(&rm_args(&name)).await
}

#[tauri::command]
pub async fn loops_run_now(state: State<'_, AppState>, name: String) -> CmdResult<LoopStarted> {
    state.runner.ws_json(&run_args(&name)).await
}

/// Runs `ws loop tick --quiet --json` now and then every TICK_INTERVAL,
/// forever, ignoring its result (failures are logged with eprintln!).
pub async fn tick_forever(runner: Runner) {
    let mut interval = tokio::time::interval(TICK_INTERVAL);
    interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
    let args = [
        "loop".to_string(),
        "tick".to_string(),
        "--quiet".to_string(),
        "--json".to_string(),
    ];
    loop {
        interval.tick().await;
        if let Err(err) = runner.ws_json_as_given::<Value>(&args).await {
            eprintln!("ws loop tick failed: {}: {}", err.code, err.message);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::PermissionsExt;

    /// A directory holding a fake `ws` script, for PATH.
    fn fake_ws(body: &str) -> tempfile::TempDir {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("ws");
        std::fs::write(&path, format!("#!/bin/sh\n{body}\n")).unwrap();
        std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o755)).unwrap();
        dir
    }

    fn request() -> NewLoop {
        NewLoop {
            name: "deps".into(),
            every: "24h".into(),
            prompt: "hi".into(),
            specs: vec![],
            model: None,
            permission_mode: None,
            keep: None,
        }
    }

    #[test]
    fn add_args_puts_options_before_separator_and_skips_blanks() {
        let req = NewLoop {
            specs: vec!["buff-bear".into(), " ".into()],
            model: Some("".into()),
            permission_mode: Some("acceptEdits".into()),
            keep: Some(3),
            ..request()
        };
        assert_eq!(
            add_args(&req),
            [
                "loop",
                "add",
                "--every=24h",
                "--prompt=hi",
                "--keep=3",
                "--permission-mode=acceptEdits",
                "--",
                "deps",
                "buff-bear",
            ]
        );
    }

    #[test]
    fn add_args_trims_values() {
        let req = NewLoop {
            name: " deps ".into(),
            every: " 24h".into(),
            prompt: "  hi  ".into(),
            model: Some(" opus ".into()),
            ..request()
        };
        assert_eq!(
            add_args(&req),
            [
                "loop",
                "add",
                "--every=24h",
                "--prompt=hi",
                "--model=opus",
                "--",
                "deps",
            ]
        );
    }

    #[test]
    fn pause_args_pause_or_resume() {
        assert_eq!(pause_args("deps", true), ["loop", "pause", "--", "deps"]);
        assert_eq!(pause_args("deps", false), ["loop", "resume", "--", "deps"]);
    }

    #[tokio::test]
    async fn list_parses_ws_loop_list_json() {
        let dir = fake_ws(
            r#"echo '{"loops":[{"name":"deps-bump","every":"24h","prompt":"Update the Go and npm dependencies, run the tests, and open a PR","specs":["buff-bear"],"model":null,"permission_mode":"acceptEdits","keep":5,"paused":false,"created_at":"2026-11-05T18:00:00Z","next_run_at":"2026-11-06T18:00:00Z","last_run":{"started_at":"2026-11-05T18:00:04Z","workspace":"loop-deps-bump-1a2b3c4d","session":"e8cdf5d8","error":null}}],"warnings":[]}'"#,
        );
        let runner = Runner::new(dir.path().display().to_string());
        let loops: Loops = runner
            .ws_json(&["loop".to_string(), "list".to_string()])
            .await
            .unwrap();
        assert_eq!(loops.loops.len(), 1);
        assert!(loops.warnings.is_empty());

        let value = serde_json::to_value(&loops.loops[0]).unwrap();
        assert_eq!(value["nextRunAt"], "2026-11-06T18:00:00Z");
        assert_eq!(value["permissionMode"], "acceptEdits");
        assert_eq!(value["lastRun"]["startedAt"], "2026-11-05T18:00:04Z");
    }

    #[tokio::test]
    async fn ws_error_becomes_app_error_code() {
        let dir = fake_ws(r#"echo '{"error":{"code":"loop_exists","message":"x"}}' >&2; exit 4"#);
        let runner = Runner::new(dir.path().display().to_string());
        let err = runner
            .ws_json::<Loop>(&add_args(&request()))
            .await
            .unwrap_err();
        assert_eq!(err.code, "loop_exists");
        assert_eq!(err.message, "x");
    }
}
