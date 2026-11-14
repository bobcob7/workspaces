//! Forgejo access tokens: environment first, then forge-tokens.json (ADR 0060).

use super::forge;
use crate::runner::AppError;
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;
use std::io::Write;
use std::os::unix::fs::OpenOptionsExt;
use std::path::{Path, PathBuf};

pub const TOKENS_FILE: &str = "forge-tokens.json";
const VERSION: u32 = 1;

/// Where a host's token came from. The token itself never leaves the backend.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum TokenSource {
    Env,
    File,
}

#[derive(Debug, Default, Serialize, Deserialize)]
#[serde(default)]
struct TokenFile {
    version: u32,
    tokens: BTreeMap<String, String>,
}

type EnvLookup = Box<dyn Fn(&str) -> Option<String> + Send + Sync>;

/// Forgejo tokens by host. Read-only from the environment; `set` writes the file.
pub struct TokenStore {
    path: Option<PathBuf>,
    env: EnvLookup,
}

impl TokenStore {
    /// The file beside `desktop_json` (None: no file), reading `std::env::var`.
    pub fn beside(desktop_json: Option<&Path>) -> Self {
        Self::with_env(
            desktop_json.map(|p| p.with_file_name(TOKENS_FILE)),
            |name| std::env::var(name).ok(),
        )
    }

    /// Same, with an injected environment (tests).
    pub fn with_env(
        path: Option<PathBuf>,
        env: impl Fn(&str) -> Option<String> + Send + Sync + 'static,
    ) -> Self {
        Self {
            path,
            env: Box::new(env),
        }
    }

    /// "WS_FORGEJO_TOKEN_" + host upper-cased, non-alphanumerics as '_'.
    pub fn env_name(host: &str) -> String {
        let suffix: String = host
            .chars()
            .map(|c| {
                if c.is_ascii_alphanumeric() {
                    c.to_ascii_uppercase()
                } else {
                    '_'
                }
            })
            .collect();
        format!("WS_FORGEJO_TOKEN_{suffix}")
    }

    /// Env first (non-empty after trim), then the file.
    pub fn token(&self, host: &str) -> Option<(String, TokenSource)> {
        let from_env = (self.env)(&Self::env_name(host))
            .map(|t| t.trim().to_string())
            .filter(|t| !t.is_empty());
        if let Some(token) = from_env {
            return Some((token, TokenSource::Env));
        }
        self.read()
            .tokens
            .remove(host)
            .filter(|t| !t.is_empty())
            .map(|t| (t, TokenSource::File))
    }

    /// Empty `token` removes the host. Rejects whitespace inside a token
    /// (`setting_invalid`). Writes temp + rename, mode 0600; a file with a
    /// newer version is never written (`state_newer`).
    pub fn set(&self, host: &str, token: &str) -> Result<(), AppError> {
        if !forge::is_host(host) {
            return Err(AppError::new(
                "setting_invalid",
                format!("\"{host}\" isn't a host name"),
            ));
        }
        let token = token.trim();
        if token.chars().any(char::is_whitespace) {
            return Err(AppError::new("setting_invalid", "a token has no spaces"));
        }
        let Some(path) = self.path.as_deref() else {
            return Err(AppError::new("internal", "no place to keep tokens"));
        };
        let mut file = self.read();
        if file.version > VERSION {
            return Err(AppError::new(
                "state_newer",
                "forge-tokens.json was written by a newer version of the app; it isn't changed",
            ));
        }
        if token.is_empty() {
            file.tokens.remove(host);
        } else {
            file.tokens.insert(host.to_string(), token.to_string());
        }
        file.version = VERSION;
        write(path, &file)
    }

    /// The file's contents. A missing or unreadable file has no tokens.
    fn read(&self) -> TokenFile {
        self.path
            .as_deref()
            .and_then(|path| std::fs::read(path).ok())
            .and_then(|bytes| serde_json::from_slice(&bytes).ok())
            .unwrap_or_default()
    }
}

/// Writes a temp file beside `path` with mode 0600 and renames it into
/// place, so a crash leaves the old file or the new one.
fn write(path: &Path, file: &TokenFile) -> Result<(), AppError> {
    let fail = |e: std::io::Error| {
        AppError::new(
            "state_write_failed",
            format!("writing {}: {e}", path.display()),
        )
    };
    if let Some(dir) = path.parent() {
        std::fs::create_dir_all(dir).map_err(fail)?;
    }
    let tmp = path.with_extension("json.tmp");
    let bytes =
        serde_json::to_vec_pretty(file).map_err(|e| AppError::new("internal", e.to_string()))?;
    // A leftover temp file keeps its old mode, so drop it before creating one.
    std::fs::remove_file(&tmp).ok();
    let mut out = std::fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(&tmp)
        .map_err(fail)?;
    out.write_all(&bytes).map_err(fail)?;
    std::fs::rename(&tmp, path).map_err(fail)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::PermissionsExt;

    const HOST: &str = "git.example.com";
    const TOKEN: &str = "0123456789abcdef0123456789abcdef01234567";

    fn no_env() -> impl Fn(&str) -> Option<String> + Send + Sync + 'static {
        |_| None
    }

    fn store_in(dir: &tempfile::TempDir) -> TokenStore {
        TokenStore::with_env(Some(dir.path().join(TOKENS_FILE)), no_env())
    }

    #[test]
    fn env_name_upper_cases_and_replaces_punctuation() {
        assert_eq!(
            TokenStore::env_name("git.example.com:3000"),
            "WS_FORGEJO_TOKEN_GIT_EXAMPLE_COM_3000"
        );
    }

    #[test]
    fn env_wins_over_file() {
        let dir = tempfile::tempdir().unwrap();
        store_in(&dir).set(HOST, TOKEN).unwrap();
        let env = TokenStore::with_env(Some(dir.path().join(TOKENS_FILE)), |name| {
            (name == "WS_FORGEJO_TOKEN_GIT_EXAMPLE_COM").then(|| "from-env".to_string())
        });
        assert_eq!(
            env.token(HOST),
            Some(("from-env".to_string(), TokenSource::Env))
        );
    }

    #[test]
    fn blank_env_is_ignored() {
        let env = TokenStore::with_env(None, |_| Some("  ".to_string()));
        assert_eq!(env.token(HOST), None);
    }

    #[test]
    fn set_then_token_reads_from_file() {
        let dir = tempfile::tempdir().unwrap();
        let store = store_in(&dir);
        store.set(HOST, TOKEN).unwrap();
        assert_eq!(
            store.token(HOST),
            Some((TOKEN.to_string(), TokenSource::File))
        );
    }

    #[test]
    fn empty_token_removes_host() {
        let dir = tempfile::tempdir().unwrap();
        let store = store_in(&dir);
        store.set(HOST, TOKEN).unwrap();
        store.set(HOST, "").unwrap();
        assert_eq!(store.token(HOST), None);
    }

    #[test]
    fn file_is_private() {
        let dir = tempfile::tempdir().unwrap();
        store_in(&dir).set(HOST, TOKEN).unwrap();
        let mode = std::fs::metadata(dir.path().join(TOKENS_FILE))
            .unwrap()
            .permissions()
            .mode();
        assert_eq!(mode & 0o777, 0o600);
    }

    #[test]
    fn token_with_spaces_is_refused() {
        let dir = tempfile::tempdir().unwrap();
        let err = store_in(&dir).set(HOST, "two words").unwrap_err();
        assert_eq!(err.code, "setting_invalid");
    }

    #[test]
    fn bad_host_is_refused() {
        let dir = tempfile::tempdir().unwrap();
        let err = store_in(&dir)
            .set("git.example.com/team", TOKEN)
            .unwrap_err();
        assert_eq!(err.code, "setting_invalid");
    }

    #[test]
    fn newer_file_is_not_written() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join(TOKENS_FILE);
        std::fs::write(&path, r#"{"version":2,"tokens":{}}"#).unwrap();
        let store = TokenStore::with_env(Some(path.clone()), no_env());
        let err = store.set(HOST, TOKEN).unwrap_err();
        assert_eq!(err.code, "state_newer");
        assert_eq!(
            std::fs::read_to_string(path).unwrap(),
            r#"{"version":2,"tokens":{}}"#
        );
    }

    #[test]
    fn missing_or_unreadable_file_has_no_tokens() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join(TOKENS_FILE);
        let store = TokenStore::with_env(Some(path.clone()), no_env());
        assert_eq!(store.token(HOST), None);
        std::fs::write(&path, "{not json").unwrap();
        assert_eq!(store.token(HOST), None);
    }
}
