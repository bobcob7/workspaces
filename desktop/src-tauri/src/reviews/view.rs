//! What the Reviews section shows: each watched repo's open pull requests,
//! joined with their review workspaces into groups (ADRs 0032, 0039).

use super::github::{self, NewCommits, PullRequest};
use super::names;
use super::store::StateFile;
use crate::model::{Entry, Kind, Link, Status};
use crate::runner::AppError;
use serde::Serialize;
use std::collections::{BTreeMap, HashMap};

/// What the last check found for one repo. A failed check keeps the pull
/// requests from the one before.
#[derive(Debug, Clone, Default)]
pub struct RepoCheck {
    pub prs: Vec<PullRequest>,
    pub checked_at_ms: Option<i64>,
    pub error: Option<String>,
}

/// The new commits between a reviewed commit and a pull request's head.
pub type CompareKey = (String, String, String);

#[derive(Debug, Default)]
pub struct CheckState {
    pub checking: bool,
    pub checked_at_ms: Option<i64>,
    pub error: Option<AppError>,
    /// Cached repo ids, from the last `ws cache list`.
    pub cached: Vec<String>,
    /// Hosts `gh` is signed in to, with the login on each.
    pub hosts: BTreeMap<String, String>,
    pub repos: BTreeMap<String, RepoCheck>,
    pub compares: HashMap<CompareKey, NewCommits>,
    pub warnings: Vec<String>,
    /// How many checks in a row hit a rate limit.
    pub backoff: u32,
}

#[derive(Debug, Clone, Copy, Serialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub enum Group {
    ToReview,
    Reviewing,
    Reviewed,
    Dismissed,
}

#[derive(Debug, Clone, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct ReviewView {
    pub workspace_id: String,
    pub session_id: Option<String>,
    pub sha: String,
    pub status: Status,
    pub created_at: Option<String>,
    pub detail: Option<String>,
    pub result: Option<String>,
    pub links: Vec<Link>,
    pub auto: bool,
    /// Whether `ws review start` made it, so its report can be read.
    pub recorded: bool,
    /// The workspace its record names as having asked for it.
    pub requester: Option<String>,
}

#[derive(Debug, Clone, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct PrView {
    pub key: String,
    pub repo: String,
    #[serde(flatten)]
    pub pr: PullRequest,
    pub group: Group,
    pub out_of_date: bool,
    pub requested: bool,
    pub mine: bool,
    /// Newest first.
    pub reviews: Vec<ReviewView>,
    pub new_commits: Option<NewCommits>,
}

#[derive(Debug, Clone, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct RepoView {
    pub id: String,
    pub github: bool,
    pub watch: bool,
    pub auto_review: bool,
    pub open: Option<usize>,
    pub checked_at_ms: Option<i64>,
    pub error: Option<String>,
}

#[derive(Debug, Clone, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct ReviewsView {
    pub checking: bool,
    pub checked_at_ms: Option<i64>,
    pub error: Option<AppError>,
    pub repos: Vec<RepoView>,
    pub prs: Vec<PrView>,
    pub warnings: Vec<String>,
}

/// Review statuses that mean the agent is still on it.
pub fn is_reviewing(status: Status) -> bool {
    matches!(
        status,
        Status::Working | Status::Starting | Status::Pending | Status::NeedsInput
    )
}

/// Whether a workspace reviews the pull request: by its review record's
/// repo and `pr.number` (ADR 0039), or, for a workspace without one, by
/// ADR 0032's name.
pub fn is_review_of(entry: &Entry, repo: &str, number: u64) -> bool {
    match &entry.review {
        Some(record) => {
            record.repo == repo && record.pr.as_ref().is_some_and(|pr| pr.number == number)
        }
        None => entry
            .workspace_id
            .as_deref()
            .is_some_and(|id| names::is_review_of(id, &entry.repos, repo, number)),
    }
}

/// The review workspaces of a pull request, newest first.
pub fn reviews_of<'a>(entries: &'a [Entry], repo: &str, number: u64) -> Vec<&'a Entry> {
    let mut found: Vec<&Entry> = entries
        .iter()
        .filter(|e| e.kind != Kind::Unknown)
        .filter(|e| is_review_of(e, repo, number))
        .collect();
    found.sort_by(|a, b| b.created_at.cmp(&a.created_at).then(b.key.cmp(&a.key)));
    found
}

/// The commit a review looked at: its record's `ref`, else its repo's.
fn reviewed_sha(entry: &Entry) -> String {
    if let Some(record) = &entry.review {
        return record.git_ref.clone();
    }
    entry
        .repos
        .first()
        .and_then(|r| r.git_ref.clone())
        .unwrap_or_default()
}

/// Whether a pull request has a review workspace at its current head.
pub fn has_review_at_head(entries: &[Entry], repo: &str, pr: &PullRequest) -> bool {
    reviews_of(entries, repo, pr.number)
        .iter()
        .any(|e| names::same_commit(&reviewed_sha(e), &pr.head_ref_oid))
}

/// The commit of the newest review, when it isn't the pull request's head.
pub fn out_of_date_sha(entries: &[Entry], repo: &str, pr: &PullRequest) -> Option<String> {
    let newest = reviews_of(entries, repo, pr.number).into_iter().next()?;
    let sha = reviewed_sha(newest);
    (!names::same_commit(&sha, &pr.head_ref_oid)).then_some(sha)
}

fn group_of(dismissed: bool, newest: Option<&Entry>) -> Group {
    if dismissed {
        return Group::Dismissed;
    }
    match newest {
        None => Group::ToReview,
        Some(e) if is_reviewing(e.status) => Group::Reviewing,
        Some(_) => Group::Reviewed,
    }
}

fn pr_view(
    repo: &str,
    pr: &PullRequest,
    check: &CheckState,
    entries: &[Entry],
    state: &StateFile,
) -> PrView {
    let host = github::split_repo(repo).map_or("", |(h, _, _)| h);
    let login = check.hosts.get(host).map(String::as_str);
    let found = reviews_of(entries, repo, pr.number);
    let dismissed = state
        .reviews
        .is_dismissed(repo, pr.number, &pr.head_ref_oid);
    let out_of_date = out_of_date_sha(entries, repo, pr);
    let new_commits = out_of_date.as_ref().and_then(|sha| {
        check
            .compares
            .get(&(repo.to_string(), sha.clone(), pr.head_ref_oid.clone()))
            .cloned()
    });
    PrView {
        key: format!("{repo}#{}", pr.number),
        repo: repo.to_string(),
        pr: pr.clone(),
        group: group_of(dismissed, found.first().copied()),
        out_of_date: out_of_date.is_some(),
        requested: pr.review_requested_from(login),
        mine: login.is_some_and(|l| l.eq_ignore_ascii_case(&pr.author.login)),
        reviews: found
            .iter()
            .map(|e| ReviewView {
                workspace_id: e.workspace_id.clone().unwrap_or_default(),
                session_id: e.session_id.clone(),
                sha: reviewed_sha(e),
                status: e.status,
                created_at: e.created_at.clone(),
                detail: e.detail.clone(),
                result: e.result.clone(),
                links: e.links.clone(),
                auto: e
                    .workspace_id
                    .as_deref()
                    .is_some_and(|id| state.reviews.is_auto_workspace(id)),
                recorded: e.review.is_some(),
                requester: e
                    .review
                    .as_ref()
                    .map(|r| r.requester.clone())
                    .filter(|r| !r.is_empty()),
            })
            .collect(),
        new_commits,
    }
}

/// The watched repos `gh` can check: cached, on a GitHub host, and watched.
pub fn watched_repos(check: &CheckState, state: &StateFile) -> Vec<String> {
    check
        .cached
        .iter()
        .filter(|id| github::is_github(id, &check.hosts) && state.reviews.watch(id).watch)
        .cloned()
        .collect()
}

pub fn build(check: &CheckState, entries: &[Entry], state: &StateFile) -> ReviewsView {
    let watched = watched_repos(check, state);
    let repos = check
        .cached
        .iter()
        .map(|id| {
            let watch = state.reviews.watch(id);
            let result = check.repos.get(id);
            RepoView {
                id: id.clone(),
                github: github::is_github(id, &check.hosts),
                watch: watch.watch,
                auto_review: watch.auto_review,
                open: result
                    .filter(|r| r.checked_at_ms.is_some())
                    .map(|r| r.prs.len()),
                checked_at_ms: result.and_then(|r| r.checked_at_ms),
                error: result.and_then(|r| r.error.clone()),
            }
        })
        .collect();
    let prs = watched
        .iter()
        .flat_map(|repo| {
            check
                .repos
                .get(repo)
                .into_iter()
                .flat_map(|r| r.prs.iter())
                .map(move |pr| (repo, pr))
        })
        .map(|(repo, pr)| pr_view(repo, pr, check, entries, state))
        .collect();
    ReviewsView {
        checking: check.checking,
        checked_at_ms: check.checked_at_ms,
        error: check.error.clone(),
        repos,
        prs,
        warnings: check.warnings.clone(),
    }
}

#[cfg(test)]
pub mod tests {
    use super::*;
    use crate::model::{Repo, ReviewPr, ReviewRecord};
    use crate::reviews::github::{Author, ReviewRequest};
    use crate::reviews::store::{PrHead, RepoWatch};

    pub const REPO: &str = "github.com/bobcob7/buff-bear";
    pub const HEAD: &str = "3f2a9c1e0b6d4a2f9e8c7b6a5d4c3b2a1f0e9d8c";
    pub const OLD: &str = "a41c9f2d0b6d4a2f9e8c7b6a5d4c3b2a1f0e9d8c";

    pub fn pr(number: u64) -> PullRequest {
        PullRequest {
            number,
            title: format!("PR {number}"),
            author: Author {
                login: "alice".to_string(),
            },
            url: format!("https://github.com/bobcob7/buff-bear/pull/{number}"),
            body: String::new(),
            is_draft: false,
            head_ref_oid: HEAD.to_string(),
            head_ref_name: "feat/x".to_string(),
            base_ref_name: "main".to_string(),
            additions: 1,
            deletions: 1,
            changed_files: 1,
            review_requests: vec![ReviewRequest {
                login: Some("bobcob7".to_string()),
            }],
            created_at: "2026-10-24T09:00:00Z".to_string(),
            updated_at: "2026-10-24T09:00:00Z".to_string(),
        }
    }

    pub fn review(number: u64, sha: &str, status: Status, created_at: &str) -> Entry {
        let id = names::review_name(REPO, number, sha);
        Entry {
            key: id.clone(),
            kind: Kind::Agent,
            name: id.clone(),
            workspace_id: Some(id),
            session_id: Some("s1".to_string()),
            path: "/w".to_string(),
            status,
            stale: Some(false),
            created_at: Some(created_at.to_string()),
            updated_at: None,
            detail: None,
            intent: None,
            result: Some("3 findings".to_string()),
            links: vec![],
            repos: vec![Repo {
                id: REPO.to_string(),
                path: "/w/buff-bear".to_string(),
                git_ref: Some(sha.to_string()),
                ref_kind: Some("commit".to_string()),
            }],
            review: None,
            hidden: false,
        }
    }

    /// A review `ws review start` made, under an id that isn't 0032's name.
    pub fn recorded(number: u64, sha: &str, status: Status, created_at: &str) -> Entry {
        Entry {
            key: "careful-look-d8efb24f".to_string(),
            workspace_id: Some("careful-look-d8efb24f".to_string()),
            review: Some(ReviewRecord {
                repo: REPO.to_string(),
                git_ref: sha.to_string(),
                requester: String::new(),
                profiles: vec![],
                pr: Some(ReviewPr {
                    number,
                    url: String::new(),
                }),
            }),
            ..review(number, sha, status, created_at)
        }
    }

    pub fn checked(prs: Vec<PullRequest>) -> CheckState {
        CheckState {
            cached: vec![REPO.to_string(), "gitlab.example.com/team/svc".to_string()],
            hosts: BTreeMap::from([("github.com".to_string(), "bobcob7".to_string())]),
            repos: BTreeMap::from([(
                REPO.to_string(),
                RepoCheck {
                    prs,
                    checked_at_ms: Some(1),
                    error: None,
                },
            )]),
            ..CheckState::default()
        }
    }

    pub fn watching() -> StateFile {
        let mut state = StateFile::default();
        state.reviews.set_watch(
            REPO,
            RepoWatch {
                watch: true,
                auto_review: false,
            },
        );
        state
    }

    fn group(entries: &[Entry], state: &StateFile) -> (Group, bool) {
        let view = build(&checked(vec![pr(42)]), entries, state);
        (view.prs[0].group, view.prs[0].out_of_date)
    }

    #[test]
    fn no_review_is_to_review() {
        assert_eq!(group(&[], &watching()), (Group::ToReview, false));
    }

    #[test]
    fn working_review_is_reviewing() {
        let entries = [review(42, HEAD, Status::Working, "2026-10-24T10:00:00Z")];
        assert_eq!(group(&entries, &watching()), (Group::Reviewing, false));
    }

    #[test]
    fn review_waiting_for_input_is_reviewing() {
        let entries = [review(42, HEAD, Status::NeedsInput, "2026-10-24T10:00:00Z")];
        assert_eq!(group(&entries, &watching()).0, Group::Reviewing);
    }

    #[test]
    fn finished_review_at_head_is_reviewed() {
        let entries = [review(42, HEAD, Status::Done, "2026-10-24T10:00:00Z")];
        assert_eq!(group(&entries, &watching()), (Group::Reviewed, false));
    }

    #[test]
    fn stopped_review_of_old_commit_is_reviewed_out_of_date() {
        let entries = [review(42, OLD, Status::Stopped, "2026-10-24T10:00:00Z")];
        assert_eq!(group(&entries, &watching()), (Group::Reviewed, true));
    }

    #[test]
    fn newest_review_decides() {
        let entries = [
            review(42, OLD, Status::Done, "2026-10-24T10:00:00Z"),
            review(42, HEAD, Status::Working, "2026-10-24T11:00:00Z"),
        ];
        let view = build(&checked(vec![pr(42)]), &entries, &watching());
        assert_eq!(view.prs[0].group, Group::Reviewing);
        assert_eq!(view.prs[0].reviews[0].sha, HEAD);
    }

    #[test]
    fn dismissed_at_head_is_dismissed() {
        let mut state = watching();
        state.reviews.dismiss(PrHead {
            repo: REPO.to_string(),
            number: 42,
            head: HEAD.to_string(),
        });
        assert_eq!(group(&[], &state).0, Group::Dismissed);
    }

    #[test]
    fn push_after_dismissal_un_dismisses() {
        let mut state = watching();
        state.reviews.dismiss(PrHead {
            repo: REPO.to_string(),
            number: 42,
            head: OLD.to_string(),
        });
        assert_eq!(group(&[], &state).0, Group::ToReview);
    }

    #[test]
    fn unwatched_repo_shows_no_pull_requests() {
        let view = build(&checked(vec![pr(42)]), &[], &StateFile::default());
        assert!(view.prs.is_empty());
        assert_eq!(view.repos[0].open, Some(1));
    }

    #[test]
    fn non_github_repo_is_listed_as_not_github() {
        let view = build(&checked(vec![]), &[], &watching());
        assert!(!view.repos[1].github);
    }

    #[test]
    fn requested_and_mine_use_host_login() {
        let mut mine = pr(7);
        mine.author.login = "BobCob7".to_string();
        let view = build(&checked(vec![mine]), &[], &watching());
        assert!(view.prs[0].requested);
        assert!(view.prs[0].mine);
    }

    #[test]
    fn auto_started_review_is_labelled_auto() {
        let mut state = watching();
        state
            .reviews
            .auto_started
            .push(crate::reviews::store::AutoStarted {
                repo: REPO.to_string(),
                number: 42,
                head: HEAD.to_string(),
                workspace: names::review_name(REPO, 42, HEAD),
            });
        let entries = [review(42, HEAD, Status::Done, "2026-10-24T10:00:00Z")];
        let view = build(&checked(vec![pr(42)]), &entries, &state);
        assert!(view.prs[0].reviews[0].auto);
    }

    #[test]
    fn review_record_matches_whatever_the_workspace_is_called() {
        let entries = [recorded(42, HEAD, Status::Working, "2026-10-24T10:00:00Z")];
        assert_eq!(group(&entries, &watching()), (Group::Reviewing, false));
    }

    #[test]
    fn review_record_of_another_pull_request_does_not_match_by_name() {
        let mut other = review(42, HEAD, Status::Working, "2026-10-24T10:00:00Z");
        other.review = recorded(7, HEAD, Status::Working, "").review;
        assert_eq!(group(&[other], &watching()).0, Group::ToReview);
    }

    #[test]
    fn review_record_without_pull_request_is_not_a_review_of_one() {
        let mut commit = recorded(42, HEAD, Status::Working, "2026-10-24T10:00:00Z");
        commit.workspace_id = Some(names::review_name(REPO, 42, HEAD));
        if let Some(record) = commit.review.as_mut() {
            record.pr = None;
        }
        assert_eq!(group(&[commit], &watching()).0, Group::ToReview);
    }

    #[test]
    fn review_record_ref_is_the_reviewed_commit() {
        let mut entry = recorded(42, OLD, Status::Done, "2026-10-24T10:00:00Z");
        entry.repos[0].git_ref = Some(HEAD.to_string());
        assert_eq!(group(&[entry], &watching()), (Group::Reviewed, true));
    }

    #[test]
    fn recorded_review_names_its_requester() {
        let mut entry = recorded(42, HEAD, Status::Done, "2026-10-24T10:00:00Z");
        if let Some(record) = entry.review.as_mut() {
            record.requester = "fix-login-d8efb24f".to_string();
        }
        let view = build(&checked(vec![pr(42)]), &[entry], &watching());
        let review = &view.prs[0].reviews[0];
        assert!(review.recorded);
        assert_eq!(review.requester.as_deref(), Some("fix-login-d8efb24f"));
    }

    #[test]
    fn review_without_record_has_no_report_or_requester() {
        let entries = [review(42, HEAD, Status::Done, "2026-10-24T10:00:00Z")];
        let view = build(&checked(vec![pr(42)]), &entries, &watching());
        let review = &view.prs[0].reviews[0];
        assert!(!review.recorded);
        assert_eq!(review.requester, None);
    }

    #[test]
    fn view_serializes_pull_request_fields_flat() {
        let view = build(&checked(vec![pr(42)]), &[], &watching());
        let json = serde_json::to_value(&view.prs[0]).unwrap();
        assert_eq!(json["key"], "github.com/bobcob7/buff-bear#42");
        assert_eq!(json["headRefOid"], HEAD);
        assert_eq!(json["group"], "toReview");
    }
}
