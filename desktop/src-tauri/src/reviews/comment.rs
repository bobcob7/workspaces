//! Comment (ADR 0041): placing the findings decided `comment` on a pull
//! request's diff, the preview, and posting them as one review with `gh
//! api`: a pending review with the line comments, a GraphQL thread per file
//! comment, then submitting it. A failed thread or submit deletes the
//! pending review.

use super::github::{self, split_repo};
use super::report::{self, Acted, Cache};
use crate::runner::{self, AppError, Outcome, Runner};
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::collections::{BTreeMap, BTreeSet};

/// The new side's line ranges of each changed file, inclusive, by path.
/// `None` is a file GitHub gave no patch for (binary, or too large).
pub type Diff = BTreeMap<String, Option<Vec<(u64, u64)>>>;

#[derive(Debug, Deserialize)]
struct PrFile {
    filename: String,
    #[serde(default)]
    patch: Option<String>,
}

/// The new side's line ranges of a unified diff's hunks, from each `@@ -a,b
/// +c,d @@` header. A hunk with no new lines (`+c,0`) has no range.
pub fn hunk_ranges(patch: &str) -> Vec<(u64, u64)> {
    patch
        .lines()
        .filter_map(|line| {
            let rest = line.strip_prefix("@@ ")?;
            let new = rest.split_whitespace().find(|p| p.starts_with('+'))?;
            let mut parts = new[1..].splitn(2, ',');
            let start: u64 = parts.next()?.parse().ok()?;
            let count: u64 = match parts.next() {
                Some(n) => n.parse().ok()?,
                None => 1,
            };
            (count > 0).then(|| (start, start + count - 1))
        })
        .collect()
}

/// Reads `gh api …/pulls/<n>/files --paginate`, which prints one JSON array
/// per page, back to back.
pub fn parse_files(stdout: &[u8]) -> Result<Diff, AppError> {
    let mut diff = Diff::new();
    for page in serde_json::Deserializer::from_slice(stdout).into_iter::<Vec<PrFile>>() {
        let page = page.map_err(|err| {
            AppError::new(
                "unexpected_output",
                format!("gh api printed unexpected JSON: {err}"),
            )
        })?;
        for file in page {
            let ranges = file.patch.as_deref().map(hunk_ranges);
            diff.insert(file.filename, ranges);
        }
    }
    Ok(diff)
}

#[derive(Debug, Clone, Deserialize)]
struct Anchor {
    file: String,
    line: u64,
    #[serde(default)]
    end_line: Option<u64>,
}

impl Anchor {
    fn end(&self) -> u64 {
        self.end_line
            .filter(|e| *e > self.line)
            .unwrap_or(self.line)
    }

    fn label(&self) -> String {
        match (self.line, self.end()) {
            (0, _) => self.file.clone(),
            (start, end) if end > start => format!("{}:{start}-{end}", self.file),
            (start, _) => format!("{}:{start}", self.file),
        }
    }
}

#[derive(Debug, Clone, Deserialize)]
struct Suggestion {
    summary: String,
}

#[derive(Debug, Clone, Deserialize)]
struct FindingDecision {
    decision: String,
    #[serde(default)]
    suggestion: Option<usize>,
    #[serde(default)]
    note: String,
    #[serde(default)]
    done: Option<Value>,
}

/// The parts of a report's finding that a comment needs.
#[derive(Debug, Clone, Deserialize)]
struct Finding {
    id: String,
    profile: String,
    priority: u32,
    category: String,
    body: String,
    #[serde(default)]
    anchors: Vec<Anchor>,
    #[serde(default)]
    suggestions: Vec<Suggestion>,
    #[serde(default)]
    decision: Option<FindingDecision>,
}

#[derive(Debug, Deserialize)]
struct Findings {
    comments: Vec<Finding>,
}

/// Where a finding lands on the pull request.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub enum Kind {
    Line,
    File,
}

/// One comment of the review, as the preview shows and edits it.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct DraftComment {
    pub finding: String,
    pub kind: Kind,
    pub path: String,
    pub line: Option<u64>,
    pub start_line: Option<u64>,
    pub body: String,
}

/// A finding's place: a line comment on its first anchor inside a hunk; else
/// a file comment on its first anchor in a changed file; else the body.
fn place(anchors: &[Anchor], diff: &Diff) -> Option<(Kind, String, Option<u64>, Option<u64>)> {
    let in_hunk = anchors.iter().find(|a| {
        a.line > 0
            && diff.get(&a.file).is_some_and(|ranges| {
                ranges.as_ref().is_some_and(|ranges| {
                    ranges
                        .iter()
                        .any(|(start, end)| *start <= a.line && a.end() <= *end)
                })
            })
    });
    if let Some(a) = in_hunk {
        let start = (a.end() > a.line).then_some(a.line);
        return Some((Kind::Line, a.file.clone(), Some(a.end()), start));
    }
    anchors
        .iter()
        .find(|a| diff.contains_key(&a.file))
        .map(|a| (Kind::File, a.file.clone(), None, None))
}

fn is_mention_char(c: char) -> bool {
    c.is_ascii_alphanumeric() || c == '-'
}

/// The length of a mention at the start of `rest` (after its `@`): a login,
/// optionally `/team`.
fn mention_len(rest: &str) -> usize {
    let login = rest
        .char_indices()
        .take_while(|(i, c)| {
            if *i == 0 {
                c.is_ascii_alphanumeric()
            } else {
                is_mention_char(*c)
            }
        })
        .count();
    if login == 0 {
        return 0;
    }
    let after = &rest[login..];
    let Some(team) = after.strip_prefix('/') else {
        return login;
    };
    let team_len = team
        .chars()
        .take_while(|c| c.is_ascii_alphanumeric() || matches!(c, '-' | '_' | '.'))
        .count();
    if team_len == 0 {
        login
    } else {
        login + 1 + team_len
    }
}

/// Wraps one line's mentions in backticks, leaving code spans alone.
fn escape_line(line: &str, out: &mut String) {
    let mut rest = line;
    let mut prev: Option<char> = None;
    while let Some(c) = rest.chars().next() {
        if c == '`' {
            let ticks = rest.chars().take_while(|&t| t == '`').count();
            let fence = &rest[..ticks];
            let span_end = rest[ticks..].find(fence).map(|i| ticks + i + ticks);
            let take = span_end.unwrap_or(ticks);
            out.push_str(&rest[..take]);
            prev = Some('`');
            rest = &rest[take..];
            continue;
        }
        let starts_word = prev.map_or(true, |p| {
            !(p.is_ascii_alphanumeric() || p == '_' || p == '`')
        });
        if c == '@' && starts_word {
            let len = mention_len(&rest[1..]);
            if len > 0 {
                out.push('`');
                out.push_str(&rest[..=len]);
                out.push('`');
                prev = Some('`');
                rest = &rest[1 + len..];
                continue;
            }
        }
        out.push(c);
        prev = Some(c);
        rest = &rest[c.len_utf8()..];
    }
}

/// Wraps `@name` in backticks, outside code, so agent-written text can't
/// mention anyone (ADR 0041).
pub fn escape_mentions(text: &str) -> String {
    let mut out = String::with_capacity(text.len());
    let mut fenced = false;
    for (i, line) in text.split('\n').enumerate() {
        if i > 0 {
            out.push('\n');
        }
        let trimmed = line.trim_start();
        if trimmed.starts_with("```") || trimmed.starts_with("~~~") {
            fenced = !fenced;
            out.push_str(line);
            continue;
        }
        if fenced {
            out.push_str(line);
        } else {
            escape_line(line, &mut out);
        }
    }
    out
}

fn band(priority: u32) -> &'static str {
    match priority {
        9.. => "Must fix",
        7..=8 => "Should fix",
        4..=6 => "Worth fixing",
        _ => "Optional",
    }
}

/// A comment's text: band and category, the body, the chosen suggestion
/// (else the reviewer's first), the person's note, and the qualified id.
fn comment_body(finding: &Finding) -> String {
    let mut text = format!(
        "**{} · {}** — {}",
        band(finding.priority),
        finding.category,
        escape_mentions(finding.body.trim())
    );
    let decision = finding.decision.as_ref();
    let chosen = decision
        .and_then(|d| d.suggestion)
        .and_then(|i| finding.suggestions.get(i))
        .or_else(|| finding.suggestions.first());
    if let Some(s) = chosen {
        text.push_str(&format!(
            "\n\n**Suggested:** {}",
            escape_mentions(s.summary.trim())
        ));
    }
    if let Some(note) = decision.map(|d| d.note.trim()).filter(|n| !n.is_empty()) {
        text.push_str(&format!("\n\n{note}"));
    }
    text.push_str(&format!("\n\n<sub>ws review · `{}`</sub>", finding.id));
    text
}

fn plural(count: usize, word: &str) -> String {
    format!("{count} {word}{}", if count == 1 { "" } else { "s" })
}

/// The review's comments and its body for the findings decided `comment`
/// and not done.
#[derive(Debug, Clone, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Placed {
    pub body: String,
    pub comments: Vec<DraftComment>,
    /// Every finding posted, wherever it lands.
    pub findings: Vec<String>,
    /// How many are only in the body.
    pub in_body: usize,
}

/// Places the report's findings decided `comment`, and writes the summary:
/// the count and profiles, then any finding with no line or file.
pub fn place_findings(report: &Value, diff: &Diff) -> Result<Placed, AppError> {
    let parsed: Findings = serde_json::from_value(report.clone()).map_err(|err| {
        AppError::new(
            "unexpected_output",
            format!("ws printed an unexpected report: {err}"),
        )
    })?;
    let chosen: Vec<&Finding> = parsed
        .comments
        .iter()
        .filter(|f| {
            f.decision.as_ref().is_some_and(|d| {
                d.decision == "comment" && d.done.as_ref().map_or(true, Value::is_null)
            })
        })
        .collect();
    let mut comments = Vec::new();
    let mut body_parts = Vec::new();
    for finding in &chosen {
        let text = comment_body(finding);
        match place(&finding.anchors, diff) {
            Some((kind, path, line, start_line)) => comments.push(DraftComment {
                finding: finding.id.clone(),
                kind,
                path,
                line,
                start_line,
                body: text,
            }),
            None => {
                let at = finding
                    .anchors
                    .first()
                    .map_or_else(String::new, |a| format!("`{}`\n\n", a.label()));
                body_parts.push(format!("{at}{text}"));
            }
        }
    }
    let profiles: BTreeSet<&str> = chosen.iter().map(|f| f.profile.as_str()).collect();
    let profiles = profiles.into_iter().collect::<Vec<_>>().join(", ");
    let mut body = format!(
        "{} from an agent review ({profiles}).",
        plural(chosen.len(), "finding")
    );
    if !comments.is_empty() {
        body.push_str(" Details inline.");
    }
    let in_body = body_parts.len();
    for part in body_parts {
        body.push_str("\n\n---\n\n");
        body.push_str(&part);
    }
    Ok(Placed {
        body,
        comments,
        findings: chosen.iter().map(|f| f.id.clone()).collect(),
        in_body,
    })
}

/// What the preview shows before anything is posted.
#[derive(Debug, Clone, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct CommentPlan {
    pub number: u64,
    /// The reviewed commit, which the comments are pinned to.
    pub commit_id: String,
    /// The pull request's head as last checked.
    pub head: String,
    /// The reviewed commit isn't the head, so GitHub may show the comments
    /// as outdated.
    pub outdated: bool,
    /// Who posts, as `gh` is signed in.
    pub login: Option<String>,
    /// An unsubmitted review of yours on the pull request, which blocks
    /// creating another.
    pub pending_review: Option<String>,
    pub pr_url: String,
    #[serde(flatten)]
    pub placed: Placed,
}

fn strings(args: &[&str]) -> Vec<String> {
    args.iter().map(|s| (*s).to_string()).collect()
}

/// `--hostname <host>` and `repos/<owner>/<name>/pulls/<n>`.
fn pr_path(repo: &str, number: u64) -> Result<(String, String), AppError> {
    let (host, owner, name) = split_repo(repo)
        .ok_or_else(|| AppError::new("usage", format!("{repo} isn't host/owner/name")))?;
    Ok((
        host.to_string(),
        format!("repos/{owner}/{name}/pulls/{number}"),
    ))
}

pub fn files_args(repo: &str, number: u64) -> Result<Vec<String>, AppError> {
    let (host, path) = pr_path(repo, number)?;
    Ok(strings(&[
        "api",
        "--hostname",
        &host,
        &format!("{path}/files"),
        "--paginate",
    ]))
}

pub fn pending_args(repo: &str, number: u64) -> Result<Vec<String>, AppError> {
    let (host, path) = pr_path(repo, number)?;
    Ok(strings(&[
        "api",
        "--hostname",
        &host,
        &format!("{path}/reviews"),
        "--paginate",
        "--jq",
        r#".[] | select(.state == "PENDING") | .html_url"#,
    ]))
}

pub fn create_args(repo: &str, number: u64) -> Result<Vec<String>, AppError> {
    let (host, path) = pr_path(repo, number)?;
    Ok(strings(&[
        "api",
        "--hostname",
        &host,
        "--method",
        "POST",
        &format!("{path}/reviews"),
        "--input",
        "-",
    ]))
}

const ADD_FILE_THREAD: &str = "mutation($review: ID!, $path: String!, $body: String!) { addPullRequestReviewThread(input: {pullRequestReviewId: $review, path: $path, body: $body, subjectType: FILE}) { thread { id } } }";

pub fn file_thread_args(
    repo: &str,
    review_node: &str,
    comment: &DraftComment,
) -> Result<Vec<String>, AppError> {
    let (host, _, _) = split_repo(repo)
        .ok_or_else(|| AppError::new("usage", format!("{repo} isn't host/owner/name")))?;
    Ok(strings(&[
        "api",
        "--hostname",
        host,
        "graphql",
        "-f",
        &format!("query={ADD_FILE_THREAD}"),
        "-f",
        &format!("review={review_node}"),
        "-f",
        &format!("path={}", comment.path),
        "-f",
        &format!("body={}", comment.body),
    ]))
}

pub fn submit_args(
    repo: &str,
    number: u64,
    review_id: u64,
    event: Event,
) -> Result<Vec<String>, AppError> {
    let (host, path) = pr_path(repo, number)?;
    Ok(strings(&[
        "api",
        "--hostname",
        &host,
        "--method",
        "POST",
        &format!("{path}/reviews/{review_id}/events"),
        "-f",
        &format!("event={}", event.as_str()),
    ]))
}

pub fn delete_args(repo: &str, number: u64, review_id: u64) -> Result<Vec<String>, AppError> {
    let (host, path) = pr_path(repo, number)?;
    Ok(strings(&[
        "api",
        "--hostname",
        &host,
        "--method",
        "DELETE",
        &format!("{path}/reviews/{review_id}"),
    ]))
}

/// The review's event; Comment unless the person picks another.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum Event {
    Comment,
    RequestChanges,
    Approve,
}

impl Event {
    fn as_str(self) -> &'static str {
        match self {
            Event::Comment => "COMMENT",
            Event::RequestChanges => "REQUEST_CHANGES",
            Event::Approve => "APPROVE",
        }
    }
}

/// The review as the person confirmed it in the preview.
#[derive(Debug, Clone, PartialEq, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct PostRequest {
    pub number: u64,
    pub commit_id: String,
    pub body: String,
    pub event: Event,
    pub comments: Vec<DraftComment>,
    pub findings: Vec<String>,
}

/// The create request's body: the commit, the summary, and the line
/// comments, with no `event`, which leaves the review pending.
pub fn create_body(req: &PostRequest) -> Value {
    let comments: Vec<Value> = req
        .comments
        .iter()
        .filter(|c| c.kind == Kind::Line)
        .map(|c| {
            let mut comment = json!({
                "path": c.path,
                "line": c.line,
                "side": "RIGHT",
                "body": c.body,
            });
            if let Some(start) = c.start_line {
                comment["start_line"] = json!(start);
                comment["start_side"] = json!("RIGHT");
            }
            comment
        })
        .collect();
    json!({ "commit_id": req.commit_id, "body": req.body, "comments": comments })
}

/// `gh api`'s failure: its first stderr line, and the messages GitHub put
/// in the response body it printed.
fn api_error(outcome: &Outcome) -> AppError {
    let mut err = github::gh_error(&outcome.stderr);
    let details: Vec<String> = serde_json::from_slice::<Value>(&outcome.stdout)
        .ok()
        .and_then(|v| v.get("errors").and_then(Value::as_array).cloned())
        .unwrap_or_default()
        .iter()
        .filter_map(|e| {
            e.as_str()
                .map(str::to_string)
                .or_else(|| e.get("message").and_then(Value::as_str).map(str::to_string))
        })
        .collect();
    if !details.is_empty() {
        err.message = format!("{} {}", err.message, details.join("; "));
    }
    err
}

/// Whether GitHub refused a second pending review on the pull request.
fn has_pending(outcome: &Outcome) -> bool {
    let text = format!(
        "{} {}",
        outcome.stderr,
        String::from_utf8_lossy(&outcome.stdout)
    )
    .to_ascii_lowercase();
    text.contains("one pending review")
}

pub const PENDING_MESSAGE: &str =
    "You have an unsubmitted review on this pull request. Finish or discard it on GitHub first.";

/// The PR's changed files and their hunks.
pub async fn read_diff(runner: &Runner, repo: &str, number: u64) -> Result<Diff, AppError> {
    let outcome = github::gh(runner, &files_args(repo, number)?).await?;
    if !outcome.success {
        return Err(api_error(&outcome));
    }
    parse_files(&outcome.stdout)
}

/// Your pending review on the pull request, if any.
pub async fn pending_review(
    runner: &Runner,
    repo: &str,
    number: u64,
) -> Result<Option<String>, AppError> {
    let outcome = github::gh(runner, &pending_args(repo, number)?).await?;
    if !outcome.success {
        return Err(api_error(&outcome));
    }
    Ok(String::from_utf8_lossy(&outcome.stdout)
        .lines()
        .map(str::trim)
        .find(|l| !l.is_empty())
        .map(str::to_string))
}

/// The pull request and review the preview needs.
pub struct Subject<'a> {
    pub repo: &'a str,
    pub git_ref: &'a str,
    pub number: u64,
    pub head: &'a str,
    pub pr_url: &'a str,
    pub login: Option<String>,
}

/// Reads the report, the diff, and any pending review, and places the
/// findings for the preview.
pub async fn plan(
    runner: &Runner,
    cache: &Cache,
    subject: Subject<'_>,
) -> Result<CommentPlan, AppError> {
    let report = report::read(runner, cache, subject.repo, subject.git_ref, false).await?;
    let diff = read_diff(runner, subject.repo, subject.number).await?;
    let pending = pending_review(runner, subject.repo, subject.number).await?;
    let placed = place_findings(&report, &diff)?;
    Ok(CommentPlan {
        number: subject.number,
        commit_id: subject.git_ref.to_string(),
        head: subject.head.to_string(),
        outdated: !super::names::same_commit(subject.git_ref, subject.head),
        login: subject.login,
        pending_review: pending,
        pr_url: subject.pr_url.to_string(),
        placed,
    })
}

#[derive(Debug, Deserialize)]
struct CreatedReview {
    id: u64,
    node_id: String,
    html_url: String,
}

/// A posted review, and the report with its findings done.
#[derive(Debug, Clone, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Posted {
    pub url: String,
    pub report: Option<Value>,
    /// Set when the review was posted but `ws review acted` failed.
    pub warning: Option<String>,
}

/// Adds the file threads to the pending review and submits it.
async fn finish(
    runner: &Runner,
    repo: &str,
    req: &PostRequest,
    created: &CreatedReview,
) -> Result<(), AppError> {
    for comment in req.comments.iter().filter(|c| c.kind == Kind::File) {
        let outcome =
            github::gh(runner, &file_thread_args(repo, &created.node_id, comment)?).await?;
        if !outcome.success {
            return Err(api_error(&outcome));
        }
    }
    let outcome = github::gh(
        runner,
        &submit_args(repo, req.number, created.id, req.event)?,
    )
    .await?;
    if !outcome.success {
        return Err(api_error(&outcome));
    }
    Ok(())
}

/// Posts the review in three steps (ADR 0041), deletes the pending review
/// if a later step fails, and records the findings as commented.
pub async fn post(
    runner: &Runner,
    cache: &Cache,
    repo: &str,
    git_ref: &str,
    req: &PostRequest,
) -> Result<Posted, AppError> {
    if req.findings.is_empty() {
        return Err(AppError::new("usage", "no findings to post"));
    }
    let input = serde_json::to_vec(&create_body(req))
        .map_err(|err| AppError::new("internal", err.to_string()))?;
    let outcome =
        github::gh_with_input(runner, &create_args(repo, req.number)?, Some(&input)).await?;
    if !outcome.success {
        if has_pending(&outcome) {
            return Err(AppError::new("review_pending", PENDING_MESSAGE));
        }
        return Err(api_error(&outcome));
    }
    let created: CreatedReview = runner::parse_json(&outcome.stdout, "gh api")?;
    if let Err(mut err) = finish(runner, repo, req, &created).await {
        let deleted = github::gh(runner, &delete_args(repo, req.number, created.id)?).await;
        let cleaned = deleted.as_ref().is_ok_and(|o| o.success);
        err.message = if cleaned {
            format!("{} Nothing was posted.", err.message)
        } else {
            format!(
                "{} The pending review couldn't be deleted; discard it on GitHub.",
                err.message
            )
        };
        return Err(err);
    }
    let url = created.html_url;
    match report::acted(
        runner,
        cache,
        repo,
        git_ref,
        &req.findings,
        Acted::Commented(&url),
    )
    .await
    {
        Ok(report) => Ok(Posted {
            url,
            report: Some(report),
            warning: None,
        }),
        Err(err) => Ok(Posted {
            url,
            report: None,
            warning: Some(format!(
                "Posted, but recording it in the review failed: {}",
                err.message
            )),
        }),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::PermissionsExt;

    const REPO: &str = "github.com/bobcob7/buff-bear";
    const REF: &str = "3f2a9c1e0b6d4a2f9e8c7b6a5d4c3b2a1f0e9d8c";

    /// Two pages of `pulls/42/files`, as `gh api --paginate` prints them.
    const FILES: &str = r#"[{"sha":"a","filename":"ratelimit/limiter.go","status":"added","additions":60,"deletions":0,"changes":60,"patch":"@@ -0,0 +1,60 @@\n+package ratelimit\n+"},{"sha":"b","filename":"ratelimit/middleware.go","status":"modified","additions":4,"deletions":1,"changes":5,"patch":"@@ -10,6 +10,9 @@ import (\n ctx\n-old\n+new\n@@ -40,3 +43,0 @@ func x() {\n-gone"}][{"sha":"c","filename":"assets/logo.png","status":"added","additions":0,"deletions":0,"changes":0}]"#;

    fn diff() -> Diff {
        parse_files(FILES.as_bytes()).unwrap()
    }

    fn finding(id: &str, anchors: Value, decision: Value) -> Value {
        json!({
            "id": id, "profile": id.split(':').next().unwrap(), "priority": 7,
            "category": "correctness", "body": "The sweeper never exits. Each test leaks one.",
            "code": null, "anchors": anchors,
            "suggestions": [{"summary": "Take a context in New", "effort": "small", "scope": "local", "pros": [], "cons": [], "code": null}],
            "decision": decision
        })
    }

    fn comment_decision() -> Value {
        json!({"decision": "comment", "suggestion": null, "note": "", "at": "t", "done": null})
    }

    fn report(comments: Vec<Value>) -> Value {
        json!({"repo": REPO, "ref": REF, "review": null, "agent": null, "profiles": [],
            "comments": comments, "superseded": [], "warnings": []})
    }

    fn anchor(file: &str, line: u64, end: Option<u64>) -> Value {
        json!([{"file": file, "line": line, "end_line": end}])
    }

    #[test]
    fn hunk_ranges_read_the_new_side_of_each_header() {
        assert_eq!(
            hunk_ranges("@@ -10,6 +10,9 @@ fn\n x\n@@ -1 +30 @@\n@@ -5,2 +40,0 @@"),
            vec![(10, 18), (30, 30)]
        );
    }

    #[test]
    fn parse_files_reads_every_page_and_marks_files_without_a_patch() {
        let diff = diff();
        assert_eq!(diff.len(), 3);
        assert_eq!(diff["ratelimit/limiter.go"], Some(vec![(1, 60)]));
        assert_eq!(diff["ratelimit/middleware.go"], Some(vec![(10, 18)]));
        assert_eq!(diff["assets/logo.png"], None);
    }

    #[test]
    fn a_line_in_a_hunk_is_a_line_comment() {
        let r = report(vec![finding(
            "go:a-1",
            anchor("ratelimit/limiter.go", 58, None),
            comment_decision(),
        )]);
        let placed = place_findings(&r, &diff()).unwrap();
        let c = &placed.comments[0];
        assert_eq!(
            (c.kind, c.path.as_str(), c.line, c.start_line),
            (Kind::Line, "ratelimit/limiter.go", Some(58), None)
        );
    }

    #[test]
    fn a_span_in_a_hunk_has_a_start_line() {
        let r = report(vec![finding(
            "go:a-1",
            anchor("ratelimit/middleware.go", 12, Some(15)),
            comment_decision(),
        )]);
        let c = &place_findings(&r, &diff()).unwrap().comments[0];
        assert_eq!((c.line, c.start_line), (Some(15), Some(12)));
    }

    #[test]
    fn a_span_leaving_the_hunk_is_a_file_comment() {
        let r = report(vec![finding(
            "go:a-1",
            anchor("ratelimit/middleware.go", 16, Some(25)),
            comment_decision(),
        )]);
        let c = &place_findings(&r, &diff()).unwrap().comments[0];
        assert_eq!((c.kind, c.line), (Kind::File, None));
    }

    #[test]
    fn a_later_anchor_in_a_hunk_wins_over_an_earlier_one_outside() {
        let anchors = json!([
            {"file": "ratelimit/middleware.go", "line": 2, "end_line": null},
            {"file": "ratelimit/limiter.go", "line": 3, "end_line": null}
        ]);
        let r = report(vec![finding("go:a-1", anchors, comment_decision())]);
        let c = &place_findings(&r, &diff()).unwrap().comments[0];
        assert_eq!(
            (c.kind, c.path.as_str()),
            (Kind::Line, "ratelimit/limiter.go")
        );
    }

    #[test]
    fn a_whole_file_anchor_or_a_file_without_a_patch_is_a_file_comment() {
        let r = report(vec![
            finding(
                "go:a-1",
                anchor("ratelimit/limiter.go", 0, None),
                comment_decision(),
            ),
            finding(
                "go:b-1",
                anchor("assets/logo.png", 3, None),
                comment_decision(),
            ),
        ]);
        let placed = place_findings(&r, &diff()).unwrap();
        assert!(placed.comments.iter().all(|c| c.kind == Kind::File));
        assert_eq!(placed.comments.len(), 2);
    }

    #[test]
    fn a_file_outside_the_diff_goes_in_the_body_with_its_anchor() {
        let r = report(vec![finding(
            "go:a-1",
            anchor("README.md", 4, None),
            comment_decision(),
        )]);
        let placed = place_findings(&r, &diff()).unwrap();
        assert!(placed.comments.is_empty());
        assert_eq!(placed.in_body, 1);
        assert!(placed.body.starts_with(
            "1 finding from an agent review (go).\n\n---\n\n`README.md:4`\n\n**Should fix"
        ));
        assert_eq!(placed.findings, vec!["go:a-1"]);
    }

    #[test]
    fn only_undone_comment_decisions_are_posted() {
        let done = json!({"decision": "comment", "suggestion": null, "note": "", "at": "t", "done": {"commented": "u", "at": "t"}});
        let fix = json!({"decision": "fix", "suggestion": 0, "note": "", "at": "t", "done": null});
        let r = report(vec![
            finding(
                "go:a-1",
                anchor("ratelimit/limiter.go", 1, None),
                comment_decision(),
            ),
            finding("go:b-1", anchor("ratelimit/limiter.go", 2, None), done),
            finding("go:c-1", anchor("ratelimit/limiter.go", 3, None), fix),
            finding(
                "security:d-1",
                anchor("ratelimit/limiter.go", 4, None),
                Value::Null,
            ),
        ]);
        let placed = place_findings(&r, &diff()).unwrap();
        assert_eq!(placed.findings, vec!["go:a-1"]);
        assert_eq!(
            placed.body,
            "1 finding from an agent review (go). Details inline."
        );
    }

    #[test]
    fn the_summary_names_the_count_and_profiles() {
        let r = report(vec![
            finding(
                "security:a-1",
                anchor("ratelimit/limiter.go", 1, None),
                comment_decision(),
            ),
            finding(
                "go:b-1",
                anchor("ratelimit/limiter.go", 2, None),
                comment_decision(),
            ),
        ]);
        assert_eq!(
            place_findings(&r, &diff()).unwrap().body,
            "2 findings from an agent review (go, security). Details inline."
        );
    }

    #[test]
    fn a_comment_has_band_category_body_suggestion_note_and_id() {
        let decision = json!({"decision": "comment", "suggestion": null, "note": "Please add a test.", "at": "t", "done": null});
        let r = report(vec![finding(
            "go:a-1",
            anchor("ratelimit/limiter.go", 1, None),
            decision,
        )]);
        assert_eq!(
            place_findings(&r, &diff()).unwrap().comments[0].body,
            "**Should fix · correctness** — The sweeper never exits. Each test leaks one.\n\n**Suggested:** Take a context in New\n\nPlease add a test.\n\n<sub>ws review · `go:a-1`</sub>"
        );
    }

    #[test]
    fn escape_mentions_wraps_logins_and_teams() {
        assert_eq!(
            escape_mentions("ask @alice or @org/team-x, cc @bob."),
            "ask `@alice` or `@org/team-x`, cc `@bob`."
        );
    }

    #[test]
    fn escape_mentions_leaves_emails_code_and_bare_at_alone() {
        assert_eq!(escape_mentions("mail a@b.com @ 3"), "mail a@b.com @ 3");
        assert_eq!(
            escape_mentions("use `@Override` here"),
            "use `@Override` here"
        );
        assert_eq!(
            escape_mentions("```java\n@Override\n```\n@x"),
            "```java\n@Override\n```\n`@x`"
        );
    }

    #[test]
    fn agent_text_in_a_comment_is_escaped_but_the_note_is_not() {
        let mut f = finding(
            "go:a-1",
            anchor("ratelimit/limiter.go", 1, None),
            json!({"decision": "comment", "suggestion": null, "note": "cc @carol", "at": "t", "done": null}),
        );
        f["body"] = json!("Ping @alice.");
        let body = &place_findings(&report(vec![f]), &diff()).unwrap().comments[0].body;
        assert!(body.contains("Ping `@alice`."));
        assert!(body.contains("\n\ncc @carol\n\n"));
    }

    fn request() -> PostRequest {
        PostRequest {
            number: 42,
            commit_id: REF.to_string(),
            body: "2 findings.".to_string(),
            event: Event::Comment,
            comments: vec![
                DraftComment {
                    finding: "go:a-1".to_string(),
                    kind: Kind::Line,
                    path: "ratelimit/limiter.go".to_string(),
                    line: Some(15),
                    start_line: Some(12),
                    body: "line text".to_string(),
                },
                DraftComment {
                    finding: "go:b-1".to_string(),
                    kind: Kind::File,
                    path: "assets/logo.png".to_string(),
                    line: None,
                    start_line: None,
                    body: "file text".to_string(),
                },
            ],
            findings: vec!["go:a-1".to_string(), "go:b-1".to_string()],
        }
    }

    #[test]
    fn create_body_pins_the_commit_and_leaves_the_review_pending() {
        let body = create_body(&request());
        assert_eq!(body["commit_id"], REF);
        assert!(body.get("event").is_none());
        assert_eq!(body["comments"].as_array().unwrap().len(), 1);
        assert_eq!(
            body["comments"][0],
            json!({"path": "ratelimit/limiter.go", "line": 15, "side": "RIGHT", "start_line": 12, "start_side": "RIGHT", "body": "line text"})
        );
    }

    #[test]
    fn event_parses_from_the_preview() {
        let e: Event = serde_json::from_value(json!("REQUEST_CHANGES")).unwrap();
        assert_eq!(e, Event::RequestChanges);
    }

    #[test]
    fn args_use_the_repo_host_and_pull_request() {
        assert_eq!(
            files_args("ghe.example.com/o/r", 7).unwrap().join(" "),
            "api --hostname ghe.example.com repos/o/r/pulls/7/files --paginate"
        );
        assert_eq!(
            submit_args(REPO, 42, 99, Event::Approve).unwrap().join(" "),
            "api --hostname github.com --method POST repos/bobcob7/buff-bear/pulls/42/reviews/99/events -f event=APPROVE"
        );
        assert_eq!(
            delete_args(REPO, 42, 99).unwrap().join(" "),
            "api --hostname github.com --method DELETE repos/bobcob7/buff-bear/pulls/42/reviews/99"
        );
        assert_eq!(files_args("bad", 1).unwrap_err().code, "usage");
    }

    #[test]
    fn file_thread_args_add_a_file_thread_to_the_pending_review() {
        let args = file_thread_args(REPO, "PRR_1", &request().comments[1]).unwrap();
        assert!(args.contains(&"graphql".to_string()));
        assert!(args.iter().any(|a| a.contains("subjectType: FILE")));
        assert!(args.contains(&"review=PRR_1".to_string()));
        assert!(args.contains(&"path=assets/logo.png".to_string()));
        assert!(args.contains(&"body=file text".to_string()));
    }

    /// Fake `gh` and `ws`: each logs its arguments, `gh` saves its stdin,
    /// and each runs its script. Nothing reaches GitHub.
    fn fake(gh: &str, ws: &str) -> (tempfile::TempDir, Runner) {
        let dir = tempfile::tempdir().unwrap();
        for (name, script) in [("gh", gh), ("ws", ws)] {
            let log = dir.path().join(format!("{name}.log"));
            let stdin = dir.path().join(format!("{name}.stdin"));
            let body = format!(
                "#!/bin/sh\necho \"$@\" >> '{}'\ncase \"$*\" in *'--input -'*) /bin/cat > '{}' ;; esac\n{script}\n",
                log.display(),
                stdin.display()
            );
            let path = dir.path().join(name);
            std::fs::write(&path, body).unwrap();
            std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o755)).unwrap();
        }
        let runner = Runner::new(dir.path().display().to_string());
        (dir, runner)
    }

    fn log(dir: &tempfile::TempDir, name: &str) -> Vec<String> {
        std::fs::read_to_string(dir.path().join(format!("{name}.log")))
            .unwrap_or_default()
            .lines()
            .map(str::to_string)
            .collect()
    }

    const CREATED: &str = r#"{"id":99,"node_id":"PRR_1","state":"PENDING","html_url":"https://github.com/bobcob7/buff-bear/pull/42#pullrequestreview-99"}"#;

    fn gh_script(thread: &str, submit: &str) -> String {
        format!(
            r#"case "$*" in
  *'/reviews/99/events'*) {submit} ;;
  *'--method DELETE'*) echo '{{}}' ;;
  *graphql*) {thread} ;;
  *'--input -'*) echo '{CREATED}' ;;
esac"#
        )
    }

    const WS_ACTED: &str = r#"echo '{"decisions":{},"warnings":[]}'"#;

    #[tokio::test]
    async fn post_creates_adds_file_threads_submits_and_records() {
        let ok = r#"echo '{"data":{}}'"#;
        let (dir, runner) = fake(
            &gh_script(ok, "echo '{}'"),
            &format!(
                r#"case "$2" in report) echo '{}' ;; acted) {WS_ACTED} ;; esac"#,
                report(vec![])
            ),
        );
        let posted = post(&runner, &Cache::default(), REPO, REF, &request())
            .await
            .unwrap();
        assert_eq!(
            posted.url,
            "https://github.com/bobcob7/buff-bear/pull/42#pullrequestreview-99"
        );
        assert!(posted.warning.is_none());
        let gh = log(&dir, "gh");
        assert_eq!(gh.len(), 3, "{gh:?}");
        assert!(gh[0].ends_with("repos/bobcob7/buff-bear/pulls/42/reviews --input -"));
        assert!(gh[1].contains("graphql"));
        assert!(gh[2].ends_with("/reviews/99/events -f event=COMMENT"));
        let sent: Value =
            serde_json::from_slice(&std::fs::read(dir.path().join("gh.stdin")).unwrap()).unwrap();
        assert_eq!(sent, create_body(&request()));
        let ws = log(&dir, "ws");
        assert!(ws[0].starts_with("review acted --json"));
        assert!(ws[0].ends_with("go:a-1 go:b-1 --commented https://github.com/bobcob7/buff-bear/pull/42#pullrequestreview-99"));
    }

    #[tokio::test]
    async fn a_failed_file_thread_deletes_the_pending_review_and_records_nothing() {
        let fail = r#"echo 'gh: Could not resolve path' >&2; exit 1"#;
        let (dir, runner) = fake(&gh_script(fail, "echo '{}'"), WS_ACTED);
        let err = post(&runner, &Cache::default(), REPO, REF, &request())
            .await
            .unwrap_err();
        assert!(err.message.contains("Could not resolve path"));
        assert!(err.message.ends_with("Nothing was posted."));
        let gh = log(&dir, "gh");
        assert_eq!(gh.len(), 3, "{gh:?}");
        assert!(gh[2].ends_with("--method DELETE repos/bobcob7/buff-bear/pulls/42/reviews/99"));
        assert!(log(&dir, "ws").is_empty());
    }

    #[tokio::test]
    async fn a_failed_submit_deletes_the_pending_review() {
        let fail = r#"echo '{"message":"Unprocessable Entity","errors":["Review body is required"]}'; echo 'gh: Unprocessable Entity (HTTP 422)' >&2; exit 1"#;
        let (dir, runner) = fake(&gh_script(r#"echo '{"data":{}}'"#, fail), WS_ACTED);
        let err = post(&runner, &Cache::default(), REPO, REF, &request())
            .await
            .unwrap_err();
        assert_eq!(
            err.message,
            "gh: Unprocessable Entity (HTTP 422) Review body is required Nothing was posted."
        );
        let gh = log(&dir, "gh");
        assert!(gh.last().unwrap().contains("--method DELETE"));
        assert!(log(&dir, "ws").is_empty());
    }

    #[tokio::test]
    async fn a_failed_cleanup_says_to_discard_the_review_on_github() {
        let script = r#"case "$*" in
  *'--method DELETE'*) exit 1 ;;
  *'/events'*) echo 'gh: boom' >&2; exit 1 ;;
  *graphql*) echo '{}' ;;
  *) echo '{"id":99,"node_id":"PRR_1","html_url":"u"}' ;;
esac"#;
        let (_dir, runner) = fake(script, WS_ACTED);
        let err = post(&runner, &Cache::default(), REPO, REF, &request())
            .await
            .unwrap_err();
        assert!(err.message.ends_with("discard it on GitHub."));
    }

    #[tokio::test]
    async fn an_existing_pending_review_is_its_own_error() {
        let script = r#"echo '{"message":"Unprocessable Entity","errors":["User can only have one pending review per pull request"]}'; echo 'gh: Unprocessable Entity (HTTP 422)' >&2; exit 1"#;
        let (dir, runner) = fake(script, WS_ACTED);
        let err = post(&runner, &Cache::default(), REPO, REF, &request())
            .await
            .unwrap_err();
        assert_eq!(err.code, "review_pending");
        assert_eq!(log(&dir, "gh").len(), 1);
    }

    #[tokio::test]
    async fn a_failed_record_still_reports_the_posted_review() {
        let (_dir, runner) = fake(
            &gh_script(r#"echo '{}'"#, "echo '{}'"),
            r#"echo '{"error":{"code":"finding_not_found","message":"no go:a-1"}}' >&2; exit 3"#,
        );
        let posted = post(&runner, &Cache::default(), REPO, REF, &request())
            .await
            .unwrap();
        assert!(posted.report.is_none());
        assert!(posted.warning.unwrap().contains("no go:a-1"));
    }

    #[tokio::test]
    async fn plan_places_findings_and_finds_a_pending_review() {
        let gh = format!(
            r#"case "$*" in
  *'/files'*) printf '%s' '{}' ;;
  *'/reviews'*) echo 'https://github.com/bobcob7/buff-bear/pull/42#pullrequestreview-7' ;;
esac"#,
            FILES.replace('\'', "")
        );
        let ws = format!(
            "echo '{}'",
            report(vec![finding(
                "go:a-1",
                anchor("ratelimit/limiter.go", 58, None),
                comment_decision()
            )])
        );
        let (dir, runner) = fake(&gh, &ws);
        let subject = Subject {
            repo: REPO,
            git_ref: REF,
            number: 42,
            head: "e71b0d4aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            pr_url: "https://github.com/bobcob7/buff-bear/pull/42",
            login: Some("bobcob7".to_string()),
        };
        let plan = plan(&runner, &Cache::default(), subject).await.unwrap();
        assert!(plan.outdated);
        assert_eq!(plan.commit_id, REF);
        assert_eq!(
            plan.pending_review.as_deref(),
            Some("https://github.com/bobcob7/buff-bear/pull/42#pullrequestreview-7")
        );
        assert_eq!(plan.placed.comments[0].kind, Kind::Line);
        let gh = log(&dir, "gh");
        assert!(gh[1].contains(r#"select(.state == "PENDING")"#));
    }
}
