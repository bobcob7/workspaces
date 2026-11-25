//! Pull request links for a background session. Claude Code reports only
//! some PRs in `state.json`'s children, and drops one once it is merged or
//! closed, so the transcript is scanned for PR URLs of any forge and every PR
//! link seen is kept in `pr-links.json` beside `desktop.json`.

use crate::jobs::{JobChild, JobState};
use serde::{Deserialize, Serialize};
use std::collections::{BTreeMap, HashSet};
use std::io::{Read, Seek, SeekFrom};
use std::path::{Path, PathBuf};
use std::sync::Mutex;
use std::time::SystemTime;

pub const FILE_NAME: &str = "pr-links.json";
/// The transcript's tail that is scanned; older links are not looked for.
const SCAN_TAIL_BYTES: u64 = 4 * 1024 * 1024;
const PR_KIND: &str = "pr";
/// Most transcripts whose scan is cached; past it the cache starts over.
const CACHE_CAP: usize = 512;

/// A transcript's scan, valid while its length and mtime are unchanged.
type ScanCache = std::collections::HashMap<PathBuf, (u64, SystemTime, Vec<String>)>;

#[derive(Debug, Default, Serialize, Deserialize)]
struct StickyFile {
    #[serde(default)]
    sessions: BTreeMap<String, Vec<String>>,
}

/// The PR links seen per session. With no path they are kept in memory only.
#[derive(Debug, Default)]
pub struct PrLinks {
    path: Option<PathBuf>,
    data: Mutex<StickyFile>,
    pub(crate) scans: Mutex<ScanCache>,
    #[cfg(test)]
    pub(crate) scan_count: std::sync::atomic::AtomicUsize,
    #[cfg(test)]
    pub(crate) scan_delay_ms: std::sync::atomic::AtomicU64,
}

impl PrLinks {
    /// Loads the file at `path`; a missing or unreadable file starts empty.
    pub fn open(path: Option<PathBuf>) -> Self {
        let data = path.as_deref().and_then(load).unwrap_or_default();
        Self {
            path,
            data: Mutex::new(data),
            ..Self::default()
        }
    }

    /// Returns `job` with its PR links added: Claude Code's children first,
    /// then the transcript's, then those kept from earlier reads. A session
    /// without a state file gets an empty job, so its kept links still show.
    pub fn augment(&self, session: &str, job: JobState) -> JobState {
        let scanned = job
            .link_scan_path
            .as_deref()
            .map_or_else(Vec::new, |p| self.scan_cached(Path::new(p)));
        let reported = job
            .children
            .iter()
            .flatten()
            .filter_map(|c| c.href.as_deref().and_then(pr_href));
        let kept = self.remember(session, reported.chain(scanned.clone()).collect());

        let mut children = job.children.clone().unwrap_or_default();
        let mut seen: HashSet<String> = children
            .iter()
            .filter_map(|c| c.href.as_deref())
            .map(|href| pr_href(href).unwrap_or_else(|| href.to_string()))
            .collect();
        for href in scanned.into_iter().chain(kept) {
            if seen.insert(href.clone()) {
                children.push(pr_child(href));
            }
        }
        JobState {
            children: Some(children),
            ..job
        }
    }

    /// `scan_file`, reused while the transcript's length and mtime are
    /// unchanged. The locks are not held while scanning.
    fn scan_cached(&self, path: &Path) -> Vec<String> {
        let stamp = std::fs::metadata(path)
            .ok()
            .and_then(|m| m.modified().ok().map(|t| (m.len(), t)));
        let Some((len, mtime)) = stamp else {
            return self.scan_counted(path);
        };
        if let Ok(cache) = self.scans.lock() {
            if let Some((l, t, links)) = cache.get(path) {
                if *l == len && *t == mtime {
                    return links.clone();
                }
            }
        }
        let links = self.scan_counted(path);
        if let Ok(mut cache) = self.scans.lock() {
            if cache.len() >= CACHE_CAP && !cache.contains_key(path) {
                cache.clear();
            }
            cache.insert(path.to_path_buf(), (len, mtime, links.clone()));
        }
        links
    }

    fn scan_counted(&self, path: &Path) -> Vec<String> {
        #[cfg(test)]
        {
            use std::sync::atomic::Ordering;
            self.scan_count.fetch_add(1, Ordering::SeqCst);
            let delay = self.scan_delay_ms.load(Ordering::SeqCst);
            if delay > 0 {
                std::thread::sleep(std::time::Duration::from_millis(delay));
            }
        }
        scan_file(path)
    }

    /// Adds `seen` to the links kept for `session`, saves when that changes
    /// the file, and returns every link kept for the session.
    fn remember(&self, session: &str, seen: Vec<String>) -> Vec<String> {
        let Ok(mut data) = self.data.lock() else {
            return seen;
        };
        let known = data.sessions.get(session).cloned().unwrap_or_default();
        let mut kept = known.clone();
        for href in seen {
            if !kept.contains(&href) {
                kept.push(href);
            }
        }
        if kept == known {
            return kept;
        }
        data.sessions.insert(session.to_string(), kept.clone());
        // A failed save only loses these links after a restart, so it is not an error.
        if let Some(path) = &self.path {
            write(path, &data).ok();
        }
        kept
    }
}

fn pr_child(href: String) -> JobChild {
    JobChild {
        href: Some(href),
        kind: Some(PR_KIND.to_string()),
    }
}

/// The PR links in a transcript's tail, normalised, in order of appearance.
/// An unreadable file has none.
pub fn scan_file(path: &Path) -> Vec<String> {
    read_tail(path).map_or_else(|_| Vec::new(), |text| pr_hrefs_in(&text))
}

fn read_tail(path: &Path) -> std::io::Result<String> {
    let mut file = std::fs::File::open(path)?;
    let len = file.metadata()?.len();
    let truncated = len > SCAN_TAIL_BYTES;
    file.seek(SeekFrom::Start(len.saturating_sub(SCAN_TAIL_BYTES)))?;
    let mut bytes = Vec::new();
    file.read_to_end(&mut bytes)?;
    if truncated {
        // The cut can fall mid-line, which would leave a partial URL.
        if let Some(newline) = bytes.iter().position(|&b| b == b'\n') {
            bytes.drain(..=newline);
        }
    }
    Ok(String::from_utf8_lossy(&bytes).into_owned())
}

/// Every distinct PR link in `text`, normalised, in order of appearance.
pub fn pr_hrefs_in(text: &str) -> Vec<String> {
    let mut hrefs: Vec<String> = Vec::new();
    let mut seen: HashSet<String> = HashSet::new();
    let mut rest = text;
    while let Some(start) = next_scheme(rest) {
        let candidate = &rest[start..];
        let end = candidate
            .find(|c: char| c.is_whitespace() || matches!(c, '"' | '\'' | '<' | '>' | '\\' | '`'))
            .unwrap_or(candidate.len());
        if let Some(href) = pr_href(&candidate[..end]) {
            if seen.insert(href.clone()) {
                hrefs.push(href);
            }
        }
        rest = &candidate[end..];
    }
    hrefs
}

/// The byte offset of the nearest `http://` or `https://` in `text`. Scans
/// once and stops at the first match, so a caller that moves past each match
/// reads every byte at most once.
fn next_scheme(text: &str) -> Option<usize> {
    let bytes = text.as_bytes();
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] == b'h'
            && (bytes[i..].starts_with(b"http://") || bytes[i..].starts_with(b"https://"))
        {
            return Some(i);
        }
        i += 1;
    }
    None
}

/// The canonical PR URL `raw` names, or None when it isn't one. A GitHub
/// `/pull/N` and a Forgejo or Gitea `/pulls/N` both count. Query, fragment,
/// trailing punctuation, and subpaths such as `/files` are dropped.
pub fn pr_href(raw: &str) -> Option<String> {
    let before_fragment = raw.split_once('#').map_or(raw, |(url, _)| url);
    let before_query = before_fragment
        .split_once('?')
        .map_or(before_fragment, |(url, _)| url);
    let url = before_query.trim_end_matches(|c: char| {
        matches!(
            c,
            '.' | ',' | ';' | ':' | '!' | ')' | ']' | '}' | '\'' | '"'
        )
    });
    let (scheme, rest) = url.split_once("://")?;
    if scheme != "https" && scheme != "http" {
        return None;
    }
    let mut parts = rest.split('/');
    let host = parts.next().filter(|h| !h.is_empty())?;
    let owner = parts.next().filter(|o| !o.is_empty())?;
    let repo = parts.next().filter(|r| !r.is_empty())?;
    let marker = parts.next()?;
    let number = parts.next()?;
    if !matches!(marker, "pull" | "pulls") {
        return None;
    }
    if number.is_empty() || !number.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    Some(format!(
        "{scheme}://{host}/{owner}/{repo}/{marker}/{number}"
    ))
}

fn load(path: &Path) -> Option<StickyFile> {
    let bytes = std::fs::read(path).ok()?;
    serde_json::from_slice(&bytes).ok()
}

/// Writes a temp file beside `path` and renames it into place, so a crash
/// leaves the old file or the new one.
fn write(path: &Path, data: &StickyFile) -> std::io::Result<()> {
    if let Some(dir) = path.parent() {
        std::fs::create_dir_all(dir)?;
    }
    let tmp = path.with_extension("json.tmp");
    let bytes = serde_json::to_vec_pretty(data).map_err(std::io::Error::other)?;
    std::fs::write(&tmp, bytes)?;
    std::fs::rename(&tmp, path)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn child(href: &str, kind: &str) -> JobChild {
        JobChild {
            href: Some(href.to_string()),
            kind: Some(kind.to_string()),
        }
    }

    fn job_with(children: Vec<JobChild>) -> JobState {
        JobState {
            children: Some(children),
            ..JobState::default()
        }
    }

    fn hrefs(job: &JobState) -> Vec<String> {
        job.children
            .iter()
            .flatten()
            .filter_map(|c| c.href.clone())
            .collect()
    }

    #[test]
    fn scan_finds_github_and_forgejo_pull_requests() {
        let text = r#"{"text":"opened https://github.com/o/r/pull/4 and"}
{"text":"see https://git.example.com/o/r/pulls/7."}"#;
        assert_eq!(
            pr_hrefs_in(text),
            [
                "https://github.com/o/r/pull/4",
                "https://git.example.com/o/r/pulls/7",
            ]
        );
    }

    #[test]
    fn pr_href_strips_trailing_punctuation_query_and_subpaths() {
        assert_eq!(
            pr_href("https://github.com/o/r/pull/4)."),
            Some("https://github.com/o/r/pull/4".to_string())
        );
        assert_eq!(
            pr_href("https://github.com/o/r/pull/4/files#diff-1"),
            Some("https://github.com/o/r/pull/4".to_string())
        );
        assert_eq!(
            pr_href("https://git.example.com/o/r/pulls/7?tab=checks"),
            Some("https://git.example.com/o/r/pulls/7".to_string())
        );
    }

    #[test]
    fn pr_href_rejects_other_links() {
        assert_eq!(pr_href("https://github.com/o/r/issues/4"), None);
        assert_eq!(pr_href("https://github.com/o/r/pull/abc"), None);
        assert_eq!(pr_href("ftp://github.com/o/r/pull/4"), None);
        assert_eq!(pr_href("https://github.com/o/r/pull/"), None);
    }

    #[test]
    fn scan_file_reads_transcript_and_missing_file_is_empty() {
        let dir = tempfile::tempdir().unwrap();
        let transcript = dir.path().join("session.jsonl");
        std::fs::write(&transcript, "{\"t\":\"https://github.com/o/r/pull/4\"}\n").unwrap();
        assert_eq!(
            scan_file(&transcript),
            ["https://github.com/o/r/pull/4".to_string()]
        );
        assert!(scan_file(&dir.path().join("missing.jsonl")).is_empty());
    }

    #[test]
    fn link_absent_from_later_read_is_still_returned() {
        let dir = tempfile::tempdir().unwrap();
        let links = PrLinks::open(Some(dir.path().join(FILE_NAME)));
        let first = links.augment(
            "s1",
            job_with(vec![child("https://github.com/o/r/pull/4", "pr")]),
        );
        assert_eq!(hrefs(&first), ["https://github.com/o/r/pull/4"]);

        let later = links.augment("s1", JobState::default());
        assert_eq!(hrefs(&later), ["https://github.com/o/r/pull/4"]);
    }

    #[test]
    fn kept_links_survive_a_restart() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join(FILE_NAME);
        PrLinks::open(Some(path.clone())).augment(
            "s1",
            job_with(vec![child("https://github.com/o/r/pull/4", "pr")]),
        );
        let reopened = PrLinks::open(Some(path));
        let later = reopened.augment("s1", JobState::default());
        assert_eq!(hrefs(&later), ["https://github.com/o/r/pull/4"]);
    }

    #[test]
    fn augment_orders_claude_then_scanned_then_kept_without_duplicates() {
        let dir = tempfile::tempdir().unwrap();
        let transcript = dir.path().join("session.jsonl");
        std::fs::write(
            &transcript,
            "https://git.example.com/o/r/pulls/7 https://github.com/o/r/pull/4\n",
        )
        .unwrap();
        let links = PrLinks::open(None);
        let job = JobState {
            link_scan_path: Some(transcript.to_string_lossy().into_owned()),
            children: Some(vec![
                child("https://github.com/o/r/pull/4", "pr"),
                child("https://github.com/o/r/issues/9", "issue"),
            ]),
            ..JobState::default()
        };
        let out = links.augment("s1", job);
        assert_eq!(
            hrefs(&out),
            [
                "https://github.com/o/r/pull/4",
                "https://github.com/o/r/issues/9",
                "https://git.example.com/o/r/pulls/7",
            ]
        );
    }

    #[test]
    fn only_pr_links_are_persisted() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join(FILE_NAME);
        PrLinks::open(Some(path.clone())).augment(
            "s1",
            job_with(vec![child("https://github.com/o/r/issues/9", "issue")]),
        );
        assert!(!path.exists(), "a file with no PR links is not written");
    }

    #[test]
    fn corrupt_or_missing_state_file_does_not_panic() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join(FILE_NAME);
        std::fs::write(&path, "{not json").unwrap();
        let links = PrLinks::open(Some(path));
        let out = links.augment(
            "s1",
            job_with(vec![child("https://github.com/o/r/pull/4", "pr")]),
        );
        assert_eq!(hrefs(&out), ["https://github.com/o/r/pull/4"]);

        let missing = PrLinks::open(Some(dir.path().join("absent/pr-links.json")));
        assert!(hrefs(&missing.augment("s1", JobState::default())).is_empty());
    }

    fn job_scanning(path: &Path) -> JobState {
        JobState {
            link_scan_path: Some(path.to_string_lossy().into_owned()),
            ..JobState::default()
        }
    }

    #[test]
    fn unchanged_transcript_is_scanned_once_and_rescanned_after_it_grows() {
        use std::sync::atomic::Ordering;
        let dir = tempfile::tempdir().unwrap();
        let transcript = dir.path().join("session.jsonl");
        std::fs::write(&transcript, "https://github.com/o/r/pull/4\n").unwrap();
        let links = PrLinks::open(None);
        let first = links.augment("s1", job_scanning(&transcript));
        let second = links.augment("s1", job_scanning(&transcript));
        assert_eq!(hrefs(&first), ["https://github.com/o/r/pull/4"]);
        assert_eq!(hrefs(&second), hrefs(&first));
        assert_eq!(links.scan_count.load(Ordering::SeqCst), 1);

        let mut grown = std::fs::OpenOptions::new()
            .append(true)
            .open(&transcript)
            .unwrap();
        std::io::Write::write_all(&mut grown, b"https://github.com/o/r/pull/9\n").unwrap();
        let third = links.augment("s1", job_scanning(&transcript));
        assert_eq!(links.scan_count.load(Ordering::SeqCst), 2);
        assert_eq!(
            hrefs(&third),
            [
                "https://github.com/o/r/pull/4",
                "https://github.com/o/r/pull/9"
            ]
        );
    }

    #[test]
    fn scan_cache_is_capped() {
        let dir = tempfile::tempdir().unwrap();
        let links = PrLinks::open(None);
        for i in 0..CACHE_CAP + 5 {
            let p = dir.path().join(format!("{i}.jsonl"));
            std::fs::write(&p, "x").unwrap();
            links.augment("s", job_scanning(&p));
        }
        assert!(links.scans.lock().unwrap().len() <= CACHE_CAP);
    }

    #[test]
    fn pr_hrefs_in_keeps_interleaved_schemes_in_order() {
        let text = "http://a.test/x https://github.com/o/r/pull/1 http://git.example.com/o/r/pulls/2 https://b.test/y";
        assert_eq!(
            pr_hrefs_in(text),
            [
                "https://github.com/o/r/pull/1",
                "http://git.example.com/o/r/pulls/2",
            ]
        );
    }

    #[test]
    fn pr_hrefs_in_drops_repeated_links_keeping_first_appearance_order() {
        let text = "https://github.com/o/r/pull/4 https://github.com/o/r/pull/7 https://github.com/o/r/pull/4/files https://github.com/o/r/pull/7";
        assert_eq!(
            pr_hrefs_in(text),
            [
                "https://github.com/o/r/pull/4",
                "https://github.com/o/r/pull/7",
            ]
        );
    }

    #[test]
    fn pr_hrefs_in_scans_4_mib_of_non_pr_links_in_linear_time() {
        use std::time::{Duration, Instant};
        let entry = format!("https://example.com/x {}", "a ".repeat(30));
        let text = entry.repeat(50_000);
        let started = Instant::now();
        let hrefs = pr_hrefs_in(&text);
        let elapsed = started.elapsed();
        assert!(hrefs.is_empty(), "no PR links are in the text");
        assert!(
            elapsed < Duration::from_millis(200),
            "took {elapsed:?} for {} bytes",
            text.len()
        );
    }
}
