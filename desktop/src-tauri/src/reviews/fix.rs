//! Fix (ADR 0041): whom the findings decided `fix` go to, the short message
//! that points at `ws review report --decided fix`, and starting a fix agent
//! when the requester can't take them.

use super::github::split_repo;
use super::names;
use super::report::{self, Acted, Cache};
use crate::commands::{new_agent_args, Created, NewAgent};
use crate::model::{Entry, Status};
use crate::runner::{AppError, Runner};
use serde::{Deserialize, Serialize};
use serde_json::Value;

/// A finding to fix, with the title the report shows.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FixFinding {
    pub id: String,
    pub title: String,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub enum Target {
    Requester,
    Agent,
}

/// The agent that asked for the review, and whether it can be sent to.
#[derive(Debug, Clone, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Requester {
    pub workspace_id: String,
    pub session_id: Option<String>,
    pub path: String,
    pub status: Option<Status>,
    pub available: bool,
    /// Why it can't be sent to: "removed", "stopped: resume it first".
    pub reason: Option<String>,
}

/// The fix agent `ws claude` would start.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct AgentPlan {
    pub name: String,
    pub spec: String,
    /// The branch a fork pull request's fix is pushed to instead.
    pub branch: Option<String>,
    /// The command, without the message, as the dialog shows it.
    pub command: String,
}

#[derive(Debug, Clone, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct FixPlan {
    pub findings: Vec<String>,
    pub requester: Option<Requester>,
    /// The requester when it can be sent to, else a fix agent.
    pub target: Target,
    pub agent: AgentPlan,
    pub requester_message: String,
    pub agent_message: String,
}

/// The review record's pull request, from the report's `review`.
#[derive(Debug, Clone, Deserialize)]
struct RecordPr {
    number: u64,
    #[serde(default)]
    head_branch: String,
    #[serde(default)]
    cross_repository: bool,
}

#[derive(Debug, Clone, Deserialize)]
struct Record {
    #[serde(default)]
    requester: String,
    pr: Option<RecordPr>,
}

fn record(report: &Value) -> Result<(Record, RecordPr), AppError> {
    let record: Option<Record> = report
        .get("review")
        .cloned()
        .and_then(|r| serde_json::from_value(r).ok());
    let Some(record) = record else {
        return Err(AppError::new(
            "review_not_found",
            "the review has no record, so the app can't tell whom to send fixes to",
        ));
    };
    let Some(pr) = record.pr.clone() else {
        return Err(AppError::new(
            "usage",
            "the review is of a commit, not a pull request",
        ));
    };
    Ok((record, pr))
}

/// Statuses whose session has a process, so typing into it reaches the
/// agent without resuming it.
fn is_live(status: Status) -> bool {
    matches!(
        status,
        Status::Working | Status::NeedsInput | Status::Idle | Status::Done | Status::Starting
    )
}

/// The requester as the latest listing shows it.
pub fn requester(id: &str, entries: &[Entry]) -> Requester {
    let entry = entries
        .iter()
        .find(|e| e.workspace_id.as_deref() == Some(id));
    let Some(entry) = entry else {
        return Requester {
            workspace_id: id.to_string(),
            session_id: None,
            path: String::new(),
            status: None,
            available: false,
            reason: Some("removed".to_string()),
        };
    };
    let reason = match (entry.session_id.as_ref(), entry.status) {
        (None, _) => Some("no session".to_string()),
        (Some(_), Status::Stopped) => Some("stopped: resume it first".to_string()),
        (Some(_), status) if !is_live(status) => Some("not running".to_string()),
        _ => None,
    };
    Requester {
        workspace_id: id.to_string(),
        session_id: entry.session_id.clone(),
        path: entry.path.clone(),
        status: Some(entry.status),
        available: reason.is_none(),
        reason,
    }
}

fn plural(count: usize, word: &str) -> String {
    format!("{count} {word}{}", if count == 1 { "" } else { "s" })
}

/// What the message is about.
pub struct About<'a> {
    pub repo: &'a str,
    pub number: u64,
    pub git_ref: &'a str,
    /// The review workspace, which `ws review report` takes as the review.
    pub review: &'a str,
}

/// The fix branch a fork pull request's fix goes to.
pub fn fork_branch(number: u64, git_ref: &str) -> String {
    format!("fix/pr-{number}-{}", names::short_sha(git_ref))
}

/// The message, kept short because the decisions are in the store: the
/// findings' ids and titles, the command that lists them in full with the
/// chosen fixes and notes, and what to do when done. `fork_branch` is the
/// branch to push to for a fork's pull request.
pub fn message(about: &About<'_>, findings: &[FixFinding], fork_branch: Option<&str>) -> String {
    let pr = split_repo(about.repo).map_or_else(
        || format!("{}#{}", about.repo, about.number),
        |(_, owner, name)| format!("{owner}/{name}#{}", about.number),
    );
    let width = findings.iter().map(|f| f.id.len()).max().unwrap_or(0);
    let list: Vec<String> = findings
        .iter()
        .map(|f| format!("  {:width$}  {}", f.id, f.title))
        .collect();
    let done = match fork_branch {
        None => format!(
            "When done, push, and start a new review with: ws review start --pr {} {}",
            about.number, about.repo
        ),
        Some(branch) => format!(
            "This pull request comes from a fork, so this workspace can't push to its branch. When done, commit, and push your fix to a new branch on origin: git push origin HEAD:{branch}"
        ),
    };
    format!(
        "Fix the {} decided \"fix\" in the review of {pr} at {}:\n{}\n\nThe chosen fixes and notes:\n  ws review report {} --decided fix --json\n\n{done}",
        plural(findings.len(), "finding"),
        names::short_sha(about.git_ref),
        list.join("\n"),
        about.review,
    )
}

/// The Reviews settings a fix agent starts with.
pub struct AgentSettings<'a> {
    pub model: &'a str,
    pub permission_mode: &'a str,
}

fn new_agent(agent: &AgentPlan, message: Option<&str>, settings: &AgentSettings<'_>) -> NewAgent {
    NewAgent {
        name: Some(agent.name.clone()),
        specs: vec![agent.spec.clone()],
        prompt: message.map(str::to_string),
        model: Some(settings.model.to_string()),
        permission_mode: Some(settings.permission_mode.to_string()),
        offline: false,
    }
}

/// `ws claude --json --name fix-<repo>-<n>-<sha7> <repo>@<head branch>`, or,
/// for a fork's pull request, at the reviewed commit.
pub fn agent_plan(
    repo: &str,
    git_ref: &str,
    number: u64,
    head_branch: &str,
    fork: bool,
    settings: &AgentSettings<'_>,
) -> AgentPlan {
    let at = if fork || head_branch.is_empty() {
        git_ref
    } else {
        head_branch
    };
    let mut plan = AgentPlan {
        name: names::fix_name(repo, number, git_ref),
        spec: format!("{repo}@{at}"),
        branch: fork.then(|| fork_branch(number, git_ref)),
        command: String::new(),
    };
    let mut args = new_agent_args(&new_agent(&plan, None, settings));
    args.insert(1, "--json".to_string());
    plan.command = format!("ws {}", args.join(" "));
    plan
}

/// Whom to send the findings to and what to say, for the dialog.
pub fn plan(
    report: &Value,
    review: &str,
    findings: &[FixFinding],
    entries: &[Entry],
    settings: &AgentSettings<'_>,
) -> Result<FixPlan, AppError> {
    if findings.is_empty() {
        return Err(AppError::new("usage", "no findings to fix"));
    }
    let (record, pr) = record(report)?;
    let repo = report
        .get("repo")
        .and_then(Value::as_str)
        .unwrap_or_default();
    let git_ref = report
        .get("ref")
        .and_then(Value::as_str)
        .unwrap_or_default();
    let requester = (!record.requester.is_empty()).then(|| requester(&record.requester, entries));
    let agent = agent_plan(
        repo,
        git_ref,
        pr.number,
        &pr.head_branch,
        pr.cross_repository,
        settings,
    );
    let about = About {
        repo,
        number: pr.number,
        git_ref,
        review,
    };
    Ok(FixPlan {
        findings: findings.iter().map(|f| f.id.clone()).collect(),
        target: if requester.as_ref().is_some_and(|r| r.available) {
            Target::Requester
        } else {
            Target::Agent
        },
        requester,
        requester_message: message(&about, findings, None),
        agent_message: message(&about, findings, agent.branch.as_deref()),
        agent,
    })
}

/// A started fix agent, and the report with its findings done.
#[derive(Debug, Clone, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct FixStarted {
    pub agent: String,
    pub session: String,
    pub report: Option<Value>,
    /// Set when the agent started but `ws review acted` failed.
    pub warning: Option<String>,
}

/// What the dialog confirmed for a fix agent.
#[derive(Debug, Clone, PartialEq, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct StartFix {
    pub findings: Vec<String>,
    pub agent: AgentPlan,
    pub message: String,
}

/// Starts the fix agent with the message as its task, then records the
/// findings as sent to it.
pub async fn start_agent(
    runner: &Runner,
    cache: &Cache,
    repo: &str,
    git_ref: &str,
    req: &StartFix,
    settings: &AgentSettings<'_>,
) -> Result<FixStarted, AppError> {
    if req.findings.is_empty() {
        return Err(AppError::new("usage", "no findings to fix"));
    }
    if req.message.trim().is_empty() {
        return Err(AppError::new("usage", "the fix agent needs a message"));
    }
    let args = new_agent_args(&new_agent(&req.agent, Some(&req.message), settings));
    let created: Created = runner.ws_json(&args).await?;
    match report::acted(
        runner,
        cache,
        repo,
        git_ref,
        &req.findings,
        Acted::FixSent(&created.id),
    )
    .await
    {
        Ok(report) => Ok(FixStarted {
            agent: created.id,
            session: created.session,
            report: Some(report),
            warning: None,
        }),
        Err(err) => Ok(FixStarted {
            agent: created.id,
            session: created.session,
            report: None,
            warning: Some(format!(
                "The fix agent started, but recording it in the review failed: {}",
                err.message
            )),
        }),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    use std::os::unix::fs::PermissionsExt;

    const REPO: &str = "github.com/bobcob7/buff-bear";
    const REF: &str = "3f2a9c1e0b6d4a2f9e8c7b6a5d4c3b2a1f0e9d8c";
    const REVIEW: &str = "review-buff-bear-42-3f2a9c1-8e1d0c2a";
    const DEFAULTS: AgentSettings<'static> = AgentSettings {
        model: "",
        permission_mode: "",
    };

    fn findings() -> Vec<FixFinding> {
        vec![
            FixFinding {
                id: "security:spoofable-client-ip-1".to_string(),
                title: "Any client can choose its own bucket".to_string(),
            },
            FixFinding {
                id: "tests:missing-over-limit-1".to_string(),
                title: "No test drives a request over the limit".to_string(),
            },
        ]
    }

    fn report(requester: &str, fork: bool) -> Value {
        json!({
            "repo": REPO, "ref": REF,
            "review": {"version": 1, "repo": REPO, "ref": REF, "requester": requester,
                "started_at": "2026-10-26T18:00:00Z", "profiles": [],
                "pr": {"number": 42, "url": "https://github.com/bobcob7/buff-bear/pull/42", "base": "main",
                    "head_branch": "feat/rate-limit", "cross_repository": fork}},
            "agent": null, "profiles": [], "comments": [], "superseded": [], "warnings": []
        })
    }

    fn agent(id: &str, status: Status, session: Option<&str>) -> Entry {
        Entry {
            key: id.to_string(),
            kind: crate::model::Kind::Agent,
            name: id.to_string(),
            workspace_id: Some(id.to_string()),
            session_id: session.map(str::to_string),
            path: format!("/w/{id}"),
            status,
            stale: None,
            created_at: None,
            updated_at: None,
            detail: None,
            intent: None,
            result: None,
            links: vec![],
            repos: vec![],
            review: None,
            hidden: false,
        }
    }

    #[test]
    fn the_requester_is_the_default_when_its_session_is_live() {
        let entries = [agent("fix-login-rate", Status::Idle, Some("e8cd"))];
        let plan = plan(
            &report("fix-login-rate", false),
            REVIEW,
            &findings(),
            &entries,
            &DEFAULTS,
        )
        .unwrap();
        assert_eq!(plan.target, Target::Requester);
        let r = plan.requester.unwrap();
        assert!(r.available);
        assert_eq!(
            (r.session_id.as_deref(), r.path.as_str()),
            (Some("e8cd"), "/w/fix-login-rate")
        );
    }

    #[test]
    fn a_removed_requester_falls_back_to_a_fix_agent() {
        let plan = plan(
            &report("fix-login-rate", false),
            REVIEW,
            &findings(),
            &[],
            &DEFAULTS,
        )
        .unwrap();
        assert_eq!(plan.target, Target::Agent);
        assert_eq!(plan.requester.unwrap().reason.as_deref(), Some("removed"));
    }

    #[test]
    fn a_stopped_requester_says_to_resume_it() {
        let entries = [agent("fix-login-rate", Status::Stopped, Some("e8cd"))];
        let plan = plan(
            &report("fix-login-rate", false),
            REVIEW,
            &findings(),
            &entries,
            &DEFAULTS,
        )
        .unwrap();
        assert_eq!(plan.target, Target::Agent);
        assert_eq!(
            plan.requester.unwrap().reason.as_deref(),
            Some("stopped: resume it first")
        );
    }

    #[test]
    fn a_review_without_a_requester_starts_a_fix_agent() {
        let plan = plan(&report("", false), REVIEW, &findings(), &[], &DEFAULTS).unwrap();
        assert_eq!(plan.target, Target::Agent);
        assert!(plan.requester.is_none());
    }

    #[test]
    fn the_fix_agent_works_on_the_pull_requests_branch() {
        let plan = plan(&report("", false), REVIEW, &findings(), &[], &DEFAULTS).unwrap();
        assert_eq!(plan.agent.name, "fix-buff-bear-42-3f2a9c1");
        assert_eq!(
            plan.agent.spec,
            "github.com/bobcob7/buff-bear@feat/rate-limit"
        );
        assert_eq!(plan.agent.branch, None);
        assert_eq!(
            plan.agent.command,
            "ws claude --json --name fix-buff-bear-42-3f2a9c1 github.com/bobcob7/buff-bear@feat/rate-limit"
        );
    }

    #[test]
    fn a_fork_pull_requests_fix_agent_starts_at_the_commit_and_pushes_a_fix_branch() {
        let settings = AgentSettings {
            model: "opus",
            permission_mode: "acceptEdits",
        };
        let plan = plan(&report("", true), REVIEW, &findings(), &[], &settings).unwrap();
        assert_eq!(plan.agent.spec, format!("{REPO}@{REF}"));
        assert_eq!(plan.agent.branch.as_deref(), Some("fix/pr-42-3f2a9c1"));
        assert!(plan
            .agent
            .command
            .ends_with("-- --model opus --permission-mode acceptEdits"));
        assert!(plan
            .agent_message
            .ends_with("git push origin HEAD:fix/pr-42-3f2a9c1"));
        assert!(!plan.agent_message.contains("ws review start"));
        assert!(plan.requester_message.contains("ws review start --pr 42"));
    }

    #[test]
    fn the_message_lists_findings_and_points_at_the_decisions() {
        let about = About {
            repo: REPO,
            number: 42,
            git_ref: REF,
            review: REVIEW,
        };
        assert_eq!(
            message(&about, &findings(), None),
            "Fix the 2 findings decided \"fix\" in the review of bobcob7/buff-bear#42 at 3f2a9c1:\n  security:spoofable-client-ip-1  Any client can choose its own bucket\n  tests:missing-over-limit-1      No test drives a request over the limit\n\nThe chosen fixes and notes:\n  ws review report review-buff-bear-42-3f2a9c1-8e1d0c2a --decided fix --json\n\nWhen done, push, and start a new review with: ws review start --pr 42 github.com/bobcob7/buff-bear"
        );
    }

    #[test]
    fn the_message_counts_one_finding() {
        let about = About {
            repo: REPO,
            number: 42,
            git_ref: REF,
            review: REVIEW,
        };
        assert!(message(&about, &findings()[..1], None).starts_with("Fix the 1 finding decided"));
    }

    #[test]
    fn plan_needs_findings_and_a_pull_request_record() {
        assert_eq!(
            plan(&report("", false), REVIEW, &[], &[], &DEFAULTS)
                .unwrap_err()
                .code,
            "usage"
        );
        let mut unrecorded = report("", false);
        unrecorded["review"] = Value::Null;
        assert_eq!(
            plan(&unrecorded, REVIEW, &findings(), &[], &DEFAULTS)
                .unwrap_err()
                .code,
            "review_not_found"
        );
    }

    fn fake_ws(script: &str) -> (tempfile::TempDir, Runner) {
        let dir = tempfile::tempdir().unwrap();
        let log = dir.path().join("ws.log");
        let body = format!(
            "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '{}'\n{script}\n",
            log.display()
        );
        let path = dir.path().join("ws");
        std::fs::write(&path, body).unwrap();
        std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o755)).unwrap();
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

    fn start_request() -> StartFix {
        StartFix {
            findings: vec!["security:spoofable-client-ip-1".to_string()],
            agent: agent_plan(REPO, REF, 42, "feat/rate-limit", false, &DEFAULTS),
            message: "Fix it".to_string(),
        }
    }

    #[tokio::test]
    async fn start_agent_runs_ws_claude_then_records_the_agent() {
        let script = format!(
            r#"case "$1" in
  claude) echo '{{"id":"fix-buff-bear-42-3f2a9c1-0a1b2c3d","session":"5f6e","path":"/w/f"}}' ;;
  review) case "$2" in acted) echo '{{"decisions":{{}},"warnings":[]}}' ;; report) echo '{}' ;; esac ;;
esac"#,
            report("", false)
        );
        let (dir, runner) = fake_ws(&script);
        let started = start_agent(
            &runner,
            &Cache::default(),
            REPO,
            REF,
            &start_request(),
            &DEFAULTS,
        )
        .await
        .unwrap();
        assert_eq!(started.agent, "fix-buff-bear-42-3f2a9c1-0a1b2c3d");
        assert!(started.warning.is_none());
        let log = calls(&dir);
        assert_eq!(
            log[0],
            "claude --json --name fix-buff-bear-42-3f2a9c1 github.com/bobcob7/buff-bear@feat/rate-limit -- Fix it"
        );
        assert_eq!(
            log[1],
            format!("review acted --json --repo {REPO} --ref {REF} security:spoofable-client-ip-1 --fix-sent fix-buff-bear-42-3f2a9c1-0a1b2c3d")
        );
    }

    #[tokio::test]
    async fn start_agent_records_nothing_when_ws_claude_fails() {
        let (dir, runner) = fake_ws(
            r#"echo '{"error":{"code":"claude_untrusted","message":"not trusted"}}' >&2; exit 1"#,
        );
        let err = start_agent(
            &runner,
            &Cache::default(),
            REPO,
            REF,
            &start_request(),
            &DEFAULTS,
        )
        .await
        .unwrap_err();
        assert_eq!(err.code, "claude_untrusted");
        assert_eq!(calls(&dir).len(), 1);
    }

    #[tokio::test]
    async fn start_agent_warns_when_recording_fails() {
        let script = r#"case "$1" in
  claude) echo '{"id":"fix-1","session":"s","path":"/w"}' ;;
  *) echo '{"error":{"code":"usage","message":"not decided fix"}}' >&2; exit 2 ;;
esac"#;
        let (_dir, runner) = fake_ws(script);
        let started = start_agent(
            &runner,
            &Cache::default(),
            REPO,
            REF,
            &start_request(),
            &DEFAULTS,
        )
        .await
        .unwrap();
        assert!(started.warning.unwrap().contains("not decided fix"));
    }
}
