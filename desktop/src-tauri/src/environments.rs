//! Remote environments: what `ws env list` shows, and enrolling, rotating,
//! and removing them, and the certificate authority they trust (ADR 0072).
//! The app never reads or writes an environment's files itself.

use crate::commands::AppState;
use crate::ipc::State;
use crate::runner::AppError;
use serde::{Deserialize, Serialize};

type CmdResult<T> = Result<T, AppError>;

/// What an environment reported about itself, as `ws env list --json` has it.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct EnvHello {
    pub ws_version: String,
    pub os: String,
    pub arch: String,
    pub claude: String,
    pub claude_version: Option<String>,
    pub cert_not_after: String,
}

/// One environment in `ws env list --json`.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct RemoteEnvironment {
    pub id: String,
    pub name: String,
    pub state: String,
    pub status: String,
    pub last_seen: Option<String>,
    pub created_at: Option<String>,
    pub enrolled_at: Option<String>,
    pub cert_not_after: Option<String>,
    pub cert_ca: Option<String>,
    pub hello: Option<EnvHello>,
    pub workspaces: Option<u32>,
}

/// Every environment `ws env list --json` lists.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct EnvironmentListing {
    pub environments: Vec<RemoteEnvironment>,
    #[serde(default)]
    pub warnings: Vec<String>,
}

/// The one-time code an environment enrolls with. The app shows it once and
/// keeps nothing.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct EnrollmentCode {
    pub id: String,
    pub name: String,
    pub state: String,
    pub code: String,
    pub ca_created: Option<bool>,
    #[serde(default)]
    pub revoked_serials: Vec<String>,
    #[serde(default)]
    pub warnings: Vec<String>,
}

/// What `ws env rm --json` prints.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct EnvironmentRemoved {
    pub id: String,
    pub name: String,
    #[serde(default)]
    pub revoked_serials: Vec<String>,
    #[serde(default)]
    pub warnings: Vec<String>,
}

/// One certificate authority: its fingerprint and when it expires.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct CaInfo {
    pub fingerprint: String,
    pub not_after: String,
}

/// What `ws env ca status --json` prints.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct CaStatus {
    pub current: Option<CaInfo>,
    pub old: Option<CaInfo>,
    pub environments_on_current: u32,
    pub environments_on_old: u32,
    #[serde(default)]
    pub warnings: Vec<String>,
}

/// What `ws env ca rotate --json` prints. `dropped` is set by `--finish`.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct CaRotated {
    pub current: Option<CaInfo>,
    pub old: Option<CaInfo>,
    pub dropped: Option<CaInfo>,
    #[serde(default)]
    pub stranded: Vec<String>,
    #[serde(default)]
    pub warnings: Vec<String>,
}

/// Every environment, for Settings and the Environment picker.
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn environments_list(state: State<'_, AppState>) -> CmdResult<EnvironmentListing> {
    state.runner.ws_json_as_given(&env_list_args()).await
}

/// Adds an environment and returns its enrollment code (`ws env add`).
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn environment_add(
    state: State<'_, AppState>,
    name: String,
) -> CmdResult<EnrollmentCode> {
    state.runner.ws_json_as_given(&env_add_args(&name)).await
}

/// Issues a new enrollment code for an environment (`ws env rotate`).
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn environment_rotate(
    state: State<'_, AppState>,
    name: String,
) -> CmdResult<EnrollmentCode> {
    state.runner.ws_json_as_given(&env_rotate_args(&name)).await
}

/// Removes an environment and revokes its certificate (`ws env rm`).
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn environment_remove(
    state: State<'_, AppState>,
    name: String,
) -> CmdResult<EnvironmentRemoved> {
    state.runner.ws_json_as_given(&env_rm_args(&name)).await
}

/// The current and old certificate authorities (`ws env ca status`).
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn environments_ca_status(state: State<'_, AppState>) -> CmdResult<CaStatus> {
    state.runner.ws_json_as_given(&env_ca_status_args()).await
}

/// Starts a CA rotation, or with `finish`, drops the old CA (`ws env ca rotate`).
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn environments_ca_rotate(
    state: State<'_, AppState>,
    finish: bool,
    force: bool,
) -> CmdResult<CaRotated> {
    state
        .runner
        .ws_json_as_given(&env_ca_rotate_args(finish, force))
        .await
}

pub fn env_list_args() -> Vec<String> {
    ["env", "list", "--json"].map(str::to_string).to_vec()
}

/// The name goes after `--`, so a name that starts with a hyphen isn't a flag.
pub fn env_add_args(name: &str) -> Vec<String> {
    named_args("add", name)
}

pub fn env_rotate_args(name: &str) -> Vec<String> {
    named_args("rotate", name)
}

pub fn env_rm_args(name: &str) -> Vec<String> {
    named_args("rm", name)
}

pub fn env_ca_status_args() -> Vec<String> {
    ["env", "ca", "status", "--json"]
        .map(str::to_string)
        .to_vec()
}

/// `--finish` and `--force` only mean something together with `--finish`.
pub fn env_ca_rotate_args(finish: bool, force: bool) -> Vec<String> {
    let mut args = ["env", "ca", "rotate", "--json"]
        .map(str::to_string)
        .to_vec();
    if !finish {
        return args;
    }
    args.push("--finish".to_string());
    if force {
        args.push("--force".to_string());
    }
    args
}

/// `env <op> --json -- <name>`.
fn named_args(op: &str, name: &str) -> Vec<String> {
    let mut args: Vec<String> = ["env", op, "--json", "--"].map(str::to_string).to_vec();
    args.push(name.to_string());
    args
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::runner::Runner;
    use std::os::unix::fs::PermissionsExt;

    /// A directory holding a fake `ws` script, for PATH. The script writes
    /// its arguments, one per line, to `args`.
    fn fake_ws(body: &str) -> (tempfile::TempDir, Runner) {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("ws");
        let script = format!(
            "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"{args}\"\n{body}\n",
            args = dir.path().join("args").display(),
        );
        std::fs::write(&path, script).unwrap();
        std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o755)).unwrap();
        let runner = Runner::new(format!("{}:/bin:/usr/bin", dir.path().display()));
        (dir, runner)
    }

    fn lines(dir: &tempfile::TempDir) -> Vec<String> {
        std::fs::read_to_string(dir.path().join("args"))
            .unwrap()
            .lines()
            .map(str::to_string)
            .collect()
    }

    #[test]
    fn env_add_args_put_the_name_after_dashes() {
        assert_eq!(env_add_args("-odd"), ["env", "add", "--json", "--", "-odd"]);
        assert_eq!(
            env_rm_args("linux-server"),
            ["env", "rm", "--json", "--", "linux-server"]
        );
    }

    #[test]
    fn ca_rotate_args_finish_force() {
        assert_eq!(
            env_ca_rotate_args(false, false),
            ["env", "ca", "rotate", "--json"]
        );
        assert_eq!(
            env_ca_rotate_args(false, true),
            ["env", "ca", "rotate", "--json"]
        );
        assert_eq!(
            env_ca_rotate_args(true, false),
            ["env", "ca", "rotate", "--json", "--finish"]
        );
        assert_eq!(
            env_ca_rotate_args(true, true),
            ["env", "ca", "rotate", "--json", "--finish", "--force"]
        );
    }

    #[tokio::test]
    async fn environments_list_decodes_ws_output() {
        let (dir, runner) = fake_ws(
            r#"echo '{"environments":[{"id":"env_1","name":"linux-server","state":"enrolled","status":"online","last_seen":"2026-11-23T08:00:00Z","created_at":null,"enrolled_at":"2026-11-01T00:00:00Z","cert_not_after":"2027-11-01T00:00:00Z","cert_ca":"sha256:ab","hello":{"ws_version":"0.9.0","os":"linux","arch":"amd64","claude":"installed","claude_version":null,"cert_not_after":"2027-11-01T00:00:00Z"},"workspaces":2}],"warnings":[]}'"#,
        );
        let listing: EnvironmentListing = runner.ws_json_as_given(&env_list_args()).await.unwrap();
        assert_eq!(lines(&dir), ["env", "list", "--json"]);
        let env = &listing.environments[0];
        assert_eq!(env.workspaces, Some(2));
        assert_eq!(env.created_at, None);
        let out = serde_json::to_value(env).unwrap();
        assert_eq!(out["lastSeen"], "2026-11-23T08:00:00Z");
        assert_eq!(out["hello"]["claudeVersion"], serde_json::Value::Null);
    }

    #[tokio::test]
    async fn environment_add_result_is_not_kept() {
        let (_dir, runner) = fake_ws(
            r#"echo '{"id":"env_2","name":"win-rig","state":"pending","code":"ENRL-SECRET-1234","ca_created":false,"revoked_serials":[],"warnings":[]}'"#,
        );
        let added: EnrollmentCode = runner
            .ws_json_as_given(&env_add_args("win-rig"))
            .await
            .unwrap();
        assert_eq!(added.code, "ENRL-SECRET-1234");
        assert!(!format!("{runner:?}").contains(&added.code));
    }
}
