//! Reviews whose agent and workspace `ws` removed once every reviewer had
//! reported, from `ws review list` (ADR 0045). The Reviews view treats each
//! as a finished review of its pull request, so the pull request stays
//! Reviewed and isn't auto-reviewed again.

use crate::model::{Entry, Kind, ReviewRecord, Status};
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

/// The removed reviews `ws review list --json` lists: those with no agent.
pub async fn removed(runner: &Runner) -> Result<Vec<Entry>, AppError> {
    let args = ["review".to_string(), "list".to_string()];
    let list: ReviewList = runner.ws_json(&args).await?;
    Ok(entries_of(list))
}

fn entries_of(list: ReviewList) -> Vec<Entry> {
    list.reviews
        .into_iter()
        .filter(|r| r.agent.is_none())
        .filter_map(|r| {
            let started_at = r.review["started_at"].as_str().map(str::to_string);
            let record = serde_json::from_value::<ReviewRecord>(r.review).ok()?;
            Some(entry(&r.id, record, started_at))
        })
        .collect()
}

/// A removed review as the Reviews view reads it: a finished review agent
/// of kind `removed`.
pub fn entry(id: &str, record: ReviewRecord, created_at: Option<String>) -> Entry {
    Entry {
        key: format!("run:{id}"),
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
    }
}

/// An entry that was a review workspace, now removed.
pub fn removed_from(e: &Entry) -> Option<Entry> {
    let id = e.workspace_id.as_deref()?;
    let record = e.review.clone()?;
    Some(entry(id, record, e.created_at.clone()))
}

/// Entries and the removed reviews whose id no entry has, for the
/// Reviews view.
pub fn merge(entries: &[Entry], removed: &[Entry]) -> Vec<Entry> {
    let mut out = entries.to_vec();
    out.extend(
        removed
            .iter()
            .filter(|r| !entries.iter().any(|e| e.workspace_id == r.workspace_id))
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
        let entries = entries_of(list);
        assert_eq!(entries.len(), 1);
        let e = &entries[0];
        assert_eq!(e.kind, Kind::Removed);
        assert_eq!(e.status, Status::Done);
        assert_eq!(e.workspace_id.as_deref(), Some("review-41"));
        assert_eq!(e.created_at.as_deref(), Some("2026-10-28T09:00:00Z"));
        assert_eq!(e.review.as_ref().unwrap().pr.as_ref().unwrap().number, 41);
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
