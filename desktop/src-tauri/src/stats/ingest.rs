//! Reads Claude Code transcript lines into hourly buckets per model and
//! language (ADR 0059). Lines that don't parse or lack a field are skipped.

use super::lang::language_of;
use super::price::{request_cost, RequestTokens};
use super::time::{hour_floor, parse_utc_ms};
use serde_json::Value;
use std::collections::{BTreeMap, VecDeque};

/// Gaps longer than this between transcript lines are idle (ADR 0059).
pub const IDLE_CAP_MS: i64 = 300_000;
/// How many recent (message id, request id) keys are kept to skip repeats.
pub const MAX_RECENT_KEYS: usize = 32;
/// How many unanswered edit tool calls are kept; the oldest are dropped.
pub const MAX_PENDING_EDITS: usize = 1_000;

const SYNTHETIC_MODEL: &str = "<synthetic>";
const ASSISTANT_MARKER: &[u8] = b"\"type\":\"assistant\"";
const USER_MARKER: &[u8] = b"\"type\":\"user\"";

/// Lines of one language changed by edits.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct LangLines {
    pub added: u64,
    pub removed: u64,
    pub edits: u64,
}

/// What one model did in one UTC hour of one transcript.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Bucket {
    pub requests: u64,
    pub tokens: RequestTokens,
    pub cost_usd: f64,
    pub active_ms: i64,
    pub lines: BTreeMap<&'static str, LangLines>,
}

/// (UTC hour start in ms, model id).
pub type BucketKey = (i64, String);

/// An edit tool call waiting for its result.
#[derive(Debug, Clone, PartialEq)]
struct PendingEdit {
    model: String,
    language: &'static str,
    /// (added, removed) counted from the call's input, used when the result
    /// has no patch.
    fallback: (u64, u64),
}

/// Reads one transcript's lines in order and keeps its buckets. Feed it
/// lines across several reads; it keeps what it needs between them.
#[derive(Debug, Default)]
pub struct Parser {
    pub buckets: BTreeMap<BucketKey, Bucket>,
    current_model: Option<String>,
    last_ts_ms: Option<i64>,
    recent_keys: VecDeque<(String, String)>,
    pending: VecDeque<(String, PendingEdit)>,
}

impl Parser {
    /// Feeds one line (without its newline). Lines that don't contain
    /// `"type":"assistant"` or `"type":"user"`, or don't parse, are ignored.
    pub fn feed_line(&mut self, line: &[u8]) {
        if !(contains(line, ASSISTANT_MARKER) || contains(line, USER_MARKER)) {
            return;
        }
        let Ok(entry) = serde_json::from_slice::<Value>(line) else {
            return;
        };
        let ts = entry
            .get("timestamp")
            .and_then(Value::as_str)
            .and_then(parse_utc_ms);
        match entry.get("type").and_then(Value::as_str) {
            Some("assistant") => self.feed_assistant(&entry, ts),
            Some("user") => self.feed_user(&entry, ts),
            _ => return,
        }
        self.track_active_time(ts);
    }

    fn feed_assistant(&mut self, entry: &Value, ts: Option<i64>) {
        let Some(message) = entry.get("message") else {
            return;
        };
        let Some(model) = message
            .get("model")
            .and_then(Value::as_str)
            .filter(|model| *model != SYNTHETIC_MODEL)
        else {
            return;
        };
        if let Some(usage) = message.get("usage").filter(|usage| usage.is_object()) {
            self.count_request(entry, message, model, usage, ts);
        }
        self.track_edit_calls(message, model);
    }

    fn count_request(
        &mut self,
        entry: &Value,
        message: &Value,
        model: &str,
        usage: &Value,
        ts: Option<i64>,
    ) {
        let key = (
            text_at(message, "id").to_string(),
            text_at(entry, "requestId").to_string(),
        );
        if self.recent_keys.contains(&key) {
            return;
        }
        if self.recent_keys.len() >= MAX_RECENT_KEYS {
            self.recent_keys.pop_front();
        }
        self.recent_keys.push_back(key);
        self.current_model = Some(model.to_string());
        let Some(ts) = ts else {
            return;
        };
        let tokens = request_tokens(usage);
        let cost = request_cost(model, &tokens);
        let bucket = self.bucket_mut(ts, model);
        bucket.requests = bucket.requests.saturating_add(1);
        add_tokens(&mut bucket.tokens, &tokens);
        bucket.cost_usd += cost;
    }

    /// Runs on every line of a request, not only the first: Claude Code writes
    /// one line per content block, so a tool call can sit on a repeat.
    fn track_edit_calls(&mut self, message: &Value, model: &str) {
        let Some(blocks) = message.get("content").and_then(Value::as_array) else {
            return;
        };
        for block in blocks
            .iter()
            .filter(|block| text_at(block, "type") == "tool_use")
        {
            let Some(id) = block.get("id").and_then(Value::as_str) else {
                continue;
            };
            let Some(input) = block.get("input") else {
                continue;
            };
            let Some(fallback) = edit_fallback(text_at(block, "name"), input) else {
                continue;
            };
            let language = language_of(path_of(input));
            self.push_pending(
                id,
                PendingEdit {
                    model: model.to_string(),
                    language,
                    fallback,
                },
            );
        }
    }

    fn feed_user(&mut self, entry: &Value, ts: Option<i64>) {
        let Some(blocks) = entry
            .get("message")
            .and_then(|message| message.get("content"))
            .and_then(Value::as_array)
        else {
            return;
        };
        let result_blocks: Vec<&Value> = blocks
            .iter()
            .filter(|block| text_at(block, "type") == "tool_result")
            .collect();
        let single_result = result_blocks.len() == 1;
        for block in result_blocks {
            let Some(id) = block.get("tool_use_id").and_then(Value::as_str) else {
                continue;
            };
            let Some(edit) = self.take_pending(id) else {
                continue;
            };
            if block.get("is_error").and_then(Value::as_bool) == Some(true) {
                continue;
            }
            let (added, removed) = result_lines(entry, single_result).unwrap_or(edit.fallback);
            let Some(at) = ts.or(self.last_ts_ms) else {
                continue;
            };
            let lines = self
                .bucket_mut(at, &edit.model)
                .lines
                .entry(edit.language)
                .or_default();
            lines.added = lines.added.saturating_add(added);
            lines.removed = lines.removed.saturating_add(removed);
            lines.edits = lines.edits.saturating_add(1);
        }
    }

    /// Adds the gap since the previous timestamp to the current model, when
    /// the gap is within the idle cap and a request has been seen.
    fn track_active_time(&mut self, ts: Option<i64>) {
        let Some(ts) = ts else {
            return;
        };
        if let (Some(last), Some(model)) = (self.last_ts_ms, self.current_model.clone()) {
            let gap = ts.saturating_sub(last);
            if (0..=IDLE_CAP_MS).contains(&gap) {
                let bucket = self.bucket_mut(ts, &model);
                bucket.active_ms = bucket.active_ms.saturating_add(gap);
            }
        }
        self.last_ts_ms = Some(ts);
    }

    fn bucket_mut(&mut self, ts_ms: i64, model: &str) -> &mut Bucket {
        self.buckets
            .entry((hour_floor(ts_ms), model.to_string()))
            .or_default()
    }

    fn push_pending(&mut self, id: &str, edit: PendingEdit) {
        if self.pending.len() >= MAX_PENDING_EDITS {
            self.pending.pop_front();
        }
        self.pending.push_back((id.to_string(), edit));
    }

    fn take_pending(&mut self, id: &str) -> Option<PendingEdit> {
        let index = self
            .pending
            .iter()
            .position(|(pending_id, _)| pending_id == id)?;
        self.pending.remove(index).map(|(_, edit)| edit)
    }
}

/// Line count of s: the number of '\n', plus 1 if s is non-empty and
/// doesn't end with '\n'.
pub fn count_lines(s: &str) -> u64 {
    let newlines = s.bytes().filter(|byte| *byte == b'\n').count();
    let unterminated = !s.is_empty() && !s.ends_with('\n');
    u64::try_from(newlines)
        .unwrap_or(u64::MAX)
        .saturating_add(u64::from(unterminated))
}

fn contains(haystack: &[u8], needle: &[u8]) -> bool {
    haystack
        .windows(needle.len())
        .any(|window| window == needle)
}

fn text_at<'a>(value: &'a Value, key: &str) -> &'a str {
    value.get(key).and_then(Value::as_str).unwrap_or("")
}

fn u64_at(value: &Value, key: &str) -> u64 {
    value.get(key).and_then(Value::as_u64).unwrap_or(0)
}

fn path_of(input: &Value) -> &str {
    let file = text_at(input, "file_path");
    if file.is_empty() {
        text_at(input, "notebook_path")
    } else {
        file
    }
}

fn request_tokens(usage: &Value) -> RequestTokens {
    let (cache_write_5m, cache_write_1h) = cache_writes(usage);
    RequestTokens {
        input: u64_at(usage, "input_tokens"),
        output: u64_at(usage, "output_tokens"),
        thinking: usage
            .get("output_tokens_details")
            .map_or(0, |details| u64_at(details, "thinking_tokens")),
        cache_write_5m,
        cache_write_1h,
        cache_read: u64_at(usage, "cache_read_input_tokens"),
    }
}

/// The 5-minute and 1-hour cache writes; without the split, all of
/// `cache_creation_input_tokens` is 5-minute.
fn cache_writes(usage: &Value) -> (u64, u64) {
    let split = usage
        .get("cache_creation")
        .filter(|split| split.is_object());
    let five_minute = split
        .and_then(|split| split.get("ephemeral_5m_input_tokens"))
        .and_then(Value::as_u64);
    let one_hour = split
        .and_then(|split| split.get("ephemeral_1h_input_tokens"))
        .and_then(Value::as_u64);
    match (five_minute, one_hour) {
        (None, None) => (u64_at(usage, "cache_creation_input_tokens"), 0),
        (five_minute, one_hour) => (five_minute.unwrap_or(0), one_hour.unwrap_or(0)),
    }
}

fn add_tokens(total: &mut RequestTokens, tokens: &RequestTokens) {
    total.input = total.input.saturating_add(tokens.input);
    total.output = total.output.saturating_add(tokens.output);
    total.thinking = total.thinking.saturating_add(tokens.thinking);
    total.cache_write_5m = total.cache_write_5m.saturating_add(tokens.cache_write_5m);
    total.cache_write_1h = total.cache_write_1h.saturating_add(tokens.cache_write_1h);
    total.cache_read = total.cache_read.saturating_add(tokens.cache_read);
}

/// (added, removed) for a tool call from its input, for results that carry
/// no patch.
fn edit_fallback(name: &str, input: &Value) -> Option<(u64, u64)> {
    match name {
        "Write" => Some((count_lines(text_at(input, "content")), 0)),
        "Edit" => Some(replacement_lines(input)),
        "MultiEdit" => Some(
            input
                .get("edits")
                .and_then(Value::as_array)
                .map_or((0, 0), |edits| {
                    edits.iter().map(replacement_lines).fold(
                        (0, 0),
                        |(added, removed), (next_added, next_removed)| {
                            (
                                added.saturating_add(next_added),
                                removed.saturating_add(next_removed),
                            )
                        },
                    )
                }),
        ),
        "NotebookEdit" => Some((count_lines(text_at(input, "new_source")), 0)),
        _ => None,
    }
}

fn replacement_lines(edit: &Value) -> (u64, u64) {
    (
        count_lines(text_at(edit, "new_string")),
        count_lines(text_at(edit, "old_string")),
    )
}

/// (added, removed) from a tool result's `toolUseResult`, when the line
/// answers exactly one tool call.
fn result_lines(entry: &Value, single_result: bool) -> Option<(u64, u64)> {
    if !single_result {
        return None;
    }
    let result = entry
        .get("toolUseResult")
        .filter(|result| result.is_object())?;
    if let Some(hunks) = result
        .get("structuredPatch")
        .and_then(Value::as_array)
        .filter(|hunks| !hunks.is_empty())
    {
        return Some(count_patch(hunks));
    }
    if text_at(result, "type") != "create" {
        return None;
    }
    result
        .get("content")
        .and_then(Value::as_str)
        .map(|content| (count_lines(content), 0))
}

fn count_patch(hunks: &[Value]) -> (u64, u64) {
    hunks
        .iter()
        .filter_map(|hunk| hunk.get("lines").and_then(Value::as_array))
        .flatten()
        .filter_map(Value::as_str)
        .fold((0, 0), |(added, removed), line| {
            if line.starts_with('+') {
                (added + 1, removed)
            } else if line.starts_with('-') {
                (added, removed + 1)
            } else {
                (added, removed)
            }
        })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::stats::time::parse_utc_ms;
    use serde_json::json;

    const MODEL: &str = "claude-haiku-5-5";
    const OTHER_MODEL: &str = "claude-sonnet-5-5";

    fn second(offset_s: i64) -> String {
        let minutes = offset_s / 60;
        let seconds = offset_s % 60;
        format!("2026-11-12T20:{minutes:02}:{seconds:02}.000Z")
    }

    fn feed(parser: &mut Parser, line: Value) {
        parser.feed_line(line.to_string().as_bytes());
    }

    fn request(ts: &str, message_id: &str, request_id: &str, model: &str, usage: Value) -> Value {
        json!({
            "type": "assistant",
            "timestamp": ts,
            "requestId": request_id,
            "message": {
                "id": message_id,
                "model": model,
                "content": [{"type": "text", "text": "ok"}],
                "usage": usage
            }
        })
    }

    fn assistant_tool_use(ts: &str, tool_use_id: &str, name: &str, input: Value) -> Value {
        json!({
            "type": "assistant",
            "timestamp": ts,
            "message": {
                "id": "msg_tool",
                "model": MODEL,
                "content": [{"type": "tool_use", "id": tool_use_id, "name": name, "input": input}]
            }
        })
    }

    fn user_tool_result(ts: &str, tool_use_id: &str, tool_use_result: Option<Value>) -> Value {
        let mut line = json!({
            "type": "user",
            "timestamp": ts,
            "message": {
                "role": "user",
                "content": [{"type": "tool_result", "tool_use_id": tool_use_id, "is_error": false, "content": "ok"}]
            }
        });
        if let Some(result) = tool_use_result {
            line["toolUseResult"] = result;
        }
        line
    }

    fn user_text(ts: &str) -> Value {
        json!({"type": "user", "timestamp": ts, "message": {"role": "user", "content": "hi"}})
    }

    fn bucket<'a>(parser: &'a Parser, ts: &str, model: &str) -> &'a Bucket {
        parser
            .buckets
            .get(&(
                hour_floor(parse_utc_ms(ts).expect("test timestamp")),
                model.to_string(),
            ))
            .expect("a bucket for that hour and model")
    }

    fn haiku_usage(input: u64, output: u64) -> Value {
        json!({"input_tokens": input, "output_tokens": output})
    }

    #[test]
    fn request_tokens_and_cost_go_to_their_hour() {
        let mut parser = Parser::default();
        let usage = json!({
            "input_tokens": 1000,
            "output_tokens": 500,
            "cache_read_input_tokens": 2000,
            "cache_creation_input_tokens": 300
        });
        feed(
            &mut parser,
            request(&second(0), "msg_1", "req_1", MODEL, usage),
        );
        let found = bucket(&parser, &second(0), MODEL);
        assert_eq!(found.requests, 1);
        assert_eq!(
            found.tokens,
            RequestTokens {
                input: 1000,
                output: 500,
                thinking: 0,
                cache_write_5m: 300,
                cache_write_1h: 0,
                cache_read: 2000,
            }
        );
        let expected = request_cost(MODEL, &found.tokens);
        assert!((found.cost_usd - expected).abs() < 1e-12);
    }

    #[test]
    fn repeated_request_is_counted_once() {
        let mut parser = Parser::default();
        feed(
            &mut parser,
            request(&second(0), "msg_1", "req_1", MODEL, haiku_usage(1000, 0)),
        );
        feed(
            &mut parser,
            request(&second(1), "msg_1", "req_1", MODEL, haiku_usage(1000, 0)),
        );
        let found = bucket(&parser, &second(0), MODEL);
        assert_eq!(found.requests, 1);
        assert_eq!(found.tokens.input, 1000);
    }

    #[test]
    fn synthetic_model_counts_no_request() {
        let mut parser = Parser::default();
        feed(
            &mut parser,
            request(
                &second(0),
                "msg_1",
                "req_1",
                "<synthetic>",
                haiku_usage(1000, 0),
            ),
        );
        assert!(parser.buckets.is_empty());
    }

    #[test]
    fn line_without_usage_counts_no_request() {
        let mut parser = Parser::default();
        let line = json!({
            "type": "assistant",
            "timestamp": second(0),
            "message": {"id": "msg_1", "model": MODEL, "content": [{"type": "text", "text": "ok"}]}
        });
        feed(&mut parser, line);
        assert!(parser.buckets.is_empty());
    }

    #[test]
    fn non_string_model_counts_nothing() {
        let mut parser = Parser::default();
        let line = json!({
            "type": "assistant",
            "timestamp": second(0),
            "requestId": "req_1",
            "message": {"id": "msg_1", "model": 42, "usage": {"input_tokens": 5}}
        });
        feed(&mut parser, line);
        assert!(parser.buckets.is_empty());
    }

    #[test]
    fn cache_writes_split_into_five_minute_and_one_hour() {
        let mut parser = Parser::default();
        let usage = json!({
            "cache_creation_input_tokens": 30,
            "cache_creation": {"ephemeral_5m_input_tokens": 10, "ephemeral_1h_input_tokens": 20}
        });
        feed(
            &mut parser,
            request(&second(0), "msg_1", "req_1", MODEL, usage),
        );
        let found = bucket(&parser, &second(0), MODEL);
        assert_eq!(
            (found.tokens.cache_write_5m, found.tokens.cache_write_1h),
            (10, 20)
        );
    }

    #[test]
    fn cache_writes_without_split_count_as_five_minute() {
        let mut parser = Parser::default();
        let usage = json!({"cache_creation_input_tokens": 30});
        feed(
            &mut parser,
            request(&second(0), "msg_1", "req_1", MODEL, usage),
        );
        let found = bucket(&parser, &second(0), MODEL);
        assert_eq!(
            (found.tokens.cache_write_5m, found.tokens.cache_write_1h),
            (30, 0)
        );
    }

    #[test]
    fn thinking_tokens_are_summed_but_not_priced() {
        let mut parser = Parser::default();
        let usage = json!({
            "output_tokens": 1000,
            "output_tokens_details": {"thinking_tokens": 400}
        });
        feed(
            &mut parser,
            request(&second(0), "msg_1", "req_1", OTHER_MODEL, usage),
        );
        let found = bucket(&parser, &second(0), OTHER_MODEL);
        assert_eq!(found.tokens.thinking, 400);
        let output_only = RequestTokens {
            output: 1000,
            ..RequestTokens::default()
        };
        assert_eq!(found.cost_usd, request_cost(OTHER_MODEL, &output_only));
    }

    #[test]
    fn edit_answered_by_patch_counts_lines_for_model_and_language() {
        let mut parser = Parser::default();
        let input =
            json!({"file_path": "/w/x/main.go", "old_string": "a\n", "new_string": "a\nb\n"});
        feed(
            &mut parser,
            assistant_tool_use(&second(0), "toolu_1", "Edit", input),
        );
        let patch = json!({"structuredPatch": [{"lines": [" a", "+b", "-c", "+d"]}]});
        feed(
            &mut parser,
            user_tool_result(&second(2), "toolu_1", Some(patch)),
        );
        let lines = bucket(&parser, &second(2), MODEL).lines["Go"];
        assert_eq!(
            lines,
            LangLines {
                added: 2,
                removed: 1,
                edits: 1
            }
        );
    }

    #[test]
    fn write_creating_file_counts_its_content_lines() {
        let mut parser = Parser::default();
        let content = "package x\n\nfunc A() {}\n";
        let input = json!({"file_path": "/w/x/new.go", "content": content});
        feed(
            &mut parser,
            assistant_tool_use(&second(0), "toolu_2", "Write", input),
        );
        let result = json!({"type": "create", "content": content, "structuredPatch": []});
        feed(
            &mut parser,
            user_tool_result(&second(1), "toolu_2", Some(result)),
        );
        let lines = bucket(&parser, &second(1), MODEL).lines["Go"];
        assert_eq!(
            lines,
            LangLines {
                added: 3,
                removed: 0,
                edits: 1
            }
        );
    }

    #[test]
    fn edit_without_result_patch_counts_input_fallback() {
        let mut parser = Parser::default();
        let input =
            json!({"file_path": "/w/x/main.go", "old_string": "a\n", "new_string": "a\nb\n"});
        feed(
            &mut parser,
            assistant_tool_use(&second(0), "toolu_1", "Edit", input),
        );
        feed(&mut parser, user_tool_result(&second(1), "toolu_1", None));
        let lines = bucket(&parser, &second(1), MODEL).lines["Go"];
        assert_eq!(
            lines,
            LangLines {
                added: 2,
                removed: 1,
                edits: 1
            }
        );
    }

    #[test]
    fn multi_edit_without_result_patch_sums_each_edit() {
        let mut parser = Parser::default();
        let input = json!({
            "file_path": "/w/x/main.go",
            "edits": [
                {"old_string": "a\n", "new_string": "a\nb\n"},
                {"old_string": "x", "new_string": "y\nz"}
            ]
        });
        feed(
            &mut parser,
            assistant_tool_use(&second(0), "toolu_3", "MultiEdit", input),
        );
        feed(&mut parser, user_tool_result(&second(1), "toolu_3", None));
        let lines = bucket(&parser, &second(1), MODEL).lines["Go"];
        assert_eq!(
            lines,
            LangLines {
                added: 4,
                removed: 2,
                edits: 1
            }
        );
    }

    #[test]
    fn notebook_edit_without_result_counts_new_source_lines() {
        let mut parser = Parser::default();
        let input = json!({"notebook_path": "/w/n.ipynb", "new_source": "print(1)\nprint(2)"});
        feed(
            &mut parser,
            assistant_tool_use(&second(0), "toolu_4", "NotebookEdit", input),
        );
        feed(&mut parser, user_tool_result(&second(1), "toolu_4", None));
        let lines = bucket(&parser, &second(1), MODEL).lines["Notebook"];
        assert_eq!(
            lines,
            LangLines {
                added: 2,
                removed: 0,
                edits: 1
            }
        );
    }

    #[test]
    fn errored_result_counts_nothing_and_clears_the_pending_call() {
        let mut parser = Parser::default();
        let input =
            json!({"file_path": "/w/x/main.go", "old_string": "a\n", "new_string": "a\nb\n"});
        feed(
            &mut parser,
            assistant_tool_use(&second(0), "toolu_1", "Edit", input),
        );
        let errored = json!({
            "type": "user",
            "timestamp": second(1),
            "message": {"content": [{"type": "tool_result", "tool_use_id": "toolu_1", "is_error": true, "content": "failed"}]}
        });
        feed(&mut parser, errored);
        let patch = json!({"structuredPatch": [{"lines": ["+b"]}]});
        feed(
            &mut parser,
            user_tool_result(&second(2), "toolu_1", Some(patch)),
        );
        assert!(parser.buckets.is_empty());
    }

    #[test]
    fn result_for_unknown_tool_call_counts_nothing() {
        let mut parser = Parser::default();
        let patch = json!({"structuredPatch": [{"lines": ["+b"]}]});
        feed(
            &mut parser,
            user_tool_result(&second(0), "toolu_missing", Some(patch)),
        );
        assert!(parser.buckets.is_empty());
    }

    #[test]
    fn gaps_up_to_the_cap_count_under_the_current_model() {
        let mut parser = Parser::default();
        feed(
            &mut parser,
            request(&second(0), "msg_1", "req_1", MODEL, haiku_usage(1, 1)),
        );
        feed(&mut parser, user_text(&second(30)));
        feed(&mut parser, user_text(&second(330)));
        assert_eq!(bucket(&parser, &second(0), MODEL).active_ms, 330_000);
    }

    #[test]
    fn gap_over_the_cap_counts_nothing() {
        let mut parser = Parser::default();
        feed(
            &mut parser,
            request(&second(0), "msg_1", "req_1", MODEL, haiku_usage(1, 1)),
        );
        feed(&mut parser, user_text(&second(301)));
        assert_eq!(bucket(&parser, &second(0), MODEL).active_ms, 0);
    }

    #[test]
    fn gaps_before_the_first_request_count_nowhere() {
        let mut parser = Parser::default();
        feed(&mut parser, user_text(&second(0)));
        feed(&mut parser, user_text(&second(10)));
        feed(
            &mut parser,
            request(&second(20), "msg_1", "req_1", MODEL, haiku_usage(1, 1)),
        );
        assert_eq!(bucket(&parser, &second(20), MODEL).active_ms, 10_000);
    }

    #[test]
    fn reply_wait_counts_for_the_replying_model() {
        let mut parser = Parser::default();
        feed(
            &mut parser,
            request(&second(0), "msg_1", "req_1", MODEL, haiku_usage(1, 1)),
        );
        feed(&mut parser, user_text(&second(20)));
        feed(
            &mut parser,
            request(
                &second(50),
                "msg_2",
                "req_2",
                OTHER_MODEL,
                haiku_usage(1, 1),
            ),
        );
        feed(&mut parser, user_text(&second(55)));
        assert_eq!(bucket(&parser, &second(0), MODEL).active_ms, 20_000);
        assert_eq!(bucket(&parser, &second(50), OTHER_MODEL).active_ms, 35_000);
    }

    #[test]
    fn lines_without_the_marker_or_valid_json_change_nothing() {
        let mut parser = Parser::default();
        parser.feed_line(br#"{"type":"progress","timestamp":"2026-11-12T20:00:00.000Z"}"#);
        parser.feed_line(b"not json at all");
        parser.feed_line(br#"{"type":"assistant", broken"#);
        assert!(parser.buckets.is_empty());
    }

    #[test]
    fn count_lines_of_empty_text_is_zero() {
        assert_eq!(count_lines(""), 0);
    }

    #[test]
    fn count_lines_of_one_unterminated_line_is_one() {
        assert_eq!(count_lines("a"), 1);
    }

    #[test]
    fn count_lines_ignores_a_final_newline() {
        assert_eq!(count_lines("a\n"), 1);
    }

    #[test]
    fn count_lines_counts_an_unterminated_last_line() {
        assert_eq!(count_lines("a\nb"), 2);
    }
}
