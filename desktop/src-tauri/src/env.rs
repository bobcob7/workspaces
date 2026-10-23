//! The PATH that `ws` and `claude` run with. An app opened from Finder or
//! the Dock gets launchd's minimal PATH, not the one the user's shell sets.

use std::io::Read;
use std::process::{Command, Stdio};
use std::time::{Duration, Instant};

const MARKER: &str = "__WS_DESKTOP_PATH__";
const SHELL_TIMEOUT: Duration = Duration::from_secs(5);

/// Returns the user's login-shell PATH, followed by this process's PATH and
/// the usual install locations of `ws` and `claude`, without duplicates.
pub fn resolve_path() -> String {
    let home = std::env::var("HOME").unwrap_or_default();
    let current = std::env::var("PATH").unwrap_or_default();
    let shell = std::env::var("SHELL").unwrap_or_else(|_| "/bin/zsh".to_string());
    let from_shell = shell_path(&shell).unwrap_or_default();
    let fallbacks = [
        format!("{home}/.local/bin"),
        format!("{home}/go/bin"),
        format!("{home}/.cargo/bin"),
        "/opt/homebrew/bin".to_string(),
        "/usr/local/bin".to_string(),
        "/usr/bin".to_string(),
        "/bin".to_string(),
    ];
    merge_paths(&[&from_shell, &current, &fallbacks.join(":")])
}

/// Asks an interactive login shell for its PATH, since tools such as nvm set
/// it in rc files. Output around the markers, such as a banner, is ignored.
fn shell_path(shell: &str) -> Option<String> {
    let script = format!("printf '%s%s%s' {MARKER} \"$PATH\" {MARKER}");
    let mut child = Command::new(shell)
        .args(["-i", "-l", "-c", &script])
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .spawn()
        .ok()?;
    let start = Instant::now();
    loop {
        if child.try_wait().ok()?.is_some() {
            break;
        }
        if start.elapsed() > SHELL_TIMEOUT {
            let _ = child.kill();
            return None;
        }
        std::thread::sleep(Duration::from_millis(20));
    }
    let mut out = String::new();
    child.stdout.take()?.read_to_string(&mut out).ok()?;
    extract_marked(&out)
}

fn extract_marked(out: &str) -> Option<String> {
    let start = out.find(MARKER)? + MARKER.len();
    let len = out[start..].find(MARKER)?;
    Some(out[start..start + len].to_string())
}

fn merge_paths(lists: &[&str]) -> String {
    let mut seen: Vec<&str> = Vec::new();
    for dir in lists.iter().flat_map(|l| l.split(':')) {
        if !dir.is_empty() && !seen.contains(&dir) {
            seen.push(dir);
        }
    }
    seen.join(":")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn merge_paths_keeps_first_occurrence_order() {
        let merged = merge_paths(&["/a:/b", "/b:/c:", ":/a:/d"]);
        assert_eq!(merged, "/a:/b:/c:/d");
    }

    #[test]
    fn extract_marked_ignores_shell_banner() {
        let out = format!("Welcome!\n{MARKER}/x:/y{MARKER}");
        assert_eq!(extract_marked(&out).as_deref(), Some("/x:/y"));
    }

    #[test]
    fn extract_marked_without_markers_is_none() {
        assert_eq!(extract_marked("no markers here"), None);
    }
}
