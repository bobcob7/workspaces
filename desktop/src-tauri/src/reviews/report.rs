//! A review's combined report and the decisions on its findings (ADR 0040):
//! `ws review report --json`, cached by repo and commit for the session, and
//! `ws review decide`.

use crate::commands::env_args;
use crate::pty::remote_env;
use crate::runner::{AppError, Runner};
use serde::Deserialize;
use serde_json::{Map, Value};
use std::collections::HashMap;
use std::path::{Path, PathBuf};
use std::sync::Mutex;

/// A review's cache key: `repo@ref`, or `env:<name>:repo@ref` on an
/// environment, so two machines' reviews never share a report.
pub type ReportKey = String;

/// Where a review lives: this machine, found by repo and commit, or an
/// environment, found by its review id there (RL8).
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct Origin<'a> {
    environment: Option<&'a str>,
    review_id: Option<&'a str>,
}

impl<'a> Origin<'a> {
    /// This machine.
    pub const LOCAL: Origin<'static> = Origin {
        environment: None,
        review_id: None,
    };

    /// The origin the command's arguments name. A remote one needs the
    /// review's id, which `ws` takes in place of `--repo` and `--ref`.
    pub fn new(
        environment: Option<&'a str>,
        review_id: Option<&'a str>,
    ) -> Result<Origin<'a>, AppError> {
        let Some(environment) = remote_env(environment) else {
            return Ok(Origin::LOCAL);
        };
        crate::pty::check_env_name(environment)?;
        let review_id = review_id.map(str::trim).filter(|id| !id.is_empty());
        let Some(review_id) = review_id else {
            return Err(AppError::new(
                "bad_args",
                format!("a review on {environment} needs its review id"),
            ));
        };
        Ok(Origin {
            environment: Some(environment),
            review_id: Some(review_id),
        })
    }

    /// The cache key of the review's report.
    pub fn key(&self, repo: &str, git_ref: &str) -> ReportKey {
        match self.environment {
            Some(env) => format!("env:{env}:{repo}@{git_ref}"),
            None => format!("{repo}@{git_ref}"),
        }
    }
}

/// Reports as `ws review report --json` printed them, by repo and commit,
/// for the session.
#[derive(Debug, Default)]
pub struct Cache {
    reports: Mutex<HashMap<ReportKey, Value>>,
}

impl Cache {
    pub fn get(&self, key: &ReportKey) -> Option<Value> {
        self.lock().get(key).cloned()
    }

    pub fn put(&self, key: ReportKey, report: Value) {
        self.lock().insert(key, report);
    }

    /// Sets the decisions `ws review decide` printed on the cached report's
    /// findings, and returns the report.
    pub fn apply(&self, key: &ReportKey, decisions: &Map<String, Value>) -> Option<Value> {
        let mut reports = self.lock();
        let report = reports.get_mut(key)?;
        apply_decisions(report, decisions);
        Some(report.clone())
    }

    fn lock(&self) -> std::sync::MutexGuard<'_, HashMap<ReportKey, Value>> {
        self.reports
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
    }
}

/// What `ws review decide` records for findings.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "camelCase")]
pub enum Decision {
    Comment,
    Fix,
    Skip,
    Clear,
}

/// One click's decision on one or more findings.
#[derive(Debug, Clone, PartialEq, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct DecideRequest {
    pub findings: Vec<String>,
    pub decision: Decision,
    #[serde(default)]
    pub suggestion: Option<u32>,
    #[serde(default)]
    pub note: String,
}

fn subject_args(repo: &str, git_ref: &str) -> [String; 4] {
    [
        "--repo".to_string(),
        repo.to_string(),
        "--ref".to_string(),
        git_ref.to_string(),
    ]
}

fn strings(parts: &[&str]) -> Vec<String> {
    parts.iter().map(|p| (*p).to_string()).collect()
}

/// The start of a `ws review <op>` command: its words, then `--env=<name>`
/// on an environment, else `--repo R --ref SHA` when `subject` is set.
fn command(
    words: &[&str],
    repo: &str,
    git_ref: &str,
    origin: Origin<'_>,
    subject: bool,
) -> Vec<String> {
    let mut args = strings(words);
    args.extend(env_args(origin.environment));
    if subject && origin.environment.is_none() {
        args.extend(subject_args(repo, git_ref));
    }
    args
}

/// What names the review: `--repo R --ref SHA` here, or `-- <id>` on an
/// environment, after `findings`. The `--` keeps an id that starts with a
/// dash from becoming a flag.
fn name_review(args: &mut Vec<String>, origin: Origin<'_>, findings: &[String]) {
    if let Some(id) = origin.review_id {
        args.extend(["--".to_string(), id.to_string()]);
    }
    args.extend(findings.iter().cloned());
}

/// `ws review report --json --repo R --ref SHA`, or `ws review report
/// --json --env=E -- <id>` on an environment.
pub fn report_args(repo: &str, git_ref: &str, origin: Origin<'_>) -> Vec<String> {
    let mut args = command(&["review", "report", "--json"], repo, git_ref, origin, true);
    name_review(&mut args, origin, &[]);
    args
}

/// `ws review report --format html --repo R --ref SHA`, or with
/// `--env=E -- <id>`.
pub fn html_args(repo: &str, git_ref: &str, origin: Origin<'_>) -> Vec<String> {
    let mut args = command(
        &["review", "report", "--format", "html"],
        repo,
        git_ref,
        origin,
        true,
    );
    name_review(&mut args, origin, &[]);
    args
}

/// `ws review decide --json --repo R --ref SHA <finding>... --<decision>
/// [--suggestion N] [--note T]`. A suggestion goes only with Fix, and a note
/// not with Clear, as `ws` requires.
pub fn decide_args(
    repo: &str,
    git_ref: &str,
    origin: Origin<'_>,
    req: &DecideRequest,
) -> Result<Vec<String>, AppError> {
    if req.findings.is_empty() {
        return Err(AppError::new("usage", "no findings to decide"));
    }
    let mut args = command(&["review", "decide", "--json"], repo, git_ref, origin, true);
    if origin.environment.is_none() {
        args.extend(req.findings.iter().cloned());
    }
    let flag = match req.decision {
        Decision::Comment => "--comment",
        Decision::Fix => "--fix",
        Decision::Skip => "--skip",
        Decision::Clear => "--clear",
    };
    args.push(flag.to_string());
    if let (Decision::Fix, Some(n)) = (req.decision, req.suggestion) {
        args.extend(["--suggestion".to_string(), n.to_string()]);
    }
    let note = req.note.trim();
    if req.decision != Decision::Clear && !note.is_empty() {
        args.extend(["--note".to_string(), note.to_string()]);
    }
    if origin.environment.is_some() {
        name_review(&mut args, origin, &req.findings);
    }
    Ok(args)
}

/// Sets each named finding's `decision`; superseded findings never carry
/// one.
pub fn apply_decisions(report: &mut Value, decisions: &Map<String, Value>) {
    let Some(comments) = report.get_mut("comments").and_then(Value::as_array_mut) else {
        return;
    };
    for comment in comments {
        let Some(id) = comment.get("id").and_then(Value::as_str) else {
            continue;
        };
        if let Some(decision) = decisions.get(id) {
            comment["decision"] = decision.clone();
        }
    }
}

/// Runs `ws review report --json`, unless `refresh` is false and the
/// session already has the report.
pub async fn read(
    runner: &Runner,
    cache: &Cache,
    repo: &str,
    git_ref: &str,
    origin: Origin<'_>,
    refresh: bool,
) -> Result<Value, AppError> {
    let key = origin.key(repo, git_ref);
    if !refresh {
        if let Some(report) = cache.get(&key) {
            return Ok(report);
        }
    }
    let report: Value = runner
        .ws_json_as_given(&report_args(repo, git_ref, origin))
        .await?;
    cache.put(key, report.clone());
    Ok(report)
}

#[derive(Deserialize)]
struct Decided {
    decisions: Map<String, Value>,
}

/// Runs `ws review decide` for one click and returns the report with the
/// decisions it printed.
pub async fn decide(
    runner: &Runner,
    cache: &Cache,
    repo: &str,
    git_ref: &str,
    origin: Origin<'_>,
    req: &DecideRequest,
) -> Result<Value, AppError> {
    let args = decide_args(repo, git_ref, origin, req)?;
    record(runner, cache, repo, git_ref, origin, &args).await
}

/// How decided findings were acted on (ADR 0041).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Acted<'a> {
    /// Posted in the review at this URL.
    Commented(&'a str),
    /// Sent to this agent to fix.
    FixSent(&'a str),
}

/// `ws review acted --json --repo R --ref SHA <finding>... (--commented URL
/// | --fix-sent AGENT)`, or `--env=E (--commented URL | --fix-sent AGENT)
/// -- <id> <finding>...` on an environment.
pub fn acted_args(
    repo: &str,
    git_ref: &str,
    origin: Origin<'_>,
    findings: &[String],
    acted: Acted<'_>,
) -> Result<Vec<String>, AppError> {
    if findings.is_empty() {
        return Err(AppError::new("usage", "no findings were acted on"));
    }
    let mut args = command(&["review", "acted", "--json"], repo, git_ref, origin, true);
    let (flag, value) = match acted {
        Acted::Commented(url) => ("--commented", url),
        Acted::FixSent(agent) => ("--fix-sent", agent),
    };
    if origin.environment.is_some() {
        args.extend([flag.to_string(), value.to_string()]);
        name_review(&mut args, origin, findings);
        return Ok(args);
    }
    args.extend(findings.iter().cloned());
    args.extend([flag.to_string(), value.to_string()]);
    Ok(args)
}

/// Runs `ws review acted` and returns the report with the findings done.
pub async fn acted(
    runner: &Runner,
    cache: &Cache,
    repo: &str,
    git_ref: &str,
    origin: Origin<'_>,
    findings: &[String],
    how: Acted<'_>,
) -> Result<Value, AppError> {
    let args = acted_args(repo, git_ref, origin, findings, how)?;
    record(runner, cache, repo, git_ref, origin, &args).await
}

/// Runs a `ws review` command that prints decisions, and applies them to
/// the cached report, or reads the report when none is cached.
async fn record(
    runner: &Runner,
    cache: &Cache,
    repo: &str,
    git_ref: &str,
    origin: Origin<'_>,
    args: &[String],
) -> Result<Value, AppError> {
    let decided: Decided = runner.ws_json_as_given(args).await?;
    let key = origin.key(repo, git_ref);
    match cache.apply(&key, &decided.decisions) {
        Some(report) => Ok(report),
        None => read(runner, cache, repo, git_ref, origin, true).await,
    }
}

/// `review-buff-bear-3f2a9c1.html` for a review of buff-bear at 3f2a9c1….
pub fn html_file_name(repo: &str, git_ref: &str) -> String {
    let name: String = repo
        .rsplit('/')
        .next()
        .unwrap_or("repo")
        .chars()
        .map(|c| {
            if c.is_ascii_alphanumeric() || c == '-' || c == '_' {
                c
            } else {
                '-'
            }
        })
        .collect();
    let sha: String = git_ref
        .chars()
        .filter(char::is_ascii_hexdigit)
        .take(7)
        .collect();
    format!("review-{name}-{sha}.html")
}

/// Writes `ws review report --format html` into `dir/reviews/` and returns
/// the file's path.
pub async fn write_html(
    runner: &Runner,
    dir: &Path,
    repo: &str,
    git_ref: &str,
    origin: Origin<'_>,
) -> Result<PathBuf, AppError> {
    let outcome = runner.run("ws", &html_args(repo, git_ref, origin)).await?;
    if !outcome.success {
        return Err(crate::runner::ws_error(&outcome.stderr));
    }
    let dir = dir.join("reviews");
    let path = dir.join(html_file_name(repo, git_ref));
    let write =
        |err: std::io::Error| AppError::new("internal", format!("writing the report: {err}"));
    std::fs::create_dir_all(&dir).map_err(write)?;
    std::fs::write(&path, &outcome.stdout).map_err(write)?;
    Ok(path)
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    use std::os::unix::fs::PermissionsExt;

    const REPO: &str = "github.com/bobcob7/buff-bear";
    const REF: &str = "3f2a9c1e0b6d4a2f9e8c7b6a5d4c3b2a1f0e9d8c";

    fn request(decision: Decision, suggestion: Option<u32>, note: &str) -> DecideRequest {
        DecideRequest {
            findings: vec!["security:a-1".to_string(), "tests:b-1".to_string()],
            decision,
            suggestion,
            note: note.to_string(),
        }
    }

    fn report() -> Value {
        json!({
            "repo": REPO, "ref": REF, "review": null, "agent": null, "profiles": [],
            "comments": [
                {"id": "security:a-1", "decision": null},
                {"id": "tests:b-1", "decision": {"decision": "skip", "suggestion": null, "note": "", "at": "t", "done": null}}
            ],
            "superseded": [{"id": "#1:a-1", "decision": null}],
            "warnings": []
        })
    }

    /// A `ws` that logs its arguments and prints what `script` does.
    fn fake_ws(script: &str) -> (tempfile::TempDir, Runner) {
        let dir = tempfile::tempdir().unwrap();
        let log = dir.path().join("ws.log");
        let body = format!("#!/bin/sh\necho \"$@\" >> '{}'\n{script}\n", log.display());
        let path = dir.path().join("ws");
        std::fs::write(&path, body).unwrap();
        std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o755)).unwrap();
        crate::test_support::wait_until_executable(&path);
        let _ = std::fs::remove_file(&log);
        let runner = Runner::new(dir.path().display().to_string());
        (dir, runner)
    }

    fn calls(dir: &tempfile::TempDir) -> Vec<String> {
        std::fs::read_to_string(dir.path().join("ws.log"))
            .unwrap_or_default()
            .lines()
            .map(str::to_string)
            .collect()
    }

    #[test]
    fn report_args_name_the_review_by_repo_and_commit() {
        assert_eq!(
            report_args(REPO, REF, Origin::LOCAL).join(" "),
            format!("review report --json --repo {REPO} --ref {REF}")
        );
    }

    #[test]
    fn decide_args_batch_findings_into_one_call() {
        let args = decide_args(
            REPO,
            REF,
            Origin::LOCAL,
            &request(Decision::Comment, None, ""),
        )
        .unwrap();
        assert_eq!(
            args.join(" "),
            format!(
                "review decide --json --repo {REPO} --ref {REF} security:a-1 tests:b-1 --comment"
            )
        );
    }

    #[test]
    fn decide_args_pass_the_suggestion_with_fix() {
        let args = decide_args(
            REPO,
            REF,
            Origin::LOCAL,
            &request(Decision::Fix, Some(1), "trusted proxies only"),
        )
        .unwrap();
        assert!(args
            .join(" ")
            .ends_with("--fix --suggestion 1 --note trusted proxies only"));
    }

    #[test]
    fn decide_args_drop_a_suggestion_without_fix() {
        let args = decide_args(
            REPO,
            REF,
            Origin::LOCAL,
            &request(Decision::Skip, Some(1), ""),
        )
        .unwrap();
        assert!(!args.contains(&"--suggestion".to_string()));
    }

    #[test]
    fn decide_args_leave_the_note_off_clear() {
        let args = decide_args(
            REPO,
            REF,
            Origin::LOCAL,
            &request(Decision::Clear, None, "keep"),
        )
        .unwrap();
        assert_eq!(args.last().unwrap(), "--clear");
    }

    #[test]
    fn decide_args_refuse_no_findings() {
        let mut req = request(Decision::Skip, None, "");
        req.findings.clear();
        assert_eq!(
            decide_args(REPO, REF, Origin::LOCAL, &req)
                .unwrap_err()
                .code,
            "usage"
        );
    }

    #[test]
    fn acted_args_name_the_review_url() {
        let ids = vec!["security:a-1".to_string()];
        let args = acted_args(
            REPO,
            REF,
            Origin::LOCAL,
            &ids,
            Acted::Commented("https://x/pull/42#r1"),
        )
        .unwrap();
        assert_eq!(
            args.join(" "),
            format!(
                "review acted --json --repo {REPO} --ref {REF} security:a-1 --commented https://x/pull/42#r1"
            )
        );
    }

    #[test]
    fn acted_args_name_the_fix_agent() {
        let ids = vec!["security:a-1".to_string(), "tests:b-1".to_string()];
        let args = acted_args(
            REPO,
            REF,
            Origin::LOCAL,
            &ids,
            Acted::FixSent("fix-login-rate"),
        )
        .unwrap();
        assert!(args
            .join(" ")
            .ends_with("security:a-1 tests:b-1 --fix-sent fix-login-rate"));
    }

    #[test]
    fn acted_args_refuse_no_findings() {
        let err = acted_args(REPO, REF, Origin::LOCAL, &[], Acted::FixSent("a")).unwrap_err();
        assert_eq!(err.code, "usage");
    }

    #[test]
    fn apply_decisions_sets_and_clears_named_findings() {
        let mut report = report();
        let decisions = json!({
            "security:a-1": {"decision": "fix", "suggestion": 0, "note": "", "at": "t", "done": null},
            "tests:b-1": null
        });
        apply_decisions(&mut report, decisions.as_object().unwrap());
        assert_eq!(report["comments"][0]["decision"]["decision"], "fix");
        assert!(report["comments"][1]["decision"].is_null());
        assert!(report["superseded"][0]["decision"].is_null());
    }

    #[test]
    fn html_file_name_uses_repo_and_short_commit() {
        assert_eq!(html_file_name(REPO, REF), "review-buff-bear-3f2a9c1.html");
        assert_eq!(html_file_name("x/y/a b", "zz"), "review-a-b-.html");
    }

    #[tokio::test]
    async fn read_caches_by_repo_and_commit() {
        let (dir, runner) = fake_ws(&format!("echo '{}'", report()));
        let cache = Cache::default();
        let first = read(&runner, &cache, REPO, REF, Origin::LOCAL, false)
            .await
            .unwrap();
        let again = read(&runner, &cache, REPO, REF, Origin::LOCAL, false)
            .await
            .unwrap();
        assert_eq!(first, again);
        assert_eq!(calls(&dir).len(), 1);
    }

    #[tokio::test]
    async fn read_with_refresh_runs_ws_again() {
        let (dir, runner) = fake_ws(&format!("echo '{}'", report()));
        let cache = Cache::default();
        read(&runner, &cache, REPO, REF, Origin::LOCAL, true)
            .await
            .unwrap();
        read(&runner, &cache, REPO, REF, Origin::LOCAL, true)
            .await
            .unwrap();
        assert_eq!(calls(&dir).len(), 2);
    }

    #[tokio::test]
    async fn read_keeps_other_commits_apart() {
        let (dir, runner) = fake_ws(&format!("echo '{}'", report()));
        let cache = Cache::default();
        read(&runner, &cache, REPO, REF, Origin::LOCAL, false)
            .await
            .unwrap();
        read(&runner, &cache, REPO, &"a".repeat(40), Origin::LOCAL, false)
            .await
            .unwrap();
        assert_eq!(calls(&dir).len(), 2);
    }

    #[tokio::test]
    async fn read_returns_ws_errors() {
        let (_dir, runner) = fake_ws(
            r#"echo '{"error":{"code":"review_not_found","message":"no review"}}' >&2; exit 3"#,
        );
        let err = read(&runner, &Cache::default(), REPO, REF, Origin::LOCAL, true)
            .await
            .unwrap_err();
        assert_eq!(err.code, "review_not_found");
    }

    #[tokio::test]
    async fn decide_updates_the_cached_report_without_rereading() {
        let script = format!(
            r#"case "$2" in
  report) echo '{}' ;;
  decide) echo '{{"repo":"{REPO}","ref":"{REF}","decisions":{{"security:a-1":{{"decision":"comment","suggestion":null,"note":"","at":"t","done":null}}}},"warnings":[]}}' ;;
esac"#,
            report()
        );
        let (dir, runner) = fake_ws(&script);
        let cache = Cache::default();
        read(&runner, &cache, REPO, REF, Origin::LOCAL, false)
            .await
            .unwrap();
        let updated = decide(
            &runner,
            &cache,
            REPO,
            REF,
            Origin::LOCAL,
            &request(Decision::Comment, None, ""),
        )
        .await
        .unwrap();
        assert_eq!(updated["comments"][0]["decision"]["decision"], "comment");
        assert_eq!(cache.get(&format!("{REPO}@{REF}")).unwrap(), updated);
        let log = calls(&dir);
        assert_eq!(log.len(), 2);
        assert!(log[1].starts_with("review decide --json"));
    }

    #[tokio::test]
    async fn decide_reads_the_report_when_none_is_cached() {
        let script = format!(
            r#"case "$2" in
  report) echo '{}' ;;
  decide) echo '{{"decisions":{{}},"warnings":[]}}' ;;
esac"#,
            report()
        );
        let (dir, runner) = fake_ws(&script);
        let report = decide(
            &runner,
            &Cache::default(),
            REPO,
            REF,
            Origin::LOCAL,
            &request(Decision::Skip, None, ""),
        )
        .await
        .unwrap();
        assert_eq!(report["repo"], REPO);
        assert_eq!(calls(&dir).len(), 2);
    }

    #[tokio::test]
    async fn write_html_saves_the_report_in_the_cache_dir() {
        let (dir, runner) = fake_ws("echo '<html>report</html>'");
        let out = tempfile::tempdir().unwrap();
        let path = write_html(&runner, out.path(), REPO, REF, Origin::LOCAL)
            .await
            .unwrap();
        assert_eq!(
            path,
            out.path().join("reviews/review-buff-bear-3f2a9c1.html")
        );
        assert_eq!(
            std::fs::read_to_string(&path).unwrap(),
            "<html>report</html>\n"
        );
        assert_eq!(
            calls(&dir)[0],
            format!("review report --format html --repo {REPO} --ref {REF}")
        );
    }

    fn remote() -> Origin<'static> {
        Origin::new(Some("linux-server"), Some("review-buff-bear-42-3f2a9c1")).unwrap()
    }

    #[test]
    fn origin_refuses_an_environment_name_ws_would_misread() {
        for name in ["-x", "--env=a", "a b", "a\tb", " a"] {
            let err = Origin::new(Some(name), Some("review-1")).unwrap_err();
            assert_eq!(err.code, "bad_args", "{name:?}");
        }
        assert!(Origin::new(Some("linux-server"), Some("review-1")).is_ok());
    }

    #[test]
    fn remote_report_args_use_env_and_id() {
        assert_eq!(
            report_args(REPO, REF, remote()).join(" "),
            "review report --json --env=linux-server -- review-buff-bear-42-3f2a9c1"
        );
        assert_eq!(
            html_args(REPO, REF, remote()).join(" "),
            "review report --format html --env=linux-server -- review-buff-bear-42-3f2a9c1"
        );
    }

    #[test]
    fn remote_cache_key_has_environment() {
        assert_eq!(
            remote().key(REPO, REF),
            format!("env:linux-server:{REPO}@{REF}")
        );
        assert_eq!(Origin::LOCAL.key(REPO, REF), format!("{REPO}@{REF}"));
    }

    #[test]
    fn remote_decide_args_name_the_review_after_the_flags() {
        let args =
            decide_args(REPO, REF, remote(), &request(Decision::Fix, Some(1), "why")).unwrap();
        assert_eq!(
            args.join(" "),
            "review decide --json --env=linux-server --fix --suggestion 1 --note why -- review-buff-bear-42-3f2a9c1 security:a-1 tests:b-1"
        );
    }

    #[test]
    fn remote_acted_args_name_the_review_after_the_flags() {
        let ids = vec!["security:a-1".to_string()];
        let args = acted_args(REPO, REF, remote(), &ids, Acted::FixSent("fix-1")).unwrap();
        assert_eq!(
            args.join(" "),
            "review acted --json --env=linux-server --fix-sent fix-1 -- review-buff-bear-42-3f2a9c1 security:a-1"
        );
    }

    #[test]
    fn a_remote_review_id_that_starts_with_a_dash_stays_after_the_dashes() {
        let origin = Origin::new(Some("linux-server"), Some("--repo")).unwrap();
        let args = report_args(REPO, REF, origin);
        assert_eq!(args[args.len() - 2..], ["--", "--repo"]);
    }

    #[test]
    fn origin_without_a_review_id_is_bad_args_when_remote() {
        for id in [None, Some(""), Some("  ")] {
            let err = Origin::new(Some("linux-server"), id).unwrap_err();
            assert_eq!(err.code, "bad_args");
        }
    }

    #[test]
    fn origin_for_local_ignores_the_review_id() {
        for env in [None, Some(""), Some("local")] {
            let origin = Origin::new(env, Some("review-1")).unwrap();
            assert_eq!(origin, Origin::LOCAL);
            assert!(report_args(REPO, REF, origin).contains(&"--repo".to_string()));
        }
    }

    #[tokio::test]
    async fn remote_and_local_reports_are_cached_apart() {
        let (dir, runner) = fake_ws(&format!("echo '{}'", report()));
        let cache = Cache::default();
        read(&runner, &cache, REPO, REF, Origin::LOCAL, false)
            .await
            .unwrap();
        read(&runner, &cache, REPO, REF, remote(), false)
            .await
            .unwrap();
        read(&runner, &cache, REPO, REF, remote(), false)
            .await
            .unwrap();
        let log = calls(&dir);
        assert_eq!(log.len(), 2);
        assert!(log[1].contains("--env=linux-server"));
    }

    #[tokio::test]
    async fn remote_decide_updates_the_remote_cached_report() {
        let script = format!(
            r#"case "$2" in
  report) echo '{}' ;;
  decide) echo '{{"decisions":{{"security:a-1":{{"decision":"skip","suggestion":null,"note":"","at":"t","done":null}}}},"warnings":[]}}' ;;
esac"#,
            report()
        );
        let (dir, runner) = fake_ws(&script);
        let cache = Cache::default();
        read(&runner, &cache, REPO, REF, remote(), false)
            .await
            .unwrap();
        let updated = decide(
            &runner,
            &cache,
            REPO,
            REF,
            remote(),
            &request(Decision::Skip, None, ""),
        )
        .await
        .unwrap();
        assert_eq!(updated["comments"][0]["decision"]["decision"], "skip");
        assert!(cache.get(&format!("{REPO}@{REF}")).is_none());
        let cached = cache.get(&remote().key(REPO, REF)).unwrap();
        assert_eq!(cached["comments"][0]["decision"]["decision"], "skip");
        assert_eq!(calls(&dir).len(), 2);
    }
}
