//! Runs `ws` and `claude` and turns their failures into [`AppError`]s.

use serde::de::DeserializeOwned;
use serde::Serialize;
use serde_json::Value;
use std::process::Stdio;
use tokio::process::Command;

/// An error the frontend can show: a `ws` error code where `ws` gave one
/// (ADR 0016), otherwise one of this app's own codes.
#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct AppError {
    pub code: String,
    pub message: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub details: Option<Value>,
}

impl AppError {
    pub fn new(code: &str, message: impl Into<String>) -> Self {
        Self {
            code: code.to_string(),
            message: message.into(),
            details: None,
        }
    }
}

/// What a finished command printed and whether it succeeded.
#[derive(Debug)]
pub struct Outcome {
    pub stdout: Vec<u8>,
    pub stderr: String,
    pub success: bool,
}

/// Runs programs with a fixed PATH and no stdin.
#[derive(Debug, Clone)]
pub struct Runner {
    path: String,
}

impl Runner {
    pub fn new(path: String) -> Self {
        Self { path }
    }

    pub fn path(&self) -> &str {
        &self.path
    }

    pub async fn run(&self, program: &str, args: &[String]) -> Result<Outcome, AppError> {
        self.run_with_env(program, args, &[]).await
    }

    pub async fn run_with_env(
        &self,
        program: &str,
        args: &[String],
        envs: &[(&str, &str)],
    ) -> Result<Outcome, AppError> {
        let output = Command::new(program)
            .args(args)
            .env("PATH", &self.path)
            .envs(envs.iter().copied())
            .stdin(Stdio::null())
            .output()
            .await
            .map_err(|err| match err.kind() {
                std::io::ErrorKind::NotFound => AppError::new(
                    &format!("{program}_missing"),
                    format!("`{program}` isn't on PATH ({})", self.path),
                ),
                _ => AppError::new("internal", format!("running {program}: {err}")),
            })?;
        Ok(Outcome {
            stdout: output.stdout,
            stderr: String::from_utf8_lossy(&output.stderr).trim().to_string(),
            success: output.status.success(),
        })
    }

    /// Runs `ws <args> --json` and parses its result, or its error.
    pub async fn ws_json<T: DeserializeOwned>(&self, args: &[String]) -> Result<T, AppError> {
        let mut args = args.to_vec();
        args.insert(1.min(args.len()), "--json".to_string());
        self.ws_json_as_given(&args).await
    }

    /// Runs `ws <args>`, which place `--json` themselves, and parses its
    /// result, or its error.
    pub async fn ws_json_as_given<T: DeserializeOwned>(
        &self,
        args: &[String],
    ) -> Result<T, AppError> {
        let outcome = self.run("ws", args).await?;
        if !outcome.success {
            return Err(ws_error(&outcome.stderr));
        }
        parse_json(&outcome.stdout, "ws")
    }

    /// Runs `claude <args>` and returns its stdout.
    pub async fn claude(&self, args: &[String]) -> Result<Vec<u8>, AppError> {
        let outcome = self.run("claude", args).await?;
        if !outcome.success {
            let message = if outcome.stderr.is_empty() {
                format!("claude {} failed", args.join(" "))
            } else {
                outcome.stderr
            };
            return Err(AppError::new("claude_failed", message));
        }
        Ok(outcome.stdout)
    }
}

pub fn parse_json<T: DeserializeOwned>(bytes: &[u8], program: &str) -> Result<T, AppError> {
    serde_json::from_slice(bytes).map_err(|err| {
        AppError::new(
            "unexpected_output",
            format!("{program} printed unexpected JSON: {err}"),
        )
    })
}

/// Reads `{"error": {"code", "message", "details"}}` from ws's stderr; any
/// other stderr becomes a `ws_failed` error carrying it.
pub fn ws_error(stderr: &str) -> AppError {
    let parsed = stderr
        .lines()
        .rev()
        .find_map(|line| serde_json::from_str::<Value>(line).ok())
        .and_then(|v| v.get("error").cloned());
    let Some(error) = parsed else {
        let message = if stderr.is_empty() {
            "ws failed"
        } else {
            stderr
        };
        return AppError::new("ws_failed", message);
    };
    AppError {
        code: error["code"].as_str().unwrap_or("ws_failed").to_string(),
        message: error["message"].as_str().unwrap_or(stderr).to_string(),
        details: error.get("details").cloned().filter(|d| !d.is_null()),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn ws_error_reads_json_error() {
        let stderr = r#"{"error":{"code":"claude_untrusted","message":"not trusted","details":{"directory":"/w"}}}"#;
        let err = ws_error(stderr);
        assert_eq!(err.code, "claude_untrusted");
        assert_eq!(err.message, "not trusted");
        assert_eq!(err.details.unwrap()["directory"], "/w");
    }

    #[test]
    fn ws_error_keeps_plain_stderr() {
        let err = ws_error("panic: something");
        assert_eq!(err, AppError::new("ws_failed", "panic: something"));
    }

    #[test]
    fn ws_error_with_empty_stderr_has_message() {
        assert_eq!(ws_error("").message, "ws failed");
    }

    #[tokio::test]
    async fn run_reports_missing_program() {
        let runner = Runner::new("/nonexistent".to_string());
        let err = runner.run("ws", &[]).await.unwrap_err();
        assert_eq!(err.code, "ws_missing");
    }
}
