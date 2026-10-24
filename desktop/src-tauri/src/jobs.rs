//! Claude Code's own record of a background session, `jobs/<id>/state.json`.
//! It's undocumented, so every field is optional and a file that can't be
//! read is treated as missing: it only adds detail to what `ws list` and
//! `claude agents --json` report.

use serde::Deserialize;
use std::path::{Path, PathBuf};

#[derive(Debug, Default, Clone, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct JobState {
    #[serde(default)]
    pub state: Option<String>,
    #[serde(default)]
    pub detail: Option<String>,
    #[serde(default)]
    pub intent: Option<String>,
    #[serde(default)]
    pub output: Option<JobOutput>,
    #[serde(default)]
    pub children: Option<Vec<JobChild>>,
    #[serde(default)]
    pub updated_at: Option<String>,
    #[serde(default)]
    pub session_id: Option<String>,
    #[serde(default)]
    pub cwd: Option<String>,
    /// The session's transcript, which Claude Code scans for links.
    #[serde(default)]
    pub link_scan_path: Option<String>,
}

#[derive(Debug, Default, Clone, Deserialize, PartialEq)]
pub struct JobOutput {
    #[serde(default)]
    pub result: Option<String>,
}

#[derive(Debug, Default, Clone, Deserialize, PartialEq)]
pub struct JobChild {
    #[serde(default)]
    pub href: Option<String>,
    #[serde(default)]
    pub kind: Option<String>,
}

/// Where Claude Code keeps a directory per background session:
/// `$CLAUDE_CONFIG_DIR/jobs`, else `$HOME/.claude/jobs` (ADR 0024).
pub fn jobs_dir() -> Option<PathBuf> {
    if let Some(dir) = std::env::var_os("CLAUDE_CONFIG_DIR").filter(|d| !d.is_empty()) {
        return Some(PathBuf::from(dir).join("jobs"));
    }
    let home = std::env::var_os("HOME").filter(|h| !h.is_empty())?;
    Some(PathBuf::from(home).join(".claude").join("jobs"))
}

pub fn read_job(dir: &Path, id: &str) -> Option<JobState> {
    if id.is_empty() || id.contains('/') || id.contains("..") {
        return None;
    }
    let bytes = std::fs::read(dir.join(id).join("state.json")).ok()?;
    serde_json::from_slice(&bytes).ok()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn read_job_parses_known_fields_and_ignores_others() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::create_dir(dir.path().join("abc123")).unwrap();
        std::fs::write(
            dir.path().join("abc123/state.json"),
            r#"{"state":"done","detail":"PR #4","output":{"result":"shipped"},
               "children":[{"id":"4","href":"https://example.com/pull/4","kind":"pr"}],
               "updatedAt":"2026-10-21T04:16:27.506Z","tokens":5}"#,
        )
        .unwrap();
        let job = read_job(dir.path(), "abc123").unwrap();
        assert_eq!(job.state.as_deref(), Some("done"));
        assert_eq!(job.output.unwrap().result.as_deref(), Some("shipped"));
        assert_eq!(job.children.unwrap()[0].kind.as_deref(), Some("pr"));
    }

    #[test]
    fn read_job_missing_file_is_none() {
        let dir = tempfile::tempdir().unwrap();
        assert_eq!(read_job(dir.path(), "nope"), None);
    }

    #[test]
    fn read_job_rejects_path_traversal() {
        let dir = tempfile::tempdir().unwrap();
        assert_eq!(read_job(dir.path(), "../x"), None);
    }
}
