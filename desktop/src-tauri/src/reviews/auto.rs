//! Which pull requests a check starts auto-reviews for (ADR 0035).

use super::github::{self, PullRequest};
use super::store::StateFile;
use super::view;
use crate::model::{Entry, Status};
use std::collections::BTreeMap;

/// Auto-started reviews whose agent is still working on it.
pub fn running(entries: &[Entry], state: &StateFile) -> usize {
    entries
        .iter()
        .filter(|e| {
            matches!(
                e.status,
                Status::Working | Status::Starting | Status::Pending
            )
        })
        .filter(|e| {
            e.workspace_id
                .as_deref()
                .is_some_and(|id| state.reviews.is_auto_workspace(id))
        })
        .count()
}

fn passes_rules(
    repo: &str,
    pr: &PullRequest,
    state: &StateFile,
    logins: &BTreeMap<String, String>,
) -> bool {
    let rules = &state.settings.reviews;
    let host = github::split_repo(repo).map_or("", |(h, _, _)| h);
    let login = logins.get(host).map(String::as_str);
    if rules.skip_drafts && pr.is_draft {
        return false;
    }
    if rules.skip_mine && login.is_some_and(|l| l.eq_ignore_ascii_case(&pr.author.login)) {
        return false;
    }
    !rules.only_requested || pr.review_requested_from(login)
}

/// The pull requests to start reviews for now, oldest update first, up to
/// the free places under the limit. Each must be in an auto-review repo, not
/// dismissed, without a review at its head, never auto-reviewed at its head
/// (so removing one doesn't start it again), and pass the rules.
pub fn select<'a>(
    prs: &[(&'a str, &'a PullRequest)],
    entries: &[Entry],
    state: &StateFile,
    logins: &BTreeMap<String, String>,
) -> Vec<(&'a str, &'a PullRequest)> {
    let limit = state.settings.reviews.max_auto_reviews as usize;
    let free = limit.saturating_sub(running(entries, state));
    let mut candidates: Vec<(&str, &PullRequest)> = prs
        .iter()
        .copied()
        .filter(|(repo, _)| state.reviews.watch(repo).auto_review)
        .filter(|(repo, pr)| {
            !state
                .reviews
                .is_dismissed(repo, pr.number, &pr.head_ref_oid)
        })
        .filter(|(repo, pr)| {
            !state
                .reviews
                .was_auto_started(repo, pr.number, &pr.head_ref_oid)
        })
        .filter(|(repo, pr)| !view::has_review_at_head(entries, repo, pr))
        .filter(|(repo, pr)| passes_rules(repo, pr, state, logins))
        .collect();
    candidates.sort_by(|a, b| {
        a.1.updated_at
            .cmp(&b.1.updated_at)
            .then(a.1.number.cmp(&b.1.number))
    });
    candidates.truncate(free);
    candidates
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::reviews::names;
    use crate::reviews::store::{AutoStarted, PrHead, RepoWatch};
    use crate::reviews::view::tests::{pr, review, HEAD, OLD, REPO};

    fn auto_state() -> StateFile {
        let mut state = StateFile::default();
        state.reviews.set_watch(
            REPO,
            RepoWatch {
                watch: true,
                auto_review: true,
            },
        );
        state
    }

    fn logins() -> BTreeMap<String, String> {
        BTreeMap::from([("github.com".to_string(), "bobcob7".to_string())])
    }

    fn numbers(prs: &[PullRequest], entries: &[Entry], state: &StateFile) -> Vec<u64> {
        let pairs: Vec<(&str, &PullRequest)> = prs.iter().map(|p| (REPO, p)).collect();
        select(&pairs, entries, state, &logins())
            .iter()
            .map(|(_, p)| p.number)
            .collect()
    }

    #[test]
    fn starts_new_pull_request_in_auto_review_repo() {
        assert_eq!(numbers(&[pr(1)], &[], &auto_state()), [1]);
    }

    #[test]
    fn watched_repo_without_auto_review_starts_nothing() {
        let mut state = auto_state();
        state.reviews.set_watch(
            REPO,
            RepoWatch {
                watch: true,
                auto_review: false,
            },
        );
        assert!(numbers(&[pr(1)], &[], &state).is_empty());
    }

    #[test]
    fn skips_drafts_when_set() {
        let mut draft = pr(1);
        draft.is_draft = true;
        assert!(numbers(&[draft.clone()], &[], &auto_state()).is_empty());
        let mut state = auto_state();
        state.settings.reviews.skip_drafts = false;
        assert_eq!(numbers(&[draft], &[], &state), [1]);
    }

    #[test]
    fn skips_my_own_when_set() {
        let mut mine = pr(1);
        mine.author.login = "bobcob7".to_string();
        assert!(numbers(&[mine], &[], &auto_state()).is_empty());
    }

    #[test]
    fn only_requested_skips_pull_requests_not_asking_me() {
        let mut state = auto_state();
        state.settings.reviews.only_requested = true;
        let mut other = pr(2);
        other.review_requests.clear();
        assert_eq!(numbers(&[pr(1), other], &[], &state), [1]);
    }

    #[test]
    fn skips_dismissed() {
        let mut state = auto_state();
        state.reviews.dismiss(PrHead {
            repo: REPO.to_string(),
            number: 1,
            head: HEAD.to_string(),
        });
        assert!(numbers(&[pr(1)], &[], &state).is_empty());
    }

    #[test]
    fn skips_pull_request_reviewed_at_head() {
        let entries = [review(1, HEAD, Status::Done, "2026-10-24T10:00:00Z")];
        assert!(numbers(&[pr(1)], &entries, &auto_state()).is_empty());
    }

    #[test]
    fn new_head_starts_new_review() {
        let entries = [review(1, OLD, Status::Done, "2026-10-24T10:00:00Z")];
        assert_eq!(numbers(&[pr(1)], &entries, &auto_state()), [1]);
    }

    #[test]
    fn removed_auto_review_is_not_started_again() {
        let mut state = auto_state();
        state.reviews.auto_started.push(AutoStarted {
            repo: REPO.to_string(),
            number: 1,
            head: HEAD.to_string(),
            workspace: names::review_name(REPO, 1, HEAD),
        });
        assert!(numbers(&[pr(1)], &[], &state).is_empty());
    }

    #[test]
    fn caps_at_limit_oldest_first() {
        let mut prs: Vec<PullRequest> = (1..=4).map(pr).collect();
        prs[0].updated_at = "2026-10-24T12:00:00Z".to_string();
        assert_eq!(numbers(&prs, &[], &auto_state()), [2, 3]);
    }

    #[test]
    fn running_auto_reviews_take_places() {
        let mut state = auto_state();
        let running_review = review(9, HEAD, Status::Working, "2026-10-24T10:00:00Z");
        state.reviews.auto_started.push(AutoStarted {
            repo: REPO.to_string(),
            number: 9,
            head: HEAD.to_string(),
            workspace: running_review.key.clone(),
        });
        assert_eq!(numbers(&[pr(1), pr(2)], &[running_review], &state), [1]);
    }

    #[test]
    fn reviews_started_by_hand_do_not_count() {
        let by_hand = review(9, HEAD, Status::Working, "2026-10-24T10:00:00Z");
        assert_eq!(numbers(&[pr(1), pr(2)], &[by_hand], &auto_state()), [1, 2]);
    }

    #[test]
    fn finished_auto_reviews_free_their_place() {
        let mut state = auto_state();
        let done = review(9, HEAD, Status::Done, "2026-10-24T10:00:00Z");
        state.reviews.auto_started.push(AutoStarted {
            repo: REPO.to_string(),
            number: 9,
            head: HEAD.to_string(),
            workspace: done.key.clone(),
        });
        assert_eq!(numbers(&[pr(1), pr(2)], &[done], &state), [1, 2]);
    }
}
