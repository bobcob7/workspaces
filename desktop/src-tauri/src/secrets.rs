//! Endpoint tokens in CONFIG_DIR/secrets.env, which ws reads as
//! `WS_ENDPOINT_TOKEN_<NAME>` (reference.md). The app writes a token and
//! never reads one back: the frontend only learns where a token came from.

use crate::commands::ws_config_dir;
use crate::runner::AppError;
use std::io::{ErrorKind, Write};
use std::os::unix::fs::OpenOptionsExt;
use std::path::Path;

pub const SECRETS_FILE: &str = "secrets.env";

/// `WS_ENDPOINT_TOKEN_` plus the name upper-cased, with '-' as '_'.
pub fn token_env(name: &str) -> String {
    format!(
        "WS_ENDPOINT_TOKEN_{}",
        name.to_ascii_uppercase().replace('-', "_")
    )
}

/// 1 to 32 lowercase letters, digits and '-', starting with a letter.
pub fn is_endpoint_name(name: &str) -> bool {
    let mut chars = name.chars();
    name.len() <= 32
        && chars.next().is_some_and(|c| c.is_ascii_lowercase())
        && chars.all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == '-')
}

/// Saves an endpoint's token, or removes it when the token is empty.
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn endpoint_token_set(name: String, token: String) -> Result<(), AppError> {
    let dir = ws_config_dir(|key| std::env::var(key).ok())
        .ok_or_else(|| AppError::new("internal", "no ws config directory"))?;
    set_endpoint_token(&dir, &name, &token)
}

pub fn set_endpoint_token(dir: &Path, name: &str, token: &str) -> Result<(), AppError> {
    if !is_endpoint_name(name) {
        return Err(AppError::new(
            "setting_invalid",
            format!("\"{name}\" isn't an endpoint name"),
        ));
    }
    let token = token.trim();
    if token.contains(['\r', '\n']) {
        return Err(AppError::new(
            "setting_invalid",
            "a token has no line breaks",
        ));
    }
    let path = dir.join(SECRETS_FILE);
    let old = match std::fs::read_to_string(&path) {
        Ok(text) => text,
        Err(err) if err.kind() == ErrorKind::NotFound => String::new(),
        Err(err) => return Err(write_error(&path, err)),
    };
    let text = edit_lines(&old, &token_env(name), token);
    if text.is_empty() && !path.exists() {
        return Ok(());
    }
    write(&path, text.as_bytes())
}

/// `old` with the assignment of `env` replaced by `value`, or removed when
/// `value` is empty. Other lines and comments stay in order; extra
/// assignments of `env` are dropped.
fn edit_lines(old: &str, env: &str, value: &str) -> String {
    let assignment = format!("{env}={value}");
    let mut out: Vec<String> = Vec::new();
    let mut written = false;
    for line in old.lines() {
        if assigned_name(line) != Some(env) {
            out.push(line.to_string());
            continue;
        }
        if value.is_empty() || written {
            continue;
        }
        out.push(assignment.clone());
        written = true;
    }
    if !written && !value.is_empty() {
        out.push(assignment);
    }
    if out.is_empty() {
        return String::new();
    }
    out.join("\n") + "\n"
}

/// The NAME of a `NAME=value` line; None for a comment, a blank line or one without '='.
fn assigned_name(line: &str) -> Option<&str> {
    let trimmed = line.trim();
    if trimmed.starts_with('#') {
        return None;
    }
    trimmed.split_once('=').map(|(name, _)| name.trim())
}

fn write_error(path: &Path, err: std::io::Error) -> AppError {
    AppError::new(
        "state_write_failed",
        format!("writing {}: {err}", path.display()),
    )
}

/// Writes a temp file beside `path` with mode 0600 and renames it into
/// place, so a crash leaves the old file or the new one.
fn write(path: &Path, bytes: &[u8]) -> Result<(), AppError> {
    let fail = |err: std::io::Error| write_error(path, err);
    if let Some(dir) = path.parent() {
        std::fs::create_dir_all(dir).map_err(fail)?;
    }
    let tmp = path.with_file_name(format!("{SECRETS_FILE}.tmp"));
    // A leftover temp file keeps its old mode, so drop it before creating one.
    std::fs::remove_file(&tmp).ok();
    let mut out = std::fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(&tmp)
        .map_err(fail)?;
    out.write_all(bytes).map_err(fail)?;
    std::fs::rename(&tmp, path).map_err(fail)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::PermissionsExt;

    #[test]
    fn token_env_upper_cases_and_replaces_dashes() {
        assert_eq!(token_env("gpu-a"), "WS_ENDPOINT_TOKEN_GPU_A");
    }

    #[test]
    fn endpoint_names_follow_the_config_rules() {
        assert!(is_endpoint_name("gpu-a2"));
        assert!(!is_endpoint_name("2gpu"));
        assert!(!is_endpoint_name("GPU"));
        assert!(!is_endpoint_name(""));
        assert!(!is_endpoint_name(&"a".repeat(33)));
    }

    #[test]
    fn set_writes_a_new_file_with_mode_0600() {
        let dir = tempfile::tempdir().unwrap();
        set_endpoint_token(dir.path(), "gpu", "abc123").unwrap();
        let text = std::fs::read_to_string(dir.path().join(SECRETS_FILE)).unwrap();
        assert_eq!(text, "WS_ENDPOINT_TOKEN_GPU=abc123\n");
        let mode = std::fs::metadata(dir.path().join(SECRETS_FILE))
            .unwrap()
            .permissions()
            .mode();
        assert_eq!(mode & 0o777, 0o600);
    }

    #[test]
    fn set_replaces_its_line_and_keeps_the_others() {
        let old = "# keys\nOTHER=1\nWS_ENDPOINT_TOKEN_GPU=old\n\nLAST=\"2\"\n";
        assert_eq!(
            edit_lines(old, "WS_ENDPOINT_TOKEN_GPU", "new"),
            "# keys\nOTHER=1\nWS_ENDPOINT_TOKEN_GPU=new\n\nLAST=\"2\"\n"
        );
    }

    #[test]
    fn empty_token_removes_its_line() {
        let old = "A=1\nWS_ENDPOINT_TOKEN_GPU=old\nWS_ENDPOINT_TOKEN_GPU=older\n";
        assert_eq!(edit_lines(old, "WS_ENDPOINT_TOKEN_GPU", ""), "A=1\n");
    }

    #[test]
    fn empty_token_with_no_file_writes_nothing_new() {
        let dir = tempfile::tempdir().unwrap();
        set_endpoint_token(dir.path(), "gpu", "").unwrap();
        assert_eq!(edit_lines("", "WS_ENDPOINT_TOKEN_GPU", ""), "");
        assert!(!dir.path().join(SECRETS_FILE).exists());
    }

    #[test]
    fn set_rejects_a_bad_name_and_a_line_break() {
        let dir = tempfile::tempdir().unwrap();
        let bad_name = set_endpoint_token(dir.path(), "Gpu", "x").unwrap_err();
        assert_eq!(bad_name.code, "setting_invalid");
        let line_break = set_endpoint_token(dir.path(), "gpu", "a\nb").unwrap_err();
        assert_eq!(line_break.code, "setting_invalid");
    }
}
