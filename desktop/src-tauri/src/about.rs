//! What Settings > About shows: the app's version and the commit it was
//! built from, and the `ws` and `claude` the app runs, with their versions
//! and where they are on its PATH.

use crate::runner::Runner;
use serde::Serialize;
use std::path::{Path, PathBuf};

#[derive(Debug, Clone, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct AppVersion {
    pub version: String,
    /// `git describe` of the commit the app was built from, if known.
    pub commit: Option<String>,
}

#[derive(Debug, Clone, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct ToolVersion {
    pub version: Option<String>,
    /// Where the program is on the app's PATH, if it is.
    pub path: Option<String>,
    /// Why the version couldn't be read.
    pub error: Option<String>,
}

#[derive(Debug, Clone, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Versions {
    pub app: AppVersion,
    pub ws: ToolVersion,
    pub claude: ToolVersion,
}

pub fn app_version(version: &str) -> AppVersion {
    let commit = option_env!("WS_DESKTOP_COMMIT")
        .filter(|c| !c.is_empty())
        .map(str::to_string);
    AppVersion {
        version: version.to_string(),
        commit,
    }
}

/// The versions of the app, and of `ws` and `claude` as the app runs them.
pub async fn versions(runner: &Runner, app: AppVersion) -> Versions {
    let (ws, claude) = tokio::join!(tool(runner, "ws"), tool(runner, "claude"));
    Versions { app, ws, claude }
}

async fn tool(runner: &Runner, program: &str) -> ToolVersion {
    let path = which(runner.path(), program).map(|p| p.display().to_string());
    match runner.run(program, &["--version".to_string()]).await {
        Ok(out) if out.success => ToolVersion {
            version: parse_version(program, &String::from_utf8_lossy(&out.stdout)),
            path,
            error: None,
        },
        Ok(out) => ToolVersion {
            version: None,
            path,
            error: Some(
                first_line(&out.stderr)
                    .unwrap_or("exited with an error")
                    .to_string(),
            ),
        },
        Err(err) => ToolVersion {
            version: None,
            path,
            error: Some(err.message),
        },
    }
}

fn first_line(text: &str) -> Option<&str> {
    text.lines().map(str::trim).find(|l| !l.is_empty())
}

/// The version in `<program> --version`'s output: `ws version <v>` from
/// ws, `<v> (Claude Code)` from claude.
pub fn parse_version(program: &str, output: &str) -> Option<String> {
    let line = first_line(output)?;
    let prefix = format!("{program} version ");
    let version = line.strip_prefix(&prefix).unwrap_or(line);
    let version = version
        .strip_suffix("(Claude Code)")
        .unwrap_or(version)
        .trim();
    (!version.is_empty()).then(|| version.to_string())
}

/// The first executable called name in the PATH-style list path.
pub fn which(path: &str, name: &str) -> Option<PathBuf> {
    std::env::split_paths(path)
        .map(|dir| dir.join(name))
        .find(|candidate| is_executable(candidate))
}

#[cfg(unix)]
fn is_executable(path: &Path) -> bool {
    use std::os::unix::fs::PermissionsExt;
    path.metadata()
        .is_ok_and(|m| m.is_file() && m.permissions().mode() & 0o111 != 0)
}

#[cfg(not(unix))]
fn is_executable(path: &Path) -> bool {
    path.is_file()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_ws_and_claude_versions() {
        assert_eq!(
            parse_version("ws", "ws version v0.3.0-4-gdc313a2\n").as_deref(),
            Some("v0.3.0-4-gdc313a2")
        );
        assert_eq!(
            parse_version("claude", "2.1.303 (Claude Code)\n").as_deref(),
            Some("2.1.303")
        );
        assert_eq!(parse_version("ws", "\n"), None);
    }

    #[test]
    fn which_finds_the_first_executable_on_the_path() {
        let first = tempfile::tempdir().unwrap();
        let second = tempfile::tempdir().unwrap();
        for dir in [&first, &second] {
            let p = dir.path().join("ws");
            std::fs::write(&p, "#!/bin/sh\n").unwrap();
        }
        let exec = second.path().join("ws");
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            std::fs::set_permissions(&exec, std::fs::Permissions::from_mode(0o755)).unwrap();
        }
        let path = std::env::join_paths([first.path(), second.path()]).unwrap();
        assert_eq!(which(path.to_str().unwrap(), "ws"), Some(exec));
        assert_eq!(which(path.to_str().unwrap(), "nope"), None);
    }

    #[tokio::test]
    async fn versions_reads_each_tool_and_says_why_it_cannot() {
        let dir = tempfile::tempdir().unwrap();
        let script = |name: &str, body: &str| {
            let p = dir.path().join(name);
            std::fs::write(&p, format!("#!/bin/sh\n{body}\n")).unwrap();
            #[cfg(unix)]
            {
                use std::os::unix::fs::PermissionsExt;
                std::fs::set_permissions(&p, std::fs::Permissions::from_mode(0o755)).unwrap();
            }
        };
        script("ws", "echo 'ws version v0.3.0'");
        script("claude", "echo 'boom' >&2; exit 1");
        let runner = Runner::new(dir.path().display().to_string());
        let got = versions(&runner, app_version("0.1.0")).await;
        assert_eq!(got.app.version, "0.1.0");
        assert_eq!(got.ws.version.as_deref(), Some("v0.3.0"));
        assert_eq!(
            got.ws.path,
            Some(dir.path().join("ws").display().to_string())
        );
        assert_eq!(got.claude.version, None);
        assert_eq!(got.claude.error.as_deref(), Some("boom"));
    }
}
