//! Opening a workspace in the file manager or an editor (ADR 0051).

use crate::commands::AppState;
use crate::ipc::State;
use crate::reviews::store::{OpenSettings, Settings};
use crate::runner::AppError;
use serde::{Deserialize, Serialize};
use std::io::Read;
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::time::{Duration, Instant};

/// Which program opens the workspace. JSON: "fileManager" | "editor".
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub enum OpenWith {
    FileManager,
    Editor,
}

/// The editor when Settings has none.
pub const DEFAULT_EDITOR: &str = "code";

/// How long a launched program may run before it counts as started.
const GRACE: Duration = Duration::from_secs(3);

/// The longest stderr tail an error message carries, in characters.
const STDERR_TAIL: usize = 500;

/// `open` on macOS, `explorer` on Windows, `xdg-open` elsewhere.
pub fn default_file_manager() -> &'static str {
    if cfg!(target_os = "macos") {
        "open"
    } else if cfg!(windows) {
        "explorer"
    } else {
        "xdg-open"
    }
}

/// The defaults, as the Settings placeholders show them.
pub fn defaults() -> OpenSettings {
    OpenSettings {
        file_manager: default_file_manager().to_string(),
        editor: DEFAULT_EDITOR.to_string(),
    }
}

/// The command to run: the trimmed setting, or the default when it's empty.
pub fn command_for(with: OpenWith, settings: &OpenSettings) -> String {
    let (setting, fallback) = match with {
        OpenWith::FileManager => (&settings.file_manager, default_file_manager()),
        OpenWith::Editor => (&settings.editor, DEFAULT_EDITOR),
    };
    match setting.trim() {
        "" => fallback.to_string(),
        trimmed => trimmed.to_string(),
    }
}

/// Why a command doesn't split into words.
#[derive(Debug, PartialEq, Eq)]
enum SplitError {
    UnclosedQuote(char),
    NoProgram,
}

impl SplitError {
    /// The error as `split_command` reports it.
    fn describe(&self) -> String {
        match self {
            Self::UnclosedQuote(quote) => format!("unclosed {quote} in the command"),
            Self::NoProgram => "no program in the command".to_string(),
        }
    }

    /// The error as Settings validation names it, after the field's name.
    fn reason(&self) -> String {
        match self {
            Self::UnclosedQuote(quote) => format!("has an unclosed {quote}"),
            Self::NoProgram => "has no program".to_string(),
        }
    }
}

/// Splits a command into words (ADR 0051): POSIX-like quoting, no expansion.
/// Err(`setting_invalid`) on an unclosed quote or when there are no words.
pub fn split_command(command: &str) -> Result<Vec<String>, AppError> {
    split_words(command).map_err(|err| AppError::new("setting_invalid", err.describe()))
}

fn split_words(command: &str) -> Result<Vec<String>, SplitError> {
    let mut words = Vec::new();
    let mut word: Option<String> = None;
    let mut chars = command.chars().peekable();
    while let Some(c) = chars.next() {
        match c {
            c if c.is_whitespace() => {
                if let Some(done) = word.take() {
                    words.push(done);
                }
            }
            '\'' => {
                let body = word.get_or_insert_with(String::new);
                loop {
                    match chars.next() {
                        Some('\'') => break,
                        Some(c) => body.push(c),
                        None => return Err(SplitError::UnclosedQuote('\'')),
                    }
                }
            }
            '"' => {
                let body = word.get_or_insert_with(String::new);
                loop {
                    match chars.next() {
                        Some('"') => break,
                        Some('\\') => match chars.peek() {
                            Some(&escaped @ ('"' | '\\')) => {
                                body.push(escaped);
                                chars.next();
                            }
                            _ => body.push('\\'),
                        },
                        Some(c) => body.push(c),
                        None => return Err(SplitError::UnclosedQuote('"')),
                    }
                }
            }
            '\\' => {
                let body = word.get_or_insert_with(String::new);
                match chars.peek() {
                    Some(&escaped)
                        if escaped.is_whitespace() || matches!(escaped, '\'' | '"' | '\\') =>
                    {
                        body.push(escaped);
                        chars.next();
                    }
                    _ => body.push('\\'),
                }
            }
            c => word.get_or_insert_with(String::new).push(c),
        }
    }
    if let Some(done) = word {
        words.push(done);
    }
    if words.is_empty() {
        return Err(SplitError::NoProgram);
    }
    Ok(words)
}

/// Rejects a non-empty setting that `split_command` rejects; the message
/// names the field: "the editor command has an unclosed \"".
pub fn validate(settings: &OpenSettings) -> Result<(), AppError> {
    check_field(OpenWith::FileManager, &settings.file_manager)?;
    check_field(OpenWith::Editor, &settings.editor)
}

fn check_field(with: OpenWith, setting: &str) -> Result<(), AppError> {
    let setting = setting.trim();
    if setting.is_empty() {
        return Ok(());
    }
    split_words(setting).map(drop).map_err(|err| {
        AppError::new(
            "setting_invalid",
            format!("the {} command {}", label(with), err.reason()),
        )
    })
}

/// The field's name in messages.
fn label(with: OpenWith) -> &'static str {
    match with {
        OpenWith::FileManager => "file manager",
        OpenWith::Editor => "editor",
    }
}

/// The directory to open: for Editor with exactly one repo,
/// `Path::new(path).join(&repos[0])`; otherwise `path`.
pub fn target_dir(with: OpenWith, path: &str, repos: &[String]) -> PathBuf {
    match (with, repos) {
        (OpenWith::Editor, [repo]) => Path::new(path).join(repo),
        _ => PathBuf::from(path),
    }
}

/// The program and arguments: every `{path}` inside a word is replaced by
/// `dir`; when no word contains `{path}`, `dir` is appended as a last word.
pub fn build_argv(command: &str, dir: &Path) -> Result<Vec<String>, AppError> {
    let mut words = split_command(command)?;
    let dir = dir.display().to_string();
    if words.iter().any(|word| word.contains("{path}")) {
        return Ok(words
            .into_iter()
            .map(|word| word.replace("{path}", &dir))
            .collect());
    }
    words.push(dir);
    Ok(words)
}

/// Starts a program. The seam tests replace.
pub trait Launch {
    /// `argv[0]` is the program, found on `path_env`.
    fn launch(&self, argv: &[String], path_env: &str) -> Result<(), AppError>;
}

/// Launches real processes; waits `grace` for an early failure.
pub struct SystemLauncher {
    pub grace: Duration,
}

impl Default for SystemLauncher {
    fn default() -> Self {
        Self { grace: GRACE }
    }
}

impl Launch for SystemLauncher {
    fn launch(&self, argv: &[String], path_env: &str) -> Result<(), AppError> {
        let Some((program, args)) = argv.split_first() else {
            return Err(AppError::new(
                "setting_invalid",
                SplitError::NoProgram.describe(),
            ));
        };
        let mut child = Command::new(program)
            .args(args)
            .env("PATH", path_env)
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::piped())
            .spawn()
            .map_err(|err| spawn_error(program, err))?;
        let started = Instant::now();
        loop {
            match child.try_wait() {
                Ok(Some(status)) => return finished(program, status, child.stderr.take()),
                Ok(None) if started.elapsed() < self.grace => {
                    std::thread::sleep(Duration::from_millis(25));
                }
                Ok(None) => {
                    let stderr = child.stderr.take();
                    std::thread::spawn(move || {
                        if let Some(mut stderr) = stderr {
                            let _ = std::io::copy(&mut stderr, &mut std::io::sink());
                        }
                        let _ = child.wait();
                    });
                    return Ok(());
                }
                Err(err) => {
                    return Err(AppError::new(
                        "open_failed",
                        format!("couldn't start `{program}`: {err}"),
                    ))
                }
            }
        }
    }
}

fn spawn_error(program: &str, err: std::io::Error) -> AppError {
    if err.kind() != std::io::ErrorKind::NotFound {
        return AppError::new("open_failed", format!("couldn't start `{program}`: {err}"));
    }
    let hint = if program == DEFAULT_EDITOR {
        "In VS Code run \"Shell Command: Install 'code' command in PATH\", or set the editor in Settings › Workspaces."
    } else {
        "Set its full path in Settings › Workspaces."
    };
    AppError::new(
        "open_program_missing",
        format!("`{program}` isn't on the app's PATH. {hint}"),
    )
}

/// Judges a program that exited within the grace period.
fn finished(
    program: &str,
    status: std::process::ExitStatus,
    stderr: Option<std::process::ChildStderr>,
) -> Result<(), AppError> {
    if status.success() || is_explorer(program) {
        return Ok(());
    }
    let mut text = String::new();
    if let Some(mut stderr) = stderr {
        let _ = stderr.read_to_string(&mut text);
    }
    let code = status
        .code()
        .map_or_else(|| "unknown".to_string(), |code| code.to_string());
    let tail = last_chars(text.trim(), STDERR_TAIL);
    let message = if tail.is_empty() {
        format!("`{program}` exited with status {code}")
    } else {
        format!("`{program}` exited with status {code}: {tail}")
    };
    Err(AppError::new("open_failed", message))
}

/// Explorer exits 1 even when it opened the window.
fn is_explorer(program: &str) -> bool {
    Path::new(program)
        .file_stem()
        .and_then(|stem| stem.to_str())
        .is_some_and(|stem| stem.eq_ignore_ascii_case("explorer"))
}

/// The last `count` characters of `text`.
fn last_chars(text: &str, count: usize) -> &str {
    match text.char_indices().rev().nth(count - 1) {
        Some((index, _)) => &text[index..],
        None => text,
    }
}

/// What was opened, for tests and the frontend.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Opened {
    pub dir: String,
    pub command: Vec<String>,
}

/// Opens the workspace: checks the target is a directory, builds the
/// argv from the setting, and launches it.
pub fn open(
    launcher: &dyn Launch,
    path_env: &str,
    settings: &OpenSettings,
    with: OpenWith,
    path: &str,
    repos: &[String],
) -> Result<Opened, AppError> {
    let dir = target_dir(with, path, repos);
    if !dir.is_dir() {
        return Err(AppError::new(
            "open_dir_missing",
            format!("{} doesn't exist", dir.display()),
        ));
    }
    let command = command_for(with, settings);
    let argv = build_argv(&command, &dir).map_err(|err| {
        AppError::new(
            "open_command_invalid",
            format!(
                "the {} command in Settings › Workspaces is invalid: {}",
                label(with),
                err.message
            ),
        )
    })?;
    launcher.launch(&argv, path_env)?;
    Ok(Opened {
        dir: dir.display().to_string(),
        command: argv,
    })
}

/// Opens a workspace or a repo in the file manager or the editor.
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn workspace_open(
    state: State<'_, AppState>,
    with: OpenWith,
    path: String,
    repos: Vec<String>,
) -> Result<Opened, AppError> {
    let settings = state.store.get().settings.open;
    let path_env = state.runner.path().to_string();
    tokio::task::spawn_blocking(move || {
        open(
            &SystemLauncher::default(),
            &path_env,
            &settings,
            with,
            &path,
            &repos,
        )
    })
    .await
    .map_err(|err| AppError::new("internal", err.to_string()))?
}

/// The platform defaults for the open commands.
#[cfg_attr(feature = "desktop", tauri::command)]
pub fn open_defaults() -> OpenSettings {
    defaults()
}

/// Saves the open commands, trimmed, after checking they split (ADR 0051).
#[cfg_attr(feature = "desktop", tauri::command)]
pub fn settings_set_open(
    state: State<'_, AppState>,
    open: OpenSettings,
) -> Result<Settings, AppError> {
    let open = OpenSettings {
        file_manager: open.file_manager.trim().to_string(),
        editor: open.editor.trim().to_string(),
    };
    validate(&open)?;
    state.store.update(|d| {
        d.settings.open = open;
        d.settings.clone()
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Mutex;

    /// Records each launch and returns `fail`, if set.
    #[derive(Default)]
    struct FakeLauncher {
        calls: Mutex<Vec<Vec<String>>>,
        fail: Option<AppError>,
    }

    impl Launch for FakeLauncher {
        fn launch(&self, argv: &[String], _path_env: &str) -> Result<(), AppError> {
            self.calls.lock().unwrap().push(argv.to_vec());
            match &self.fail {
                Some(err) => Err(err.clone()),
                None => Ok(()),
            }
        }
    }

    fn words(list: &[&str]) -> Vec<String> {
        list.iter().map(|word| word.to_string()).collect()
    }

    fn settings(file_manager: &str, editor: &str) -> OpenSettings {
        OpenSettings {
            file_manager: file_manager.to_string(),
            editor: editor.to_string(),
        }
    }

    #[test]
    fn split_reads_plain_words_and_runs_of_whitespace_as_one_gap() {
        assert_eq!(split_command("code").unwrap(), words(&["code"]));
        assert_eq!(split_command("code  -n\t").unwrap(), words(&["code", "-n"]));
    }

    #[test]
    fn split_keeps_double_quoted_spaces() {
        assert_eq!(
            split_command(r#"open -a "Visual Studio Code""#).unwrap(),
            words(&["open", "-a", "Visual Studio Code"])
        );
    }

    #[test]
    fn split_unescapes_quote_and_backslash_in_double_quotes() {
        assert_eq!(
            split_command(r#""say \"hi\" \\ \n""#).unwrap(),
            words(&[r#"say "hi" \ \n"#])
        );
    }

    #[test]
    fn split_keeps_single_quoted_text_literal() {
        assert_eq!(
            split_command("open -a 'Visual Studio Code'").unwrap(),
            words(&["open", "-a", "Visual Studio Code"])
        );
        assert_eq!(split_command(r"'a\ b'").unwrap(), words(&[r"a\ b"]));
    }

    #[test]
    fn split_accepts_backslash_escaped_space() {
        assert_eq!(
            split_command(r"/Applications/My\ Editor.app/bin/ed --wait").unwrap(),
            words(&["/Applications/My Editor.app/bin/ed", "--wait"])
        );
    }

    #[test]
    fn split_keeps_backslashes_in_windows_paths() {
        assert_eq!(
            split_command(r"C:\Windows\explorer.exe").unwrap(),
            words(&[r"C:\Windows\explorer.exe"])
        );
        assert_eq!(
            split_command(r#""C:\Program Files\x.exe" --new"#).unwrap(),
            words(&[r"C:\Program Files\x.exe", "--new"])
        );
    }

    #[test]
    fn split_keeps_an_empty_quoted_word() {
        assert_eq!(split_command(r#"a "" b"#).unwrap(), words(&["a", "", "b"]));
    }

    #[test]
    fn split_keeps_the_placeholder_as_a_word() {
        assert_eq!(
            split_command("idea --line 1 {path}").unwrap(),
            words(&["idea", "--line", "1", "{path}"])
        );
    }

    #[test]
    fn split_rejects_an_unclosed_double_quote() {
        let err = split_command(r#"open -a "Visual Studio Code"#).unwrap_err();
        assert_eq!(err.code, "setting_invalid");
        assert_eq!(err.message, r#"unclosed " in the command"#);
    }

    #[test]
    fn split_rejects_an_unclosed_single_quote() {
        let err = split_command("open -a 'Code").unwrap_err();
        assert_eq!(err.code, "setting_invalid");
        assert_eq!(err.message, "unclosed ' in the command");
    }

    #[test]
    fn split_rejects_a_blank_command() {
        let err = split_command("   ").unwrap_err();
        assert_eq!(err.code, "setting_invalid");
        assert_eq!(err.message, "no program in the command");
    }

    #[test]
    fn build_argv_appends_dir_when_no_word_has_a_placeholder() {
        assert_eq!(
            build_argv("open -a Code", Path::new("/w/x y")).unwrap(),
            words(&["open", "-a", "Code", "/w/x y"])
        );
    }

    #[test]
    fn build_argv_replaces_placeholder_inside_a_word() {
        assert_eq!(
            build_argv("idea --dir={path}", Path::new("/w")).unwrap(),
            words(&["idea", "--dir=/w"])
        );
        assert_eq!(
            build_argv("idea --line 1 {path}", Path::new("/w")).unwrap(),
            words(&["idea", "--line", "1", "/w"])
        );
    }

    #[test]
    fn build_argv_keeps_a_dir_with_spaces_as_one_word() {
        assert_eq!(
            build_argv("code {path}", Path::new("/w/x y")).unwrap(),
            words(&["code", "/w/x y"])
        );
    }

    #[test]
    fn target_dir_is_the_repo_for_an_editor_with_one_repo() {
        assert_eq!(
            target_dir(OpenWith::Editor, "/w", &words(&["buff-bear"])),
            PathBuf::from("/w/buff-bear")
        );
    }

    #[test]
    fn target_dir_is_the_workspace_for_an_editor_with_two_repos_or_none() {
        assert_eq!(
            target_dir(OpenWith::Editor, "/w", &words(&["a", "b"])),
            PathBuf::from("/w")
        );
        assert_eq!(target_dir(OpenWith::Editor, "/w", &[]), PathBuf::from("/w"));
    }

    #[test]
    fn target_dir_is_the_workspace_for_the_file_manager() {
        assert_eq!(
            target_dir(OpenWith::FileManager, "/w", &words(&["buff-bear"])),
            PathBuf::from("/w")
        );
    }

    #[test]
    fn command_for_uses_the_default_when_the_setting_is_blank() {
        assert_eq!(
            command_for(OpenWith::FileManager, &OpenSettings::default()),
            default_file_manager()
        );
        assert_eq!(
            command_for(OpenWith::Editor, &settings("", "   ")),
            DEFAULT_EDITOR
        );
    }

    #[test]
    fn command_for_trims_a_set_value() {
        assert_eq!(
            command_for(OpenWith::Editor, &settings("", "  zed \n")),
            "zed"
        );
    }

    #[test]
    fn open_launches_the_repo_in_the_editor() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::create_dir(dir.path().join("repo")).unwrap();
        let path = dir.path().display().to_string();
        let launcher = FakeLauncher::default();
        let opened = open(
            &launcher,
            "",
            &OpenSettings::default(),
            OpenWith::Editor,
            &path,
            &words(&["repo"]),
        )
        .unwrap();
        let repo = dir.path().join("repo").display().to_string();
        assert_eq!(
            launcher.calls.lock().unwrap().as_slice(),
            [words(&["code", &repo])]
        );
        assert_eq!(
            opened,
            Opened {
                dir: repo,
                command: words(&["code", &dir.path().join("repo").display().to_string()]),
            }
        );
    }

    #[test]
    fn open_runs_a_custom_file_manager_command_on_the_workspace() {
        let dir = tempfile::tempdir().unwrap();
        std::fs::create_dir(dir.path().join("repo")).unwrap();
        let path = dir.path().display().to_string();
        let launcher = FakeLauncher::default();
        open(
            &launcher,
            "",
            &settings(r#"open -a "Visual Studio Code""#, ""),
            OpenWith::FileManager,
            &path,
            &words(&["repo"]),
        )
        .unwrap();
        assert_eq!(
            launcher.calls.lock().unwrap().as_slice(),
            [words(&["open", "-a", "Visual Studio Code", &path])]
        );
    }

    #[test]
    fn open_rejects_a_missing_directory_without_launching() {
        let dir = tempfile::tempdir().unwrap();
        let missing = dir.path().join("gone").display().to_string();
        let launcher = FakeLauncher::default();
        let err = open(
            &launcher,
            "",
            &OpenSettings::default(),
            OpenWith::FileManager,
            &missing,
            &[],
        )
        .unwrap_err();
        assert_eq!(err.code, "open_dir_missing");
        assert_eq!(err.message, format!("{missing} doesn't exist"));
        assert!(launcher.calls.lock().unwrap().is_empty());
    }

    #[test]
    fn open_rejects_a_stored_command_that_does_not_split_without_launching() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().display().to_string();
        let launcher = FakeLauncher::default();
        let err = open(
            &launcher,
            "",
            &settings("", r#"code "-n"#),
            OpenWith::Editor,
            &path,
            &[],
        )
        .unwrap_err();
        assert_eq!(err.code, "open_command_invalid");
        assert_eq!(
            err.message,
            r#"the editor command in Settings › Workspaces is invalid: unclosed " in the command"#
        );
        assert!(launcher.calls.lock().unwrap().is_empty());
    }

    #[test]
    fn open_returns_the_launchers_error_unchanged() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().display().to_string();
        let launcher = FakeLauncher {
            calls: Mutex::default(),
            fail: Some(AppError::new("open_failed", "boom")),
        };
        let err = open(
            &launcher,
            "",
            &OpenSettings::default(),
            OpenWith::FileManager,
            &path,
            &[],
        )
        .unwrap_err();
        assert_eq!(err, AppError::new("open_failed", "boom"));
    }

    #[test]
    fn validate_accepts_empty_and_plain_commands() {
        assert!(validate(&OpenSettings::default()).is_ok());
        assert!(validate(&settings("", "code -n")).is_ok());
        assert!(validate(&settings("   ", "")).is_ok());
    }

    #[test]
    fn validate_names_the_editor_with_an_unclosed_quote() {
        let err = validate(&settings("", r#"code -n ""#)).unwrap_err();
        assert_eq!(err.code, "setting_invalid");
        assert!(
            err.message.starts_with("the editor command"),
            "{}",
            err.message
        );
        assert_eq!(err.message, r#"the editor command has an unclosed ""#);
    }

    #[test]
    fn validate_names_the_file_manager_with_an_unclosed_quote() {
        let err = validate(&settings("open -a 'Code", "")).unwrap_err();
        assert_eq!(err.message, "the file manager command has an unclosed '");
    }

    #[test]
    fn last_chars_keeps_the_end_of_a_text_on_a_character_boundary() {
        assert_eq!(last_chars("abcdé", 2), "dé");
        assert_eq!(last_chars("ab", 5), "ab");
    }

    #[test]
    fn default_editor_is_code() {
        assert_eq!(defaults().editor, "code");
    }

    #[cfg(unix)]
    mod system {
        use super::*;
        use std::os::unix::fs::PermissionsExt;

        fn script(dir: &Path, name: &str, body: &str) {
            let path = dir.join(name);
            std::fs::write(&path, format!("#!/bin/sh\n{body}\n")).unwrap();
            std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o755)).unwrap();
        }

        /// The script dir, then the system dirs so `sh` builtins' helpers
        /// such as `sleep` are found.
        fn path_env(dir: &Path) -> String {
            format!("{}:/usr/bin:/bin", dir.display())
        }

        /// A grace short enough to test "still running", for the sleeping
        /// program only.
        fn quick() -> SystemLauncher {
            SystemLauncher {
                grace: Duration::from_millis(300),
            }
        }

        /// A grace long enough that a script which exits on its own never
        /// reads as still running, even when the suite loads the machine.
        fn patient() -> SystemLauncher {
            SystemLauncher {
                grace: Duration::from_secs(10),
            }
        }

        #[test]
        fn launch_is_ok_when_the_program_exits_zero() {
            let dir = tempfile::tempdir().unwrap();
            script(dir.path(), "ok", "exit 0");
            let result = patient().launch(&words(&["ok"]), &path_env(dir.path()));
            assert!(result.is_ok(), "{result:?}");
        }

        #[test]
        fn launch_reports_a_failure_with_its_stderr() {
            let dir = tempfile::tempdir().unwrap();
            script(dir.path(), "fail", "echo boom >&2; exit 3");
            let err = patient()
                .launch(&words(&["fail"]), &path_env(dir.path()))
                .unwrap_err();
            assert_eq!(err.code, "open_failed");
            assert!(
                err.message.contains("exited with status 3"),
                "{}",
                err.message
            );
            assert!(err.message.contains("boom"), "{}", err.message);
        }

        #[test]
        fn launch_counts_a_program_still_running_after_the_grace_as_started() {
            let dir = tempfile::tempdir().unwrap();
            let started = Instant::now();
            let result = quick().launch(&words(&["sleep", "5"]), &path_env(dir.path()));
            assert!(result.is_ok(), "{result:?}");
            assert!(started.elapsed() < Duration::from_secs(2));
        }

        #[test]
        fn launch_reports_a_missing_program() {
            let dir = tempfile::tempdir().unwrap();
            let err = patient()
                .launch(&words(&["nope"]), &dir.path().display().to_string())
                .unwrap_err();
            assert_eq!(err.code, "open_program_missing");
            assert!(err.message.contains("`nope` isn't on the app's PATH."));
        }

        #[test]
        fn launch_tells_how_to_install_the_code_command() {
            let dir = tempfile::tempdir().unwrap();
            let err = patient()
                .launch(&words(&["code"]), &dir.path().display().to_string())
                .unwrap_err();
            assert_eq!(err.code, "open_program_missing");
            assert!(
                err.message
                    .contains("Shell Command: Install 'code' command in PATH"),
                "{}",
                err.message
            );
        }

        #[test]
        fn launch_treats_a_failing_explorer_as_started() {
            let dir = tempfile::tempdir().unwrap();
            script(dir.path(), "explorer", "exit 1");
            let result = patient().launch(&words(&["explorer"]), &path_env(dir.path()));
            assert!(result.is_ok(), "{result:?}");
        }
    }
}
