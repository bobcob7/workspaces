//! Workspace Online: the desktop backend served over HTTP (ADR 0053).

pub mod auth;
pub mod config;
pub mod dispatch;
pub mod push;
pub mod server;
pub mod wire;

use std::ffi::OsString;
use std::io::Write;
use std::os::unix::fs::OpenOptionsExt;
use std::path::{Path, PathBuf};

/// Writes `bytes` to `path` through a temporary file in the same
/// directory and a rename, with mode 0600.
pub(crate) fn write_private(path: &Path, bytes: &[u8]) -> std::io::Result<()> {
    if let Some(parent) = path.parent() {
        std::fs::create_dir_all(parent)?;
    }
    let mut tmp_name = OsString::from(path.as_os_str());
    tmp_name.push(format!(".tmp-{}", std::process::id()));
    let tmp = PathBuf::from(tmp_name);
    let mut file = std::fs::OpenOptions::new()
        .write(true)
        .create(true)
        .truncate(true)
        .mode(0o600)
        .open(&tmp)?;
    file.write_all(bytes)?;
    file.sync_all()?;
    std::fs::rename(&tmp, path)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::PermissionsExt;

    #[test]
    fn write_private_creates_missing_directories_and_replaces_contents() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("nested/secret.json");
        write_private(&path, b"first").unwrap();
        write_private(&path, b"second").unwrap();
        assert_eq!(std::fs::read(&path).unwrap(), b"second");
    }

    #[test]
    fn write_private_sets_mode_0600() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("secret.json");
        write_private(&path, b"x").unwrap();
        let mode = std::fs::metadata(&path).unwrap().permissions().mode();
        assert_eq!(mode & 0o777, 0o600);
    }
}
