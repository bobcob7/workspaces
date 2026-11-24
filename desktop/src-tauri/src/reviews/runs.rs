//! Reviews whose agent and workspace `ws` removed once every reviewer had
//! reported, from `ws review list` (ADR 0045). The Reviews view treats each
//! as a finished review of its pull request, so the pull request stays
//! Reviewed and isn't auto-reviewed again.

use crate::model::{Entry, Kind, ReviewRecord, Status};
use crate::pty::remote_env;
use crate::runner::{AppError, Runner};
use serde::Deserialize;
use serde_json::Value;

#[derive(Deserialize)]
struct ReviewList {
    reviews: Vec<ReviewRun>,
}

#[derive(Deserialize)]
struct ReviewRun {
    id: String,
    review: Value,
    agent: Option<Value>,
}

/// The reviews `ws review list --json` lists. Here the removed ones, those
/// with no agent; on an environment, the runs of every review there, since
/// the Agents listing keeps none of that environment's reviews (R1).
pub async fn removed(runner: &Runner, environment: Option<&str>) -> Result<Vec<Entry>, AppError> {
    let mut args = ["review".to_string(), "list".to_string()].to_vec();
    args.extend(crate::commands::env_args(environment));
    let list: ReviewList = runner.ws_json(&args).await?;
    Ok(entries_of(list, remote_env(environment)))
}

fn entries_of(list: ReviewList, environment: Option<&str>) -> Vec<Entry> {
    list.reviews
        .into_iter()
        .filter(|r| environment.is_some() || r.agent.is_none())
        .filter_map(|r| {
            let started_at = r.review["started_at"].as_str().map(str::to_string);
            let record = serde_json::from_value::<ReviewRecord>(r.review).ok()?;
            let mut run = entry_in(environment.unwrap_or("local"), &r.id, record, started_at);
            if let Some(agent) = &r.agent {
                run.kind = Kind::Agent;
                run.status = agent_status(agent["status"].as_str().unwrap_or_default());
                run.hidden = false;
            }
            Some(run)
        })
        .collect()
}

/// A review agent's status as `ws review list` prints it.
fn agent_status(state: &str) -> Status {
    match state {
        "working" => Status::Working,
        "starting" => Status::Starting,
        "stopped" => Status::Stopped,
        "running" => Status::Running,
        "idle" => Status::Idle,
        _ => Status::Unknown,
    }
}

/// A removed review as the Reviews view reads it: a finished review agent
/// of kind `removed`.
pub fn entry(id: &str, record: ReviewRecord, created_at: Option<String>) -> Entry {
    entry_in("local", id, record, created_at)
}

/// A removed review on `environment` ("local" or a name).
pub fn entry_in(
    environment: &str,
    id: &str,
    record: ReviewRecord,
    created_at: Option<String>,
) -> Entry {
    let key = if environment == "local" {
        format!("run:{id}")
    } else {
        format!("env:{environment}:run:{id}")
    };
    Entry {
        key,
        kind: Kind::Removed,
        name: id.to_string(),
        workspace_id: Some(id.to_string()),
        session_id: None,
        path: String::new(),
        status: Status::Done,
        stale: None,
        created_at,
        updated_at: None,
        detail: None,
        intent: None,
        result: None,
        links: Vec::new(),
        repos: Vec::new(),
        review: Some(record),
        hidden: true,
        parent_id: None,
        loop_run: None,
        runner: String::new(),
        model: None,
        ws_runner: None,
        progress: None,
        rollup: None,
        environment: environment.to_string(),
        environment_online: true,
        snapshot_stale: false,
    }
}

/// An entry that was a review workspace, now removed.
pub fn removed_from(e: &Entry) -> Option<Entry> {
    let id = e.workspace_id.as_deref()?;
    let record = e.review.clone()?;
    Some(entry_in(&e.environment, id, record, e.created_at.clone()))
}

/// Whether an entry on `environment` has this workspace id. A workspace id
/// is only unique on its own machine, so another environment's entry with
/// the same id is not the review's workspace.
pub fn has_workspace(entries: &[Entry], environment: &str, workspace_id: &Option<String>) -> bool {
    entries
        .iter()
        .any(|e| e.environment == environment && &e.workspace_id == workspace_id)
}

/// Entries and the runs whose id no entry on their own environment has, for
/// the Reviews view.
pub fn merge(entries: &[Entry], runs: &[Entry]) -> Vec<Entry> {
    let mut out = entries.to_vec();
    out.extend(
        runs.iter()
            .filter(|r| !has_workspace(entries, &r.environment, &r.workspace_id))
            .cloned(),
    );
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    const LIST: &str = r#"{"reviews":[
        {"id":"review-41","review":{"repo":"github.com/o/r","ref":"9c04e2a7d1f3","requester":"","started_at":"2026-10-28T09:00:00Z","profiles":[],"pr":{"number":41,"url":"u"}},"agent":null},
        {"id":"review-42","review":{"repo":"github.com/o/r","ref":"3f2a9c1e","requester":"","started_at":"2026-10-28T10:00:00Z","profiles":[],"pr":{"number":42,"url":"u"}},"agent":{"workspace":"review-42","status":"working"}}
    ],"warnings":[]}"#;

    #[test]
    fn only_runs_without_an_agent_are_removed_reviews() {
        let list: ReviewList = serde_json::from_str(LIST).unwrap();
        let entries = entries_of(list, None);
        assert_eq!(entries.len(), 1);
        let e = &entries[0];
        assert_eq!(e.kind, Kind::Removed);
        assert_eq!(e.status, Status::Done);
        assert_eq!(e.workspace_id.as_deref(), Some("review-41"));
        assert_eq!(e.created_at.as_deref(), Some("2026-10-28T09:00:00Z"));
        assert_eq!(e.review.as_ref().unwrap().pr.as_ref().unwrap().number, 41);
    }

    #[test]
    fn a_remote_list_keeps_runs_with_an_agent_in_their_environment() {
        let list: ReviewList = serde_json::from_str(LIST).unwrap();
        let entries = entries_of(list, Some("linux-server"));
        assert_eq!(entries.len(), 2);
        assert!(entries.iter().all(|e| e.environment == "linux-server"));
        assert_eq!(entries[0].key, "env:linux-server:run:review-41");
        assert_eq!(
            (entries[0].kind, entries[0].status),
            (Kind::Removed, Status::Done)
        );
        assert_eq!(
            (entries[1].kind, entries[1].status),
            (Kind::Agent, Status::Working)
        );
        assert!(!entries[1].hidden);
    }

    #[test]
    fn a_local_list_ignores_the_environment_named_local() {
        let list: ReviewList = serde_json::from_str(LIST).unwrap();
        assert_eq!(entries_of(list, remote_env(Some("local"))).len(), 1);
    }

    #[test]
    fn a_removed_remote_review_stays_remote() {
        let record = ReviewRecord {
            repo: "github.com/o/r".into(),
            git_ref: "abc".into(),
            requester: String::new(),
            profiles: vec![],
            pr: None,
        };
        let remote = Entry {
            kind: Kind::Agent,
            ..entry_in("linux-server", "a", record, None)
        };
        let gone = removed_from(&remote).unwrap();
        assert_eq!(gone.environment, "linux-server");
        assert_eq!(gone.kind, Kind::Removed);
    }

    #[test]
    fn a_remote_run_survives_a_local_workspace_with_its_id() {
        let record = ReviewRecord {
            repo: "github.com/o/r".into(),
            git_ref: "abc".into(),
            requester: String::new(),
            profiles: vec![],
            pr: None,
        };
        let local = Entry {
            kind: Kind::Agent,
            ..entry("a", record.clone(), None)
        };
        let remote_run = entry_in("linux-server", "a", record, None);
        let merged = merge(&[local], std::slice::from_ref(&remote_run));
        assert_eq!(merged.len(), 2);
        assert_eq!(merged[1].environment, "linux-server");
        let listed = Entry {
            kind: Kind::Agent,
            ..remote_run.clone()
        };
        assert_eq!(merge(&[listed], &[remote_run]).len(), 1);
    }

    #[test]
    fn remote_review_workspace_is_not_joined() {
        let record = ReviewRecord {
            repo: "github.com/o/r".into(),
            git_ref: "abc".into(),
            requester: String::new(),
            profiles: vec![],
            pr: None,
        };
        let removed = vec![entry("a", record.clone(), None)];
        let remote = Entry {
            kind: Kind::Agent,
            environment: "linux-server".into(),
            ..entry("a", record, None)
        };
        let merged = merge(&[remote], &removed);
        assert_eq!(merged.len(), 2);
        assert_eq!(merged[1].kind, Kind::Removed);
    }

    #[test]
    fn merge_prefers_a_listed_workspace() {
        let record = ReviewRecord {
            repo: "github.com/o/r".into(),
            git_ref: "abc".into(),
            requester: String::new(),
            profiles: vec![],
            pr: None,
        };
        let removed = vec![
            entry("a", record.clone(), None),
            entry("b", record.clone(), None),
        ];
        let listed = Entry {
            kind: Kind::Agent,
            ..entry("a", record, None)
        };
        let merged = merge(&[listed], &removed);
        assert_eq!(merged.len(), 2);
        assert_eq!(merged[0].kind, Kind::Agent);
        assert_eq!(merged[1].workspace_id.as_deref(), Some("b"));
    }
}
