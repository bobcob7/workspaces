//! Keeps one parser per transcript and reads only what was appended since the
//! last refresh (ADR 0059, "Reading transcripts: incremental cache").

use super::ingest::Parser;
use serde::Serialize;
use std::collections::BTreeMap;
use std::fs::{self, File, Metadata};
use std::io::{self, Read, Seek, SeekFrom};
#[cfg(unix)]
use std::os::unix::fs::MetadataExt;
use std::path::{Path, PathBuf};
use std::time::{Instant, SystemTime};

/// A refresh within this long of the last one reuses what was read.
pub const MIN_REFRESH_MS: i64 = 5_000;

/// What one refresh scanned.
#[derive(Debug, Clone, Default, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct ScanInfo {
    /// Transcript files known after the refresh.
    pub files: u64,
    /// Files read, wholly or from an offset, by this refresh.
    pub files_read: u64,
    /// Bytes of complete lines read by this refresh.
    pub bytes_read: u64,
    /// Wall-clock time the refresh took.
    pub duration_ms: u64,
    /// True when the refresh was within MIN_REFRESH_MS of the last one.
    pub cached: bool,
}

/// What was read of one transcript file.
#[derive(Debug, Default)]
pub struct FileState {
    len: u64,
    modified: Option<SystemTime>,
    /// (dev, ino) on Unix; None elsewhere.
    id: Option<(u64, u64)>,
    /// The byte after the last complete line read.
    offset: u64,
    pub parser: Parser,
}

/// Every transcript's state, refreshed incrementally.
#[derive(Debug, Default)]
pub struct TranscriptCache {
    files: BTreeMap<PathBuf, FileState>,
    last_refresh_ms: Option<i64>,
}

impl TranscriptCache {
    /// Brings every transcript under config_dir/projects up to date, unless
    /// the last refresh was under MIN_REFRESH_MS before now_ms.
    pub fn refresh(&mut self, config_dir: &Path, now_ms: i64) -> ScanInfo {
        let started = Instant::now();
        if self.is_fresh(now_ms) {
            return ScanInfo {
                files: as_u64(self.files.len()),
                cached: true,
                duration_ms: elapsed_ms(started),
                ..ScanInfo::default()
            };
        }
        self.last_refresh_ms = Some(now_ms);
        let paths = transcript_files(config_dir);
        self.files
            .retain(|path, _| paths.binary_search(path).is_ok());
        let mut info = ScanInfo {
            files: as_u64(paths.len()),
            ..ScanInfo::default()
        };
        for path in &paths {
            self.refresh_file(path, &mut info);
        }
        info.duration_ms = elapsed_ms(started);
        info
    }

    /// The parsers, one per transcript file.
    pub fn parsers(&self) -> impl Iterator<Item = &Parser> {
        self.files.values().map(|state| &state.parser)
    }

    fn is_fresh(&self, now_ms: i64) -> bool {
        self.last_refresh_ms
            .is_some_and(|last| (0..MIN_REFRESH_MS).contains(&now_ms.saturating_sub(last)))
    }

    /// Feeds the complete lines appended since the last read. A read error
    /// leaves the file's state as it was.
    fn refresh_file(&mut self, path: &Path, info: &mut ScanInfo) {
        let Ok(metadata) = fs::metadata(path) else {
            return;
        };
        let (len, modified, id) = (metadata.len(), metadata.modified().ok(), file_id(&metadata));
        let (offset, restart) = match self.files.get(path) {
            Some(state) if (state.len, state.modified, state.id) == (len, modified, id) => return,
            Some(state) if len >= state.offset && id == state.id => (state.offset, false),
            _ => (0, true),
        };
        let Ok(bytes) = read_from(path, offset) else {
            return;
        };
        let complete = bytes
            .iter()
            .rposition(|byte| *byte == b'\n')
            .map_or(0, |last| last + 1);
        let state = self.files.entry(path.to_path_buf()).or_default();
        if restart {
            *state = FileState::default();
        }
        for line in bytes[..complete].split(|byte| *byte == b'\n') {
            state.parser.feed_line(line);
        }
        state.offset = offset + as_u64(complete);
        state.len = len;
        state.modified = modified;
        state.id = id;
        info.files_read += 1;
        info.bytes_read += as_u64(complete);
    }
}

/// projects/*/*.jsonl and projects/*/*/subagents/*.jsonl under config_dir,
/// sorted; an unreadable directory is skipped.
pub fn transcript_files(config_dir: &Path) -> Vec<PathBuf> {
    let mut files = Vec::new();
    for project in child_dirs(&config_dir.join("projects")) {
        files.extend(jsonl_files(&project));
        for session in child_dirs(&project) {
            files.extend(jsonl_files(&session.join("subagents")));
        }
    }
    files.sort();
    files
}

fn child_dirs(dir: &Path) -> Vec<PathBuf> {
    let Ok(entries) = fs::read_dir(dir) else {
        return Vec::new();
    };
    entries
        .filter_map(Result::ok)
        .map(|entry| entry.path())
        .filter(|path| path.is_dir())
        .collect()
}

fn jsonl_files(dir: &Path) -> Vec<PathBuf> {
    let Ok(entries) = fs::read_dir(dir) else {
        return Vec::new();
    };
    entries
        .filter_map(Result::ok)
        .map(|entry| entry.path())
        .filter(|path| path.is_file() && path.extension().is_some_and(|ext| ext == "jsonl"))
        .collect()
}

fn read_from(path: &Path, offset: u64) -> io::Result<Vec<u8>> {
    let mut file = File::open(path)?;
    file.seek(SeekFrom::Start(offset))?;
    let mut bytes = Vec::new();
    file.read_to_end(&mut bytes)?;
    Ok(bytes)
}

#[cfg(unix)]
fn file_id(metadata: &Metadata) -> Option<(u64, u64)> {
    Some((metadata.dev(), metadata.ino()))
}

#[cfg(not(unix))]
fn file_id(_metadata: &Metadata) -> Option<(u64, u64)> {
    None
}

fn as_u64(count: usize) -> u64 {
    u64::try_from(count).unwrap_or(u64::MAX)
}

fn elapsed_ms(started: Instant) -> u64 {
    u64::try_from(started.elapsed().as_millis()).unwrap_or(u64::MAX)
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    use std::io::Write;

    const LATER_MS: i64 = MIN_REFRESH_MS * 2;

    fn request_line(message_id: &str) -> String {
        json!({
            "type": "assistant",
            "timestamp": "2026-11-12T20:00:01.000Z",
            "requestId": format!("req_{message_id}"),
            "message": {
                "id": message_id,
                "model": "claude-haiku-5-5",
                "content": [{"type": "text", "text": "ok"}],
                "usage": {"input_tokens": 10, "output_tokens": 5}
            }
        })
        .to_string()
    }

    fn write_transcript(path: &Path, lines: &[String]) {
        if let Some(parent) = path.parent() {
            fs::create_dir_all(parent).expect("create transcript directory");
        }
        let mut text = lines.join("\n");
        text.push('\n');
        fs::write(path, text).expect("write transcript");
    }

    fn append(path: &Path, text: &str) {
        let mut file = fs::OpenOptions::new()
            .append(true)
            .open(path)
            .expect("open transcript for append");
        file.write_all(text.as_bytes())
            .expect("append to transcript");
    }

    fn requests(cache: &TranscriptCache) -> u64 {
        cache
            .parsers()
            .flat_map(|parser| parser.buckets.values())
            .map(|bucket| bucket.requests)
            .sum()
    }

    #[test]
    fn finds_top_level_and_subagent_transcripts() {
        let dir = tempfile::tempdir().expect("tempdir");
        let top = dir.path().join("projects/p1/a.jsonl");
        let sub = dir.path().join("projects/p1/s1/subagents/b.jsonl");
        write_transcript(&top, &[]);
        write_transcript(&sub, &[]);
        let mut expected = vec![top, sub];
        expected.sort();
        assert_eq!(transcript_files(dir.path()), expected);
    }

    #[test]
    fn ignores_files_outside_the_transcript_layout() {
        let dir = tempfile::tempdir().expect("tempdir");
        write_transcript(&dir.path().join("projects/p1/s1/other.jsonl"), &[]);
        write_transcript(&dir.path().join("projects/p1/notes.txt"), &[]);
        assert!(transcript_files(dir.path()).is_empty());
    }

    #[test]
    fn unchanged_transcripts_are_not_read_after_min_refresh() {
        let dir = tempfile::tempdir().expect("tempdir");
        write_transcript(
            &dir.path().join("projects/p1/a.jsonl"),
            &[request_line("msg_a")],
        );
        let mut cache = TranscriptCache::default();
        cache.refresh(dir.path(), 0);
        let info = cache.refresh(dir.path(), LATER_MS);
        assert_eq!(info.files_read, 0);
    }

    #[test]
    fn refresh_within_min_refresh_is_cached() {
        let dir = tempfile::tempdir().expect("tempdir");
        let mut cache = TranscriptCache::default();
        cache.refresh(dir.path(), 0);
        let info = cache.refresh(dir.path(), MIN_REFRESH_MS - 1);
        assert!(info.cached);
    }

    #[test]
    fn appended_line_reads_only_its_bytes() {
        let dir = tempfile::tempdir().expect("tempdir");
        let path = dir.path().join("projects/p1/a.jsonl");
        write_transcript(&path, &[request_line("msg_a")]);
        let mut cache = TranscriptCache::default();
        cache.refresh(dir.path(), 0);
        let second = request_line("msg_b");
        append(&path, &format!("{second}\n"));
        let info = cache.refresh(dir.path(), LATER_MS);
        assert_eq!(info.bytes_read, as_u64(second.len() + 1));
    }

    #[test]
    fn appended_line_adds_its_request() {
        let dir = tempfile::tempdir().expect("tempdir");
        let path = dir.path().join("projects/p1/a.jsonl");
        write_transcript(&path, &[request_line("msg_a")]);
        let mut cache = TranscriptCache::default();
        cache.refresh(dir.path(), 0);
        append(&path, &format!("{}\n", request_line("msg_b")));
        cache.refresh(dir.path(), LATER_MS);
        assert_eq!(requests(&cache), 2);
    }

    #[test]
    fn partial_last_line_is_not_counted_until_completed() {
        let dir = tempfile::tempdir().expect("tempdir");
        let path = dir.path().join("projects/p1/a.jsonl");
        let line = request_line("msg_b");
        write_transcript(&path, &[request_line("msg_a")]);
        append(&path, &line[..line.len() - 1]);
        let mut cache = TranscriptCache::default();
        cache.refresh(dir.path(), 0);
        let partial = cache.refresh(dir.path(), LATER_MS);
        assert_eq!((requests(&cache), partial.bytes_read), (1, 0));
    }

    #[test]
    fn completed_partial_line_is_counted_once() {
        let dir = tempfile::tempdir().expect("tempdir");
        let path = dir.path().join("projects/p1/a.jsonl");
        let line = request_line("msg_b");
        write_transcript(&path, &[request_line("msg_a")]);
        append(&path, &line[..line.len() - 1]);
        let mut cache = TranscriptCache::default();
        cache.refresh(dir.path(), 0);
        cache.refresh(dir.path(), LATER_MS);
        append(&path, &format!("{}\n", &line[line.len() - 1..]));
        cache.refresh(dir.path(), LATER_MS * 2);
        assert_eq!(requests(&cache), 2);
    }

    #[test]
    fn truncated_and_rewritten_file_is_read_from_its_start() {
        let dir = tempfile::tempdir().expect("tempdir");
        let path = dir.path().join("projects/p1/a.jsonl");
        write_transcript(&path, &[request_line("msg_a"), request_line("msg_b")]);
        let mut cache = TranscriptCache::default();
        cache.refresh(dir.path(), 0);
        write_transcript(&path, &[request_line("msg_c")]);
        cache.refresh(dir.path(), LATER_MS);
        assert_eq!(requests(&cache), 1);
    }

    #[test]
    fn deleted_file_disappears_from_parsers() {
        let dir = tempfile::tempdir().expect("tempdir");
        let kept = dir.path().join("projects/p1/a.jsonl");
        let removed = dir.path().join("projects/p1/b.jsonl");
        write_transcript(&kept, &[request_line("msg_a")]);
        write_transcript(&removed, &[request_line("msg_b")]);
        let mut cache = TranscriptCache::default();
        cache.refresh(dir.path(), 0);
        fs::remove_file(&removed).expect("remove transcript");
        cache.refresh(dir.path(), LATER_MS);
        assert_eq!(cache.parsers().count(), 1);
    }
}
