//! config.toml's settings through `ws config` (ADR 0054). The app never
//! reads or writes config.toml itself.

use crate::commands::AppState;
use crate::ipc::State;
use crate::runner::{AppError, Runner};
use serde::{Deserialize, Serialize};

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
