//! Agent loops (ADR 0048): `ws loop … --json`, and a `ws loop tick` every
//! minute while the app is open.

use crate::commands::{env_args, AppState};
use crate::ipc::State;
use crate::pty::remote_env;
use crate::runner::{AppError, Runner};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::collections::BTreeMap;
use std::time::Duration;

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
    /// Null in a summary loop read from a snapshot.
    #[serde(default, deserialize_with = "null_default")]
    pub prompt: String,
    #[serde(default, deserialize_with = "null_default")]
    pub specs: Vec<String>,
    pub model: Option<String>,
    pub permission_mode: Option<String>,
    #[serde(default, deserialize_with = "null_default")]
    pub keep: u32,
    pub paused: bool,
    #[serde(default, deserialize_with = "null_default")]
    pub created_at: String,
    pub next_run_at: Option<String>,
    pub last_run: Option<LoopRun>,
    #[serde(default)]
    pub script: Option<String>,
    #[serde(default)]
    pub script_timeout: Option<String>,
    #[serde(default, deserialize_with = "null_default")]
    pub vars: BTreeMap<String, String>,
    #[serde(default)]
    pub last_gate: Option<LoopGate>,
    /// Whether runs start hidden (ADR 0057).
    #[serde(default = "default_true")]
    pub hidden: bool,
    /// What runs each run: `claude` or `ws` (ADR 0070).
    #[serde(default)]
    pub runner: Option<String>,
    /// The endpoint a `ws` loop's runs use instead of the model's.
    #[serde(default)]
    pub endpoint: Option<String>,
    /// The environment a remote loop lives on; None for a local one.
    #[serde(default)]
    pub environment: Option<String>,
    /// The snapshot is old or the environment offline (`ws loop list --env`).
    #[serde(default)]
    pub stale: bool,
    /// Read from a snapshot: no prompt, script or vars.
    #[serde(default)]
    pub summary: bool,
}

fn default_true() -> bool {
    true
}

/// A JSON null, or a missing value, becomes the type's default.
fn null_default<'de, D, T>(deserializer: D) -> Result<T, D::Error>
where
    D: serde::Deserializer<'de>,
    T: Default + Deserialize<'de>,
{
    Ok(Option::<T>::deserialize(deserializer)?.unwrap_or_default())
}

#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
#[serde(rename_all(deserialize = "snake_case", serialize = "camelCase"))]
pub struct Loops {
    pub loops: Vec<Loop>,
    #[serde(default)]
    pub warnings: Vec<String>,
    #[serde(default)]
    pub environment_status: Option<String>,
    #[serde(default)]
    pub loops_unknown: bool,
}

#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
#[serde(rename_all(deserialize = "snake_case", serialize = "camelCase"))]
pub struct LoopStarted {
    pub name: String,
    pub workspace: String,
    pub session: String,
    pub path: String,
}

/// A run's verdict, from its report (ADR 0056).
#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
#[serde(rename_all(deserialize = "snake_case", serialize = "camelCase"))]
pub struct LoopRunVerdict {
    pub verdict: String,
    pub outcome: String,
    pub recorded_at: String,
    pub report: Option<String>,
}

/// One run in a loop's history, from `ws loop history --json`.
#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
#[serde(rename_all(deserialize = "snake_case", serialize = "camelCase"))]
pub struct LoopHistoryRun {
    pub seq: u32,
    pub status: String,
    pub trigger: String,
    pub started_at: String,
    pub finished_at: Option<String>,
    pub duration_seconds: Option<u64>,
    pub workspace: Option<String>,
    pub workspace_exists: bool,
    pub session: Option<String>,
    pub error: Option<LoopError>,
    pub gate: Option<LoopGate>,
    pub repeats: u32,
    pub verdict: Option<LoopRunVerdict>,
}

/// A loop's run history, newest first.
#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
#[serde(rename_all(deserialize = "snake_case", serialize = "camelCase"))]
pub struct LoopHistory {
    #[serde(rename = "loop")]
    pub loop_name: String,
    pub runs: Vec<LoopHistoryRun>,
    pub total: u32,
    pub warnings: Vec<String>,
}

/// One run, with its report's text when it has one.
#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
#[serde(rename_all(deserialize = "snake_case", serialize = "camelCase"))]
pub struct LoopRunDetail {
    #[serde(rename = "loop")]
    pub loop_name: String,
    pub run: LoopHistoryRun,
    pub report_text: Option<String>,
    pub warnings: Vec<String>,
}

/// One key of a loop's memory.
#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
#[serde(rename_all(deserialize = "snake_case", serialize = "camelCase"))]
pub struct LoopMemoryEntry {
    pub key: String,
    pub value: String,
    pub rev: u32,
    pub updated_at: String,
    pub updated_by: Option<String>,
}

/// A loop's memory, sorted by key.
#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
#[serde(rename_all(deserialize = "snake_case", serialize = "camelCase"))]
pub struct LoopMemory {
    #[serde(rename = "loop")]
    pub loop_name: String,
    pub entries: Vec<LoopMemoryEntry>,
    pub bytes: u64,
    pub warnings: Vec<String>,
}

/// The entry a memory set wrote.
#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
#[serde(rename_all(deserialize = "snake_case", serialize = "camelCase"))]
pub struct LoopMemorySet {
    #[serde(rename = "loop")]
    pub loop_name: String,
    pub entry: LoopMemoryEntry,
    pub warnings: Vec<String>,
}

/// The result of removing one memory key.
#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
#[serde(rename_all(deserialize = "snake_case", serialize = "camelCase"))]
pub struct LoopMemoryRemoved {
    #[serde(rename = "loop")]
    pub loop_name: String,
    pub key: String,
    pub removed: bool,
    pub warnings: Vec<String>,
}

/// The result of clearing a loop's history or memory.
#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
#[serde(rename_all(deserialize = "snake_case", serialize = "camelCase"))]
pub struct LoopCleared {
    #[serde(rename = "loop")]
    pub loop_name: String,
    pub removed: u32,
    pub warnings: Vec<String>,
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
    /// Not sent when None, so `ws` keeps its default or the loop's setting (ADR 0057).
    #[serde(default)]
    pub hidden: Option<bool>,
    /// `claude` or `ws`; blank or None is `claude`. The form sends it on every edit.
    #[serde(default)]
    pub runner: Option<String>,
    #[serde(default)]
    pub endpoint: Option<String>,
    /// The environment the loop lives on; None or `local` is this machine.
    #[serde(default)]
    pub environment: Option<String>,
    /// The user confirmed what a remote loop runs; sends `--yes`.
    #[serde(default)]
    pub confirmed: bool,
    /// The loop's values before the edit; a remote edit sends only the
    /// fields that differ (R3's round-trip rule).
    #[serde(default)]
    pub previous: Option<Box<NewLoop>>,
}

fn non_blank(value: Option<&str>) -> Option<&str> {
    value.map(str::trim).filter(|s| !s.is_empty())
}

/// The loop's environment when it names a remote one, checked so `ws` can't
/// take it for an option or several arguments.
fn target(environment: Option<&str>) -> CmdResult<Option<&str>> {
    let Some(name) = remote_env(environment) else {
        return Ok(None);
    };
    crate::pty::check_env_name(name)?;
    Ok(Some(name))
}

/// `["loop", <sub>…, ("--env=<name>"), ("--yes")]`: the flags come right
/// after the subcommand.
fn base(sub: &[&str], environment: Option<&str>, confirmed: bool) -> Vec<String> {
    let mut args = vec!["loop".to_string()];
    args.extend(sub.iter().map(|s| s.to_string()));
    args.extend(env_args(environment));
    if confirmed {
        args.push("--yes".to_string());
    }
    args
}

/// `ws loop add` without `--json`, which the runner adds:
/// ["loop", "add", ("--env=<e>"), ("--yes"), ("--title=<t>"), "--every=<every>",
///  "--prompt=<prompt>", ("--keep=<n>"), ("--runner=ws"), ("--endpoint=<e>"),
///  ("--model=<m>"), ("--permission-mode=<p>"), ("--hidden=<b>"),
///  ("--script=<s>"), ("--script-timeout=<t>"), ("--var=<k>=<v>")...,
///  "--", <name>, <specs...>].
/// `--yes` is sent only when the request is confirmed.
/// Blank optional values, and blank specs, are left out; values are trimmed,
/// except the script (whose whitespace matters) and a var's value.
/// A script timeout needs a script; a var needs a non-blank key. The claude
/// runner adds no runner flag.
pub fn add_args(req: &NewLoop) -> Vec<String> {
    let mut args = base(&["add"], req.environment.as_deref(), req.confirmed);
    if let Some(title) = non_blank(req.title.as_deref()) {
        args.push(format!("--title={title}"));
    }
    args.push(format!("--every={}", req.every.trim()));
    args.push(format!("--prompt={}", req.prompt.trim()));
    if let Some(keep) = req.keep {
        args.push(format!("--keep={keep}"));
    }
    if non_blank(req.runner.as_deref()) == Some("ws") {
        args.push("--runner=ws".to_string());
        if let Some(endpoint) = non_blank(req.endpoint.as_deref()) {
            args.push(format!("--endpoint={endpoint}"));
        }
    }
    if let Some(model) = non_blank(req.model.as_deref()) {
        args.push(format!("--model={model}"));
    }
    if let Some(mode) = non_blank(req.permission_mode.as_deref()) {
        args.push(format!("--permission-mode={mode}"));
    }
    if let Some(hidden) = req.hidden {
        args.push(format!("--hidden={hidden}"));
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

fn clean_specs(req: &NewLoop) -> Vec<String> {
    req.specs
        .iter()
        .filter_map(|s| non_blank(Some(s)).map(str::to_string))
        .collect()
}

fn clean_vars(req: &NewLoop) -> Vec<String> {
    req.vars
        .iter()
        .filter_map(|var| {
            non_blank(Some(var.key.as_str())).map(|key| format!("--var={key}={}", var.value))
        })
        .collect()
}

fn clean_script(req: &NewLoop) -> Option<&str> {
    req.script.as_deref().filter(|s| !s.trim().is_empty())
}

/// `ws loop edit` without `--json`, which the runner adds. Every field is
/// sent, so the request replaces the loop's editable fields (ADR 0052):
/// ["loop", "edit", ("--env=<e>"), ("--yes"), "--title=<t>", "--every=<every>",
///  "--prompt=<prompt>", ("--keep=<n>"), ("--runner=<r>", "--endpoint=<e or "">"),
///  "--model=<m>", ("--permission-mode=<p>" unless the runner is ws),
///  ("--hidden=<b>"), ("--script=<s>", ("--script-timeout=<t>")) | "--no-script",
///  ("--var=<k>=<v>")... | "--no-vars", ("--no-specs"),
///  "--", <name>, <specs...>].
/// The runner and endpoint are sent only when the request has a runner, which
/// the form always does, so a blank endpoint clears one and a claude loop's
/// runner is set back to claude (ADR 0070).
/// Values are trimmed as in add_args (not the script or a var's value);
/// a blank title, model or permission mode is sent as "" to clear it.
/// For a remote loop whose request has its `previous` values, only the fields
/// that differ are sent, so a value the agent redacted is never written back
/// over the secret (R3). A local loop always sends every field.
pub fn edit_args(req: &NewLoop) -> Vec<String> {
    let previous = remote_env(req.environment.as_deref()).and(req.previous.as_deref());
    let changed =
        |differs: fn(&NewLoop, &NewLoop) -> bool| previous.is_none_or(|prev| differs(prev, req));
    let mut args = base(&["edit"], req.environment.as_deref(), req.confirmed);
    if changed(|a, b| title_of(a) != title_of(b)) {
        args.push(format!("--title={}", title_of(req)));
    }
    if changed(|a, b| a.every.trim() != b.every.trim()) {
        args.push(format!("--every={}", req.every.trim()));
    }
    if changed(|a, b| a.prompt.trim() != b.prompt.trim()) {
        args.push(format!("--prompt={}", req.prompt.trim()));
    }
    if let Some(keep) = req.keep {
        if changed(|a, b| a.keep != b.keep) {
            args.push(format!("--keep={keep}"));
        }
    }
    let runner = non_blank(req.runner.as_deref());
    if let Some(runner) = runner {
        if changed(|a, b| runner_of(a) != runner_of(b) || endpoint_of(a) != endpoint_of(b)) {
            args.push(format!("--runner={runner}"));
            args.push(format!("--endpoint={}", endpoint_of(req)));
        }
    }
    if changed(|a, b| model_of(a) != model_of(b)) {
        args.push(format!("--model={}", model_of(req)));
    }
    if runner != Some("ws") && changed(|a, b| mode_of(a) != mode_of(b)) {
        args.push(format!("--permission-mode={}", mode_of(req)));
    }
    if let Some(hidden) = req.hidden {
        if changed(|a, b| a.hidden != b.hidden) {
            args.push(format!("--hidden={hidden}"));
        }
    }
    if changed(|a, b| clean_script(a) != clean_script(b)) {
        match clean_script(req) {
            Some(script) => {
                args.push(format!("--script={script}"));
                if let Some(timeout) = non_blank(req.script_timeout.as_deref()) {
                    args.push(format!("--script-timeout={timeout}"));
                }
            }
            None => args.push("--no-script".to_string()),
        }
    } else if let (Some(prev), Some(_)) = (previous, clean_script(req)) {
        if non_blank(prev.script_timeout.as_deref()) != non_blank(req.script_timeout.as_deref()) {
            if let Some(timeout) = non_blank(req.script_timeout.as_deref()) {
                args.push(format!("--script-timeout={timeout}"));
            }
        }
    }
    let vars = clean_vars(req);
    if changed(|a, b| clean_vars(a) != clean_vars(b)) {
        if vars.is_empty() {
            args.push("--no-vars".to_string());
        }
        args.extend(vars);
    }
    let specs = clean_specs(req);
    let send_specs = changed(|a, b| clean_specs(a) != clean_specs(b));
    if send_specs && specs.is_empty() {
        args.push("--no-specs".to_string());
    }
    args.push("--".to_string());
    args.push(req.name.trim().to_string());
    if send_specs {
        args.extend(specs);
    }
    args
}

fn title_of(r: &NewLoop) -> &str {
    non_blank(r.title.as_deref()).unwrap_or("")
}

fn runner_of(r: &NewLoop) -> &str {
    non_blank(r.runner.as_deref()).unwrap_or("")
}

fn endpoint_of(r: &NewLoop) -> &str {
    non_blank(r.endpoint.as_deref()).unwrap_or("")
}

fn model_of(r: &NewLoop) -> &str {
    non_blank(r.model.as_deref()).unwrap_or("")
}

fn mode_of(r: &NewLoop) -> &str {
    non_blank(r.permission_mode.as_deref()).unwrap_or("")
}

/// `ws loop list [--env=<e> --cached]`; a remote list reads the snapshot, so
/// it is cheap and works offline (R4).
pub fn list_args(environment: Option<&str>) -> Vec<String> {
    let mut args = base(&["list"], environment, false);
    if remote_env(environment).is_some() {
        args.push("--cached".to_string());
    }
    args
}

/// ["loop", "pause" | "resume", ("--env=<e>"), "--", <name>]
pub fn pause_args(name: &str, paused: bool, environment: Option<&str>) -> Vec<String> {
    let action = if paused { "pause" } else { "resume" };
    let mut args = base(&[action], environment, false);
    args.extend(["--".to_string(), name.to_string()]);
    args
}

/// ["loop", "rm", ("--env=<e>"), ("--yes"), "--", <name>]
fn rm_args(name: &str, environment: Option<&str>, confirmed: bool) -> Vec<String> {
    let mut args = base(&["rm"], environment, confirmed);
    args.extend(["--".to_string(), name.to_string()]);
    args
}

/// ["loop", "run", ("--env=<e>"), "--", <name>]
fn run_args(name: &str, environment: Option<&str>) -> Vec<String> {
    let mut args = base(&["run"], environment, false);
    args.extend(["--".to_string(), name.to_string()]);
    args
}

/// `ws loop history` without `--json`, which the runner adds:
/// ["loop", "history", ("--env=<e>"), ("--limit=<n>"), "--", <name>].
pub fn history_args(name: &str, limit: Option<u32>, environment: Option<&str>) -> Vec<String> {
    let mut args = base(&["history"], environment, false);
    if let Some(limit) = limit {
        args.push(format!("--limit={limit}"));
    }
    args.extend(["--".to_string(), name.to_string()]);
    args
}

/// ["loop", "history", ("--env=<e>"), "--run=<seq>", "--", <name>]
pub fn history_run_args(name: &str, seq: u32, environment: Option<&str>) -> Vec<String> {
    let mut args = base(&["history"], environment, false);
    args.push(format!("--run={seq}"));
    args.extend(["--".to_string(), name.to_string()]);
    args
}

/// ["loop", "history", ("--env=<e>"), ("--yes"), "--clear", "--", <name>]
pub fn history_clear_args(name: &str, environment: Option<&str>, confirmed: bool) -> Vec<String> {
    let mut args = base(&["history"], environment, confirmed);
    args.extend(["--clear".to_string(), "--".to_string(), name.to_string()]);
    args
}

/// ["loop", "memory", "list", ("--env=<e>"), "--", <name>]
pub fn memory_list_args(name: &str, environment: Option<&str>) -> Vec<String> {
    let mut args = base(&["memory", "list"], environment, false);
    args.extend(["--".to_string(), name.to_string()]);
    args
}

/// The value goes on stdin, so the args name `--file=-` and carry `--json`
/// themselves: ["loop", "memory", "set", ("--env=<e>"), "--json",
/// ("--expect-rev=<n>"), "--file=-", "--", <name>, <key>].
pub fn memory_set_args(
    name: &str,
    key: &str,
    expect_rev: Option<i64>,
    environment: Option<&str>,
) -> Vec<String> {
    let mut args = base(&["memory", "set"], environment, false);
    args.push("--json".to_string());
    if let Some(rev) = expect_rev {
        args.push(format!("--expect-rev={rev}"));
    }
    args.push("--file=-".to_string());
    args.extend(["--".to_string(), name.to_string(), key.to_string()]);
    args
}

/// ["loop", "memory", "rm", ("--env=<e>"), "--", <name>, <key>]
pub fn memory_remove_args(name: &str, key: &str, environment: Option<&str>) -> Vec<String> {
    let mut args = base(&["memory", "rm"], environment, false);
    args.extend(["--".to_string(), name.to_string(), key.to_string()]);
    args
}

/// ["loop", "memory", "clear", ("--env=<e>"), ("--yes"), "--", <name>]
pub fn memory_clear_args(name: &str, environment: Option<&str>, confirmed: bool) -> Vec<String> {
    let mut args = base(&["memory", "clear"], environment, confirmed);
    args.extend(["--".to_string(), name.to_string()]);
    args
}

#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn loops_list(
    state: State<'_, AppState>,
    environment: Option<String>,
) -> CmdResult<Loops> {
    let env = target(environment.as_deref())?;
    let mut loops: Loops = state.runner.ws_json(&list_args(env)).await?;
    if let Some(name) = env {
        for item in &mut loops.loops {
            item.environment.get_or_insert_with(|| name.to_string());
        }
    }
    Ok(loops)
}

#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn loops_add(state: State<'_, AppState>, request: NewLoop) -> CmdResult<Loop> {
    target(request.environment.as_deref())?;
    state.runner.ws_json(&add_args(&request)).await
}

#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn loops_edit(state: State<'_, AppState>, request: NewLoop) -> CmdResult<Loop> {
    target(request.environment.as_deref())?;
    state.runner.ws_json(&edit_args(&request)).await
}

#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn loops_set_paused(
    state: State<'_, AppState>,
    name: String,
    paused: bool,
    environment: Option<String>,
) -> CmdResult<Loop> {
    let env = target(environment.as_deref())?;
    state.runner.ws_json(&pause_args(&name, paused, env)).await
}

#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn loops_remove(
    state: State<'_, AppState>,
    name: String,
    environment: Option<String>,
    confirmed: Option<bool>,
) -> CmdResult<Loop> {
    let env = target(environment.as_deref())?;
    state
        .runner
        .ws_json(&rm_args(&name, env, confirmed.unwrap_or(false)))
        .await
}

#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn loops_run_now(
    state: State<'_, AppState>,
    name: String,
    environment: Option<String>,
) -> CmdResult<LoopStarted> {
    let env = target(environment.as_deref())?;
    state.runner.ws_json(&run_args(&name, env)).await
}

#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn loops_history(
    state: State<'_, AppState>,
    name: String,
    limit: Option<u32>,
    environment: Option<String>,
) -> CmdResult<LoopHistory> {
    let env = target(environment.as_deref())?;
    state.runner.ws_json(&history_args(&name, limit, env)).await
}

#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn loops_history_run(
    state: State<'_, AppState>,
    name: String,
    seq: u32,
    environment: Option<String>,
) -> CmdResult<LoopRunDetail> {
    let env = target(environment.as_deref())?;
    state
        .runner
        .ws_json(&history_run_args(&name, seq, env))
        .await
}

#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn loops_history_clear(
    state: State<'_, AppState>,
    name: String,
    environment: Option<String>,
    confirmed: Option<bool>,
) -> CmdResult<LoopCleared> {
    let env = target(environment.as_deref())?;
    state
        .runner
        .ws_json(&history_clear_args(&name, env, confirmed.unwrap_or(false)))
        .await
}

#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn loops_memory(
    state: State<'_, AppState>,
    name: String,
    environment: Option<String>,
) -> CmdResult<LoopMemory> {
    let env = target(environment.as_deref())?;
    state.runner.ws_json(&memory_list_args(&name, env)).await
}

#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn loops_memory_set(
    state: State<'_, AppState>,
    name: String,
    key: String,
    value: String,
    expect_rev: Option<i64>,
    environment: Option<String>,
) -> CmdResult<LoopMemorySet> {
    let env = target(environment.as_deref())?;
    state
        .runner
        .ws_json_with_input(
            &memory_set_args(&name, &key, expect_rev, env),
            Some(value.as_bytes()),
        )
        .await
}

#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn loops_memory_remove(
    state: State<'_, AppState>,
    name: String,
    key: String,
    environment: Option<String>,
) -> CmdResult<LoopMemoryRemoved> {
    let env = target(environment.as_deref())?;
    state
        .runner
        .ws_json(&memory_remove_args(&name, &key, env))
        .await
}

#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn loops_memory_clear(
    state: State<'_, AppState>,
    name: String,
    environment: Option<String>,
    confirmed: Option<bool>,
) -> CmdResult<LoopCleared> {
    let env = target(environment.as_deref())?;
    state
        .runner
        .ws_json(&memory_clear_args(&name, env, confirmed.unwrap_or(false)))
        .await
}

/// The args of the local tick: it never takes `--env`, since a remote
/// environment's agent ticks its own loops (R2).
pub fn tick_args() -> Vec<String> {
    ["loop", "tick", "--quiet", "--json"]
        .map(str::to_string)
        .to_vec()
}

/// Runs `ws loop tick --quiet --json` now and then every TICK_INTERVAL,
/// forever, ignoring its result (failures are logged with eprintln!).
pub async fn tick_forever(runner: Runner) {
    let mut interval = tokio::time::interval(TICK_INTERVAL);
    interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
    let args = tick_args();
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
            hidden: None,
            runner: None,
            endpoint: None,
            environment: None,
            confirmed: false,
            previous: None,
        }
    }

    #[test]
    fn loop_without_hidden_is_hidden() {
        let json = r#"{"name":"deps","every":"24h","prompt":"hi","specs":[],"model":null,
            "permission_mode":null,"keep":3,"paused":false,"created_at":"2026-11-01T00:00:00Z",
            "next_run_at":null,"last_run":null}"#;
        let loop_: Loop = serde_json::from_str(json).unwrap();
        assert!(loop_.hidden);
    }

    #[test]
    fn add_args_sends_hidden_only_when_set() {
        let shown = NewLoop {
            hidden: Some(false),
            permission_mode: Some("acceptEdits".into()),
            ..request()
        };
        assert_eq!(
            add_args(&shown),
            [
                "loop",
                "add",
                "--every=24h",
                "--prompt=hi",
                "--permission-mode=acceptEdits",
                "--hidden=false",
                "--",
                "deps",
            ]
        );
        assert!(!add_args(&request())
            .iter()
            .any(|a| a.starts_with("--hidden")));
    }

    #[test]
    fn edit_args_sends_hidden_only_when_set() {
        let hidden = NewLoop {
            hidden: Some(true),
            ..request()
        };
        let args = edit_args(&hidden);
        let position = args.iter().position(|a| a == "--hidden=true").unwrap();
        assert_eq!(args[position - 1], "--permission-mode=");
        assert_eq!(args[position + 1], "--no-script");
        assert!(!edit_args(&request())
            .iter()
            .any(|a| a.starts_with("--hidden")));
    }

    #[test]
    fn add_args_for_ws_loop_sends_runner_and_endpoint_before_model() {
        let req = NewLoop {
            runner: Some("ws".into()),
            endpoint: Some("gpu".into()),
            model: Some("qwen9b".into()),
            permission_mode: Some("acceptEdits".into()),
            ..request()
        };
        assert_eq!(
            add_args(&req),
            [
                "loop",
                "add",
                "--every=24h",
                "--prompt=hi",
                "--runner=ws",
                "--endpoint=gpu",
                "--model=qwen9b",
                "--permission-mode=acceptEdits",
                "--",
                "deps",
            ]
        );
    }

    #[test]
    fn add_args_for_claude_loop_sends_no_runner_flags() {
        let req = NewLoop {
            runner: Some("claude".into()),
            endpoint: Some(" ".into()),
            model: Some("opus".into()),
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
                "deps"
            ]
        );
    }

    #[test]
    fn add_args_for_claude_loop_drops_a_stale_endpoint() {
        let req = NewLoop {
            runner: Some("claude".into()),
            endpoint: Some("gpu".into()),
            ..request()
        };
        let args = add_args(&req);
        assert!(!args
            .iter()
            .any(|a| a.starts_with("--runner") || a.starts_with("--endpoint")));
        let blank_runner = NewLoop {
            runner: None,
            endpoint: Some("gpu".into()),
            ..request()
        };
        assert!(!add_args(&blank_runner)
            .iter()
            .any(|a| a.starts_with("--endpoint")));
    }

    #[test]
    fn edit_args_for_ws_loop_sends_runner_and_endpoint_but_no_permission_mode() {
        let req = NewLoop {
            runner: Some("ws".into()),
            endpoint: Some("gpu".into()),
            model: Some("qwen9b".into()),
            permission_mode: Some("acceptEdits".into()),
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
                "--runner=ws",
                "--endpoint=gpu",
                "--model=qwen9b",
                "--no-script",
                "--no-vars",
                "--no-specs",
                "--",
                "deps",
            ]
        );
    }

    #[test]
    fn edit_args_switching_to_claude_clears_the_endpoint() {
        let req = NewLoop {
            runner: Some("claude".into()),
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
                "--runner=claude",
                "--endpoint=",
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
    fn loop_record_reads_runner_and_endpoint_and_defaults_them() {
        let ws = r#"{"name":"deps","every":"24h","prompt":"hi","specs":[],"model":"qwen9b",
            "permission_mode":null,"keep":3,"paused":false,"created_at":"2026-11-01T00:00:00Z",
            "next_run_at":null,"last_run":null,"runner":"ws","endpoint":"gpu"}"#;
        let loop_: Loop = serde_json::from_str(ws).unwrap();
        assert_eq!(loop_.runner.as_deref(), Some("ws"));
        assert_eq!(loop_.endpoint.as_deref(), Some("gpu"));
        let claude = r#"{"name":"deps","every":"24h","prompt":"hi","specs":[],"model":null,
            "permission_mode":null,"keep":3,"paused":false,"created_at":"2026-11-01T00:00:00Z",
            "next_run_at":null,"last_run":null}"#;
        let loop_: Loop = serde_json::from_str(claude).unwrap();
        assert_eq!(loop_.runner, None);
        assert_eq!(loop_.endpoint, None);
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
        assert_eq!(
            pause_args("deps", true, None),
            ["loop", "pause", "--", "deps"]
        );
        assert_eq!(
            pause_args("deps", false, None),
            ["loop", "resume", "--", "deps"]
        );
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

    #[test]
    fn history_args_takes_optional_limit() {
        assert_eq!(
            history_args("deps", Some(20), None),
            ["loop", "history", "--limit=20", "--", "deps"]
        );
        assert_eq!(
            history_args("deps", None, None),
            ["loop", "history", "--", "deps"]
        );
    }

    #[test]
    fn history_run_args_names_the_seq() {
        assert_eq!(
            history_run_args("deps", 56, None),
            ["loop", "history", "--run=56", "--", "deps"]
        );
    }

    #[test]
    fn history_clear_args_clears_history() {
        assert_eq!(
            history_clear_args("deps", None, false),
            ["loop", "history", "--clear", "--", "deps"]
        );
    }

    #[test]
    fn memory_list_args_lists_the_loop() {
        assert_eq!(
            memory_list_args("deps", None),
            ["loop", "memory", "list", "--", "deps"]
        );
    }

    #[test]
    fn memory_set_args_reads_the_value_from_stdin() {
        assert_eq!(
            memory_set_args("deps", "seen", Some(3), None),
            [
                "loop",
                "memory",
                "set",
                "--json",
                "--expect-rev=3",
                "--file=-",
                "--",
                "deps",
                "seen",
            ]
        );
        assert_eq!(
            memory_set_args("deps", "seen", None, None),
            ["loop", "memory", "set", "--json", "--file=-", "--", "deps", "seen",]
        );
    }

    #[test]
    fn memory_remove_args_removes_one_key() {
        assert_eq!(
            memory_remove_args("deps", "seen", None),
            ["loop", "memory", "rm", "--", "deps", "seen"]
        );
    }

    #[test]
    fn memory_clear_args_clears_the_loop() {
        assert_eq!(
            memory_clear_args("deps", None, false),
            ["loop", "memory", "clear", "--", "deps"]
        );
    }

    #[test]
    fn history_sample_round_trips_camel_case() {
        let json = r#"{"loop": "deps-bump", "runs": [{"seq": 42, "status": "succeeded", "trigger": "tick", "started_at": "2026-11-10T08:00:00Z", "finished_at": "2026-11-10T08:14:30Z", "duration_seconds": 870, "workspace": "loop-deps-bump-1a2b3c4d", "workspace_exists": true, "session": "e8cdf5d8", "error": null, "gate": {"at": "2026-11-10T07:59:58Z", "outcome": "passed", "exit_code": 0, "message": null}, "repeats": 1, "verdict": {"verdict": "PASS", "outcome": "pass", "recorded_at": "2026-11-10T08:14:30Z", "report": "/x/reports/42.md"}}], "total": 57, "warnings": []}"#;
        let history: LoopHistory = serde_json::from_str(json).unwrap();
        assert_eq!(history.loop_name, "deps-bump");
        assert_eq!(history.runs[0].duration_seconds, Some(870));

        let value = serde_json::to_value(&history).unwrap();
        assert_eq!(value["loop"], "deps-bump");
        assert_eq!(value["total"], 57);
        assert_eq!(value["runs"][0]["workspaceExists"], true);
        assert_eq!(value["runs"][0]["durationSeconds"], 870);
        assert_eq!(value["runs"][0]["gate"]["exitCode"], 0);
        assert_eq!(
            value["runs"][0]["verdict"]["recordedAt"],
            "2026-11-10T08:14:30Z"
        );
    }

    fn remote_request() -> NewLoop {
        NewLoop {
            environment: Some("linux-server".into()),
            ..request()
        }
    }

    #[test]
    fn remote_list_args_are_cached() {
        assert_eq!(
            list_args(Some("linux-server")),
            ["loop", "list", "--env=linux-server", "--cached"]
        );
        assert_eq!(list_args(None), ["loop", "list"]);
        assert_eq!(list_args(Some("local")), ["loop", "list"]);
    }

    #[test]
    fn add_args_with_env_and_confirm_pass_yes() {
        let req = NewLoop {
            confirmed: true,
            script: Some("exit 0".into()),
            ..remote_request()
        };
        assert_eq!(
            add_args(&req),
            [
                "loop",
                "add",
                "--env=linux-server",
                "--yes",
                "--every=24h",
                "--prompt=hi",
                "--script=exit 0",
                "--",
                "deps",
            ]
        );
    }

    #[test]
    fn add_args_without_confirm_have_no_yes() {
        let args = add_args(&remote_request());
        assert_eq!(
            args,
            [
                "loop",
                "add",
                "--env=linux-server",
                "--every=24h",
                "--prompt=hi",
                "--",
                "deps",
            ]
        );
        assert!(!add_args(&request()).contains(&"--yes".to_string()));
    }

    #[test]
    fn remote_edit_args_send_only_changed_fields() {
        let previous = NewLoop {
            title: Some("Deps".into()),
            script: Some("exit 0".into()),
            script_timeout: Some("1m".into()),
            vars: vec![LoopVar {
                key: "A".into(),
                value: "1".into(),
            }],
            specs: vec!["api".into()],
            model: Some("opus".into()),
            permission_mode: Some("acceptEdits".into()),
            keep: Some(5),
            hidden: Some(true),
            runner: Some("claude".into()),
            ..remote_request()
        };
        let same = NewLoop {
            previous: Some(Box::new(clone(&previous))),
            ..clone(&previous)
        };
        assert_eq!(
            edit_args(&same),
            ["loop", "edit", "--env=linux-server", "--", "deps"]
        );
        let changed = NewLoop {
            prompt: "new".into(),
            confirmed: true,
            previous: Some(Box::new(clone(&previous))),
            ..clone(&previous)
        };
        assert_eq!(
            edit_args(&changed),
            [
                "loop",
                "edit",
                "--env=linux-server",
                "--yes",
                "--prompt=new",
                "--",
                "deps"
            ]
        );
        let cleared = NewLoop {
            script: None,
            vars: vec![],
            specs: vec![],
            model: None,
            previous: Some(Box::new(clone(&previous))),
            ..clone(&previous)
        };
        assert_eq!(
            edit_args(&cleared),
            [
                "loop",
                "edit",
                "--env=linux-server",
                "--model=",
                "--no-script",
                "--no-vars",
                "--no-specs",
                "--",
                "deps"
            ]
        );
        let timeout = NewLoop {
            script_timeout: Some("5m".into()),
            previous: Some(Box::new(clone(&previous))),
            ..clone(&previous)
        };
        assert_eq!(
            edit_args(&timeout),
            [
                "loop",
                "edit",
                "--env=linux-server",
                "--script-timeout=5m",
                "--",
                "deps"
            ]
        );
        let specs = NewLoop {
            specs: vec!["api".into(), "web".into()],
            previous: Some(Box::new(clone(&previous))),
            ..clone(&previous)
        };
        assert_eq!(
            edit_args(&specs),
            [
                "loop",
                "edit",
                "--env=linux-server",
                "--",
                "deps",
                "api",
                "web"
            ]
        );
    }

    fn clone(req: &NewLoop) -> NewLoop {
        NewLoop {
            name: req.name.clone(),
            title: req.title.clone(),
            every: req.every.clone(),
            prompt: req.prompt.clone(),
            specs: req.specs.clone(),
            model: req.model.clone(),
            permission_mode: req.permission_mode.clone(),
            keep: req.keep,
            script: req.script.clone(),
            script_timeout: req.script_timeout.clone(),
            vars: req
                .vars
                .iter()
                .map(|v| LoopVar {
                    key: v.key.clone(),
                    value: v.value.clone(),
                })
                .collect(),
            hidden: req.hidden,
            runner: req.runner.clone(),
            endpoint: req.endpoint.clone(),
            environment: req.environment.clone(),
            confirmed: req.confirmed,
            previous: None,
        }
    }

    #[test]
    fn local_edit_args_ignore_previous() {
        let local = NewLoop {
            previous: Some(Box::new(request())),
            ..request()
        };
        assert_eq!(edit_args(&local), edit_args(&request()));
        assert!(edit_args(&local).contains(&"--prompt=hi".to_string()));
    }

    #[test]
    fn local_edit_args_are_unchanged() {
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
    fn remove_args_with_env_pass_yes() {
        assert_eq!(
            rm_args("deps", Some("linux-server"), true),
            ["loop", "rm", "--env=linux-server", "--yes", "--", "deps"]
        );
        assert_eq!(rm_args("deps", None, false), ["loop", "rm", "--", "deps"]);
        assert_eq!(
            history_clear_args("deps", Some("linux-server"), true),
            [
                "loop",
                "history",
                "--env=linux-server",
                "--yes",
                "--clear",
                "--",
                "deps"
            ]
        );
        assert_eq!(
            memory_clear_args("deps", Some("linux-server"), true),
            [
                "loop",
                "memory",
                "clear",
                "--env=linux-server",
                "--yes",
                "--",
                "deps"
            ]
        );
        assert!(
            !memory_clear_args("deps", Some("linux-server"), false).contains(&"--yes".to_string())
        );
        assert!(
            !history_clear_args("deps", Some("linux-server"), false).contains(&"--yes".to_string())
        );
    }

    #[test]
    fn env_goes_right_after_the_subcommand() {
        let env = Some("linux-server");
        assert_eq!(
            pause_args("deps", true, env),
            ["loop", "pause", "--env=linux-server", "--", "deps"]
        );
        assert_eq!(
            run_args("deps", env),
            ["loop", "run", "--env=linux-server", "--", "deps"]
        );
        assert_eq!(
            history_args("deps", Some(5), env),
            [
                "loop",
                "history",
                "--env=linux-server",
                "--limit=5",
                "--",
                "deps"
            ]
        );
        assert_eq!(
            history_run_args("deps", 3, env),
            [
                "loop",
                "history",
                "--env=linux-server",
                "--run=3",
                "--",
                "deps"
            ]
        );
        assert_eq!(
            memory_list_args("deps", env),
            ["loop", "memory", "list", "--env=linux-server", "--", "deps"]
        );
        assert_eq!(
            memory_remove_args("deps", "k", env),
            [
                "loop",
                "memory",
                "rm",
                "--env=linux-server",
                "--",
                "deps",
                "k"
            ]
        );
    }

    #[test]
    fn memory_set_with_env_keeps_stdin() {
        assert_eq!(
            memory_set_args("deps", "seen", Some(3), Some("linux-server")),
            [
                "loop",
                "memory",
                "set",
                "--env=linux-server",
                "--json",
                "--expect-rev=3",
                "--file=-",
                "--",
                "deps",
                "seen",
            ]
        );
    }

    #[test]
    fn tick_forever_args_have_no_env() {
        assert_eq!(tick_args(), ["loop", "tick", "--quiet", "--json"]);
    }

    #[test]
    fn target_refuses_names_ws_would_misread() {
        for name in ["-x", "--env=a", "a b"] {
            assert_eq!(target(Some(name)).unwrap_err().code, "bad_args", "{name:?}");
        }
        assert_eq!(target(Some("local")).unwrap(), None);
        assert_eq!(target(Some("")).unwrap(), None);
        assert_eq!(target(None).unwrap(), None);
        assert_eq!(target(Some("linux-server")).unwrap(), Some("linux-server"));
    }

    #[test]
    fn summary_loop_from_a_snapshot_parses() {
        let json = r#"{"name":"deps","title":"Deps","every":"1h","prompt":null,"specs":null,
            "model":null,"permission_mode":null,"keep":null,"paused":false,"created_at":null,
            "next_run_at":"2026-11-23T10:00:00Z","last_run":null,"script":null,"vars":null,
            "hidden":true,"runner":"claude","endpoint":null,"summary":true,
            "environment":"linux-server","stale":true}"#;
        let item: Loop = serde_json::from_str(json).unwrap();
        assert!(item.summary && item.stale);
        assert_eq!(item.environment.as_deref(), Some("linux-server"));
        assert_eq!(item.prompt, "");
        assert!(item.vars.is_empty());
        let local: Loop = serde_json::from_str(
            r#"{"name":"a","every":"1h","prompt":"p","specs":[],"model":null,"permission_mode":null,
            "keep":3,"paused":false,"created_at":"x","next_run_at":null,"last_run":null}"#,
        )
        .unwrap();
        assert!(!local.summary && !local.stale && local.environment.is_none());
    }

    #[test]
    fn loops_serialize_environment_status_in_camel_case() {
        let json = r#"{"loops":[],"warnings":[],"environment":"w","environment_status":"offline","loops_unknown":true}"#;
        let loops: Loops = serde_json::from_str(json).unwrap();
        assert_eq!(loops.environment_status.as_deref(), Some("offline"));
        assert!(loops.loops_unknown);
        let out = serde_json::to_value(&loops).unwrap();
        assert_eq!(out["environmentStatus"], "offline");
        assert_eq!(out["loopsUnknown"], true);
    }
}
