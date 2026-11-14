//! Running `gh` for pull requests (ADR 0035).

use super::merge::{merge_info, Checks, MergeInfo, MergeSignals, ReviewState};
use crate::runner::{self, AppError, Runner};
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;

/// The fields asked of `gh pr list`. `commits` can't be among them: with 100
/// pull requests it exceeds GitHub's node limit.
const PR_FIELDS: &str = "number,title,author,url,body,isDraft,headRefOid,headRefName,baseRefName,additions,deletions,changedFiles,reviewRequests,createdAt,updatedAt,mergeable,mergeStateStatus,reviewDecision,statusCheckRollup";

#[derive(Debug, Clone, PartialEq, Default, Deserialize, Serialize)]
pub struct Author {
    pub login: String,
}

#[derive(Debug, Clone, PartialEq, Default, Deserialize, Serialize)]
pub struct ReviewRequest {
    /// A user's login; teams have none.
    pub login: Option<String>,
}

/// One entry of a pull request's status check rollup: a check run or a status context.
#[derive(Debug, Clone, PartialEq, Default, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct RollupItem {
    #[serde(rename = "__typename", default)]
    pub typename: String,
    #[serde(default)]
    pub status: String,
    #[serde(default)]
    pub conclusion: String,
    #[serde(default)]
    pub state: String,
}

#[derive(Debug, Clone, PartialEq, Default, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct PullRequest {
    pub number: u64,
    pub title: String,
    pub author: Author,
    pub url: String,
    #[serde(default)]
    pub body: String,
    pub is_draft: bool,
    pub head_ref_oid: String,
    pub head_ref_name: String,
    pub base_ref_name: String,
    #[serde(default)]
    pub additions: u64,
    #[serde(default)]
    pub deletions: u64,
    #[serde(default)]
    pub changed_files: u64,
    #[serde(default)]
    pub review_requests: Vec<ReviewRequest>,
    pub created_at: String,
    pub updated_at: String,
    /// Filled by `list_prs` (GitHub) or `forgejo::list_prs`; absent in old JSON.
    #[serde(default)]
    pub merge: MergeInfo,
    #[serde(default, skip_serializing)]
    pub mergeable: String,
    #[serde(default, skip_serializing)]
    pub merge_state_status: String,
    #[serde(default, skip_serializing)]
    pub review_decision: String,
    #[serde(default, skip_serializing)]
    pub status_check_rollup: Vec<RollupItem>,
}

impl PullRequest {
    pub fn review_requested_from(&self, login: Option<&str>) -> bool {
        login.is_some_and(|me| {
            self.review_requests.iter().any(|r| {
                r.login
                    .as_deref()
                    .is_some_and(|l| l.eq_ignore_ascii_case(me))
            })
        })
    }
}

/// The commits pushed since a review.
#[derive(Debug, Clone, PartialEq, Deserialize, Serialize)]
pub struct NewCommits {
    pub count: u64,
    pub subjects: Vec<String>,
}

/// A repo id's host, owner, and name, if it has exactly those three parts.
pub fn split_repo(repo_id: &str) -> Option<(&str, &str, &str)> {
    let mut parts = repo_id.split('/');
    let (host, owner, name) = (parts.next()?, parts.next()?, parts.next()?);
    if parts.next().is_some() || host.is_empty() || owner.is_empty() || name.is_empty() {
        return None;
    }
    Some((host, owner, name))
}

pub fn pr_list_args(repo_id: &str) -> Vec<String> {
    [
        "pr", "list", "--repo", repo_id, "--state", "open", "--limit", "100", "--json", PR_FIELDS,
    ]
    .iter()
    .map(|s| (*s).to_string())
    .collect()
}

/// Turns a failed `gh` run into an error: `gh_auth` when it says to log in,
/// `gh_rate_limited` when it mentions a rate limit, else `gh_failed` with
/// its first line.
pub fn gh_error(stderr: &str) -> AppError {
    let lower = stderr.to_ascii_lowercase();
    let first = stderr
        .lines()
        .map(str::trim)
        .find(|l| !l.is_empty())
        .unwrap_or("gh failed");
    if lower.contains("gh auth login") || lower.contains("not logged in") {
        return AppError::new(
            "gh_auth",
            "gh isn't signed in. Run gh auth login in a terminal, then Check now.",
        );
    }
    if lower.contains("rate limit") {
        return AppError::new("gh_rate_limited", first);
    }
    AppError::new("gh_failed", first)
}

pub async fn gh(runner: &Runner, args: &[String]) -> Result<runner::Outcome, AppError> {
    gh_with_input(runner, args, None).await
}

/// Runs `gh` with `input`, if any, on its stdin, as `gh api --input -` reads it.
pub async fn gh_with_input(
    runner: &Runner,
    args: &[String],
    input: Option<&[u8]>,
) -> Result<runner::Outcome, AppError> {
    runner
        .run_with_input(
            "gh",
            args,
            &[("GH_PROMPT_DISABLED", "1"), ("NO_COLOR", "1")],
            input,
        )
        .await
        .map_err(|e| {
            if e.code == "gh_missing" {
                AppError::new(
                    "gh_missing",
                    "gh isn't installed. Run brew install gh in a terminal, then gh auth login.",
                )
            } else {
                e
            }
        })
}

#[derive(Deserialize)]
struct AuthStatus {
    hosts: BTreeMap<String, Vec<AuthAccount>>,
}

#[derive(Deserialize)]
struct AuthAccount {
    state: String,
    #[serde(default)]
    active: bool,
    login: Option<String>,
}

/// Reads `gh auth status --json hosts` into each signed-in host's login.
/// It prints the JSON even when it exits non-zero for a bad token.
pub fn parse_auth(stdout: &[u8]) -> Option<BTreeMap<String, String>> {
    let status: AuthStatus = serde_json::from_slice(stdout).ok()?;
    Some(
        status
            .hosts
            .into_iter()
            .filter_map(|(host, accounts)| {
                let login = accounts
                    .into_iter()
                    .find(|a| a.active && a.state == "success")?
                    .login?;
                Some((host, login))
            })
            .collect(),
    )
}

/// The hosts `gh` is signed in to, with the login on each.
pub async fn signed_in_hosts(runner: &Runner) -> Result<BTreeMap<String, String>, AppError> {
    let args: Vec<String> = ["auth", "status", "--json", "hosts"]
        .iter()
        .map(|s| (*s).to_string())
        .collect();
    let outcome = gh(runner, &args).await?;
    let hosts = parse_auth(&outcome.stdout).unwrap_or_default();
    if hosts.is_empty() {
        return Err(AppError::new(
            "gh_auth",
            "gh isn't signed in. Run gh auth login in a terminal, then Check now.",
        ));
    }
    Ok(hosts)
}

/// The merge signals GitHub reports for a pull request (ADR 0060).
pub fn github_signals(pr: &PullRequest) -> MergeSignals {
    let mut checks = Checks::default();
    for item in &pr.status_check_rollup {
        count_check(&mut checks, item);
    }
    MergeSignals {
        draft: pr.is_draft || pr.merge_state_status == "DRAFT",
        conflicting: conflicting(pr),
        checks,
        behind: pr.merge_state_status == "BEHIND",
        review: review_state(&pr.review_decision),
        blocked: pr.merge_state_status == "BLOCKED",
    }
}

fn conflicting(pr: &PullRequest) -> Option<bool> {
    if pr.mergeable == "CONFLICTING" || pr.merge_state_status == "DIRTY" {
        Some(true)
    } else if pr.mergeable == "MERGEABLE" {
        Some(false)
    } else {
        None
    }
}

fn review_state(decision: &str) -> ReviewState {
    match decision {
        "APPROVED" => ReviewState::Approved,
        "CHANGES_REQUESTED" => ReviewState::ChangesRequested,
        "REVIEW_REQUIRED" => ReviewState::Required,
        _ => ReviewState::NotRequired,
    }
}

fn count_check(checks: &mut Checks, item: &RollupItem) {
    checks.total += 1;
    if is_pending(item) {
        checks.pending += 1;
    } else if is_failed(item) {
        checks.failed += 1;
    } else {
        checks.passed += 1;
    }
}

fn is_pending(item: &RollupItem) -> bool {
    match item.typename.as_str() {
        "CheckRun" => item.status != "COMPLETED",
        "StatusContext" => matches!(item.state.as_str(), "PENDING" | "EXPECTED"),
        _ => false,
    }
}

fn is_failed(item: &RollupItem) -> bool {
    match item.typename.as_str() {
        "CheckRun" => matches!(
            item.conclusion.as_str(),
            "FAILURE" | "TIMED_OUT" | "CANCELLED" | "ACTION_REQUIRED" | "STARTUP_FAILURE"
        ),
        "StatusContext" => matches!(item.state.as_str(), "FAILURE" | "ERROR"),
        _ => false,
    }
}

/// Parses `gh pr list --json` output and fills each pull request's merge status.
pub fn parse_prs(stdout: &[u8]) -> Result<Vec<PullRequest>, AppError> {
    let mut prs: Vec<PullRequest> = runner::parse_json(stdout, "gh pr list")?;
    for pr in &mut prs {
        pr.merge = merge_info(&github_signals(pr));
    }
    Ok(prs)
}

pub async fn list_prs(runner: &Runner, repo_id: &str) -> Result<Vec<PullRequest>, AppError> {
    let outcome = gh(runner, &pr_list_args(repo_id)).await?;
    if !outcome.success {
        return Err(gh_error(&outcome.stderr));
    }
    parse_prs(&outcome.stdout)
}

#[derive(Deserialize)]
struct Compare {
    ahead_by: u64,
    commits: Vec<String>,
}

pub fn compare_args(repo_id: &str, base: &str, head: &str) -> Option<Vec<String>> {
    let (host, owner, name) = split_repo(repo_id)?;
    Some(
        [
            "api",
            "--hostname",
            host,
            &format!("repos/{owner}/{name}/compare/{base}...{head}"),
            "--jq",
            r#"{ahead_by, commits: [.commits[].commit.message | split("\n")[0]]}"#,
        ]
        .iter()
        .map(|s| (*s).to_string())
        .collect(),
    )
}

/// The commits on `head` that `base` doesn't have, newest last.
pub async fn new_commits(
    runner: &Runner,
    repo_id: &str,
    base: &str,
    head: &str,
) -> Result<NewCommits, AppError> {
    let args = compare_args(repo_id, base, head)
        .ok_or_else(|| AppError::new("usage", format!("{repo_id} isn't host/owner/name")))?;
    let outcome = gh(runner, &args).await?;
    if !outcome.success {
        return Err(gh_error(&outcome.stderr));
    }
    let compare: Compare = runner::parse_json(&outcome.stdout, "gh api compare")?;
    Ok(NewCommits {
        count: compare.ahead_by,
        subjects: compare.commits,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::reviews::merge::MergeStatus;

    #[test]
    fn pr_list_args_pass_cache_id_as_repo() {
        let args = pr_list_args("ghe.example.com/team/svc");
        assert_eq!(
            &args[..4],
            ["pr", "list", "--repo", "ghe.example.com/team/svc"]
        );
        assert!(args.contains(&"--state".to_string()));
        assert!(!args.last().unwrap().contains("commits"));
    }

    #[test]
    fn parses_gh_pr_list_output() {
        let out = br#"[{"additions":214,"author":{"id":"x","is_bot":false,"login":"alice","name":"Alice"},"baseRefName":"main","body":"Adds a limit.","changedFiles":9,"createdAt":"2026-10-24T09:00:00Z","deletions":37,"headRefName":"feat/rate-limit","headRefOid":"3f2a9c1e0b6d4a2f9e8c7b6a5d4c3b2a1f0e9d8c","isDraft":false,"number":42,"reviewRequests":[{"__typename":"User","login":"bobcob7"},{"__typename":"Team","name":"core","slug":"core"}],"title":"Add rate limiting to login","updatedAt":"2026-10-24T09:30:00Z","url":"https://github.com/bobcob7/buff-bear/pull/42"}]"#;
        let prs: Vec<PullRequest> = runner::parse_json(out, "gh").unwrap();
        assert_eq!(prs[0].number, 42);
        assert_eq!(prs[0].author.login, "alice");
        assert!(prs[0].review_requested_from(Some("BobCob7")));
        assert!(!prs[0].review_requested_from(Some("core")));
    }

    #[test]
    fn parse_auth_keeps_active_successful_accounts() {
        let out = br#"{"hosts":{"github.com":[{"state":"success","active":true,"host":"github.com","login":"bobcob7"}],"ghe.example.com":[{"state":"error","active":true,"login":"me"}]}}"#;
        let hosts = parse_auth(out).unwrap();
        assert_eq!(hosts.len(), 1);
        assert_eq!(hosts["github.com"], "bobcob7");
    }

    #[test]
    fn gh_error_classifies_rate_limit() {
        let err = gh_error("GraphQL: API rate limit exceeded for user ID 1.\n");
        assert_eq!(err.code, "gh_rate_limited");
        assert_eq!(
            err.message,
            "GraphQL: API rate limit exceeded for user ID 1."
        );
    }

    #[test]
    fn gh_error_classifies_login_prompt() {
        let err = gh_error("To get started with GitHub CLI, please run:  gh auth login");
        assert_eq!(err.code, "gh_auth");
    }

    #[test]
    fn gh_error_keeps_first_line() {
        let err = gh_error(
            "\nGraphQL: Could not resolve to a Repository with the name 'o/x'. (repository)\n",
        );
        assert_eq!(err.code, "gh_failed");
        assert!(err.message.starts_with("GraphQL: Could not resolve"));
    }

    #[test]
    fn compare_args_use_host_and_three_dot_range() {
        let args = compare_args("github.com/o/r", "aaa", "bbb").unwrap();
        assert_eq!(
            &args[..4],
            [
                "api",
                "--hostname",
                "github.com",
                "repos/o/r/compare/aaa...bbb"
            ]
        );
    }

    fn pr_with(mergeable: &str, merge_state_status: &str, review_decision: &str) -> PullRequest {
        PullRequest {
            mergeable: mergeable.to_string(),
            merge_state_status: merge_state_status.to_string(),
            review_decision: review_decision.to_string(),
            ..PullRequest::default()
        }
    }

    fn item(typename: &str, status: &str, conclusion: &str, state: &str) -> RollupItem {
        RollupItem {
            typename: typename.to_string(),
            status: status.to_string(),
            conclusion: conclusion.to_string(),
            state: state.to_string(),
        }
    }

    #[test]
    fn pr_list_args_ask_for_merge_fields() {
        let args = pr_list_args("github.com/o/r");
        let fields = args.last().unwrap();
        assert!(fields.ends_with("mergeable,mergeStateStatus,reviewDecision,statusCheckRollup"));
        assert!(!fields.contains("commits"));
    }

    #[test]
    fn parse_prs_fills_merge_status() {
        let out = br#"[{"additions":3,"author":{"login":"alice"},"baseRefName":"main","body":"","changedFiles":1,"createdAt":"2026-11-13T09:00:00Z","deletions":1,"headRefName":"feat/x","headRefOid":"3f2a9c1e0b6d4a2f9e8c7b6a5d4c3b2a1f0e9d8c","isDraft":false,"mergeStateStatus":"BLOCKED","mergeable":"MERGEABLE","number":7,"reviewDecision":"REVIEW_REQUIRED","reviewRequests":[{"__typename":"User","login":"bobcob7"}],"statusCheckRollup":[{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"SUCCESS","workflowName":"CI"},{"__typename":"CheckRun","name":"lint","status":"COMPLETED","conclusion":"FAILURE","workflowName":"CI"},{"__typename":"StatusContext","context":"ci/woodpecker/pr/ci","state":"PENDING"}],"title":"Add x","updatedAt":"2026-11-13T09:30:00Z","url":"https://github.com/bobcob7/buff-bear/pull/7"}]"#;
        let prs = parse_prs(out).unwrap();
        assert_eq!(
            prs[0].merge,
            MergeInfo {
                status: MergeStatus::Failing,
                reasons: vec![
                    MergeStatus::Failing,
                    MergeStatus::AwaitingApproval,
                    MergeStatus::Pending
                ],
                checks: Checks {
                    total: 3,
                    passed: 1,
                    failed: 1,
                    pending: 1
                },
            }
        );
    }

    #[test]
    fn pull_request_without_merge_fields_is_unknown() {
        let out = br#"[{"additions":214,"author":{"id":"x","is_bot":false,"login":"alice","name":"Alice"},"baseRefName":"main","body":"Adds a limit.","changedFiles":9,"createdAt":"2026-10-24T09:00:00Z","deletions":37,"headRefName":"feat/rate-limit","headRefOid":"3f2a9c1e0b6d4a2f9e8c7b6a5d4c3b2a1f0e9d8c","isDraft":false,"number":42,"reviewRequests":[{"__typename":"User","login":"bobcob7"},{"__typename":"Team","name":"core","slug":"core"}],"title":"Add rate limiting to login","updatedAt":"2026-10-24T09:30:00Z","url":"https://github.com/bobcob7/buff-bear/pull/42"}]"#;
        let prs = parse_prs(out).unwrap();
        assert_eq!(prs[0].merge.status, MergeStatus::Unknown);
        assert_eq!(prs[0].merge.reasons, [MergeStatus::Unknown]);
    }

    #[test]
    fn github_signals_maps_conflicts() {
        let conflicts = |mergeable: &str, state: &str| {
            github_signals(&pr_with(mergeable, state, "")).conflicting
        };
        assert_eq!(conflicts("CONFLICTING", ""), Some(true));
        assert_eq!(conflicts("MERGEABLE", "DIRTY"), Some(true));
        assert_eq!(conflicts("MERGEABLE", ""), Some(false));
        assert_eq!(conflicts("UNKNOWN", ""), None);
    }

    #[test]
    fn github_signals_maps_reviews_and_behind() {
        let review = |decision: &str| github_signals(&pr_with("", "", decision)).review;
        assert_eq!(review("APPROVED"), ReviewState::Approved);
        assert_eq!(review("CHANGES_REQUESTED"), ReviewState::ChangesRequested);
        assert_eq!(review("REVIEW_REQUIRED"), ReviewState::Required);
        assert_eq!(review(""), ReviewState::NotRequired);
        assert!(github_signals(&pr_with("", "BEHIND", "")).behind);
        assert!(github_signals(&pr_with("", "BLOCKED", "")).blocked);
        assert!(github_signals(&pr_with("", "DRAFT", "")).draft);
    }

    #[test]
    fn github_signals_counts_checks() {
        let pr = PullRequest {
            status_check_rollup: vec![
                item("CheckRun", "IN_PROGRESS", "", ""),
                item("CheckRun", "COMPLETED", "TIMED_OUT", ""),
                item("CheckRun", "COMPLETED", "NEUTRAL", ""),
                item("CheckRun", "COMPLETED", "SKIPPED", ""),
                item("StatusContext", "", "", "ERROR"),
                item("StatusContext", "", "", "EXPECTED"),
                item("StatusContext", "", "", "SUCCESS"),
            ],
            ..PullRequest::default()
        };
        assert_eq!(
            github_signals(&pr).checks,
            Checks {
                total: 7,
                passed: 3,
                failed: 2,
                pending: 2
            }
        );
    }

    #[test]
    fn merge_is_serialized_but_raw_fields_are_not() {
        let value = serde_json::to_value(PullRequest::default()).unwrap();
        assert!(value.get("merge").is_some());
        for raw in [
            "mergeable",
            "mergeStateStatus",
            "reviewDecision",
            "statusCheckRollup",
        ] {
            assert!(value.get(raw).is_none(), "{raw} is serialized");
        }
    }
}
