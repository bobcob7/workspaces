//! The transcript scan (ADR 0084): every 30 s, read what each local agent's
//! transcript gained, find `@artifact:` citations in user messages and agent
//! replies, and hand them to `ws artifact scan record`, which records each
//! (message, artifact) pair once. `ws` never parses a transcript; this module
//! reuses the conversation view's parser, so the scan and the view agree.

use crate::model::WsList;
use crate::runner::Runner;
use crate::transcript::{self, Item};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::collections::HashMap;
use std::io::{Read, Seek, SeekFrom};
use std::path::{Path, PathBuf};
use std::time::Duration;

/// How often the sweep runs.
pub const SWEEP_EVERY: Duration = Duration::from_secs(30);
/// Most bytes read from one transcript per sweep.
pub const STEP_BYTES: u64 = 4 * 1024 * 1024;
/// How far the cursor may move past the stored one before it's stored
/// without a citation.
pub const PERSIST_AFTER: u64 = 1024 * 1024;
/// Most citations `ws artifact scan record` takes at once.
pub const MAX_CITATIONS: usize = 1000;
/// Longest message key `ws` accepts.
const MAX_KEY_BYTES: usize = 128;
const MARKER: &[u8] = b"@artifact:";
const HINT_MARKER: &str = "run \"ws machine artifact get <id>\"";
const ID_HEX: usize = 8;

/// Which transcript format a workspace writes.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Format {
    /// Claude Code's `projects/<dir>/<session>.jsonl`.
    Claude,
    /// A `ws` runner's `state/<id>/runner/transcript.jsonl`.
    Runner,
}

/// One message that cites artifacts: the input `ws artifact scan record` reads.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Citation {
    pub key: String,
    pub at: Option<String>,
    pub refs: Vec<String>,
}

/// `ws artifact scan record`'s input (version 1).
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct ScanInput {
    pub version: u32,
    pub workspace: String,
    pub transcript: String,
    pub offset: u64,
    pub citations: Vec<Citation>,
}

/// What one step of reading a transcript found.
#[derive(Debug, Default, PartialEq)]
pub struct Step {
    pub citations: Vec<Citation>,
    /// The offset to read from next.
    pub next: u64,
    /// The file was shorter than the cursor, so it was read from the start.
    pub reset: bool,
}

/// The artifact references in text outside code, as `@artifact:<id>[/r<n>]`,
/// without repeats, in first-seen order.
pub fn refs_in(text: &str) -> Vec<String> {
    let plain = blank_code(text);
    let mut refs: Vec<String> = Vec::new();
    let mut rest = plain.as_str();
    while let Some(at) = rest.find("@artifact:") {
        rest = &rest[at + MARKER.len()..];
        if let Some(found) = reference_at(rest) {
            if !refs.contains(&found) {
                refs.push(found);
            }
        }
    }
    refs
}

/// The reference starting at text, just past `@artifact:`, as the regex
/// `art-[0-9a-f]{8}(?:/r[1-9]\d{0,2})?` in `internal/artifact` matches it.
fn reference_at(text: &str) -> Option<String> {
    let id = text.get(..4 + ID_HEX)?;
    let hex = id.strip_prefix("art-")?;
    if !hex
        .bytes()
        .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
    {
        return None;
    }
    let rev = revision_after(&text[id.len()..]);
    Some(match rev {
        Some(digits) => format!("@artifact:{id}/r{digits}"),
        None => format!("@artifact:{id}"),
    })
}

/// The digits of a `/r<n>` at the start of text: a non-zero digit and up to
/// two more.
fn revision_after(text: &str) -> Option<&str> {
    let digits = text.strip_prefix("/r")?;
    let bytes = digits.as_bytes();
    if !bytes.first().is_some_and(|b| (b'1'..=b'9').contains(b)) {
        return None;
    }
    let len = bytes
        .iter()
        .take(3)
        .take_while(|b| b.is_ascii_digit())
        .count();
    Some(&digits[..len])
}

/// text with fenced code blocks and inline code spans replaced by spaces, so
/// a reference in code isn't a citation (the conversation view doesn't link
/// it either).
pub fn blank_code(text: &str) -> String {
    let fenced = blank_fences(text);
    blank_spans(&fenced)
}

/// Blanks every line of a fenced block, its fences included: a line that
/// opens with three or more backticks or tildes (after up to three spaces),
/// up to a line of at least as many of the same character, or the end.
fn blank_fences(text: &str) -> String {
    let mut out = String::with_capacity(text.len());
    let mut open: Option<(u8, usize)> = None;
    for line in text.split_inclusive('\n') {
        let fence = fence_of(line);
        let inside = match (open, fence) {
            (None, Some(f)) => {
                open = Some(f);
                true
            }
            (Some((c, n)), Some((fc, fn_))) if fc == c && fn_ >= n && closes(line) => {
                open = None;
                true
            }
            (Some(_), _) => true,
            (None, None) => false,
        };
        out.push_str(&if inside {
            spaces(line)
        } else {
            line.to_string()
        });
    }
    out
}

/// The fence character and length a line opens with, if any.
fn fence_of(line: &str) -> Option<(u8, usize)> {
    let indent = line.bytes().take_while(|&b| b == b' ').count();
    if indent > 3 {
        return None;
    }
    let rest = &line.as_bytes()[indent..];
    let c = *rest.first()?;
    if c != b'`' && c != b'~' {
        return None;
    }
    let n = rest.iter().take_while(|&&b| b == c).count();
    (n >= 3).then_some((c, n))
}

/// Whether a fence line has nothing after its fence, so it can close a block.
fn closes(line: &str) -> bool {
    line.trim_start_matches(' ')
        .trim_start_matches(['`', '~'])
        .trim()
        .is_empty()
}

/// Blanks inline code spans: a run of N backticks up to the next run of
/// exactly N. A run with no match stays as it is.
fn blank_spans(text: &str) -> String {
    let bytes = text.as_bytes();
    let mut out = bytes.to_vec();
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] != b'`' {
            i += 1;
            continue;
        }
        let n = run_len(bytes, i);
        match closing_run(bytes, i + n, n) {
            Some(end) => {
                out[i..end].iter_mut().for_each(|b| *b = b' ');
                i = end;
            }
            None => i += n,
        }
    }
    String::from_utf8(out).unwrap_or_default()
}

fn run_len(bytes: &[u8], at: usize) -> usize {
    bytes[at..].iter().take_while(|&&b| b == b'`').count()
}

/// The end of the first run of exactly n backticks at or after from.
fn closing_run(bytes: &[u8], from: usize, n: usize) -> Option<usize> {
    let mut j = from;
    while j < bytes.len() {
        if bytes[j] != b'`' {
            j += 1;
            continue;
        }
        let m = run_len(bytes, j);
        if m == n {
            return Some(j + m);
        }
        j += m;
    }
    None
}

/// Every byte of line as a space, its newline kept.
fn spaces(line: &str) -> String {
    line.chars()
        .map(|c| if c == '\n' { '\n' } else { ' ' })
        .collect()
}

/// The citations one transcript line holds: user messages without the hint
/// line, and agent replies. Thinking, tools and notices aren't citations.
pub fn citations_of(format: Format, line_at: u64, entry: &Value) -> Option<Citation> {
    let items = match format {
        Format::Claude => transcript::items_of(entry),
        Format::Runner => transcript::runner_event_items(entry),
    };
    let mut refs: Vec<String> = Vec::new();
    let mut at = None;
    for item in &items {
        let (text, when) = match item {
            Item::User { text, at, .. } if !text.contains(HINT_MARKER) => (text, at),
            Item::Assistant { text, at, .. } => (text, at),
            _ => continue,
        };
        for r in refs_in(text) {
            if !refs.contains(&r) {
                refs.push(r);
            }
        }
        at = at.or_else(|| when.clone());
    }
    let key = message_key(format, line_at, entry)?;
    (!refs.is_empty()).then_some(Citation { key, at, refs })
}

/// The message's key: Claude Code's `uuid`, or the line's offset for a
/// runner. None when it can't be one `ws` accepts.
fn message_key(format: Format, line_at: u64, entry: &Value) -> Option<String> {
    let key = match format {
        Format::Claude => entry.get("uuid").and_then(Value::as_str)?.to_string(),
        Format::Runner => format!("r{line_at}"),
    };
    (!key.is_empty() && key.len() <= MAX_KEY_BYTES).then_some(key)
}

/// Reads up to STEP_BYTES of whole lines of path from offset, or from 0 when
/// the file is shorter than offset, and returns their citations. A missing
/// file is None. A half-written last line is left for the next step; a line
/// longer than a step is skipped. At most MAX_CITATIONS are returned, and
/// `next` stops after the line that held the last of them.
pub fn scan_step(path: &Path, offset: u64, format: Format) -> std::io::Result<Option<Step>> {
    let mut file = match std::fs::File::open(path) {
        Ok(f) => f,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(None),
        Err(e) => return Err(e),
    };
    let len = file.metadata()?.len();
    let reset = len < offset;
    let start = if reset { 0 } else { offset };
    file.seek(SeekFrom::Start(start))?;
    let mut bytes = Vec::new();
    file.take(STEP_BYTES).read_to_end(&mut bytes)?;
    let complete = bytes.iter().rposition(|&b| b == b'\n').map_or(0, |i| i + 1);
    if complete == 0 {
        let skip = if bytes.len() as u64 == STEP_BYTES {
            STEP_BYTES
        } else {
            0
        };
        return Ok(Some(Step {
            citations: Vec::new(),
            next: start + skip,
            reset,
        }));
    }
    let (citations, used) = citations_in(&bytes[..complete], start, format);
    Ok(Some(Step {
        citations,
        next: start + used as u64,
        reset,
    }))
}

/// The citations in whole lines, and how many bytes were used: all of them,
/// unless MAX_CITATIONS was reached first.
fn citations_in(bytes: &[u8], start: u64, format: Format) -> (Vec<Citation>, usize) {
    let mut citations: Vec<Citation> = Vec::new();
    let mut used = 0;
    for line in bytes.split_inclusive(|&b| b == b'\n') {
        let line_at = start + used as u64;
        used += line.len();
        if !contains(line, MARKER) {
            continue;
        }
        let Ok(entry) = serde_json::from_slice::<Value>(line) else {
            continue;
        };
        if let Some(c) = citations_of(format, line_at, &entry) {
            citations.push(c);
        }
        if citations.len() == MAX_CITATIONS {
            break;
        }
    }
    (citations, used)
}

fn contains(haystack: &[u8], needle: &[u8]) -> bool {
    haystack.windows(needle.len()).any(|w| w == needle)
}

/// A workspace's place in its transcript.
#[derive(Debug, Clone, PartialEq)]
pub struct Cursor {
    pub transcript: PathBuf,
    /// Where the next read starts.
    pub offset: u64,
    /// The offset `ws` has stored.
    pub stored: u64,
}

/// Whether a step should be handed to `ws`: it found citations, moved the
/// cursor PERSIST_AFTER past the stored one, or the transcript changed.
pub fn should_record(cursor: Option<&Cursor>, path: &Path, step: &Step) -> bool {
    let Some(c) = cursor else {
        return true;
    };
    !step.citations.is_empty()
        || c.transcript != path
        || step.reset
        || step.next.saturating_sub(c.stored) >= PERSIST_AFTER
}

/// Where a workspace's scan starts: its cursor when the transcript is the
/// same, else 0.
pub fn start_of(cursor: Option<&Cursor>, path: &Path) -> u64 {
    match cursor {
        Some(c) if c.transcript == path => c.offset,
        _ => 0,
    }
}

/// A local agent's workspace and the transcript to scan.
#[derive(Debug, Clone, PartialEq)]
pub struct Target {
    pub workspace: String,
    pub path: PathBuf,
    pub format: Format,
}

/// The local agents in a `ws list` and their transcripts: a runner's under
/// state_dir, a Claude agent's from its job state.
pub fn targets(list: &WsList, state_dir: &Path, jobs_dir: Option<&Path>) -> Vec<Target> {
    list.workspaces
        .iter()
        .filter(|w| w.environment.as_deref().is_none_or(|e| e == "local"))
        .filter_map(|w| {
            let id = w.id.as_deref().filter(|id| valid_id(id))?;
            let session = w.session.as_ref().filter(|s| s.kind != "shell")?;
            if session.runner.as_deref() == Some("ws") {
                let path = state_dir.join(id).join("runner").join("transcript.jsonl");
                return Some(Target {
                    workspace: id.to_string(),
                    path,
                    format: Format::Runner,
                });
            }
            let jobs = jobs_dir?;
            let job = crate::jobs::read_job(jobs, session.id.as_deref()?)?;
            let path = transcript::transcript_path(jobs.parent()?, &job)?;
            Some(Target {
                workspace: id.to_string(),
                path,
                format: Format::Claude,
            })
        })
        .collect()
}

/// A workspace id `ws` would accept: one path element, not hidden.
fn valid_id(id: &str) -> bool {
    !id.is_empty()
        && id.len() <= 128
        && !id.starts_with('.')
        && !id.chars().any(|c| c == '/' || c == '\\' || c.is_control())
}

#[derive(Deserialize)]
struct Cursors {
    cursors: Vec<StoredCursor>,
}

#[derive(Deserialize)]
struct StoredCursor {
    workspace: String,
    transcript: String,
    offset: u64,
}

#[derive(Deserialize)]
struct Recorded {
    offset: u64,
}

/// The sweep's state: each workspace's cursor, loaded once from `ws`.
#[derive(Default)]
pub struct Sweeper {
    cursors: HashMap<String, Cursor>,
    loaded: bool,
}

impl Sweeper {
    async fn load(&mut self, runner: &Runner) {
        let args = strings(&["artifact", "scan", "cursors", "--json"]);
        let Ok(stored) = runner.ws_json_as_given::<Cursors>(&args).await else {
            return;
        };
        for c in stored.cursors {
            let cursor = Cursor {
                transcript: PathBuf::from(c.transcript),
                offset: c.offset,
                stored: c.offset,
            };
            self.cursors.insert(c.workspace, cursor);
        }
        self.loaded = true;
    }

    /// One pass over every local agent. Failures leave the cursor where it
    /// was, so the next pass tries again; `ws` dedupes what it sees twice.
    pub async fn sweep(&mut self, runner: &Runner, state_dir: &Path, jobs_dir: Option<&Path>) {
        if !self.loaded {
            self.load(runner).await;
            if !self.loaded {
                return;
            }
        }
        let Ok(list) = runner.ws_json::<WsList>(&strings(&["list", "--all"])).await else {
            return;
        };
        let found = targets(&list, state_dir, jobs_dir);
        self.cursors
            .retain(|id, _| found.iter().any(|t| &t.workspace == id));
        for target in found {
            self.scan(runner, target).await;
        }
    }

    async fn scan(&mut self, runner: &Runner, target: Target) {
        let cursor = self.cursors.get(&target.workspace).cloned();
        let start = start_of(cursor.as_ref(), &target.path);
        let path = target.path.clone();
        let format = target.format;
        let read = tokio::task::spawn_blocking(move || scan_step(&path, start, format)).await;
        let Ok(Ok(Some(step))) = read else {
            return;
        };
        if step.next == start && !step.reset && cursor.is_some() {
            return;
        }
        if !should_record(cursor.as_ref(), &target.path, &step) {
            let stored = cursor.map_or(0, |c| c.stored);
            self.cursors.insert(
                target.workspace,
                Cursor {
                    transcript: target.path,
                    offset: step.next,
                    stored,
                },
            );
            return;
        }
        self.record(runner, target, step).await;
    }

    async fn record(&mut self, runner: &Runner, target: Target, step: Step) {
        let input = ScanInput {
            version: 1,
            workspace: target.workspace.clone(),
            transcript: target.path.to_string_lossy().into_owned(),
            offset: step.next,
            citations: step.citations,
        };
        let Ok(bytes) = serde_json::to_vec(&input) else {
            return;
        };
        let args = strings(&["artifact", "scan", "record", "-", "--json"]);
        match runner
            .ws_json_with_input::<Recorded>(&args, Some(&bytes))
            .await
        {
            Ok(done) => {
                let offset = done.offset.max(step.next);
                self.cursors.insert(
                    target.workspace,
                    Cursor {
                        transcript: target.path,
                        offset,
                        stored: offset,
                    },
                );
            }
            Err(err) if err.code == "workspace_not_found" => {
                self.cursors.remove(&target.workspace);
            }
            Err(_) => {}
        }
    }
}

/// Runs the sweep every SWEEP_EVERY while the app runs.
pub async fn sweep_forever(runner: Runner, jobs_dir: Option<PathBuf>) {
    let Some(state_dir) = crate::commands::ws_state_dir(|name| std::env::var(name).ok()) else {
        return;
    };
    let mut sweeper = Sweeper::default();
    loop {
        sweeper
            .sweep(&runner, &state_dir, jobs_dir.as_deref())
            .await;
        tokio::time::sleep(SWEEP_EVERY).await;
    }
}

fn strings(args: &[&str]) -> Vec<String> {
    args.iter().map(|s| s.to_string()).collect()
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    const ID: &str = "art-1a2b3c4d";

    fn claude_line(uuid: &str, kind: &str, text: &str) -> String {
        let content = if kind == "assistant" {
            serde_json::json!([{"type": "text", "text": text}])
        } else {
            serde_json::json!(text)
        };
        let line = serde_json::json!({"type": kind, "uuid": uuid, "timestamp": "2026-11-26T18:00:00Z", "message": {"content": content}});
        format!("{line}\n")
    }

    fn write(dir: &Path, lines: &[String]) -> PathBuf {
        let path = dir.join("t.jsonl");
        std::fs::write(&path, lines.concat()).unwrap();
        path
    }

    #[test]
    fn refs_in_keeps_revisions_and_drops_repeats() {
        let text = format!("see @artifact:{ID}/r2 and @artifact:{ID} and @artifact:{ID}/r2");
        assert_eq!(
            refs_in(&text),
            [format!("@artifact:{ID}/r2"), format!("@artifact:{ID}")]
        );
    }

    #[test]
    fn refs_in_matches_the_go_regex_at_the_edges() {
        assert_eq!(
            refs_in(&format!("@artifact:{ID}/r1234")),
            [format!("@artifact:{ID}/r123")]
        );
        assert_eq!(
            refs_in(&format!("@artifact:{ID}/r0")),
            [format!("@artifact:{ID}")]
        );
        assert!(refs_in("@artifact:art-1A2B3C4D @artifact:art-1a2b").is_empty());
    }

    #[test]
    fn refs_in_inline_code_are_not_found() {
        let text = format!(
            "`@artifact:{ID}` and ``x @artifact:art-5e6f7a8b ` y`` but @artifact:art-0badf00d"
        );
        assert_eq!(refs_in(&text), ["@artifact:art-0badf00d"]);
    }

    #[test]
    fn refs_in_fences_are_not_found() {
        let text = format!("```sh\nws machine artifact get @artifact:{ID}\n```\nthen ~~~~\n~~~~\n@artifact:art-5e6f7a8b\n~~~~~\nafter @artifact:art-0badf00d");
        assert_eq!(refs_in(&text), ["@artifact:art-0badf00d"]);
    }

    #[test]
    fn an_unclosed_fence_runs_to_the_end() {
        assert!(refs_in(&format!("```\n@artifact:{ID}")).is_empty());
    }

    #[test]
    fn an_unmatched_backtick_is_text() {
        assert_eq!(
            refs_in(&format!("a ` b @artifact:{ID}")),
            [format!("@artifact:{ID}")]
        );
    }

    #[test]
    fn user_messages_with_the_hint_line_are_skipped() {
        let hinted = format!("fix @artifact:{ID}\n\n(Artifacts {ID}: run \"ws machine artifact get <id>\" for each, then read the file at the \"path\" it prints.)");
        let entry: Value = serde_json::from_str(&claude_line("u1", "user", &hinted)).unwrap();
        assert_eq!(citations_of(Format::Claude, 0, &entry), None);
        let typed: Value = serde_json::from_str(&claude_line(
            "u2",
            "user",
            &format!("look at @artifact:{ID}"),
        ))
        .unwrap();
        assert_eq!(citations_of(Format::Claude, 0, &typed).unwrap().key, "u2");
    }

    #[test]
    fn thinking_and_tools_are_not_citations() {
        let thinking = serde_json::json!({"type": "assistant", "uuid": "a1", "message": {"content": [
            {"type": "thinking", "thinking": format!("@artifact:{ID}")},
            {"type": "tool_use", "id": "t1", "name": "Bash", "input": {"command": format!("echo @artifact:{ID}")}}]}});
        assert_eq!(citations_of(Format::Claude, 0, &thinking), None);
    }

    #[test]
    fn one_message_counts_once() {
        let entry = serde_json::json!({"type": "assistant", "uuid": "a1", "message": {"content": [
            {"type": "text", "text": format!("@artifact:{ID}")},
            {"type": "text", "text": format!("again @artifact:{ID}")}]}});
        let c = citations_of(Format::Claude, 0, &entry).unwrap();
        assert_eq!(c.refs, [format!("@artifact:{ID}")]);
        assert_eq!(c.at, None);
    }

    #[test]
    fn scan_step_reads_whole_lines_and_waits_for_a_partial_one() {
        let dir = tempfile::tempdir().unwrap();
        let first = claude_line("a1", "assistant", &format!("see @artifact:{ID}"));
        let path = write(dir.path(), std::slice::from_ref(&first));
        let mut f = std::fs::OpenOptions::new()
            .append(true)
            .open(&path)
            .unwrap();
        f.write_all(b"{\"type\":\"assistant\",\"uuid\":\"a2\",\"message\":{\"content\":\"@artifact:art-5e6f").unwrap();
        let step = scan_step(&path, 0, Format::Claude).unwrap().unwrap();
        assert_eq!(step.citations.len(), 1);
        assert_eq!(
            step.citations[0].at.as_deref(),
            Some("2026-11-26T18:00:00Z")
        );
        assert_eq!(step.next, first.len() as u64);
        assert!(!step.reset);
    }

    #[test]
    fn scan_step_from_the_end_finds_nothing_and_stays() {
        let dir = tempfile::tempdir().unwrap();
        let line = claude_line("a1", "assistant", &format!("@artifact:{ID}"));
        let path = write(dir.path(), std::slice::from_ref(&line));
        let step = scan_step(&path, line.len() as u64, Format::Claude)
            .unwrap()
            .unwrap();
        assert_eq!(
            step,
            Step {
                citations: Vec::new(),
                next: line.len() as u64,
                reset: false
            }
        );
    }

    #[test]
    fn a_shorter_file_is_read_from_the_start() {
        let dir = tempfile::tempdir().unwrap();
        let path = write(
            dir.path(),
            &[claude_line("a1", "assistant", &format!("@artifact:{ID}"))],
        );
        let step = scan_step(&path, 1_000_000, Format::Claude)
            .unwrap()
            .unwrap();
        assert!(step.reset);
        assert_eq!(step.citations.len(), 1);
    }

    #[test]
    fn a_missing_file_is_none() {
        let dir = tempfile::tempdir().unwrap();
        assert_eq!(
            scan_step(&dir.path().join("none.jsonl"), 0, Format::Claude).unwrap(),
            None
        );
    }

    #[test]
    fn steps_end_on_a_line_boundary_so_a_citation_never_spans_two() {
        let dir = tempfile::tempdir().unwrap();
        let filler = claude_line("f", "assistant", &"x".repeat(1024 * 1024));
        let cited = claude_line("c1", "assistant", &format!("@artifact:{ID}"));
        let mut lines = vec![filler.clone(), filler.clone(), filler.clone()];
        lines.push(cited.clone());
        lines.push(filler.clone());
        let path = write(dir.path(), &lines);
        let first = scan_step(&path, 0, Format::Claude).unwrap().unwrap();
        assert_eq!(
            first.next,
            (3 * filler.len() + cited.len()) as u64,
            "the step stops after the last whole line"
        );
        let mut found = first.citations.len();
        let mut at = first.next;
        while at < std::fs::metadata(&path).unwrap().len() {
            let step = scan_step(&path, at, Format::Claude).unwrap().unwrap();
            found += step.citations.len();
            assert!(step.next > at);
            at = step.next;
        }
        assert_eq!(found, 1);
    }

    #[test]
    fn a_line_longer_than_a_step_is_skipped_whole() {
        let dir = tempfile::tempdir().unwrap();
        let long = claude_line(
            "big",
            "assistant",
            &format!("@artifact:{ID} {}", "y".repeat(STEP_BYTES as usize)),
        );
        let after = claude_line("c1", "assistant", &format!("@artifact:{ID}"));
        let path = write(dir.path(), &[long, after]);
        let first = scan_step(&path, 0, Format::Claude).unwrap().unwrap();
        assert!(first.citations.is_empty());
        assert_eq!(first.next, STEP_BYTES);
        let second = scan_step(&path, first.next, Format::Claude)
            .unwrap()
            .unwrap();
        assert_eq!(second.citations.len(), 1);
        assert_eq!(second.citations[0].key, "c1");
    }

    #[test]
    fn at_most_max_citations_per_step() {
        let dir = tempfile::tempdir().unwrap();
        let lines: Vec<String> = (0..MAX_CITATIONS + 5)
            .map(|i| claude_line(&format!("m{i}"), "assistant", &format!("@artifact:{ID}")))
            .collect();
        let path = write(dir.path(), &lines);
        let step = scan_step(&path, 0, Format::Claude).unwrap().unwrap();
        assert_eq!(step.citations.len(), MAX_CITATIONS);
        assert_eq!(step.next, lines[..MAX_CITATIONS].concat().len() as u64);
    }

    #[test]
    fn runner_lines_are_keyed_by_offset() {
        let event = serde_json::json!({"kind": "response", "time": "2026-11-26T18:00:00Z", "reasoning": "@artifact:art-5e6f7a8b", "content": format!("done, see @artifact:{ID}")});
        let c = citations_of(Format::Runner, 9120, &event).unwrap();
        assert_eq!(c.key, "r9120");
        assert_eq!(c.refs, [format!("@artifact:{ID}")]);
        assert_eq!(c.at.as_deref(), Some("2026-11-26T18:00:00Z"));
    }

    #[test]
    fn should_record_rules() {
        let path = PathBuf::from("/t");
        let cursor = Cursor {
            transcript: path.clone(),
            offset: 10,
            stored: 10,
        };
        let quiet = Step {
            citations: Vec::new(),
            next: 20,
            reset: false,
        };
        assert!(!should_record(Some(&cursor), &path, &quiet));
        let far = Step {
            next: 10 + PERSIST_AFTER,
            ..Step::default()
        };
        assert!(should_record(Some(&cursor), &path, &far));
        assert!(should_record(Some(&cursor), Path::new("/other"), &quiet));
        assert!(should_record(None, &path, &quiet));
        let cited = Step {
            citations: vec![Citation {
                key: "k".into(),
                at: None,
                refs: vec![],
            }],
            next: 11,
            reset: false,
        };
        assert!(should_record(Some(&cursor), &path, &cited));
    }

    #[test]
    fn a_new_transcript_starts_at_zero() {
        let cursor = Cursor {
            transcript: PathBuf::from("/a"),
            offset: 50,
            stored: 50,
        };
        assert_eq!(start_of(Some(&cursor), Path::new("/a")), 50);
        assert_eq!(start_of(Some(&cursor), Path::new("/b")), 0);
        assert_eq!(start_of(None, Path::new("/a")), 0);
    }

    #[test]
    fn scan_input_matches_the_go_fixture() {
        let fixture = Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("../../internal/cli/testdata/scan-record.json");
        let raw: Value = serde_json::from_slice(&std::fs::read(fixture).unwrap()).unwrap();
        let parsed: ScanInput = serde_json::from_value(raw.clone()).unwrap();
        assert_eq!(serde_json::to_value(&parsed).unwrap(), raw);
    }

    #[test]
    fn targets_are_local_agents_with_a_transcript() {
        let list: WsList = serde_json::from_value(serde_json::json!({"workspaces": [
            {"id": "run-1", "path": "/w/run-1", "created_at": null, "session": {"kind": "agent", "id": "s1", "state": "running", "stale": null, "runner": "ws"}},
            {"id": "far-1", "path": "/w/far-1", "created_at": null, "environment": "win-rig", "session": {"kind": "agent", "id": "s2", "state": "running", "stale": null, "runner": "ws"}},
            {"id": "sh-1", "path": "/w/sh-1", "created_at": null, "session": {"kind": "shell", "id": null, "state": "running", "stale": null}},
            {"id": "../x", "path": "/w/x", "created_at": null, "session": {"kind": "agent", "id": "s3", "state": "running", "stale": null, "runner": "ws"}}
        ]})).unwrap();
        let found = targets(&list, Path::new("/cfg/state"), None);
        assert_eq!(
            found,
            [Target {
                workspace: "run-1".into(),
                path: PathBuf::from("/cfg/state/run-1/runner/transcript.jsonl"),
                format: Format::Runner
            }]
        );
    }
}
