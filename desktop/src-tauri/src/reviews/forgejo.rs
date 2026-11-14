//! Reading pull requests from a Forgejo host's REST API (ADR 0060).

use super::github::{self, Author, PullRequest, ReviewRequest};
use super::http::Http;
use super::merge::{merge_info, Checks, MergeSignals, ReviewState};
use crate::runner::AppError;
use serde::de::DeserializeOwned;
use serde::Deserialize;
use serde_json::Value;
use std::collections::BTreeMap;
use std::sync::Arc;
use tokio::sync::Semaphore;
use tokio::task::JoinSet;

const PAGE_SIZE: usize = 50;
const MAX_PAGES: usize = 2;
const DETAIL_CONCURRENCY: usize = 4;

/// What a host answered to `/version`.
pub enum Probe {
    Forgejo,
    NotForgejo,
    Unreachable,
}

/// A pull request as Forgejo sends it.
#[derive(Debug, Deserialize)]
pub struct FjPull {
    #[serde(default)]
    number: u64,
    title: String,
    #[serde(default)]
    body: Option<String>,
    html_url: String,
    #[serde(default)]
    draft: bool,
    #[serde(default)]
    mergeable: bool,
    #[serde(default)]
    merge_base: Option<String>,
    user: FjUser,
    head: FjRef,
    base: FjRef,
    #[serde(default)]
    additions: Option<u64>,
    #[serde(default)]
    deletions: Option<u64>,
    #[serde(default)]
    changed_files: Option<u64>,
    #[serde(default)]
    requested_reviewers: Option<Vec<FjUser>>,
    created_at: String,
    updated_at: String,
}

#[derive(Debug, Deserialize)]
struct FjUser {
    login: String,
}

#[derive(Debug, Deserialize)]
struct FjRef {
    #[serde(rename = "ref")]
    name: String,
    sha: String,
}

/// The combined commit status of a pull request's head.
#[derive(Debug, Deserialize)]
pub struct FjStatus {
    #[allow(dead_code)]
    state: String,
    #[serde(default)]
    statuses: Option<Vec<FjStatusItem>>,
}

#[derive(Debug, Deserialize)]
struct FjStatusItem {
    status: String,
}

/// One review of a pull request.
#[derive(Debug, Deserialize)]
pub struct FjReview {
    #[serde(default)]
    id: u64,
    #[serde(default)]
    user: Option<FjUser>,
    state: String,
    #[serde(default)]
    stale: bool,
    #[serde(default)]
    official: bool,
    #[serde(default)]
    dismissed: bool,
}

/// A branch protection rule.
#[derive(Debug, Deserialize)]
pub struct FjProtection {
    rule_name: String,
    #[serde(default)]
    branch_name: Option<String>,
    #[serde(default)]
    required_approvals: u32,
    #[serde(default)]
    block_on_outdated_branch: bool,
}

/// The protection that applies to a pull request's base branch.
pub struct Protection {
    pub required_approvals: u32,
    pub block_on_outdated_branch: bool,
}

/// Whether `host` runs Forgejo, from its `/version` answer.
pub async fn probe(http: &dyn Http, host: &str) -> Probe {
    match http.get(&api_url(host, "/version"), None).await {
        Err(_) => Probe::Unreachable,
        Ok(response) if response.status == 200 && is_version(&response.body) => Probe::Forgejo,
        Ok(_) => Probe::NotForgejo,
    }
}

fn is_version(body: &[u8]) -> bool {
    serde_json::from_slice::<Value>(body)
        .is_ok_and(|answer| answer.get("version").is_some_and(Value::is_string))
}

/// The login of the user `token` belongs to.
pub async fn login(http: &dyn Http, host: &str, token: &str) -> Result<String, AppError> {
    let user: FjUser = get_json(http, host, &api_url(host, "/user"), Some(token)).await?;
    Ok(user.login)
}

/// The error for a non-2xx answer. The token is never in it.
pub fn http_error(host: &str, status: u16, body: &[u8], had_token: bool) -> AppError {
    match status {
        401 | 403 if had_token => AppError::new(
            "forge_auth",
            format!(
                "{host} rejected the token (HTTP {status}). Set a new one in Settings › Forges."
            ),
        ),
        401 | 403 => AppError::new(
            "forge_auth",
            format!("{host} needs a token (HTTP {status}). Set one in Settings › Forges."),
        ),
        429 => AppError::new(
            "forge_rate_limited",
            format!("{host}: rate limited (HTTP 429)"),
        ),
        _ => AppError::new("forge_failed", failed_message(host, status, body)),
    }
}

fn failed_message(host: &str, status: u16, body: &[u8]) -> String {
    #[derive(Deserialize)]
    struct ErrorBody {
        message: Option<String>,
    }
    match serde_json::from_slice::<ErrorBody>(body)
        .ok()
        .and_then(|b| b.message)
    {
        Some(message) => format!("{host}: HTTP {status}: {message}"),
        None => format!("{host}: HTTP {status}"),
    }
}

/// The open pull requests of `repo_id` (`host/owner/name`), in the order
/// Forgejo sent them, with their merge status.
pub async fn list_prs(
    http: Arc<dyn Http>,
    repo_id: &str,
    token: Option<&str>,
) -> Result<Vec<PullRequest>, AppError> {
    let Some((host, owner, name)) = github::split_repo(repo_id) else {
        return Err(AppError::new(
            "usage",
            format!("{repo_id} isn't host/owner/name"),
        ));
    };
    let repo_url = format!("{}/repos/{owner}/{name}", base_url(host));
    let pulls = fetch_pulls(http.as_ref(), host, &repo_url, token).await?;
    let protections = fetch_protections(http.as_ref(), host, &repo_url, token).await;
    let details = fetch_details(&http, host, &pulls, &repo_url, token).await;
    let rules = protections.as_deref().map_err(|_| ());
    Ok(pulls
        .iter()
        .zip(details)
        .map(|(pull, (status, reviews))| {
            let signals = forgejo_signals(pull, status.as_ref(), &reviews, rules);
            pull_request(pull, &signals)
        })
        .collect())
}

async fn fetch_pulls(
    http: &dyn Http,
    host: &str,
    repo_url: &str,
    token: Option<&str>,
) -> Result<Vec<FjPull>, AppError> {
    let mut pulls = Vec::new();
    for page in 1..=MAX_PAGES {
        let url =
            format!("{repo_url}/pulls?state=open&sort=recentupdate&limit={PAGE_SIZE}&page={page}");
        let batch: Vec<FjPull> = get_json(http, host, &url, token).await?;
        let is_full = batch.len() == PAGE_SIZE;
        pulls.extend(batch);
        if !is_full {
            break;
        }
    }
    Ok(pulls)
}

async fn fetch_protections(
    http: &dyn Http,
    host: &str,
    repo_url: &str,
    token: Option<&str>,
) -> Result<Vec<FjProtection>, ()> {
    get_json(http, host, &format!("{repo_url}/branch_protections"), token)
        .await
        .map_err(|_| ())
}

/// Each pull request's status and reviews, four pull requests at a time. A
/// call that fails leaves its part empty; the slot keeps the pull request's
/// place in the list.
async fn fetch_details(
    http: &Arc<dyn Http>,
    host: &str,
    pulls: &[FjPull],
    repo_url: &str,
    token: Option<&str>,
) -> Vec<(Option<FjStatus>, Vec<FjReview>)> {
    let permits = Arc::new(Semaphore::new(DETAIL_CONCURRENCY));
    let mut tasks = JoinSet::new();
    for (index, pull) in pulls.iter().enumerate() {
        let http = Arc::clone(http);
        let permits = Arc::clone(&permits);
        let host = host.to_string();
        let status_url = format!("{repo_url}/commits/{}/status", pull.head.sha);
        let reviews_url = format!("{repo_url}/pulls/{}/reviews", pull.number);
        let token = token.map(str::to_string);
        tasks.spawn(async move {
            let Ok(_permit) = permits.acquire_owned().await else {
                return (index, None, Vec::new());
            };
            let token = token.as_deref();
            let status = get_json(http.as_ref(), &host, &status_url, token)
                .await
                .ok();
            let reviews = get_json(http.as_ref(), &host, &reviews_url, token)
                .await
                .unwrap_or_default();
            (index, status, reviews)
        });
    }
    let mut details: Vec<(Option<FjStatus>, Vec<FjReview>)> =
        pulls.iter().map(|_| (None, Vec::new())).collect();
    while let Some(done) = tasks.join_next().await {
        if let Ok((index, status, reviews)) = done {
            details[index] = (status, reviews);
        }
    }
    details
}

async fn get_json<T: DeserializeOwned>(
    http: &dyn Http,
    host: &str,
    url: &str,
    token: Option<&str>,
) -> Result<T, AppError> {
    let response = http.get(url, token).await?;
    if !(200..300).contains(&response.status) {
        return Err(http_error(
            host,
            response.status,
            &response.body,
            token.is_some(),
        ));
    }
    serde_json::from_slice(&response.body)
        .map_err(|_| AppError::new("forge_failed", format!("{host}: unexpected response")))
}

fn pull_request(pull: &FjPull, signals: &MergeSignals) -> PullRequest {
    PullRequest {
        number: pull.number,
        title: pull.title.clone(),
        author: Author {
            login: pull.user.login.clone(),
        },
        url: pull.html_url.clone(),
        body: pull.body.clone().unwrap_or_default(),
        is_draft: pull.draft,
        head_ref_oid: pull.head.sha.clone(),
        head_ref_name: pull.head.name.clone(),
        base_ref_name: pull.base.name.clone(),
        additions: pull.additions.unwrap_or_default(),
        deletions: pull.deletions.unwrap_or_default(),
        changed_files: pull.changed_files.unwrap_or_default(),
        review_requests: pull
            .requested_reviewers
            .iter()
            .flatten()
            .map(|user| ReviewRequest {
                login: Some(user.login.clone()),
            })
            .collect(),
        created_at: pull.created_at.clone(),
        updated_at: pull.updated_at.clone(),
        merge: merge_info(signals),
        ..PullRequest::default()
    }
}

/// The rule for `base`: the rule whose name is `base` exactly, else the
/// first whose name is a glob matching it. None when no rule applies.
pub fn protection_for(rules: &[FjProtection], base: &str) -> Option<Protection> {
    let rule = rules
        .iter()
        .find(|rule| rule_name(rule) == base)
        .or_else(|| rules.iter().find(|rule| glob_match(rule_name(rule), base)))?;
    Some(Protection {
        required_approvals: rule.required_approvals,
        block_on_outdated_branch: rule.block_on_outdated_branch,
    })
}

fn rule_name(rule: &FjProtection) -> &str {
    if rule.rule_name.is_empty() {
        rule.branch_name.as_deref().unwrap_or_default()
    } else {
        &rule.rule_name
    }
}

/// What the forge says about a pull request's mergeability. `protections`
/// is Err when the branch protections couldn't be read.
pub fn forgejo_signals(
    pull: &FjPull,
    status: Option<&FjStatus>,
    reviews: &[FjReview],
    protections: Result<&[FjProtection], ()>,
) -> MergeSignals {
    let protection = effective_protection(&pull.base.name, protections);
    MergeSignals {
        draft: pull.draft,
        conflicting: Some(!pull.mergeable),
        checks: checks_of(status),
        behind: protection.block_on_outdated_branch
            && pull.merge_base.as_deref() != Some(pull.base.sha.as_str()),
        review: review_state(reviews, protection.required_approvals),
        blocked: false,
    }
}

/// Unreadable protections count as one required approval, so a pull request
/// is never ready when the rules couldn't be checked.
fn effective_protection(base: &str, protections: Result<&[FjProtection], ()>) -> Protection {
    match protections {
        Ok(rules) => protection_for(rules, base).unwrap_or(Protection {
            required_approvals: 0,
            block_on_outdated_branch: false,
        }),
        Err(()) => Protection {
            required_approvals: 1,
            block_on_outdated_branch: false,
        },
    }
}

fn checks_of(status: Option<&FjStatus>) -> Checks {
    let mut checks = Checks::default();
    for item in status.iter().flat_map(|s| s.statuses.iter().flatten()) {
        checks.total += 1;
        match item.status.as_str() {
            "pending" => checks.pending += 1,
            "failure" | "error" => checks.failed += 1,
            _ => checks.passed += 1,
        }
    }
    checks
}

fn review_state(reviews: &[FjReview], required: u32) -> ReviewState {
    let latest = latest_reviews(reviews);
    if latest
        .iter()
        .any(|review| review.state == "REQUEST_CHANGES")
    {
        return ReviewState::ChangesRequested;
    }
    let approvals = u32::try_from(latest.len()).unwrap_or(u32::MAX);
    if approvals < required {
        ReviewState::Required
    } else if approvals > 0 {
        ReviewState::Approved
    } else {
        ReviewState::NotRequired
    }
}

/// Each user's last official, current review that approves or requests
/// changes.
fn latest_reviews(reviews: &[FjReview]) -> Vec<&FjReview> {
    let mut latest: BTreeMap<&str, &FjReview> = BTreeMap::new();
    for review in reviews.iter().filter(|review| counts_toward_review(review)) {
        let Some(user) = &review.user else {
            continue;
        };
        let kept = latest.entry(user.login.as_str()).or_insert(review);
        if review.id > kept.id {
            *kept = review;
        }
    }
    latest.into_values().collect()
}

fn counts_toward_review(review: &FjReview) -> bool {
    review.official
        && !review.stale
        && !review.dismissed
        && matches!(review.state.as_str(), "APPROVED" | "REQUEST_CHANGES")
}

fn base_url(host: &str) -> String {
    format!("https://{host}/api/v1")
}

fn api_url(host: &str, path: &str) -> String {
    format!("{}{path}", base_url(host))
}

/// Whether `text` matches `pattern`, where `*` matches any run of
/// characters, including none.
fn glob_match(pattern: &str, text: &str) -> bool {
    match pattern.split_once('*') {
        None => pattern == text,
        Some((head, rest)) => {
            let Some(tail) = text.strip_prefix(head) else {
                return false;
            };
            (0..=tail.len())
                .filter(|&i| tail.is_char_boundary(i))
                .any(|i| glob_match(rest, &tail[i..]))
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::reviews::http::FakeHttp;
    use crate::reviews::merge::{MergeInfo, MergeStatus};

    const HOST: &str = "git.example.com";
    const TOKEN: &str = "s3cr3t-value";
    const REPO: &str = "git.example.com/team/svc";
    const SHA: &str = "8db6637666e157ddd3af5d5033ac7edc347f2c10";
    const VERSION_URL: &str = "https://git.example.com/api/v1/version";
    const USER_URL: &str = "https://git.example.com/api/v1/user";
    const PULLS_PAGE_1: &str = "https://git.example.com/api/v1/repos/team/svc/pulls?state=open&sort=recentupdate&limit=50&page=1";
    const PULLS_PAGE_2: &str = "https://git.example.com/api/v1/repos/team/svc/pulls?state=open&sort=recentupdate&limit=50&page=2";
    const PROTECTIONS_URL: &str =
        "https://git.example.com/api/v1/repos/team/svc/branch_protections";
    const STATUS_URL: &str = "https://git.example.com/api/v1/repos/team/svc/commits/8db6637666e157ddd3af5d5033ac7edc347f2c10/status";
    const REVIEWS_URL: &str = "https://git.example.com/api/v1/repos/team/svc/pulls/15262/reviews";

    const PULL: &str = r#"{"number":15262,"title":"refactor: move RemoveTeamMember","body":"Moves it.","html_url":"https://git.example.com/team/svc/pulls/15262","draft":false,"mergeable":true,"merge_base":"7aa74e4f83288716cbe38f58f06a12f77995d38c","user":{"login":"wetneb","id":94198},"head":{"ref":"remove_member","sha":"8db6637666e157ddd3af5d5033ac7edc347f2c10","label":"remove_member"},"base":{"ref":"forgejo","sha":"7aa74e4f83288716cbe38f58f06a12f77995d38c"},"additions":428,"deletions":461,"changed_files":16,"requested_reviewers":[{"login":"Gusted","id":26734}],"requested_reviewers_teams":[],"created_at":"2026-11-13T22:14:22+01:00","updated_at":"2026-11-13T22:14:30+01:00","state":"open"}"#;
    const STATUS: &str = r#"{"state":"failure","sha":"8db6637666e157ddd3af5d5033ac7edc347f2c10","total_count":3,"statuses":[{"context":"testing / unit","status":"success"},{"context":"testing / lint","status":"failure"},{"context":"issue-labels / cascade","status":"skipped"}]}"#;
    const REVIEWS: &str = r#"[{"id":1,"user":{"login":"Gusted"},"state":"REQUEST_REVIEW","stale":false,"official":true,"dismissed":false,"commit_id":""},
 {"id":2,"user":{"login":"Cyborus"},"state":"APPROVED","stale":false,"official":true,"dismissed":false,"commit_id":"8db6637666e157ddd3af5d5033ac7edc347f2c10"}]"#;
    const PROTECTIONS: &str = r#"[{"rule_name":"forgejo","branch_name":"forgejo","required_approvals":2,"block_on_outdated_branch":true}]"#;

    fn pulls_json(count: usize) -> String {
        format!("[{}]", vec![PULL; count].join(","))
    }

    fn fixture_forge() -> FakeHttp {
        FakeHttp::new()
            .route(PULLS_PAGE_1, 200, &format!("[{PULL}]"))
            .route(PROTECTIONS_URL, 200, PROTECTIONS)
            .route(STATUS_URL, 200, STATUS)
            .route(REVIEWS_URL, 200, REVIEWS)
    }

    fn fixture_pull() -> FjPull {
        serde_json::from_str(PULL).unwrap()
    }

    fn fixture_status(statuses: &str) -> FjStatus {
        serde_json::from_str(&format!(r#"{{"state":"success","statuses":[{statuses}]}}"#)).unwrap()
    }

    fn review(id: u64, user: &str, state: &str) -> FjReview {
        FjReview {
            id,
            user: Some(FjUser {
                login: user.to_string(),
            }),
            state: state.to_string(),
            stale: false,
            official: true,
            dismissed: false,
        }
    }

    fn rule(rule_name: &str, approvals: u32, block: bool) -> FjProtection {
        FjProtection {
            rule_name: rule_name.to_string(),
            branch_name: Some(rule_name.to_string()),
            required_approvals: approvals,
            block_on_outdated_branch: block,
        }
    }

    #[tokio::test]
    async fn probe_finds_forgejo_by_version() {
        let fake = FakeHttp::new().route(VERSION_URL, 200, r#"{"version":"17.0.0"}"#);
        assert!(matches!(probe(&fake, HOST).await, Probe::Forgejo));
    }

    #[tokio::test]
    async fn probe_rejects_other_answers() {
        for (status, body) in [(404, "{}"), (410, "{}"), (200, "<html>")] {
            let fake = FakeHttp::new().route(VERSION_URL, status, body);
            assert!(matches!(probe(&fake, HOST).await, Probe::NotForgejo));
        }
    }

    #[tokio::test]
    async fn probe_reports_unreachable() {
        let fake = FakeHttp::new().fail(VERSION_URL);
        assert!(matches!(probe(&fake, HOST).await, Probe::Unreachable));
    }

    #[tokio::test]
    async fn login_reads_user_and_sends_token() {
        let fake = FakeHttp::new().route(USER_URL, 200, r#"{"login":"bob"}"#);
        let name = login(&fake, HOST, TOKEN).await.unwrap();
        assert_eq!(name, "bob");
        assert_eq!(
            fake.calls(),
            vec![(USER_URL.to_string(), Some(TOKEN.to_string()))]
        );
    }

    #[tokio::test]
    async fn login_rejected_token_is_forge_auth() {
        let fake = FakeHttp::new().route(USER_URL, 401, "{}");
        let error = login(&fake, HOST, TOKEN).await.unwrap_err();
        assert_eq!(error.code, "forge_auth");
        assert!(
            error.message.contains("rejected the token"),
            "{}",
            error.message
        );
        assert!(!error.message.contains(TOKEN));
    }

    #[tokio::test]
    async fn list_prs_maps_fixture() {
        let fake = Arc::new(fixture_forge());
        let prs = list_prs(fake, REPO, Some(TOKEN)).await.unwrap();
        assert_eq!(prs.len(), 1);
        let pr = &prs[0];
        assert_eq!(pr.number, 15262);
        assert_eq!(pr.title, "refactor: move RemoveTeamMember");
        assert_eq!(pr.body, "Moves it.");
        assert_eq!(pr.url, "https://git.example.com/team/svc/pulls/15262");
        assert_eq!(pr.author.login, "wetneb");
        assert!(!pr.is_draft);
        assert_eq!(pr.head_ref_oid, SHA);
        assert_eq!(pr.head_ref_name, "remove_member");
        assert_eq!(pr.base_ref_name, "forgejo");
        assert_eq!(
            (pr.additions, pr.deletions, pr.changed_files),
            (428, 461, 16)
        );
        assert_eq!(pr.review_requests[0].login, Some("Gusted".to_string()));
        assert_eq!(pr.created_at, "2026-11-13T22:14:22+01:00");
        assert_eq!(pr.updated_at, "2026-11-13T22:14:30+01:00");
        assert_eq!(
            pr.merge,
            MergeInfo {
                status: MergeStatus::Failing,
                reasons: vec![MergeStatus::Failing, MergeStatus::AwaitingApproval],
                checks: Checks {
                    total: 3,
                    passed: 2,
                    failed: 1,
                    pending: 0,
                },
            }
        );
    }

    #[tokio::test]
    async fn list_prs_reads_second_page_when_first_is_full() {
        let fake = Arc::new(
            FakeHttp::new()
                .route(PULLS_PAGE_1, 200, &pulls_json(50))
                .route(PULLS_PAGE_2, 200, &pulls_json(3)),
        );
        let prs = list_prs(fake.clone(), REPO, None).await.unwrap();
        assert_eq!(prs.len(), 53);
        let pulls_calls = fake
            .calls()
            .iter()
            .filter(|(url, _)| url.contains("/pulls?"))
            .count();
        assert_eq!(pulls_calls, 2);
    }

    #[tokio::test]
    async fn list_prs_stops_after_short_page() {
        let fake = Arc::new(FakeHttp::new().route(PULLS_PAGE_1, 200, &pulls_json(10)));
        let prs = list_prs(fake.clone(), REPO, None).await.unwrap();
        assert_eq!(prs.len(), 10);
        let pulls_calls = fake
            .calls()
            .iter()
            .filter(|(url, _)| url.contains("/pulls?"))
            .count();
        assert_eq!(pulls_calls, 1);
    }

    #[tokio::test]
    async fn list_prs_sends_token_on_every_call() {
        let fake = Arc::new(fixture_forge());
        list_prs(fake.clone(), REPO, Some(TOKEN)).await.unwrap();
        let calls = fake.calls();
        assert_eq!(calls.len(), 4);
        assert!(calls
            .iter()
            .all(|(_, token)| token.as_deref() == Some(TOKEN)));
    }

    #[tokio::test]
    async fn list_prs_without_token_sends_none() {
        let fake = Arc::new(fixture_forge());
        list_prs(fake.clone(), REPO, None).await.unwrap();
        assert!(fake.calls().iter().all(|(_, token)| token.is_none()));
    }

    #[tokio::test]
    async fn list_prs_without_token_on_401_needs_token() {
        let fake = Arc::new(FakeHttp::new().route(PULLS_PAGE_1, 401, "{}"));
        let error = list_prs(fake, REPO, None).await.unwrap_err();
        assert_eq!(error.code, "forge_auth");
        assert!(error.message.contains("needs a token"), "{}", error.message);
    }

    #[tokio::test]
    async fn list_prs_rate_limit_is_forge_rate_limited() {
        let fake = Arc::new(FakeHttp::new().route(PULLS_PAGE_1, 429, "{}"));
        let error = list_prs(fake, REPO, None).await.unwrap_err();
        assert_eq!(error.code, "forge_rate_limited");
    }

    #[tokio::test]
    async fn list_prs_server_error_names_host_and_message() {
        let fake = Arc::new(FakeHttp::new().route(PULLS_PAGE_1, 500, r#"{"message":"boom"}"#));
        let error = list_prs(fake, REPO, None).await.unwrap_err();
        assert_eq!(error.code, "forge_failed");
        assert_eq!(error.message, "git.example.com: HTTP 500: boom");
    }

    #[tokio::test]
    async fn failed_status_call_leaves_checks_empty() {
        let fake = Arc::new(
            FakeHttp::new()
                .route(PULLS_PAGE_1, 200, &format!("[{PULL}]"))
                .route(PROTECTIONS_URL, 200, PROTECTIONS)
                .route(STATUS_URL, 500, "{}")
                .route(REVIEWS_URL, 200, "[]"),
        );
        let prs = list_prs(fake, REPO, None).await.unwrap();
        assert_eq!(prs[0].merge.checks.total, 0);
    }

    #[test]
    fn unreadable_protections_require_one_approval() {
        let mut pull = fixture_pull();
        pull.merge_base = Some("0000".to_string());
        let signals = forgejo_signals(&pull, None, &[], Err(()));
        let info = merge_info(&signals);
        assert!(info.reasons.contains(&MergeStatus::AwaitingApproval));
        assert!(!info.reasons.contains(&MergeStatus::Behind));
    }

    #[test]
    fn protection_for_matches_exact_then_glob() {
        let rules = [rule("forgejo", 2, false), rule("release/*", 1, false)];
        assert_eq!(
            protection_for(&rules, "forgejo")
                .unwrap()
                .required_approvals,
            2
        );
        assert_eq!(
            protection_for(&rules, "release/1.0")
                .unwrap()
                .required_approvals,
            1
        );
        assert!(protection_for(&rules, "main").is_none());
        let star = [rule("*", 3, false)];
        assert_eq!(
            protection_for(&star, "anything/at/all")
                .unwrap()
                .required_approvals,
            3
        );
        let unnamed = [FjProtection {
            rule_name: String::new(),
            branch_name: Some("main".to_string()),
            required_approvals: 4,
            block_on_outdated_branch: false,
        }];
        assert_eq!(
            protection_for(&unnamed, "main").unwrap().required_approvals,
            4
        );
    }

    #[test]
    fn forgejo_signals_uses_latest_official_review_per_user() {
        let pull = fixture_pull();
        let mut stale = review(4, "Dana", "REQUEST_CHANGES");
        stale.stale = true;
        let mut official_not = review(5, "Erin", "REQUEST_CHANGES");
        official_not.official = false;
        let reviews = [
            review(1, "Alex", "REQUEST_CHANGES"),
            review(3, "Alex", "APPROVED"),
            review(5, "Alex", "REQUEST_REVIEW"),
            stale,
            official_not,
        ];
        let signals = forgejo_signals(&pull, None, &reviews, Ok(&[]));
        assert_eq!(signals.review, ReviewState::Approved);
    }

    #[test]
    fn forgejo_signals_behind_only_when_protection_blocks_outdated() {
        let mut pull = fixture_pull();
        pull.merge_base = Some("0000".to_string());
        let blocking = [rule("forgejo", 0, true)];
        assert!(forgejo_signals(&pull, None, &[], Ok(&blocking)).behind);
        let lenient = [rule("forgejo", 0, false)];
        assert!(!forgejo_signals(&pull, None, &[], Ok(&lenient)).behind);
    }

    #[test]
    fn no_protection_rule_means_no_approval_required() {
        let pull = fixture_pull();
        let none = forgejo_signals(&pull, None, &[], Ok(&[]));
        assert_eq!(none.review, ReviewState::NotRequired);
        let approved = forgejo_signals(&pull, None, &[review(1, "Alex", "APPROVED")], Ok(&[]));
        assert_eq!(approved.review, ReviewState::Approved);
    }

    #[test]
    fn forgejo_status_values_map_to_checks() {
        let pull = fixture_pull();
        let status = fixture_status(
            r#"{"status":"success"},{"status":"warning"},{"status":"skipped"},{"status":"failure"},{"status":"error"},{"status":"pending"}"#,
        );
        let signals = forgejo_signals(&pull, Some(&status), &[], Ok(&[]));
        assert_eq!(
            signals.checks,
            Checks {
                total: 6,
                passed: 3,
                failed: 2,
                pending: 1,
            }
        );
    }

    #[test]
    fn mergeable_false_is_conflicting() {
        let mut pull = fixture_pull();
        pull.mergeable = false;
        assert_eq!(
            forgejo_signals(&pull, None, &[], Ok(&[])).conflicting,
            Some(true)
        );
    }
}
