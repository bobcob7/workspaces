//! Terminals: `claude attach <id>` or `ws shell` in a pseudo-terminal,
//! streamed to an xterm.js view in the frontend.

use crate::runner::AppError;
use portable_pty::{native_pty_system, ChildKiller, CommandBuilder, MasterPty, PtySize};
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::io::{Read, Write};
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU32, Ordering};
use std::sync::{Arc, Mutex};
use tauri::ipc::Channel;

/// What a terminal runs.
#[derive(Debug, Clone, Deserialize)]
#[serde(
    tag = "kind",
    rename_all = "camelCase",
    rename_all_fields = "camelCase"
)]
pub enum TermSpec {
    /// `claude attach <session_id>`, which also resumes a stopped session.
    Attach { session_id: String, cwd: String },
    /// `ws shell [--name <name>] <specs>...`: a new shell workspace that
    /// lives as long as this terminal.
    Shell {
        name: Option<String>,
        specs: Vec<String>,
    },
}

#[derive(Debug, Clone, Serialize)]
#[serde(tag = "event", rename_all = "camelCase")]
pub enum TermEvent {
    Data { data: String },
    Exit { code: Option<u32> },
}

struct Term {
    writer: Box<dyn Write + Send>,
    master: Box<dyn MasterPty + Send>,
    killer: Box<dyn ChildKiller + Send + Sync>,
}

/// The open terminals, by id.
#[derive(Default)]
pub struct Terminals {
    next: AtomicU32,
    terms: Arc<Mutex<HashMap<u32, Term>>>,
}

fn size(cols: u16, rows: u16) -> PtySize {
    PtySize {
        rows: rows.max(1),
        cols: cols.max(1),
        pixel_width: 0,
        pixel_height: 0,
    }
}

fn pty_error(what: &str, err: impl std::fmt::Display) -> AppError {
    AppError::new("terminal_failed", format!("{what}: {err}"))
}

/// Finds program in a PATH string, since the pty's child gets the PATH the
/// app resolved rather than the app's own.
fn find_program(program: &str, path: &str) -> Option<PathBuf> {
    path.split(':')
        .map(|dir| Path::new(dir).join(program))
        .find(|candidate| candidate.is_file())
}

fn command_for(spec: &TermSpec) -> (&'static str, Vec<String>, Option<String>) {
    match spec {
        TermSpec::Attach { session_id, cwd } => (
            "claude",
            vec!["attach".to_string(), session_id.clone()],
            Some(cwd.clone()),
        ),
        TermSpec::Shell { name, specs } => {
            let mut args = vec!["shell".to_string()];
            if let Some(name) = name.as_ref().filter(|n| !n.is_empty()) {
                args.extend(["--name".to_string(), name.clone()]);
            }
            args.extend(specs.iter().cloned());
            ("ws", args, None)
        }
    }
}

impl Terminals {
    pub fn open(
        &self,
        spec: &TermSpec,
        path_env: &str,
        cols: u16,
        rows: u16,
        events: Channel<TermEvent>,
    ) -> Result<u32, AppError> {
        let (program, args, cwd) = command_for(spec);
        let resolved = find_program(program, path_env).ok_or_else(|| {
            AppError::new(
                &format!("{program}_missing"),
                format!("`{program}` isn't on PATH ({path_env})"),
            )
        })?;
        let pair = native_pty_system()
            .openpty(size(cols, rows))
            .map_err(|e| pty_error("opening a terminal", e))?;
        let mut cmd = CommandBuilder::new(resolved);
        cmd.args(&args);
        cmd.env("PATH", path_env);
        cmd.env("TERM", "xterm-256color");
        cmd.env("COLORTERM", "truecolor");
        let home = std::env::var("HOME").unwrap_or_else(|_| "/".to_string());
        let dir = cwd.filter(|d| Path::new(d).is_dir()).unwrap_or(home);
        cmd.cwd(dir);
        let mut child = pair
            .slave
            .spawn_command(cmd)
            .map_err(|e| pty_error(&format!("starting {program}"), e))?;
        drop(pair.slave);
        let reader = pair
            .master
            .try_clone_reader()
            .map_err(|e| pty_error("reading the terminal", e))?;
        let writer = pair
            .master
            .take_writer()
            .map_err(|e| pty_error("writing the terminal", e))?;
        let id = self.next.fetch_add(1, Ordering::Relaxed) + 1;
        let term = Term {
            writer,
            master: pair.master,
            killer: child.clone_killer(),
        };
        self.lock().insert(id, term);
        let terms = Arc::clone(&self.terms);
        std::thread::spawn(move || {
            pump(reader, &events);
            let code = child.wait().ok().map(|status| status.exit_code());
            if let Ok(mut terms) = terms.lock() {
                terms.remove(&id);
            }
            let _ = events.send(TermEvent::Exit { code });
        });
        Ok(id)
    }

    pub fn write(&self, id: u32, data: &str) -> Result<(), AppError> {
        let mut terms = self.lock();
        let term = terms.get_mut(&id).ok_or_else(|| not_open(id))?;
        term.writer
            .write_all(data.as_bytes())
            .and_then(|()| term.writer.flush())
            .map_err(|e| pty_error("writing the terminal", e))
    }

    pub fn resize(&self, id: u32, cols: u16, rows: u16) -> Result<(), AppError> {
        let terms = self.lock();
        let term = terms.get(&id).ok_or_else(|| not_open(id))?;
        term.master
            .resize(size(cols, rows))
            .map_err(|e| pty_error("resizing the terminal", e))
    }

    /// Ends the terminal's process. For `claude attach` that only detaches:
    /// the session keeps running in Claude Code's background service.
    pub fn close(&self, id: u32) -> Result<(), AppError> {
        let Some(mut term) = self.lock().remove(&id) else {
            return Ok(());
        };
        term.killer
            .kill()
            .map_err(|e| pty_error("closing the terminal", e))
    }

    pub fn close_all(&self) {
        let ids: Vec<u32> = self.lock().keys().copied().collect();
        for id in ids {
            let _ = self.close(id);
        }
    }

    fn lock(&self) -> std::sync::MutexGuard<'_, HashMap<u32, Term>> {
        self.terms
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
    }
}

fn not_open(id: u32) -> AppError {
    AppError::new("terminal_closed", format!("terminal {id} isn't open"))
}

fn pump(mut reader: Box<dyn Read + Send>, events: &Channel<TermEvent>) {
    let mut buf = [0u8; 8192];
    let mut pending = Vec::new();
    loop {
        let n = match reader.read(&mut buf) {
            Ok(0) | Err(_) => return,
            Ok(n) => n,
        };
        pending.extend_from_slice(&buf[..n]);
        let data = take_text(&mut pending);
        if !data.is_empty() && events.send(TermEvent::Data { data }).is_err() {
            return;
        }
    }
}

/// Takes the complete UTF-8 text from the front of pending, leaving a
/// character split across reads for the next one. Invalid bytes become
/// U+FFFD.
fn take_text(pending: &mut Vec<u8>) -> String {
    let mut out = String::new();
    loop {
        match std::str::from_utf8(pending) {
            Ok(text) => {
                out.push_str(text);
                pending.clear();
                return out;
            }
            Err(err) => {
                let valid = err.valid_up_to();
                out.push_str(std::str::from_utf8(&pending[..valid]).unwrap_or_default());
                match err.error_len() {
                    None => {
                        pending.drain(..valid);
                        return out;
                    }
                    Some(len) => {
                        out.push(char::REPLACEMENT_CHARACTER);
                        pending.drain(..valid + len);
                    }
                }
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn take_text_keeps_split_character_for_next_read() {
        let bytes = "a·b".as_bytes();
        let mut pending = bytes[..2].to_vec();
        assert_eq!(take_text(&mut pending), "a");
        pending.extend_from_slice(&bytes[2..]);
        assert_eq!(take_text(&mut pending), "·b");
        assert!(pending.is_empty());
    }

    #[test]
    fn take_text_replaces_invalid_bytes() {
        let mut pending = vec![b'x', 0xff, b'y'];
        assert_eq!(take_text(&mut pending), "x\u{FFFD}y");
    }

    #[test]
    fn shell_command_passes_name_and_specs() {
        let spec = TermSpec::Shell {
            name: Some("try".into()),
            specs: vec!["api@v2".into()],
        };
        let (program, args, cwd) = command_for(&spec);
        assert_eq!((program, cwd), ("ws", None));
        assert_eq!(args, ["shell", "--name", "try", "api@v2"]);
    }

    #[test]
    fn shell_command_omits_empty_name() {
        let spec = TermSpec::Shell {
            name: Some(String::new()),
            specs: vec![],
        };
        assert_eq!(command_for(&spec).1, ["shell"]);
    }

    #[test]
    fn attach_command_runs_in_session_directory() {
        let spec: TermSpec =
            serde_json::from_str(r#"{"kind":"attach","sessionId":"e8cdf5d8","cwd":"/w/x"}"#)
                .unwrap();
        let (program, args, cwd) = command_for(&spec);
        assert_eq!((program, cwd.as_deref()), ("claude", Some("/w/x")));
        assert_eq!(args, ["attach", "e8cdf5d8"]);
    }

    #[test]
    fn terminal_streams_output_takes_input_and_reports_exit() {
        use std::os::unix::fs::PermissionsExt;
        use std::sync::mpsc;
        use std::time::Duration;
        use tauri::ipc::InvokeResponseBody;
        let dir = tempfile::tempdir().unwrap();
        let ws = dir.path().join("ws");
        std::fs::write(
            &ws,
            "#!/bin/sh\necho \"args: $*\"\nread line\necho \"got: $line\"\nexit 3\n",
        )
        .unwrap();
        std::fs::set_permissions(&ws, std::fs::Permissions::from_mode(0o755)).unwrap();
        let (tx, rx) = mpsc::channel::<String>();
        let channel = Channel::new(move |body| {
            if let InvokeResponseBody::Json(json) = body {
                let _ = tx.send(json);
            }
            Ok(())
        });
        let terminals = Terminals::default();
        let spec = TermSpec::Shell {
            name: Some("t".into()),
            specs: vec!["r@main".into()],
        };
        let id = terminals
            .open(
                &spec,
                &format!("{}:/bin:/usr/bin", dir.path().display()),
                80,
                24,
                channel,
            )
            .unwrap();
        terminals.write(id, "hello\r").unwrap();
        let mut output = String::new();
        let mut exit = None;
        while let Ok(json) = rx.recv_timeout(Duration::from_secs(5)) {
            let event: serde_json::Value = serde_json::from_str(&json).unwrap();
            match event["event"].as_str() {
                Some("data") => output.push_str(event["data"].as_str().unwrap()),
                _ => {
                    exit = Some(event["code"].clone());
                    break;
                }
            }
        }
        assert!(
            output.contains("args: shell --name t r@main"),
            "output: {output:?}"
        );
        assert!(output.contains("got: hello"), "output: {output:?}");
        assert_eq!(exit, Some(serde_json::json!(3)));
    }

    /// Attaches to a real session for a few seconds, then detaches:
    /// `WS_DESKTOP_ATTACH=<id> cargo test attach_live -- --ignored`.
    #[test]
    #[ignore = "attaches to a real Claude Code session"]
    fn attach_live() {
        use std::time::Duration;
        let id = std::env::var("WS_DESKTOP_ATTACH").expect("WS_DESKTOP_ATTACH");
        let received = Arc::new(Mutex::new(0usize));
        let counter = Arc::clone(&received);
        let channel = Channel::new(move |body| {
            if let tauri::ipc::InvokeResponseBody::Json(json) = body {
                *counter.lock().unwrap() += json.len();
            }
            Ok(())
        });
        let terminals = Terminals::default();
        let spec = TermSpec::Attach {
            session_id: id,
            cwd: "/".into(),
        };
        let term = terminals
            .open(&spec, &crate::env::resolve_path(), 120, 40, channel)
            .unwrap();
        std::thread::sleep(Duration::from_secs(4));
        terminals.close(term).unwrap();
        let bytes = *received.lock().unwrap();
        println!("received {bytes} bytes of events");
        assert!(bytes > 1000);
    }

    #[test]
    fn find_program_searches_each_directory() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::write(dir.path().join("ws"), "").unwrap();
        let path = format!("/nonexistent:{}", dir.path().display());
        assert_eq!(find_program("ws", &path), Some(dir.path().join("ws")));
        assert_eq!(find_program("claude", &path), None);
    }
}
