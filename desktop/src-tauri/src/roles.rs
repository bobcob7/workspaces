//! Agent roles: what `ws machine roles` lists, and adding, editing, and
//! removing them (ADR 0050). The app never reads or writes a role file
//! itself.

use crate::commands::AppState;
use crate::ipc::State;
use crate::runner::{AppError, Runner};
use serde::{Deserialize, Serialize};

type CmdResult<T> = Result<T, AppError>;

/// A role as `ws machine roles --json` lists it (ADR 0050).
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct ListedRole {
    pub name: String,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub model: String,
    #[serde(default)]
    pub pass: Vec<String>,
    #[serde(default)]
    pub fail: Vec<String>,
    #[serde(default)]
    pub vars: Vec<String>,
    #[serde(default)]
    pub claude_args: Vec<String>,
    /// "builtin", "user", or "override".
    pub source: String,
}

/// One role with its prompt template, as `ws machine roles show`, `add`,
/// and `edit` print it. `path` is null for a built-in.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct RoleDetail {
    #[serde(flatten)]
    pub role: ListedRole,
    #[serde(default)]
    pub prompt: String,
    #[serde(default)]
    pub path: Option<String>,
}

/// What the editor saves: every field is sent, and `prompt` goes on stdin.
#[derive(Debug, Clone, Default, PartialEq, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct RoleDraft {
    pub description: String,
    pub model: String,
    pub pass: Vec<String>,
    pub fail: Vec<String>,
    pub vars: Vec<String>,
    pub claude_args: Vec<String>,
    pub prompt: String,
}

/// What `ws machine roles rm --json` prints. `restored` is true when the
/// removed file was an override, so the built-in is back.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct RoleRemoved {
    pub name: String,
    #[serde(default)]
    pub path: String,
    pub restored: bool,
}

#[derive(Deserialize)]
struct RolesList {
    roles: Vec<ListedRole>,
}

/// Every role `ws machine roles` lists, for Settings.
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn roles_list(state: State<'_, AppState>) -> CmdResult<Vec<ListedRole>> {
    list(&state.runner).await
}

async fn list(runner: &Runner) -> CmdResult<Vec<ListedRole>> {
    let args = ["machine", "roles", "--json"].map(str::to_string);
    let listing: RolesList = runner.ws_json_as_given(&args).await?;
    Ok(listing.roles)
}

/// One role with its prompt template (`ws machine roles show`), for the editor.
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn roles_show(state: State<'_, AppState>, name: String) -> CmdResult<RoleDetail> {
    show(&state.runner, &name).await
}

async fn show(runner: &Runner, name: &str) -> CmdResult<RoleDetail> {
    runner.ws_json_as_given(&op_args("show", name, &[])).await
}

/// Adds a role (`ws machine roles add`).
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn roles_add(
    state: State<'_, AppState>,
    name: String,
    draft: RoleDraft,
) -> CmdResult<RoleDetail> {
    write(&state.runner, "add", &name, &draft).await
}

/// Changes a role (`ws machine roles edit`). A built-in becomes an override.
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn roles_edit(
    state: State<'_, AppState>,
    name: String,
    draft: RoleDraft,
) -> CmdResult<RoleDetail> {
    write(&state.runner, "edit", &name, &draft).await
}

/// Removes a role's file (`ws machine roles rm`). An override's built-in comes back.
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn roles_remove(state: State<'_, AppState>, name: String) -> CmdResult<RoleRemoved> {
    remove(&state.runner, &name).await
}

async fn remove(runner: &Runner, name: &str) -> CmdResult<RoleRemoved> {
    runner.ws_json_as_given(&op_args("rm", name, &[])).await
}

async fn write(runner: &Runner, op: &str, name: &str, draft: &RoleDraft) -> CmdResult<RoleDetail> {
    let args = write_args(op, name, draft);
    runner
        .ws_json_with_input(&args, Some(draft.prompt.as_bytes()))
        .await
}

/// `ws machine roles <op> --json [flags] -- <name>`. The name goes after
/// `--`, so a name that starts with a hyphen isn't a flag.
fn op_args(op: &str, name: &str, flags: &[String]) -> Vec<String> {
    let mut args: Vec<String> = ["machine", "roles", op, "--json"]
        .map(str::to_string)
        .to_vec();
    args.extend_from_slice(flags);
    args.push("--".to_string());
    args.push(name.to_string());
    args
}

/// The arguments of `add` or `edit` for the draft; the prompt is read from
/// stdin (`-`). `edit` clears a list the draft leaves empty, since ws keeps
/// a list it isn't given.
fn write_args(op: &str, name: &str, draft: &RoleDraft) -> Vec<String> {
    let clears = op == "edit";
    let mut flags = vec![
        "--description".to_string(),
        draft.description.clone(),
        "--model".to_string(),
        draft.model.clone(),
    ];
    repeated(&mut flags, "--pass", &draft.pass);
    repeated(&mut flags, "--fail", &draft.fail);
    if draft.vars.is_empty() && clears {
        flags.push("--no-vars".to_string());
    } else {
        repeated(&mut flags, "--vars", &draft.vars);
    }
    if draft.claude_args.is_empty() && clears {
        flags.push("--no-claude-args".to_string());
    } else {
        repeated(&mut flags, "--claude-arg", &draft.claude_args);
    }
    let mut args = op_args(op, name, &flags);
    args.push("-".to_string());
    args
}

/// `flag value` for each item, in order.
fn repeated(flags: &mut Vec<String>, flag: &str, items: &[String]) {
    for item in items {
        flags.extend([flag.to_string(), item.clone()]);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::PermissionsExt;

    /// A directory holding a fake `ws` script, for PATH. The script writes
    /// its arguments, one per line, to `args` and its stdin to `stdin`.
    fn fake_ws(body: &str) -> (tempfile::TempDir, Runner) {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("ws");
        let script = format!(
            "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"{args}\"\ncat > \"{stdin}\"\n{body}\n",
            args = dir.path().join("args").display(),
            stdin = dir.path().join("stdin").display(),
        );
        std::fs::write(&path, script).unwrap();
        std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o755)).unwrap();
        let runner = Runner::new(format!("{}:/bin:/usr/bin", dir.path().display()));
        (dir, runner)
    }

    fn read(dir: &tempfile::TempDir, file: &str) -> String {
        std::fs::read_to_string(dir.path().join(file)).unwrap()
    }

    fn lines(dir: &tempfile::TempDir) -> Vec<String> {
        read(dir, "args").lines().map(str::to_string).collect()
    }

    const DETAIL: &str = r#"{"name":"docs-writer","description":"Docs","model":"haiku","pass":["PASS"],"fail":["FAIL"],"vars":["TASK"],"claude_args":["--effort","high"],"source":"user","prompt":"Write {{.Vars.TASK}}.","path":"/c/machine/roles/docs-writer.md","warnings":[]}"#;

    fn draft() -> RoleDraft {
        RoleDraft {
            description: "Docs".to_string(),
            model: "haiku".to_string(),
            pass: vec!["PASS".to_string()],
            fail: vec!["FAIL".to_string()],
            vars: vec!["TASK".to_string()],
            claude_args: vec!["--effort".to_string(), "high".to_string()],
            prompt: "Write {{.Vars.TASK}}.\n".to_string(),
        }
    }

    #[tokio::test]
    async fn add_passes_every_flag_and_the_prompt_on_stdin() {
        let (dir, runner) = fake_ws(&format!("echo '{DETAIL}'"));
        let added = write(&runner, "add", "docs-writer", &draft())
            .await
            .unwrap();
        assert_eq!(
            lines(&dir),
            [
                "machine",
                "roles",
                "add",
                "--json",
                "--description",
                "Docs",
                "--model",
                "haiku",
                "--pass",
                "PASS",
                "--fail",
                "FAIL",
                "--vars",
                "TASK",
                "--claude-arg",
                "--effort",
                "--claude-arg",
                "high",
                "--",
                "docs-writer",
                "-"
            ]
        );
        assert_eq!(read(&dir, "stdin"), "Write {{.Vars.TASK}}.\n");
        assert_eq!(added.role.claude_args, ["--effort", "high"]);
        assert_eq!(
            added.path.as_deref(),
            Some("/c/machine/roles/docs-writer.md")
        );
    }

    #[tokio::test]
    async fn edit_with_empty_vars_passes_no_vars() {
        let (dir, runner) = fake_ws(&format!("echo '{DETAIL}'"));
        let changes = RoleDraft {
            vars: Vec::new(),
            ..draft()
        };
        write(&runner, "edit", "verifier", &changes).await.unwrap();
        let args = lines(&dir);
        assert!(args.contains(&"--no-vars".to_string()));
        assert!(!args.contains(&"--vars".to_string()));
        assert_eq!(args[2], "edit");
    }

    #[tokio::test]
    async fn list_parses_the_roles_ws_lists_and_serializes_claude_args() {
        let (dir, runner) = fake_ws(
            r#"echo '{"roles":[{"name":"implementer","description":"Implements one task","model":"haiku","pass":["PASS"],"fail":["FAIL"],"vars":["TASK","TASKS","BASE","BRANCH"],"claude_args":["--permission-mode","auto"],"source":"builtin"}],"warnings":[]}'"#,
        );
        let roles = list(&runner).await.unwrap();
        assert_eq!(lines(&dir), ["machine", "roles", "--json"]);
        assert_eq!(roles[0].claude_args, ["--permission-mode", "auto"]);
        let out = serde_json::to_value(&roles[0]).unwrap();
        assert_eq!(out["claudeArgs"][1], "auto");
    }

    #[tokio::test]
    async fn remove_reads_whether_the_builtin_came_back() {
        let (dir, runner) = fake_ws(
            r#"echo '{"name":"reviewer","path":"/c/machine/roles/reviewer.md","restored":true,"warnings":[]}'"#,
        );
        let removed = remove(&runner, "reviewer").await.unwrap();
        assert_eq!(&lines(&dir)[2..], ["rm", "--json", "--", "reviewer"]);
        assert!(removed.restored);
    }
}
