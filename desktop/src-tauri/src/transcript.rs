//! A background session's conversation, read from the transcript Claude Code
//! writes as it works (`projects/<dir>/<session>.jsonl`). The format is
//! undocumented, so lines and fields that don't parse are skipped: the
//! conversation is a view, and the terminal stays the source of truth.

use crate::jobs::JobState;
use serde::Serialize;
use serde_json::Value;
use std::io::{Read, Seek, SeekFrom};
use std::path::{Path, PathBuf};

/// Longest text kept for a tool's input or result; the rest is cut.
const MAX_TOOL_TEXT: usize = 4000;
/// Most items returned at once; older ones are counted in `dropped`.
const MAX_ITEMS: usize = 2000;

#[derive(Debug, Clone, Serialize, PartialEq)]
#[serde(tag = "kind", rename_all = "camelCase")]
pub enum Item {
    #[serde(rename_all = "camelCase")]
    User {
        id: String,
        at: Option<String>,
        text: String,
    },
    #[serde(rename_all = "camelCase")]
    Assistant {
        id: String,
        at: Option<String>,
        text: String,
    },
    #[serde(rename_all = "camelCase")]
    Thinking {
        id: String,
        at: Option<String>,
        text: String,
    },
    #[serde(rename_all = "camelCase")]
    ToolUse {
        id: String,
        at: Option<String>,
        tool_use_id: String,
        name: String,
        summary: String,
        input: String,
    },
    #[serde(rename_all = "camelCase")]
    ToolResult {
        id: String,
        at: Option<String>,
        tool_use_id: String,
        text: String,
        is_error: bool,
    },
    #[serde(rename_all = "camelCase")]
    Notice {
        id: String,
        at: Option<String>,
        text: String,
    },
}

#[derive(Debug, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Chunk {
    pub items: Vec<Item>,
    /// The offset to read from next time.
    pub next: u64,
    /// The file is shorter than the offset asked for, so it was read from
    /// the start and `items` replaces what was read before.
    pub reset: bool,
    /// How many items before `items` were left out.
    pub dropped: usize,
    pub found: bool,
}

/// The transcript of a background session: the path its job state records,
/// else where Claude Code puts a session's transcript for its directory.
pub fn transcript_path(config_dir: &Path, job: &JobState) -> Option<PathBuf> {
    if let Some(path) = job.link_scan_path.as_deref().map(PathBuf::from) {
        if path.starts_with(config_dir.join("projects")) && path.is_file() {
            return Some(path);
        }
    }
    let session = job.session_id.as_deref()?;
    let cwd = job.cwd.as_deref()?;
    if session.contains('/') || session.contains("..") {
        return None;
    }
    Some(
        config_dir
            .join("projects")
            .join(project_dir_name(cwd))
            .join(format!("{session}.jsonl")),
    )
}

fn project_dir_name(cwd: &str) -> String {
    cwd.chars()
        .map(|c| if c.is_ascii_alphanumeric() { c } else { '-' })
        .collect()
}

/// Reads the whole lines written to path since offset.
pub fn read_from(path: &Path, offset: u64) -> std::io::Result<Chunk> {
    let mut file = match std::fs::File::open(path) {
        Ok(f) => f,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
            return Ok(Chunk {
                items: Vec::new(),
                next: 0,
                reset: offset > 0,
                dropped: 0,
                found: false,
            })
        }
        Err(e) => return Err(e),
    };
    let len = file.metadata()?.len();
    let reset = len < offset;
    let start = if reset { 0 } else { offset };
    file.seek(SeekFrom::Start(start))?;
    let mut bytes = Vec::new();
    file.read_to_end(&mut bytes)?;
    let complete = bytes.iter().rposition(|&b| b == b'\n').map_or(0, |i| i + 1);
    let mut items: Vec<Item> = bytes[..complete]
        .split(|&b| b == b'\n')
        .filter_map(|line| serde_json::from_slice::<Value>(line).ok())
        .flat_map(|entry| items_of(&entry))
        .collect();
    let dropped = items.len().saturating_sub(MAX_ITEMS);
    items.drain(..dropped);
    Ok(Chunk {
        items,
        next: start + complete as u64,
        reset,
        dropped,
        found: true,
    })
}

/// The items one transcript line shows. A subagent's messages
/// (`isSidechain`) and ones Claude Code adds for the model (`isMeta`) show
/// nothing.
fn items_of(entry: &Value) -> Vec<Item> {
    let flag = |k: &str| entry.get(k).and_then(Value::as_bool).unwrap_or(false);
    if flag("isSidechain") || flag("isMeta") {
        return Vec::new();
    }
    let uuid = entry
        .get("uuid")
        .and_then(Value::as_str)
        .unwrap_or_default();
    let at = entry
        .get("timestamp")
        .and_then(Value::as_str)
        .map(str::to_string);
    let content = entry.get("message").and_then(|m| m.get("content"));
    match entry.get("type").and_then(Value::as_str) {
        Some("user") if flag("isCompactSummary") => vec![Item::Notice {
            id: uuid.to_string(),
            at,
            text: "Conversation compacted".to_string(),
        }],
        Some("user") => user_items(uuid, at, content),
        Some("assistant") => assistant_items(uuid, at, content),
        Some("system") => system_item(uuid, at, entry).into_iter().collect(),
        _ => Vec::new(),
    }
}

fn user_items(uuid: &str, at: Option<String>, content: Option<&Value>) -> Vec<Item> {
    match content {
        Some(Value::String(s)) => user_text(uuid, at, s).into_iter().collect(),
        Some(Value::Array(blocks)) => blocks
            .iter()
            .enumerate()
            .filter_map(|(i, block)| {
                let id = format!("{uuid}:{i}");
                match block.get("type").and_then(Value::as_str)? {
                    "text" => user_text(&id, at.clone(), block.get("text")?.as_str()?),
                    "image" => Some(Item::User {
                        id,
                        at: at.clone(),
                        text: "[image]".to_string(),
                    }),
                    "tool_result" => Some(Item::ToolResult {
                        id,
                        at: at.clone(),
                        tool_use_id: str_field(block, "tool_use_id"),
                        text: truncate(&result_text(block.get("content"))),
                        is_error: block
                            .get("is_error")
                            .and_then(Value::as_bool)
                            .unwrap_or(false),
                    }),
                    _ => None,
                }
            })
            .collect(),
        _ => Vec::new(),
    }
}

/// A user message, or what Claude Code records in one: a slash command, its
/// output, a `!` command, or a background task finishing.
fn user_text(id: &str, at: Option<String>, raw: &str) -> Option<Item> {
    let id = id.to_string();
    let text = strip_tag(raw, "system-reminder");
    let text = text.trim();
    if text.is_empty() {
        return None;
    }
    let notice = |text: String| {
        Some(Item::Notice {
            id: id.clone(),
            at: at.clone(),
            text,
        })
    };
    if let Some(name) = tag(text, "command-name") {
        let args = tag(text, "command-args").unwrap_or_default();
        return notice(format!("{name} {args}").trim_end().to_string());
    }
    if let Some(out) = tag(text, "local-command-stdout") {
        let out = strip_ansi(out);
        return if out.trim().is_empty() {
            None
        } else {
            notice(out.trim().to_string())
        };
    }
    if text.starts_with("<task-notification>") {
        return notice(
            tag(text, "summary")
                .unwrap_or("A background task finished")
                .to_string(),
        );
    }
    if let Some(cmd) = tag(text, "bash-input") {
        return Some(Item::User {
            id,
            at,
            text: format!("! {cmd}"),
        });
    }
    if let Some(out) = tag(text, "bash-stdout") {
        let err = tag(text, "bash-stderr").unwrap_or_default();
        return notice(truncate(format!("{out}{err}").trim()));
    }
    Some(Item::User {
        id,
        at,
        text: text.to_string(),
    })
}

fn assistant_items(uuid: &str, at: Option<String>, content: Option<&Value>) -> Vec<Item> {
    let Some(Value::Array(blocks)) = content else {
        return Vec::new();
    };
    blocks
        .iter()
        .enumerate()
        .filter_map(|(i, block)| {
            let id = format!("{uuid}:{i}");
            let at = at.clone();
            match block.get("type").and_then(Value::as_str)? {
                "text" => {
                    let text = block.get("text")?.as_str()?.trim();
                    (!text.is_empty()).then(|| Item::Assistant {
                        id,
                        at,
                        text: text.to_string(),
                    })
                }
                "thinking" => {
                    let text = block.get("thinking")?.as_str()?.trim();
                    (!text.is_empty()).then(|| Item::Thinking {
                        id,
                        at,
                        text: text.to_string(),
                    })
                }
                "tool_use" => {
                    let name = str_field(block, "name");
                    let input = block.get("input").cloned().unwrap_or(Value::Null);
                    Some(Item::ToolUse {
                        id,
                        at,
                        tool_use_id: str_field(block, "id"),
                        summary: tool_summary(&name, &input),
                        input: truncate(&tool_input(&name, &input)),
                        name,
                    })
                }
                _ => None,
            }
        })
        .collect()
}

fn system_item(uuid: &str, at: Option<String>, entry: &Value) -> Option<Item> {
    let content = entry
        .get("content")
        .and_then(Value::as_str)
        .unwrap_or_default();
    let text = match entry.get("subtype").and_then(Value::as_str)? {
        "compact_boundary" => "Conversation compacted".to_string(),
        "away_summary" | "informational" => content.trim().to_string(),
        "local_command" => return user_text(uuid, at, content),
        _ => return None,
    };
    (!text.is_empty()).then(|| Item::Notice {
        id: uuid.to_string(),
        at,
        text,
    })
}

fn str_field(value: &Value, key: &str) -> String {
    value
        .get(key)
        .and_then(Value::as_str)
        .unwrap_or_default()
        .to_string()
}

fn result_text(content: Option<&Value>) -> String {
    match content {
        Some(Value::String(s)) => s.clone(),
        Some(Value::Array(parts)) => parts
            .iter()
            .filter_map(|p| match p.get("type").and_then(Value::as_str)? {
                "text" => p.get("text")?.as_str().map(str::to_string),
                "image" => Some("[image]".to_string()),
                "document" => Some("[document]".to_string()),
                _ => None,
            })
            .collect::<Vec<_>>()
            .join("\n"),
        _ => String::new(),
    }
}

/// One line saying what a tool call is about: the command, file, or
/// pattern, as Claude Code's own terminal shows it.
fn tool_summary(name: &str, input: &Value) -> String {
    let field = |k: &str| input.get(k).and_then(Value::as_str);
    let known = match name {
        "Bash" => field("description").or_else(|| field("command")),
        "Read" | "Write" | "Edit" | "NotebookEdit" => {
            field("file_path").or_else(|| field("notebook_path"))
        }
        "Grep" | "Glob" => field("pattern"),
        "WebFetch" => field("url"),
        "WebSearch" => field("query"),
        "Agent" | "Task" => field("description"),
        _ => None,
    };
    let any = || {
        input
            .as_object()?
            .values()
            .find_map(|v| v.as_str().filter(|s| !s.trim().is_empty()))
    };
    let line = known.or_else(any).unwrap_or_default();
    let line = line.lines().next().unwrap_or_default().trim();
    cut(line, 200)
}

fn tool_input(name: &str, input: &Value) -> String {
    if name == "Bash" {
        if let Some(cmd) = input.get("command").and_then(Value::as_str) {
            return cmd.to_string();
        }
    }
    serde_json::to_string_pretty(input).unwrap_or_default()
}

fn truncate(text: &str) -> String {
    cut(text, MAX_TOOL_TEXT)
}

/// text cut to at most max characters, with an ellipsis if it was longer.
fn cut(text: &str, max: usize) -> String {
    match text.char_indices().nth(max) {
        Some((i, _)) => format!("{}…", &text[..i]),
        None => text.to_string(),
    }
}

/// The text inside the first `<name>…</name>` in text.
fn tag<'a>(text: &'a str, name: &str) -> Option<&'a str> {
    let open = format!("<{name}>");
    let close = format!("</{name}>");
    let start = text.find(&open)? + open.len();
    let end = text[start..].find(&close)? + start;
    Some(&text[start..end])
}

fn strip_tag(text: &str, name: &str) -> String {
    let open = format!("<{name}>");
    let close = format!("</{name}>");
    let mut out = String::with_capacity(text.len());
    let mut rest = text;
    while let Some(start) = rest.find(&open) {
        out.push_str(&rest[..start]);
        match rest[start..].find(&close) {
            Some(end) => rest = &rest[start + end + close.len()..],
            None => {
                rest = "";
                break;
            }
        }
    }
    out.push_str(rest);
    out
}

fn strip_ansi(text: &str) -> String {
    let mut out = String::with_capacity(text.len());
    let mut chars = text.chars().peekable();
    while let Some(c) = chars.next() {
        if c != '\x1b' {
            out.push(c);
            continue;
        }
        if chars.next_if_eq(&'[').is_some() {
            while chars.next().is_some_and(|c| !c.is_ascii_alphabetic()) {}
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    fn items(line: &str) -> Vec<Item> {
        items_of(&serde_json::from_str(line).unwrap())
    }

    fn texts(items: &[Item]) -> Vec<String> {
        items
            .iter()
            .map(|i| match i {
                Item::User { text, .. } => format!("user: {text}"),
                Item::Assistant { text, .. } => format!("assistant: {text}"),
                Item::Thinking { text, .. } => format!("thinking: {text}"),
                Item::ToolUse { name, summary, .. } => format!("tool: {name} {summary}"),
                Item::ToolResult { text, is_error, .. } => format!("result({is_error}): {text}"),
                Item::Notice { text, .. } => format!("notice: {text}"),
            })
            .collect()
    }

    #[test]
    fn user_prompt_drops_system_reminders() {
        let got = items(
            r#"{"type":"user","uuid":"u1","message":{"role":"user","content":"<system-reminder>x</system-reminder>Fix the login"}}"#,
        );
        assert_eq!(texts(&got), ["user: Fix the login"]);
    }

    #[test]
    fn meta_and_sidechain_messages_are_hidden() {
        assert!(
            items(r#"{"type":"user","isMeta":true,"message":{"content":"caveat"}}"#).is_empty()
        );
        assert!(items(r#"{"type":"assistant","isSidechain":true,"message":{"content":[{"type":"text","text":"sub"}]}}"#).is_empty());
    }

    #[test]
    fn assistant_blocks_become_text_thinking_and_tool_use() {
        let got = items(
            r#"{"type":"assistant","uuid":"a1","message":{"content":[
            {"type":"thinking","thinking":"hmm"},
            {"type":"thinking","thinking":"","signature":"sig"},
            {"type":"text","text":"Looking."},
            {"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls -la","description":"List files"}}]}}"#,
        );
        assert_eq!(
            texts(&got),
            [
                "thinking: hmm",
                "assistant: Looking.",
                "tool: Bash List files"
            ]
        );
        let Item::ToolUse {
            input, tool_use_id, ..
        } = &got[2]
        else {
            panic!("not a tool use");
        };
        assert_eq!((input.as_str(), tool_use_id.as_str()), ("ls -la", "t1"));
    }

    #[test]
    fn tool_results_keep_their_tool_use_id_and_error() {
        let got = items(
            r#"{"type":"user","uuid":"u2","message":{"content":[
            {"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"boom"},
            {"type":"tool_result","tool_use_id":"t2","content":[{"type":"text","text":"a"},{"type":"image"}]}]}}"#,
        );
        assert_eq!(
            texts(&got),
            ["result(true): boom", "result(false): a\n[image]"]
        );
    }

    #[test]
    fn slash_commands_and_task_notifications_are_notices() {
        let cmd = items(
            r#"{"type":"user","uuid":"u","message":{"content":"<command-name>/compact</command-name>\n<command-args>keep tests</command-args>"}}"#,
        );
        let out = items(
            r#"{"type":"user","uuid":"u","message":{"content":"<local-command-stdout>\u001b[2mCompacted\u001b[22m</local-command-stdout>"}}"#,
        );
        let task = items(
            r#"{"type":"user","uuid":"u","message":{"content":"<task-notification><summary>Agent \"x\" finished</summary></task-notification>"}}"#,
        );
        assert_eq!(
            [texts(&cmd), texts(&out), texts(&task)].concat(),
            [
                "notice: /compact keep tests",
                "notice: Compacted",
                "notice: Agent \"x\" finished"
            ]
        );
    }

    #[test]
    fn compaction_shows_as_one_notice() {
        let summary = items(
            r#"{"type":"user","isCompactSummary":true,"message":{"content":"long summary"}}"#,
        );
        let boundary = items(r#"{"type":"system","subtype":"compact_boundary","content":"x"}"#);
        assert_eq!(
            [texts(&summary), texts(&boundary)].concat(),
            [
                "notice: Conversation compacted",
                "notice: Conversation compacted"
            ]
        );
    }

    #[test]
    fn bookkeeping_lines_show_nothing() {
        assert!(items(r#"{"type":"system","subtype":"turn_duration","durationMs":5}"#).is_empty());
        assert!(items(r#"{"type":"file-history-snapshot","snapshot":{}}"#).is_empty());
    }

    #[test]
    fn tool_summary_falls_back_to_first_string_field() {
        let input = serde_json::json!({"n": 1, "query": "line one\nline two"});
        assert_eq!(tool_summary("mcp__x__search", &input), "line one");
    }

    #[test]
    fn cut_respects_character_boundaries() {
        assert_eq!(cut("héllo", 2), "hé…");
        assert_eq!(cut("hi", 2), "hi");
    }

    #[test]
    fn read_from_returns_whole_lines_and_resumes() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("s.jsonl");
        let mut file = std::fs::File::create(&path).unwrap();
        let first = r#"{"type":"user","uuid":"1","message":{"content":"one"}}"#;
        write!(file, "{first}\n{{\"type\":\"user\",\"uuid\":\"2\"").unwrap();
        let chunk = read_from(&path, 0).unwrap();
        assert_eq!(texts(&chunk.items), ["user: one"]);
        assert_eq!(chunk.next, first.len() as u64 + 1);
        writeln!(file, r#","message":{{"content":"two"}}}}"#).unwrap();
        let next = read_from(&path, chunk.next).unwrap();
        assert_eq!(
            (texts(&next.items), next.reset),
            (vec!["user: two".to_string()], false)
        );
    }

    #[test]
    fn read_from_starts_over_when_the_file_shrinks() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("s.jsonl");
        std::fs::write(
            &path,
            "{\"type\":\"user\",\"uuid\":\"1\",\"message\":{\"content\":\"one\"}}\n",
        )
        .unwrap();
        let chunk = read_from(&path, 10_000).unwrap();
        assert_eq!((chunk.reset, chunk.items.len()), (true, 1));
    }

    #[test]
    fn read_from_missing_file_is_not_found() {
        let dir = tempfile::tempdir().unwrap();
        let chunk = read_from(&dir.path().join("none.jsonl"), 0).unwrap();
        assert!(!chunk.found);
    }

    #[test]
    fn transcript_path_derives_from_cwd_and_session() {
        let job = JobState {
            session_id: Some("abc-123".into()),
            cwd: Some("/Users/me/Application Support/w".into()),
            ..JobState::default()
        };
        assert_eq!(
            transcript_path(Path::new("/c"), &job),
            Some(PathBuf::from(
                "/c/projects/-Users-me-Application-Support-w/abc-123.jsonl"
            ))
        );
    }

    #[test]
    fn transcript_path_ignores_recorded_path_outside_projects() {
        let job = JobState {
            link_scan_path: Some("/etc/passwd".into()),
            ..JobState::default()
        };
        assert_eq!(transcript_path(Path::new("/c"), &job), None);
    }
}
