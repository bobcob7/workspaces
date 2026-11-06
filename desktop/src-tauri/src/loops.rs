//! Agent loops (ADR 0048): `ws loop … --json`, and a `ws loop tick` every
//! minute while the app is open.

use crate::commands::AppState;
use crate::runner::{AppError, Runner};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::collections::BTreeMap;
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
pub struct LoopGate {
    pub at: String,
    pub outcome: String,
    pub exit_code: Option<i64>,
    pub message: Option<String>,
}

#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
#[serde(rename_all(deserialize = "snake_case", serialize = "camelCase"))]
pub struct Loop {
    pub name: String,
    #[serde(default)]
    pub title: String,
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
    #[serde(default)]
    pub script: Option<String>,
    #[serde(default)]
    pub script_timeout: Option<String>,
    #[serde(default)]
    pub vars: BTreeMap<String, String>,
    #[serde(default)]
    pub last_gate: Option<LoopGate>,
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

/// One row of the form's variables.
#[derive(Debug, Clone, Deserialize)]
pub struct LoopVar {
    pub key: String,
    pub value: String,
}

/// What the Loops form sends.
#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct NewLoop {
    pub name: String,
    #[serde(default)]
    pub title: Option<String>,
    pub every: String,
    pub prompt: String,
    #[serde(default)]
    pub specs: Vec<String>,
    pub model: Option<String>,
    pub permission_mode: Option<String>,
    pub keep: Option<u32>,
    pub script: Option<String>,
    pub script_timeout: Option<String>,
    #[serde(default)]
    pub vars: Vec<LoopVar>,
}

fn non_blank(value: Option<&str>) -> Option<&str> {
    value.map(str::trim).filter(|s| !s.is_empty())
}

/// `ws loop add` without `--json`, which the runner adds:
/// ["loop", "add", ("--title=<t>"), "--every=<every>", "--prompt=<prompt>",
///  ("--keep=<n>"), ("--model=<m>"), ("--permission-mode=<p>"),
///  ("--script=<s>"), ("--script-timeout=<t>"), ("--var=<k>=<v>")...,
///  "--", <name>, <specs...>].
/// Blank optional values, and blank specs, are left out; values are trimmed,
/// except the script (whose whitespace matters) and a var's value.
/// A script timeout needs a script; a var needs a non-blank key.
pub fn add_args(req: &NewLoop) -> Vec<String> {
    let mut args = vec!["loop".to_string(), "add".to_string()];
    if let Some(title) = non_blank(req.title.as_deref()) {
        args.push(format!("--title={title}"));
    }
    args.push(format!("--every={}", req.every.trim()));
    args.push(format!("--prompt={}", req.prompt.trim()));
    if let Some(keep) = req.keep {
        args.push(format!("--keep={keep}"));
    }
    if let Some(model) = non_blank(req.model.as_deref()) {
        args.push(format!("--model={model}"));
    }
    if let Some(mode) = non_blank(req.permission_mode.as_deref()) {
        args.push(format!("--permission-mode={mode}"));
    }
    if let Some(script) = req.script.as_deref().filter(|s| !s.trim().is_empty()) {
        args.push(format!("--script={script}"));
        if let Some(timeout) = non_blank(req.script_timeout.as_deref()) {
            args.push(format!("--script-timeout={timeout}"));
        }
    }
    for var in &req.vars {
        if let Some(key) = non_blank(Some(var.key.as_str())) {
            args.push(format!("--var={key}={}", var.value));
        }
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

/// `ws loop edit` without `--json`, which the runner adds. Every field is
/// sent, so the request replaces the loop's editable fields (ADR 0052):
/// ["loop", "edit", "--title=<t>", "--every=<every>", "--prompt=<prompt>",
///  ("--keep=<n>"), "--model=<m>", "--permission-mode=<p>",
///  ("--script=<s>", ("--script-timeout=<t>")) | "--no-script",
///  ("--var=<k>=<v>")... | "--no-vars", ("--no-specs"),
///  "--", <name>, <specs...>].
/// Values are trimmed as in add_args (not the script or a var's value);
/// a blank title, model or permission mode is sent as "" to clear it.
pub fn edit_args(req: &NewLoop) -> Vec<String> {
    let title = non_blank(req.title.as_deref()).unwrap_or("");
    let mut args = vec![
        "loop".to_string(),
        "edit".to_string(),
        format!("--title={title}"),
        format!("--every={}", req.every.trim()),
        format!("--prompt={}", req.prompt.trim()),
    ];
    if let Some(keep) = req.keep {
        args.push(format!("--keep={keep}"));
    }
    let model = non_blank(req.model.as_deref()).unwrap_or("");
    args.push(format!("--model={model}"));
    let mode = non_blank(req.permission_mode.as_deref()).unwrap_or("");
    args.push(format!("--permission-mode={mode}"));
    match req.script.as_deref().filter(|s| !s.trim().is_empty()) {
        Some(script) => {
            args.push(format!("--script={script}"));
            if let Some(timeout) = non_blank(req.script_timeout.as_deref()) {
                args.push(format!("--script-timeout={timeout}"));
            }
        }
        None => args.push("--no-script".to_string()),
    }
    let vars: Vec<String> = req
        .vars
        .iter()
        .filter_map(|var| {
            non_blank(Some(var.key.as_str())).map(|key| format!("--var={key}={}", var.value))
        })
        .collect();
    if vars.is_empty() {
        args.push("--no-vars".to_string());
    }
    args.extend(vars);
    let specs: Vec<String> = req
        .specs
        .iter()
        .filter_map(|s| non_blank(Some(s)).map(str::to_string))
        .collect();
    if specs.is_empty() {
        args.push("--no-specs".to_string());
    }
    args.push("--".to_string());
    args.push(req.name.trim().to_string());
    args.extend(specs);
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
pub async fn loops_edit(state: State<'_, AppState>, request: NewLoop) -> CmdResult<Loop> {
    state.runner.ws_json(&edit_args(&request)).await
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
            title: None,
            every: "24h".into(),
            prompt: "hi".into(),
            specs: vec![],
            model: None,
            permission_mode: None,
            keep: None,
            script: None,
            script_timeout: None,
            vars: vec![],
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
    fn add_args_passes_script_timeout_and_vars() {
        let req = NewLoop {
            script: Some("gh issue list | grep .\n".into()),
            script_timeout: Some(" 2m ".into()),
            vars: vec![
                LoopVar {
                    key: " REPO ".into(),
                    value: "a b".into(),
                },
                LoopVar {
                    key: " ".into(),
                    value: "x".into(),
                },
            ],
            ..request()
        };
        assert_eq!(
            add_args(&req),
            [
                "loop",
                "add",
                "--every=24h",
                "--prompt=hi",
                "--script=gh issue list | grep .\n",
                "--script-timeout=2m",
                "--var=REPO=a b",
                "--",
                "deps",
            ]
        );
    }

    #[test]
    fn add_args_skips_script_timeout_without_script() {
        let req = NewLoop {
            script: Some("  ".into()),
            script_timeout: Some("2m".into()),
            ..request()
        };
        assert_eq!(
            add_args(&req),
            ["loop", "add", "--every=24h", "--prompt=hi", "--", "deps"]
        );
    }

    #[test]
    fn edit_args_clears_blank_fields_and_sends_no_flags() {
        assert_eq!(
            edit_args(&request()),
            [
                "loop",
                "edit",
                "--title=",
                "--every=24h",
                "--prompt=hi",
                "--model=",
                "--permission-mode=",
                "--no-script",
                "--no-vars",
                "--no-specs",
                "--",
                "deps",
            ]
        );
    }

    #[test]
    fn edit_args_sends_every_field() {
        let req = NewLoop {
            specs: vec!["buff-bear".into(), " ".into()],
            model: Some(" opus ".into()),
            permission_mode: Some("acceptEdits".into()),
            keep: Some(3),
            script: Some("gh issue list | grep .\n".into()),
            script_timeout: Some(" 2m ".into()),
            vars: vec![
                LoopVar {
                    key: "REPO".into(),
                    value: "a b".into(),
                },
                LoopVar {
                    key: " ".into(),
                    value: "x".into(),
                },
            ],
            ..request()
        };
        assert_eq!(
            edit_args(&req),
            [
                "loop",
                "edit",
                "--title=",
                "--every=24h",
                "--prompt=hi",
                "--keep=3",
                "--model=opus",
                "--permission-mode=acceptEdits",
                "--script=gh issue list | grep .\n",
                "--script-timeout=2m",
                "--var=REPO=a b",
                "--",
                "deps",
                "buff-bear",
            ]
        );
    }

    #[test]
    fn edit_args_blank_script_ignores_timeout() {
        let req = NewLoop {
            script: Some("  ".into()),
            script_timeout: Some("2m".into()),
            ..request()
        };
        assert_eq!(
            edit_args(&req),
            [
                "loop",
                "edit",
                "--title=",
                "--every=24h",
                "--prompt=hi",
                "--model=",
                "--permission-mode=",
                "--no-script",
                "--no-vars",
                "--no-specs",
                "--",
                "deps",
            ]
        );
    }

    #[test]
    fn add_args_passes_trimmed_title_before_every() {
        let req = NewLoop {
            title: Some("  Nightly deps bump ".into()),
            ..request()
        };
        assert_eq!(
            add_args(&req),
            [
                "loop",
                "add",
                "--title=Nightly deps bump",
                "--every=24h",
                "--prompt=hi",
                "--",
                "deps",
            ]
        );
    }

    #[test]
    fn add_args_skips_blank_title() {
        let req = NewLoop {
            title: Some("   ".into()),
            ..request()
        };
        assert_eq!(
            add_args(&req),
            ["loop", "add", "--every=24h", "--prompt=hi", "--", "deps"]
        );
    }

    #[test]
    fn edit_args_sends_trimmed_title() {
        let req = NewLoop {
            title: Some(" Nightly deps bump ".into()),
            ..request()
        };
        let args = edit_args(&req);
        assert_eq!(args[2], "--title=Nightly deps bump");
        assert_eq!(args[3], "--every=24h");
    }

    #[test]
    fn edit_args_blank_title_clears_it() {
        let req = NewLoop {
            title: Some("  ".into()),
            ..request()
        };
        let args = edit_args(&req);
        assert_eq!(args[2], "--title=");
    }

    #[test]
    fn loop_title_round_trips_and_defaults_to_empty() {
        let titled = r#"{"name":"deps-bump","title":"Nightly deps","every":"24h","prompt":"hi","specs":[],"model":null,"permission_mode":null,"keep":5,"paused":false,"created_at":"2026-11-05T18:00:00Z","next_run_at":null,"last_run":null}"#;
        let parsed: Loop = serde_json::from_str(titled).unwrap();
        assert_eq!(parsed.title, "Nightly deps");
        let value = serde_json::to_value(&parsed).unwrap();
        assert_eq!(value["title"], "Nightly deps");

        let untitled = r#"{"name":"deps-bump","every":"24h","prompt":"hi","specs":[],"model":null,"permission_mode":null,"keep":5,"paused":false,"created_at":"2026-11-05T18:00:00Z","next_run_at":null,"last_run":null}"#;
        let parsed: Loop = serde_json::from_str(untitled).unwrap();
        assert_eq!(parsed.title, "");
    }

    #[tokio::test]
    async fn edit_args_parse_ws_loop_edit_json() {
        let dir = fake_ws(
            r#"while IFS= read -r line; do printf '%s\n' "$line"; done <<'EOF'
{"name":"deps","every":"24h","prompt":"hi","specs":[],"model":null,"permission_mode":null,"keep":5,"paused":false,"created_at":"2026-11-05T18:00:00Z","next_run_at":null,"last_run":null}
EOF"#,
        );
        let runner = Runner::new(dir.path().display().to_string());
        let loop_: Loop = runner.ws_json(&edit_args(&request())).await.unwrap();
        assert_eq!(loop_.name, "deps");
        assert_eq!(loop_.prompt, "hi");
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
    async fn list_parses_script_vars_and_last_gate() {
        let dir = fake_ws(
            r#"while IFS= read -r line; do printf '%s\n' "$line"; done <<'EOF'
{"loops":[{"name":"deps-bump","every":"24h","prompt":"Update the Go and npm dependencies, run the tests, and open a PR","specs":["buff-bear"],"model":null,"permission_mode":"acceptEdits","keep":5,"paused":false,"created_at":"2026-11-05T18:00:00Z","next_run_at":"2026-11-06T18:00:00Z","last_run":{"started_at":"2026-11-05T18:00:04Z","workspace":"loop-deps-bump-1a2b3c4d","session":"e8cdf5d8","error":null},"script":"go list -m -u -f '{{if .Update}}{{.Path}}{{end}}' all | grep .","script_timeout":"2m","vars":{"BRANCH":"main"},"last_gate":{"at":"2026-11-05T18:00:02Z","outcome":"passed","exit_code":0,"message":null}}],"warnings":[]}
EOF"#,
        );
        let runner = Runner::new(dir.path().display().to_string());
        let loops: Loops = runner
            .ws_json(&["loop".to_string(), "list".to_string()])
            .await
            .unwrap();

        let value = serde_json::to_value(&loops.loops[0]).unwrap();
        assert_eq!(value["scriptTimeout"], "2m");
        assert_eq!(value["lastGate"]["exitCode"], 0);
        assert_eq!(value["vars"]["BRANCH"], "main");
        assert_eq!(value["lastGate"]["outcome"], "passed");
    }

    #[test]
    fn loop_without_script_fields_parses_with_defaults() {
        let json = r#"{"name":"deps-bump","every":"24h","prompt":"hi","specs":[],"model":null,"permission_mode":null,"keep":5,"paused":false,"created_at":"2026-11-05T18:00:00Z","next_run_at":null,"last_run":null}"#;
        let parsed: Loop = serde_json::from_str(json).unwrap();
        assert!(parsed.vars.is_empty());
        assert_eq!(parsed.script, None);
        assert_eq!(parsed.script_timeout, None);
        assert_eq!(parsed.last_gate, None);
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
