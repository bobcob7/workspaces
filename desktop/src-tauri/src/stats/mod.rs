//! Token usage, lines of code and host load for the stats bar and the
//! Stats section (ADR 0059).

pub mod aggregate;
pub mod cache;
pub mod ingest;
pub mod lang;
pub mod price;
pub mod time;

use crate::commands::{self, AppState};
use crate::host::{self, CpuTicks, HostStats};
use crate::ipc::State;
use crate::runner::AppError;
use aggregate::{ProductivityRow, Usage};
use cache::{ScanInfo, TranscriptCache};
use serde::Serialize;
use std::path::Path;
use std::sync::{Arc, Mutex};

/// Everything the stats bar and the Stats section show.
#[derive(Debug, Clone, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Stats {
    pub generated_at_ms: i64,
    pub since_ms: Option<i64>,
    pub system: HostStats,
    pub usage: Usage,
    pub productivity: Vec<ProductivityRow>,
    pub scan: ScanInfo,
    pub warnings: Vec<String>,
}

/// The transcript cache and the last CPU sample, shared by every caller.
#[derive(Debug, Default)]
pub struct Cache {
    inner: Mutex<Inner>,
}

#[derive(Debug, Default)]
struct Inner {
    transcripts: TranscriptCache,
    cpu: Option<CpuTicks>,
}

impl Cache {
    /// Refreshes the transcripts under config_dir (none when None, with a
    /// warning "no Claude Code config directory"), samples the host, and
    /// aggregates the window starting at since_ms.
    pub fn stats(
        &self,
        config_dir: Option<&Path>,
        disk: Option<&Path>,
        since_ms: Option<i64>,
        now_ms: i64,
    ) -> Stats {
        let mut guard = self.inner.lock().unwrap_or_else(|e| e.into_inner());
        let Inner { transcripts, cpu } = &mut *guard;
        let mut warnings = Vec::new();
        let scan = match config_dir {
            Some(dir) => transcripts.refresh(dir, now_ms),
            None => {
                *transcripts = TranscriptCache::default();
                warnings.push("no Claude Code config directory".to_string());
                ScanInfo::default()
            }
        };
        let (system, ticks) = host::sample(*cpu, disk);
        *cpu = ticks;
        let aggregate::Aggregate {
            usage,
            productivity,
            warnings: usage_warnings,
        } = aggregate::aggregate(transcripts.parsers(), since_ms);
        warnings.extend(usage_warnings);
        Stats {
            generated_at_ms: now_ms,
            since_ms,
            system,
            usage,
            productivity,
            scan,
            warnings,
        }
    }
}

/// Token usage, lines of code and host load for a window (ADR 0059).
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn stats_get(
    state: State<'_, AppState>,
    since_ms: Option<i64>,
) -> Result<Stats, AppError> {
    let cache = Arc::clone(&state.stats);
    let config_dir = state
        .jobs_dir
        .as_deref()
        .and_then(Path::parent)
        .map(Path::to_path_buf);
    tokio::task::spawn_blocking(move || {
        cache.stats(
            config_dir.as_deref(),
            host::disk_path().as_deref(),
            since_ms,
            commands::now_ms(),
        )
    })
    .await
    .map_err(|e| AppError::new("internal", e.to_string()))
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    use std::fs;

    /// 2026-11-12T20:30:00Z, after the fixture's requests.
    const NOW_MS: i64 = 1_794_515_400_000;
    /// 2026-11-12T21:00:00Z, the hour after the fixture's requests.
    const NEXT_HOUR_MS: i64 = 1_794_517_200_000;

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

    fn edit_line() -> String {
        json!({
            "type": "assistant",
            "timestamp": "2026-11-12T20:00:02.000Z",
            "message": {
                "id": "msg_edit",
                "model": "claude-haiku-5-5",
                "content": [{
                    "type": "tool_use",
                    "id": "toolu_1",
                    "name": "Edit",
                    "input": {"file_path": "/w/x/main.go", "old_string": "a\n", "new_string": "a\nb\n"}
                }]
            }
        })
        .to_string()
    }

    fn edit_result_line() -> String {
        json!({
            "type": "user",
            "timestamp": "2026-11-12T20:00:03.000Z",
            "message": {
                "content": [{"type": "tool_result", "tool_use_id": "toolu_1", "is_error": false, "content": "ok"}]
            }
        })
        .to_string()
    }

    /// A config dir holding one transcript: two requests and one edit.
    fn config_dir_with_transcript() -> tempfile::TempDir {
        let dir = tempfile::tempdir().expect("temp config dir");
        let transcript = dir.path().join("projects/-w/session.jsonl");
        fs::create_dir_all(transcript.parent().expect("project dir")).expect("create project dir");
        let lines = [
            request_line("msg_1"),
            request_line("msg_2"),
            edit_line(),
            edit_result_line(),
        ];
        fs::write(&transcript, format!("{}\n", lines.join("\n"))).expect("write transcript");
        dir
    }

    #[test]
    fn missing_config_dir_gives_zero_totals_and_a_warning() {
        let stats = Cache::default().stats(None, None, None, NOW_MS);
        assert_eq!(stats.usage.totals.requests, 0);
        assert_eq!(stats.scan, ScanInfo::default());
        assert!(stats
            .warnings
            .contains(&"no Claude Code config directory".to_string()));
    }

    #[test]
    fn transcript_requests_and_edits_are_summed() {
        let dir = config_dir_with_transcript();
        let stats = Cache::default().stats(Some(dir.path()), None, None, NOW_MS);
        assert_eq!(stats.usage.totals.requests, 2);
        assert_eq!(stats.usage.totals.input_tokens, 20);
        assert_eq!(stats.usage.totals.output_tokens, 10);
        assert_eq!(stats.scan.files, 1);
        assert_eq!(
            stats.usage.by_language.iter().map(|l| l.edits).sum::<u64>(),
            1
        );
    }

    #[test]
    fn refresh_within_five_seconds_is_served_from_cache() {
        let dir = config_dir_with_transcript();
        let cache = Cache::default();
        cache.stats(Some(dir.path()), None, None, NOW_MS);
        let stats = cache.stats(Some(dir.path()), None, None, NOW_MS + 1_000);
        assert!(stats.scan.cached);
    }

    #[test]
    fn window_starting_after_the_requests_hour_counts_no_requests() {
        let dir = config_dir_with_transcript();
        let stats = Cache::default().stats(Some(dir.path()), None, Some(NEXT_HOUR_MS), NOW_MS);
        assert_eq!(stats.usage.totals.requests, 0);
        assert_eq!(stats.since_ms, Some(NEXT_HOUR_MS));
    }
}
