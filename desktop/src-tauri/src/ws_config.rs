//! config.toml's settings through `ws config` (ADR 0054). The app never
//! reads or writes config.toml itself.

use crate::commands::AppState;
use crate::ipc::State;
use crate::runner::{AppError, Runner};
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;

type CmdResult<T> = Result<T, AppError>;

/// One key, as `ws config --json` prints it. `value` and `default` are a
/// string, a list of strings, or a bool.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct ConfigSetting {
    pub key: String,
    #[serde(rename = "type")]
    pub kind: String,
    pub value: serde_json::Value,
    pub default: serde_json::Value,
    pub set: bool,
}

/// The result of `ws config get`.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct ConfigGet {
    pub path: String,
    pub setting: ConfigSetting,
}

/// Every key of config.toml, from `ws config list`.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct ConfigListing {
    pub path: String,
    pub settings: Vec<ConfigSetting>,
}

/// The result of `ws config set` or `unset`.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct ConfigChange {
    pub path: String,
    pub setting: ConfigSetting,
    pub changed: bool,
}

/// Every key of config.toml (`ws config list`).
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn config_list(state: State<'_, AppState>) -> CmdResult<ConfigListing> {
    list(&state.runner).await
}

async fn list(runner: &Runner) -> CmdResult<ConfigListing> {
    runner
        .ws_json(&["config".to_string(), "list".to_string()])
        .await
}

/// One key (`ws config get -- <key>`).
pub(crate) async fn get(runner: &Runner, key: &str) -> CmdResult<ConfigGet> {
    let args = [
        "config".to_string(),
        "get".to_string(),
        "--".to_string(),
        key.to_string(),
    ];
    runner.ws_json(&args).await
}

/// The runner's keys as Settings › Models shows them: the default model,
/// and each endpoint, model and role routing (reference.md, config.toml).
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct RunnerSettings {
    pub runner: RunnerModel,
    pub endpoints: Vec<EndpointSettings>,
    pub models: Vec<ModelSettings>,
    pub roles: Vec<RoleSettings>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct RunnerModel {
    pub model: String,
}

/// One `[endpoints.<name>]`, with its effective values.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct EndpointSettings {
    pub name: String,
    pub url: String,
    pub api: String,
    pub probe: String,
    pub request_timeout: String,
    pub idle_timeout: String,
}

/// One `[models.<alias>]`, with its effective values.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct ModelSettings {
    pub alias: String,
    pub endpoint: String,
    pub id: String,
    pub context_tokens: i64,
    pub max_output: i64,
    pub temperature: f64,
    pub reasoning: String,
}

/// One `[runner.roles.<role>]`, with its effective values.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct RoleSettings {
    pub role: String,
    pub runner: String,
    pub model: String,
    pub max_steps: i64,
    pub timeout: String,
    pub max_tokens: i64,
    pub mcp_servers: Vec<String>,
}

/// The runner's keys, from `ws config list` (Settings › Models; T26 reuses it).
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn runner_settings_get(state: State<'_, AppState>) -> CmdResult<RunnerSettings> {
    list(&state.runner)
        .await
        .map(|listing| runner_settings(&listing))
}

/// The entries of one kind (`endpoints`, `models`, `runner.roles`), by name,
/// each with its keys under their leaf names.
type Entries<'a> = BTreeMap<&'a str, BTreeMap<&'a str, &'a serde_json::Value>>;

fn entries<'a>(settings: &'a [ConfigSetting], kind: &str) -> Entries<'a> {
    let mut out = Entries::new();
    for setting in settings {
        let Some(rest) = setting
            .key
            .strip_prefix(kind)
            .and_then(|rest| rest.strip_prefix('.'))
        else {
            continue;
        };
        let Some((name, leaf)) = rest.split_once('.') else {
            continue;
        };
        out.entry(name).or_default().insert(leaf, &setting.value);
    }
    out
}

fn text(keys: &BTreeMap<&str, &serde_json::Value>, leaf: &str) -> String {
    keys.get(leaf)
        .and_then(|value| value.as_str())
        .unwrap_or_default()
        .to_string()
}

fn number(keys: &BTreeMap<&str, &serde_json::Value>, leaf: &str) -> i64 {
    keys.get(leaf)
        .and_then(|value| value.as_i64())
        .unwrap_or_default()
}

fn fraction(keys: &BTreeMap<&str, &serde_json::Value>, leaf: &str) -> f64 {
    keys.get(leaf)
        .and_then(|value| value.as_f64())
        .unwrap_or_default()
}

fn texts(keys: &BTreeMap<&str, &serde_json::Value>, leaf: &str) -> Vec<String> {
    keys.get(leaf)
        .and_then(|value| value.as_array())
        .map(|items| {
            items
                .iter()
                .filter_map(|item| item.as_str().map(str::to_string))
                .collect()
        })
        .unwrap_or_default()
}

fn runner_settings(listing: &ConfigListing) -> RunnerSettings {
    let settings = &listing.settings;
    let model = settings
        .iter()
        .find(|setting| setting.key == "runner.model")
        .and_then(|setting| setting.value.as_str())
        .unwrap_or_default()
        .to_string();
    RunnerSettings {
        runner: RunnerModel { model },
        endpoints: entries(settings, "endpoints")
            .into_iter()
            .map(|(name, keys)| EndpointSettings {
                name: name.to_string(),
                url: text(&keys, "url"),
                api: text(&keys, "api"),
                probe: text(&keys, "probe"),
                request_timeout: text(&keys, "request_timeout"),
                idle_timeout: text(&keys, "idle_timeout"),
            })
            .collect(),
        models: entries(settings, "models")
            .into_iter()
            .map(|(alias, keys)| ModelSettings {
                alias: alias.to_string(),
                endpoint: text(&keys, "endpoint"),
                id: text(&keys, "id"),
                context_tokens: number(&keys, "context_tokens"),
                max_output: number(&keys, "max_output"),
                temperature: fraction(&keys, "temperature"),
                reasoning: text(&keys, "reasoning"),
            })
            .collect(),
        roles: entries(settings, "runner.roles")
            .into_iter()
            .map(|(role, keys)| RoleSettings {
                role: role.to_string(),
                runner: text(&keys, "runner"),
                model: text(&keys, "model"),
                max_steps: number(&keys, "max_steps"),
                timeout: text(&keys, "timeout"),
                max_tokens: number(&keys, "max_tokens"),
                mcp_servers: texts(&keys, "mcp_servers"),
            })
            .collect(),
    }
}

/// Sets a key (`ws config set -- <key> <values...>`).
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn config_set(
    state: State<'_, AppState>,
    key: String,
    values: Vec<String>,
) -> CmdResult<ConfigChange> {
    set(&state.runner, &key, &values).await
}

pub(crate) async fn set(runner: &Runner, key: &str, values: &[String]) -> CmdResult<ConfigChange> {
    let mut args = vec![
        "config".to_string(),
        "set".to_string(),
        "--".to_string(),
        key.to_string(),
    ];
    args.extend(values.iter().cloned());
    runner.ws_json(&args).await
}

/// Removes a key (`ws config unset -- <key>`).
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn config_unset(state: State<'_, AppState>, key: String) -> CmdResult<ConfigChange> {
    unset(&state.runner, &key).await
}

pub(crate) async fn unset(runner: &Runner, key: &str) -> CmdResult<ConfigChange> {
    let args = [
        "config".to_string(),
        "unset".to_string(),
        "--".to_string(),
        key.to_string(),
    ];
    runner.ws_json(&args).await
}

#[cfg(test)]
pub(crate) mod tests {
    use super::*;
    use std::os::unix::fs::PermissionsExt;

    /// A directory holding a fake `ws` script, for PATH. The script also
    /// writes its arguments to `args` in the same directory.
    pub(crate) fn fake_ws(body: &str) -> (tempfile::TempDir, Runner) {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("ws");
        let script = format!(
            "#!/bin/sh\necho \"$@\" > \"{}\"\n{body}\n",
            dir.path().join("args").display()
        );
        std::fs::write(&path, script).unwrap();
        std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o755)).unwrap();
        let runner = Runner::new(dir.path().display().to_string());
        (dir, runner)
    }

    pub(crate) fn args(dir: &tempfile::TempDir) -> String {
        std::fs::read_to_string(dir.path().join("args"))
            .unwrap()
            .trim()
            .to_string()
    }

    const CHANGE: &str = r#"{"path":"/c/config.toml","setting":{"key":"defaults.owners","type":"strings","value":["a","b"],"default":[],"set":true},"changed":true,"warnings":[]}"#;

    #[tokio::test]
    async fn set_passes_key_and_values() {
        let (dir, runner) = fake_ws(&format!("echo '{CHANGE}'"));
        let values = ["a".to_string(), "b".to_string()];
        let change = set(&runner, "defaults.owners", &values).await.unwrap();
        assert_eq!(args(&dir), "config --json set -- defaults.owners a b");
        assert!(change.changed);
        assert_eq!(change.setting.value, serde_json::json!(["a", "b"]));
    }

    #[tokio::test]
    async fn get_passes_key() {
        let (dir, runner) = fake_ws(
            r#"echo '{"path":"/c/config.toml","setting":{"key":"hosts","type":"hosts","value":[],"default":[],"set":false}}'"#,
        );
        let got = get(&runner, "hosts").await.unwrap();
        assert_eq!(args(&dir), "config --json get -- hosts");
        assert_eq!(got.setting.key, "hosts");
    }

    #[tokio::test]
    async fn unset_passes_key() {
        let (dir, runner) = fake_ws(
            r#"echo '{"path":"/c/config.toml","setting":{"key":"review.timeout","type":"duration","value":"60m","default":"60m","set":false},"changed":false,"warnings":[]}'"#,
        );
        let change = unset(&runner, "review.timeout").await.unwrap();
        assert_eq!(args(&dir), "config --json unset -- review.timeout");
        assert!(!change.setting.set);
    }

    #[tokio::test]
    async fn list_parses_every_type() {
        let (dir, runner) = fake_ws(
            r#"echo '{"path":"/c/config.toml","settings":[{"key":"defaults.host","type":"string","value":"github.com","default":"github.com","set":true},{"key":"defaults.owners","type":"strings","value":[],"default":[],"set":false},{"key":"review.timeout","type":"duration","value":"1h","default":"60m","set":true},{"key":"review.remove_finished","type":"bool","value":true,"default":true,"set":false}],"warnings":[]}'"#,
        );
        let listing = list(&runner).await.unwrap();
        assert_eq!(args(&dir), "config --json list");
        let kinds: Vec<&str> = listing.settings.iter().map(|s| s.kind.as_str()).collect();
        assert_eq!(kinds, ["string", "strings", "duration", "bool"]);
        assert_eq!(listing.settings[3].value, serde_json::json!(true));
    }

    #[tokio::test]
    async fn runner_settings_reads_the_runner_keys_from_the_listing() {
        let (dir, runner) = fake_ws(
            r#"echo '{"path":"/c/config.toml","settings":[{"key":"runner.model","type":"string","value":"qwen9b","default":"","set":true},{"key":"endpoints.gpu.url","type":"string","value":"http://gpu:1234","default":"","set":true},{"key":"endpoints.gpu.api","type":"string","value":"openai","default":"openai","set":false},{"key":"endpoints.gpu.probe","type":"string","value":"none","default":"auto","set":true},{"key":"endpoints.gpu.request_timeout","type":"duration","value":"10m","default":"10m","set":false},{"key":"endpoints.gpu.idle_timeout","type":"duration","value":"90s","default":"90s","set":false},{"key":"models.qwen9b.endpoint","type":"string","value":"gpu","default":"","set":true},{"key":"models.qwen9b.id","type":"string","value":"qwen/qwen3.5-9b","default":"","set":true},{"key":"models.qwen9b.context_tokens","type":"int","value":65536,"default":0,"set":true},{"key":"models.qwen9b.max_output","type":"int","value":4096,"default":4096,"set":false},{"key":"models.qwen9b.temperature","type":"float","value":0.2,"default":0.2,"set":false},{"key":"models.qwen9b.reasoning","type":"string","value":"","default":"","set":false},{"key":"runner.roles.verifier.runner","type":"string","value":"ws","default":"claude","set":true},{"key":"runner.roles.verifier.model","type":"string","value":"qwen9b","default":"","set":true},{"key":"runner.roles.verifier.max_steps","type":"int","value":30,"default":40,"set":true},{"key":"runner.roles.verifier.timeout","type":"duration","value":"30m","default":"60m","set":true},{"key":"runner.roles.verifier.max_tokens","type":"int","value":2000000,"default":2000000,"set":false},{"key":"runner.roles.verifier.mcp_servers","type":"strings","value":[],"default":[],"set":false},{"key":"runner.env.METRICS_URL","type":"string","value":"x","default":"","set":true}],"warnings":[]}'"#,
        );
        let settings = runner_settings(&list(&runner).await.unwrap());
        assert_eq!(args(&dir), "config --json list");
        assert_eq!(settings.runner.model, "qwen9b");
        assert_eq!(settings.endpoints[0].probe, "none");
        assert_eq!(settings.models[0].context_tokens, 65536);
        assert!((settings.models[0].temperature - 0.2).abs() < f64::EPSILON);
        assert_eq!(settings.roles[0].runner, "ws");
        assert_eq!(settings.roles[0].max_steps, 30);
        assert_eq!(settings.roles.len(), 1);
    }

    #[tokio::test]
    async fn runner_settings_is_empty_without_runner_tables() {
        let (_dir, runner) = fake_ws(
            r#"echo '{"path":"/c/config.toml","settings":[{"key":"defaults.host","type":"string","value":"github.com","default":"github.com","set":true}],"warnings":[]}'"#,
        );
        let settings = runner_settings(&list(&runner).await.unwrap());
        assert_eq!(settings.runner.model, "");
        assert!(settings.endpoints.is_empty());
        assert!(settings.models.is_empty());
        assert!(settings.roles.is_empty());
    }

    #[tokio::test]
    async fn invalid_value_keeps_the_ws_error_code() {
        let (_dir, runner) = fake_ws(
            r#"echo '{"error":{"code":"config_value_invalid","message":"bad host"}}' >&2; exit 2"#,
        );
        let err = set(&runner, "defaults.host", &["a/b".to_string()])
            .await
            .unwrap_err();
        assert_eq!(err.code, "config_value_invalid");
    }
}
