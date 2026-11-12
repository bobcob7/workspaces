//! Sums the hourly buckets of every transcript into the usage totals and the
//! productivity rows of the Stats section (ADR 0059, "Metric definitions").

use super::ingest::{Bucket, LangLines, Parser};
use super::price::{self, RequestTokens};
use super::time::hour_floor;
use serde::Serialize;
use std::collections::BTreeMap;

/// Token counts and cost summed over requests.
#[derive(Debug, Clone, Default, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct TokenCounts {
    pub requests: u64,
    pub input_tokens: u64,
    pub output_tokens: u64,
    pub thinking_tokens: u64,
    pub cache_write_tokens: u64,
    pub cache_read_tokens: u64,
    pub total_tokens: u64,
    pub cost_usd: f64,
    pub cache_hit_ratio: Option<f64>,
}

/// One model's usage over the window.
#[derive(Debug, Clone, Default, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct ModelUsage {
    pub model: String,
    pub label: String,
    pub family: &'static str,
    pub priced: bool,
    pub sessions: u64,
    pub active_minutes: f64,
    pub lines_added: u64,
    pub lines_removed: u64,
    #[serde(flatten)]
    pub tokens: TokenCounts,
}

/// Lines of one language changed by edits over the window.
#[derive(Debug, Clone, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct LanguageLines {
    pub language: &'static str,
    pub lines_added: u64,
    pub lines_removed: u64,
    pub edits: u64,
}

/// Usage over the window, for the stats bar and the Usage section.
#[derive(Debug, Clone, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Usage {
    pub totals: TokenCounts,
    pub sessions: u64,
    pub active_minutes: f64,
    pub by_model: Vec<ModelUsage>,
    pub by_language: Vec<LanguageLines>,
}

/// One model writing one language, with the effort allocated to it.
#[derive(Debug, Clone, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ProductivityRow {
    pub model: String,
    pub label: String,
    pub family: &'static str,
    pub language: &'static str,
    pub lines_added: u64,
    pub lines_removed: u64,
    pub edits: u64,
    pub active_minutes: f64,
    pub tokens: u64,
    pub cost_usd: f64,
    pub share_of_language: Option<f64>,
    pub loc_per_minute: Option<f64>,
    pub minutes_per_100_loc: Option<f64>,
    pub tokens_per_loc: Option<f64>,
    pub cost_per_loc: Option<f64>,
}

/// What `stats_get` reports about usage for a window.
#[derive(Debug, Clone, PartialEq)]
pub struct Aggregate {
    pub usage: Usage,
    pub productivity: Vec<ProductivityRow>,
    pub warnings: Vec<String>,
}

/// Sums the buckets of every parser whose hour is at or after
/// hour_floor(since_ms) (all of them when None), and derives the
/// productivity rows (ADR 0059 "Metric definitions").
pub fn aggregate<'a>(
    parsers: impl IntoIterator<Item = &'a Parser>,
    since_ms: Option<i64>,
) -> Aggregate {
    let floor = since_ms.map(hour_floor);
    let mut sums = Sums::default();
    for parser in parsers {
        sums.add_session(parser, floor);
    }
    sums.finish()
}

/// a / b, or None when b is 0 (or not finite).
fn ratio(a: f64, b: f64) -> Option<f64> {
    if b == 0.0 || !b.is_finite() {
        return None;
    }
    Some(a / b)
}

impl TokenCounts {
    fn from_bucket(bucket: &Bucket) -> Self {
        let tokens: RequestTokens = bucket.tokens;
        Self {
            requests: bucket.requests,
            input_tokens: tokens.input,
            output_tokens: tokens.output,
            thinking_tokens: tokens.thinking,
            cache_write_tokens: tokens.cache_write_5m.saturating_add(tokens.cache_write_1h),
            cache_read_tokens: tokens.cache_read,
            cost_usd: bucket.cost_usd,
            ..Self::default()
        }
    }

    fn add(&mut self, other: &Self) {
        self.requests = self.requests.saturating_add(other.requests);
        self.input_tokens = self.input_tokens.saturating_add(other.input_tokens);
        self.output_tokens = self.output_tokens.saturating_add(other.output_tokens);
        self.thinking_tokens = self.thinking_tokens.saturating_add(other.thinking_tokens);
        self.cache_write_tokens = self
            .cache_write_tokens
            .saturating_add(other.cache_write_tokens);
        self.cache_read_tokens = self
            .cache_read_tokens
            .saturating_add(other.cache_read_tokens);
        self.total_tokens = self.total_tokens.saturating_add(other.total_tokens);
        self.cost_usd += other.cost_usd;
    }

    /// Sets total_tokens and cache_hit_ratio from the summed counts.
    fn seal(&mut self) {
        self.total_tokens = self
            .input_tokens
            .saturating_add(self.output_tokens)
            .saturating_add(self.cache_write_tokens)
            .saturating_add(self.cache_read_tokens);
        let prompt = self
            .input_tokens
            .saturating_add(self.cache_read_tokens)
            .saturating_add(self.cache_write_tokens);
        self.cache_hit_ratio = ratio(self.cache_read_tokens as f64, prompt as f64);
    }
}

impl ModelUsage {
    fn new(model: &str) -> Self {
        Self {
            model: model.to_string(),
            label: price::label(model),
            family: price::family(model),
            priced: price::rates_for(model, 0).is_some(),
            ..Self::default()
        }
    }
}

/// One model's activity in one transcript over the window.
#[derive(Default)]
struct SessionModel {
    counts: TokenCounts,
    active_ms: i64,
    lines: BTreeMap<&'static str, LangLines>,
}

impl SessionModel {
    fn absorb(&mut self, bucket: &Bucket) {
        self.counts.add(&TokenCounts::from_bucket(bucket));
        self.active_ms = self.active_ms.saturating_add(bucket.active_ms);
        for (language, lines) in &bucket.lines {
            add_lines(self.lines.entry(*language).or_default(), lines);
        }
    }

    fn is_empty(&self) -> bool {
        self.counts.requests == 0 && self.active_ms == 0 && self.lines.is_empty()
    }

    fn changed_lines(&self) -> u64 {
        self.lines.values().map(lines_changed).sum()
    }
}

/// The effort and cost allocated to one model writing one language.
#[derive(Default)]
struct RowSums {
    lines: LangLines,
    active_minutes: f64,
    tokens: f64,
    cost_usd: f64,
}

#[derive(Default)]
struct Sums {
    totals: TokenCounts,
    sessions: u64,
    models: BTreeMap<String, ModelUsage>,
    languages: BTreeMap<&'static str, LangLines>,
    rows: BTreeMap<(String, &'static str), RowSums>,
}

impl Sums {
    fn add_session(&mut self, parser: &Parser, floor: Option<i64>) {
        let mut models: BTreeMap<&str, SessionModel> = BTreeMap::new();
        for ((hour, model), bucket) in &parser.buckets {
            if floor.is_some_and(|floor| *hour < floor) {
                continue;
            }
            models.entry(model.as_str()).or_default().absorb(bucket);
        }
        let mut has_request = false;
        for (model, session) in &mut models {
            session.counts.seal();
            if session.is_empty() {
                continue;
            }
            has_request |= session.counts.requests > 0;
            self.add_model(model, session);
        }
        if has_request {
            self.sessions += 1;
        }
    }

    fn add_model(&mut self, model: &str, session: &SessionModel) {
        self.totals.add(&session.counts);
        let usage = self
            .models
            .entry(model.to_string())
            .or_insert_with(|| ModelUsage::new(model));
        usage.tokens.add(&session.counts);
        usage.active_minutes += minutes(session.active_ms);
        if session.counts.requests > 0 {
            usage.sessions += 1;
        }
        for lines in session.lines.values() {
            usage.lines_added = usage.lines_added.saturating_add(lines.added);
            usage.lines_removed = usage.lines_removed.saturating_add(lines.removed);
        }
        for (language, lines) in &session.lines {
            add_lines(self.languages.entry(*language).or_default(), lines);
        }
        self.allocate(model, session);
    }

    /// Splits this session's minutes, tokens and cost across the languages
    /// it changed, in proportion to lines changed.
    fn allocate(&mut self, model: &str, session: &SessionModel) {
        let changed = session.changed_lines();
        if changed == 0 {
            return;
        }
        let active_minutes = minutes(session.active_ms);
        for (language, lines) in &session.lines {
            let share = lines_changed(lines) as f64 / changed as f64;
            let row = self.rows.entry((model.to_string(), *language)).or_default();
            add_lines(&mut row.lines, lines);
            row.active_minutes += active_minutes * share;
            row.tokens += session.counts.total_tokens as f64 * share;
            row.cost_usd += session.counts.cost_usd * share;
        }
    }

    fn finish(self) -> Aggregate {
        let warnings = self
            .models
            .values()
            .filter(|usage| !usage.priced && usage.tokens.requests > 0)
            .map(|usage| format!("no price for {}; its cost shows as $0", usage.model))
            .collect();
        let mut totals = self.totals;
        totals.seal();
        let by_model = sorted_models(self.models);
        let active_minutes = by_model.iter().map(|usage| usage.active_minutes).sum();
        let by_language = sorted_languages(&self.languages);
        let productivity = productivity_rows(self.rows, &self.languages);
        Aggregate {
            usage: Usage {
                totals,
                sessions: self.sessions,
                active_minutes,
                by_model,
                by_language,
            },
            productivity,
            warnings,
        }
    }
}

fn sorted_models(models: BTreeMap<String, ModelUsage>) -> Vec<ModelUsage> {
    let mut sorted: Vec<ModelUsage> = models.into_values().collect();
    for usage in &mut sorted {
        usage.tokens.seal();
    }
    sorted.sort_by(|a, b| {
        b.tokens
            .total_tokens
            .cmp(&a.tokens.total_tokens)
            .then_with(|| a.model.cmp(&b.model))
    });
    sorted
}

fn sorted_languages(languages: &BTreeMap<&'static str, LangLines>) -> Vec<LanguageLines> {
    let mut sorted: Vec<LanguageLines> = languages
        .iter()
        .map(|(&language, lines)| LanguageLines {
            language,
            lines_added: lines.added,
            lines_removed: lines.removed,
            edits: lines.edits,
        })
        .collect();
    sorted.sort_by(|a, b| {
        b.lines_added
            .cmp(&a.lines_added)
            .then_with(|| a.language.cmp(b.language))
    });
    sorted
}

fn productivity_rows(
    rows: BTreeMap<(String, &'static str), RowSums>,
    languages: &BTreeMap<&'static str, LangLines>,
) -> Vec<ProductivityRow> {
    let mut sorted: Vec<ProductivityRow> = rows
        .into_iter()
        .map(|((model, language), sums)| {
            let added = sums.lines.added as f64;
            let tokens = sums.tokens.round() as u64;
            ProductivityRow {
                label: price::label(&model),
                family: price::family(&model),
                share_of_language: ratio(added, language_added(languages, language) as f64),
                loc_per_minute: ratio(added, sums.active_minutes),
                minutes_per_100_loc: ratio(100.0 * sums.active_minutes, added),
                tokens_per_loc: ratio(tokens as f64, added),
                cost_per_loc: ratio(sums.cost_usd, added),
                lines_added: sums.lines.added,
                lines_removed: sums.lines.removed,
                edits: sums.lines.edits,
                active_minutes: sums.active_minutes,
                tokens,
                cost_usd: sums.cost_usd,
                model,
                language,
            }
        })
        .collect();
    sorted.sort_by(|a, b| {
        language_added(languages, b.language)
            .cmp(&language_added(languages, a.language))
            .then_with(|| b.lines_added.cmp(&a.lines_added))
            .then_with(|| a.model.cmp(&b.model))
            .then_with(|| a.language.cmp(b.language))
    });
    sorted
}

fn language_added(languages: &BTreeMap<&'static str, LangLines>, language: &str) -> u64 {
    languages.get(language).map_or(0, |lines| lines.added)
}

fn add_lines(total: &mut LangLines, lines: &LangLines) {
    total.added = total.added.saturating_add(lines.added);
    total.removed = total.removed.saturating_add(lines.removed);
    total.edits = total.edits.saturating_add(lines.edits);
}

fn lines_changed(lines: &LangLines) -> u64 {
    lines.added.saturating_add(lines.removed)
}

fn minutes(active_ms: i64) -> f64 {
    active_ms as f64 / 60_000.0
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::stats::time::{parse_utc_ms, HOUR_MS};

    const MODEL: &str = "claude-haiku-5-5";
    const SONNET: &str = "claude-sonnet-5-5";
    const OPUS: &str = "claude-opus-5-5";
    const UNPRICED: &str = "mystery-model";
    const MINUTE_MS: i64 = 60_000;

    fn base_hour() -> i64 {
        parse_utc_ms("2026-11-12T20:00:00.000Z").expect("test timestamp")
    }

    fn hour(offset: i64) -> i64 {
        base_hour() + offset * HOUR_MS
    }

    fn bucket(requests: u64, input: u64, output: u64, cost_usd: f64, active_ms: i64) -> Bucket {
        Bucket {
            requests,
            tokens: RequestTokens {
                input,
                output,
                ..RequestTokens::default()
            },
            cost_usd,
            active_ms,
            lines: BTreeMap::new(),
        }
    }

    fn with_lines(mut bucket: Bucket, language: &'static str, added: u64, removed: u64) -> Bucket {
        bucket.lines.insert(
            language,
            LangLines {
                added,
                removed,
                edits: 1,
            },
        );
        bucket
    }

    fn parser(buckets: Vec<(i64, &str, Bucket)>) -> Parser {
        let mut parser = Parser::default();
        parser.buckets = buckets
            .into_iter()
            .map(|(hour, model, bucket)| ((hour, model.to_string()), bucket))
            .collect();
        parser
    }

    fn row<'a>(aggregate: &'a Aggregate, model: &str, language: &str) -> &'a ProductivityRow {
        aggregate
            .productivity
            .iter()
            .find(|row| row.model == model && row.language == language)
            .expect("a productivity row for that model and language")
    }

    fn model<'a>(aggregate: &'a Aggregate, model: &str) -> &'a ModelUsage {
        aggregate
            .usage
            .by_model
            .iter()
            .find(|usage| usage.model == model)
            .expect("a model usage for that model")
    }

    fn assert_close(actual: f64, expected: f64) {
        assert!(
            (actual - expected).abs() < 1e-9,
            "expected {expected}, got {actual}"
        );
    }

    #[test]
    fn buckets_before_the_window_start_are_excluded() {
        let parsers = [parser(vec![
            (hour(0), MODEL, bucket(1, 10, 0, 0.0, 0)),
            (hour(1), MODEL, bucket(2, 10, 0, 0.0, 0)),
        ])];
        let aggregate = aggregate(&parsers, Some(hour(1) + 5 * MINUTE_MS));
        assert_eq!(aggregate.usage.totals.requests, 2);
    }

    #[test]
    fn no_window_start_counts_every_bucket() {
        let parsers = [parser(vec![
            (hour(0), MODEL, bucket(1, 10, 0, 0.0, 0)),
            (hour(1), MODEL, bucket(2, 10, 0, 0.0, 0)),
        ])];
        let aggregate = aggregate(&parsers, None);
        assert_eq!(aggregate.usage.totals.requests, 3);
    }

    #[test]
    fn model_counts_sum_requests_and_tokens_across_hours() {
        let parsers = [parser(vec![
            (hour(0), MODEL, bucket(1, 100, 50, 0.0, 0)),
            (hour(1), MODEL, bucket(2, 200, 25, 0.0, 0)),
        ])];
        let aggregate = aggregate(&parsers, None);
        let usage = model(&aggregate, MODEL);
        assert_eq!(
            (
                usage.tokens.requests,
                usage.tokens.input_tokens,
                usage.tokens.output_tokens
            ),
            (3, 300, 75)
        );
    }

    #[test]
    fn sessions_count_files_not_requests() {
        let parsers = [
            parser(vec![(hour(0), MODEL, bucket(2, 10, 0, 0.0, 0))]),
            parser(vec![(hour(0), MODEL, bucket(2, 10, 0, 0.0, 0))]),
        ];
        let aggregate = aggregate(&parsers, None);
        assert_eq!(aggregate.usage.sessions, 2);
    }

    #[test]
    fn cache_hit_ratio_is_cache_reads_over_all_input() {
        let mut cached = bucket(1, 100, 0, 0.0, 0);
        cached.tokens.cache_read = 300;
        cached.tokens.cache_write_5m = 100;
        let parsers = [parser(vec![(hour(0), MODEL, cached)])];
        let aggregate = aggregate(&parsers, None);
        assert_eq!(aggregate.usage.totals.cache_hit_ratio, Some(0.6));
    }

    #[test]
    fn cache_hit_ratio_is_none_without_input() {
        let parsers = [parser(vec![(hour(0), MODEL, bucket(1, 0, 40, 0.0, 0))])];
        let aggregate = aggregate(&parsers, None);
        assert_eq!(aggregate.usage.totals.cache_hit_ratio, None);
    }

    #[test]
    fn active_minutes_split_by_lines_changed() {
        let parsers = [parser(vec![(
            hour(0),
            MODEL,
            with_lines(
                with_lines(bucket(1, 0, 0, 0.0, 40 * MINUTE_MS), "Go", 30, 0),
                "Markdown",
                10,
                0,
            ),
        )])];
        let aggregate = aggregate(&parsers, None);
        assert_close(row(&aggregate, MODEL, "Go").active_minutes, 30.0);
        assert_close(row(&aggregate, MODEL, "Markdown").active_minutes, 10.0);
    }

    #[test]
    fn tokens_and_cost_split_by_lines_changed() {
        let parsers = [parser(vec![(
            hour(0),
            MODEL,
            with_lines(
                with_lines(bucket(1, 100, 0, 4.0, 0), "Go", 30, 0),
                "Markdown",
                10,
                0,
            ),
        )])];
        let aggregate = aggregate(&parsers, None);
        let go = row(&aggregate, MODEL, "Go");
        let markdown = row(&aggregate, MODEL, "Markdown");
        assert_eq!((go.tokens, markdown.tokens), (75, 25));
        assert_close(go.cost_usd, 3.0);
        assert_close(markdown.cost_usd, 1.0);
    }

    #[test]
    fn session_without_edits_counts_in_by_model_only() {
        let parsers = [parser(vec![(
            hour(0),
            MODEL,
            bucket(1, 10, 0, 0.0, MINUTE_MS),
        )])];
        let aggregate = aggregate(&parsers, None);
        assert!(aggregate.productivity.is_empty());
        assert_eq!(model(&aggregate, MODEL).tokens.requests, 1);
    }

    #[test]
    fn share_of_language_compares_models() {
        let parsers = [
            parser(vec![(
                hour(0),
                MODEL,
                with_lines(bucket(1, 0, 0, 0.0, 0), "Go", 62, 0),
            )]),
            parser(vec![(
                hour(0),
                SONNET,
                with_lines(bucket(1, 0, 0, 0.0, 0), "Go", 38, 0),
            )]),
        ];
        let aggregate = aggregate(&parsers, None);
        assert_eq!(row(&aggregate, MODEL, "Go").share_of_language, Some(0.62));
    }

    #[test]
    fn ratios_are_none_when_their_denominator_is_zero() {
        let parsers = [parser(vec![(
            hour(0),
            MODEL,
            with_lines(bucket(1, 100, 0, 1.0, 0), "Go", 0, 5),
        )])];
        let aggregate = aggregate(&parsers, None);
        let go = row(&aggregate, MODEL, "Go");
        assert_eq!(
            (
                go.share_of_language,
                go.loc_per_minute,
                go.minutes_per_100_loc,
                go.tokens_per_loc,
                go.cost_per_loc,
            ),
            (None, None, None, None, None)
        );
    }

    #[test]
    fn unpriced_model_costs_nothing_and_warns_once() {
        let parsers = [parser(vec![
            (hour(0), UNPRICED, bucket(1, 10, 5, 0.0, 0)),
            (hour(1), UNPRICED, bucket(1, 10, 5, 0.0, 0)),
        ])];
        let aggregate = aggregate(&parsers, None);
        let usage = model(&aggregate, UNPRICED);
        assert_eq!(
            (
                usage.priced,
                usage.tokens.cost_usd,
                aggregate.warnings.clone()
            ),
            (
                false,
                0.0,
                vec!["no price for mystery-model; its cost shows as $0".to_string()]
            )
        );
    }

    #[test]
    fn by_model_sorts_by_total_tokens_then_model() {
        let parsers = [parser(vec![
            (hour(0), SONNET, bucket(1, 100, 0, 0.0, 0)),
            (hour(0), MODEL, bucket(1, 100, 0, 0.0, 0)),
            (hour(0), OPUS, bucket(1, 500, 0, 0.0, 0)),
        ])];
        let aggregate = aggregate(&parsers, None);
        let order: Vec<&str> = aggregate
            .usage
            .by_model
            .iter()
            .map(|usage| usage.model.as_str())
            .collect();
        assert_eq!(order, vec![OPUS, MODEL, SONNET]);
    }

    #[test]
    fn by_language_sorts_by_lines_added_then_language() {
        let parsers = [parser(vec![(
            hour(0),
            MODEL,
            with_lines(
                with_lines(
                    with_lines(bucket(1, 0, 0, 0.0, 0), "Go", 10, 0),
                    "Rust",
                    10,
                    0,
                ),
                "Markdown",
                30,
                0,
            ),
        )])];
        let aggregate = aggregate(&parsers, None);
        let order: Vec<&str> = aggregate
            .usage
            .by_language
            .iter()
            .map(|lines| lines.language)
            .collect();
        assert_eq!(order, vec!["Markdown", "Go", "Rust"]);
    }

    #[test]
    fn productivity_sorts_by_language_total_then_lines_then_model() {
        let parsers = [
            parser(vec![(
                hour(0),
                MODEL,
                with_lines(bucket(1, 0, 0, 0.0, 0), "Go", 20, 0),
            )]),
            parser(vec![(
                hour(0),
                SONNET,
                with_lines(bucket(1, 0, 0, 0.0, 0), "Go", 10, 0),
            )]),
            parser(vec![(
                hour(0),
                MODEL,
                with_lines(bucket(1, 0, 0, 0.0, 0), "Markdown", 25, 0),
            )]),
        ];
        let aggregate = aggregate(&parsers, None);
        let order: Vec<(&str, &str)> = aggregate
            .productivity
            .iter()
            .map(|row| (row.language, row.model.as_str()))
            .collect();
        assert_eq!(
            order,
            vec![("Go", MODEL), ("Go", SONNET), ("Markdown", MODEL)]
        );
    }

    #[test]
    fn productivity_row_serialises_metric_keys() {
        let parsers = [parser(vec![(
            hour(0),
            MODEL,
            with_lines(bucket(1, 0, 0, 0.0, MINUTE_MS), "Go", 10, 0),
        )])];
        let aggregate = aggregate(&parsers, None);
        let json = serde_json::to_value(&aggregate.productivity[0]).expect("serialise row");
        assert!(json.get("minutesPer100Loc").is_some() && json.get("shareOfLanguage").is_some());
    }
}
