//! The list the app shows: `ws list --json` workspaces, plus Claude Code
//! background sessions that aren't in a workspace, with detail from each
//! session's job state.

use crate::jobs::JobState;
use serde::{Deserialize, Serialize};
use serde_json::Value;

/// How long a session without a process counts as starting, as in
/// `ws list` (ADR 0024).
const STARTING_WINDOW_MS: i64 = 10_000;

#[derive(Debug, Deserialize)]
pub struct WsList {
    pub workspaces: Vec<WsWorkspace>,
    #[serde(default)]
    pub warnings: Vec<Value>,
}

#[derive(Debug, Deserialize)]
pub struct WsWorkspace {
    pub id: Option<String>,
    /// The display name, from `--name` or `ws rename` (ADR 0036).
    #[serde(default)]
    pub name: Option<String>,
    pub path: String,
    pub created_at: Option<String>,
    pub session: Option<WsSession>,
    #[serde(default)]
    pub repos: Vec<Repo>,
}

#[derive(Debug, Deserialize)]
pub struct WsSession {
    pub kind: String,
    pub id: Option<String>,
    pub state: String,
    pub stale: Option<bool>,
}

#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct Repo {
    pub id: String,
    pub path: String,
    #[serde(rename = "ref")]
    pub git_ref: Option<String>,
    pub ref_kind: Option<String>,
}

/// One entry of `claude agents --json --all`.
#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ClaudeAgent {
    pub id: Option<String>,
    pub name: Option<String>,
    pub cwd: String,
    pub kind: String,
    pub status: Option<String>,
    pub pid: Option<i64>,
    pub started_at: Option<i64>,
}

#[derive(Debug, Clone, Copy, Serialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub enum Status {
    Working,
    NeedsInput,
    Idle,
    Done,
    Starting,
    Stopped,
    Running,
    Pending,
    Unknown,
}

#[derive(Debug, Clone, Copy, Serialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub enum Kind {
    Agent,
    Shell,
    Pending,
    Unknown,
}

#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct Link {
    pub href: String,
    pub kind: String,
}

#[derive(Debug, Clone, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Entry {
    pub key: String,
    pub kind: Kind,
    pub name: String,
    pub workspace_id: Option<String>,
    pub session_id: Option<String>,
    pub path: String,
    pub status: Status,
    pub stale: Option<bool>,
    pub created_at: Option<String>,
    pub updated_at: Option<String>,
    pub detail: Option<String>,
    pub intent: Option<String>,
    pub result: Option<String>,
    pub links: Vec<Link>,
    pub repos: Vec<Repo>,
}

#[derive(Debug, Serialize)]
pub struct Listing {
    pub entries: Vec<Entry>,
    pub warnings: Vec<String>,
}

/// Turns `ws list`'s reading of a session into a status. `ws` can't tell an
/// idle session that is waiting for an answer from one that has finished;
/// Claude Code's own job state can, so it decides among those.
fn agent_status(ws_state: &str, job: Option<&JobState>) -> Status {
    match ws_state {
        "working" => Status::Working,
        "idle" => match job.and_then(|j| j.state.as_deref()) {
            Some("blocked") => Status::NeedsInput,
            Some("done") => Status::Done,
            _ => Status::Idle,
        },
        "starting" => Status::Starting,
        "stopped" => Status::Stopped,
        "running" => Status::Running,
        _ => Status::Unknown,
    }
}

/// Reads a session `ws` doesn't know the way `ws list` reads its own
/// (ADR 0024): by pid and status, not Claude Code's `state`.
fn session_state(agent: &ClaudeAgent, now_ms: i64) -> &'static str {
    if agent.pid.is_none() {
        let recent = agent
            .started_at
            .is_some_and(|t| now_ms - t < STARTING_WINDOW_MS);
        return if recent { "starting" } else { "stopped" };
    }
    if agent.status.as_deref() == Some("busy") {
        return "working";
    }
    "idle"
}

fn links(job: Option<&JobState>) -> Vec<Link> {
    job.and_then(|j| j.children.as_ref())
        .into_iter()
        .flatten()
        .filter_map(|c| {
            Some(Link {
                href: c.href.clone()?,
                kind: c.kind.clone().unwrap_or_default(),
            })
        })
        .collect()
}

fn workspace_entry(ws: WsWorkspace, job_for: &dyn Fn(&str) -> Option<JobState>) -> Entry {
    let name = ws
        .name
        .clone()
        .or_else(|| ws.id.clone())
        .unwrap_or_else(|| ws.path.rsplit('/').next().unwrap_or(&ws.path).to_string());
    let key = ws.id.clone().unwrap_or_else(|| format!("path:{}", ws.path));
    let (kind, status, session_id, stale) = match &ws.session {
        None if ws.id.is_none() => (Kind::Unknown, Status::Unknown, None, None),
        None => (Kind::Pending, Status::Pending, None, None),
        Some(s) if s.kind == "shell" => (Kind::Shell, agent_status(&s.state, None), None, None),
        Some(s) => (Kind::Agent, Status::Unknown, s.id.clone(), s.stale),
    };
    let job = session_id.as_deref().and_then(job_for);
    let status = match (&ws.session, kind) {
        (Some(s), Kind::Agent) => agent_status(&s.state, job.as_ref()),
        _ => status,
    };
    Entry {
        key,
        kind,
        name,
        workspace_id: ws.id,
        session_id,
        path: ws.path,
        status,
        stale,
        created_at: ws.created_at,
        updated_at: job.as_ref().and_then(|j| j.updated_at.clone()),
        detail: job.as_ref().and_then(|j| j.detail.clone()),
        intent: job.as_ref().and_then(|j| j.intent.clone()),
        result: job.as_ref().and_then(|j| j.output.as_ref()?.result.clone()),
        links: links(job.as_ref()),
        repos: ws.repos,
    }
}

fn session_entry(agent: &ClaudeAgent, id: &str, job: Option<JobState>, now_ms: i64) -> Entry {
    Entry {
        key: format!("session:{id}"),
        kind: Kind::Agent,
        name: agent.name.clone().unwrap_or_else(|| id.to_string()),
        workspace_id: None,
        session_id: Some(id.to_string()),
        path: agent.cwd.clone(),
        status: agent_status(session_state(agent, now_ms), job.as_ref()),
        stale: None,
        created_at: None,
        updated_at: job.as_ref().and_then(|j| j.updated_at.clone()),
        detail: job.as_ref().and_then(|j| j.detail.clone()),
        intent: job.as_ref().and_then(|j| j.intent.clone()),
        result: job.as_ref().and_then(|j| j.output.as_ref()?.result.clone()),
        links: links(job.as_ref()),
        repos: Vec::new(),
    }
}

/// Builds the listing: every workspace `ws` lists, then each background
/// session no workspace records, such as one started with `claude --bg`.
pub fn merge(
    ws: Option<WsList>,
    agents: &[ClaudeAgent],
    job_for: &dyn Fn(&str) -> Option<JobState>,
    now_ms: i64,
) -> Vec<Entry> {
    let mut entries: Vec<Entry> = ws
        .map(|l| l.workspaces)
        .unwrap_or_default()
        .into_iter()
        .map(|w| workspace_entry(w, job_for))
        .collect();
    for agent in agents.iter().filter(|a| a.kind == "background") {
        let Some(id) = agent.id.as_deref() else {
            continue;
        };
        if entries.iter().any(|e| e.session_id.as_deref() == Some(id)) {
            continue;
        }
        entries.push(session_entry(agent, id, job_for(id), now_ms));
    }
    entries
}

/// Renders a `ws` warning, which is a string or `{"code", "message"}`.
pub fn warning_text(warning: &Value) -> String {
    match warning {
        Value::String(s) => s.clone(),
        other => other
            .get("message")
            .and_then(Value::as_str)
            .map_or_else(|| other.to_string(), str::to_string),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::jobs::{JobChild, JobOutput};

    const NOW: i64 = 1_800_000_000_000;

    fn job(state: &str) -> JobState {
        JobState {
            state: Some(state.to_string()),
            ..JobState::default()
        }
    }

    fn ws_list(json: &str) -> WsList {
        serde_json::from_str(json).unwrap()
    }

    fn agent(id: &str, pid: Option<i64>, status: Option<&str>, started_at: i64) -> ClaudeAgent {
        ClaudeAgent {
            id: Some(id.to_string()),
            name: Some(format!("name-{id}")),
            cwd: format!("/elsewhere/{id}"),
            kind: "background".to_string(),
            status: status.map(str::to_string),
            pid,
            started_at: Some(started_at),
        }
    }

    const ONE_AGENT: &str = r#"{"workspaces":[{"id":"fix-login","path":"/w/fix-login",
        "created_at":"2026-10-18T08:12:00Z",
        "session":{"kind":"agent","id":"e8cdf5d8","state":"idle","pid":1,"stale":false},
        "repos":[{"id":"github.com/o/r","path":"r","ref":"main","ref_kind":"branch"}]}],"warnings":[]}"#;

    #[test]
    fn idle_agent_with_blocked_job_needs_input() {
        assert_eq!(
            agent_status("idle", Some(&job("blocked"))),
            Status::NeedsInput
        );
    }

    #[test]
    fn idle_agent_with_done_job_is_done() {
        assert_eq!(agent_status("idle", Some(&job("done"))), Status::Done);
    }

    #[test]
    fn idle_agent_with_stale_working_job_is_idle() {
        assert_eq!(agent_status("idle", Some(&job("working"))), Status::Idle);
    }

    #[test]
    fn working_agent_ignores_job_state() {
        assert_eq!(agent_status("working", Some(&job("done"))), Status::Working);
    }

    #[test]
    fn unknown_ws_state_is_unknown() {
        assert_eq!(agent_status("frobbing", None), Status::Unknown);
    }

    #[test]
    fn merge_adds_job_detail_to_workspace_agent() {
        let job_for = |id: &str| {
            (id == "e8cdf5d8").then(|| JobState {
                state: Some("done".into()),
                detail: Some("PR #4".into()),
                output: Some(JobOutput {
                    result: Some("shipped".into()),
                }),
                children: Some(vec![JobChild {
                    href: Some("https://example.com/pull/4".into()),
                    kind: Some("pr".into()),
                }]),
                ..JobState::default()
            })
        };
        let entries = merge(Some(ws_list(ONE_AGENT)), &[], &job_for, NOW);
        let e = &entries[0];
        assert_eq!(
            (e.key.as_str(), e.kind, e.status),
            ("fix-login", Kind::Agent, Status::Done)
        );
        assert_eq!(e.session_id.as_deref(), Some("e8cdf5d8"));
        assert_eq!(e.result.as_deref(), Some("shipped"));
        assert_eq!(e.links[0].kind, "pr");
        assert_eq!(e.repos[0].git_ref.as_deref(), Some("main"));
    }

    #[test]
    fn merge_skips_sessions_already_in_a_workspace() {
        let agents = [agent("e8cdf5d8", Some(1), Some("idle"), NOW)];
        let entries = merge(Some(ws_list(ONE_AGENT)), &agents, &|_| None, NOW);
        assert_eq!(entries.len(), 1);
    }

    #[test]
    fn merge_lists_background_sessions_outside_workspaces() {
        let agents = [
            agent("aaaa1111", Some(1), Some("busy"), NOW - 60_000),
            agent("bbbb2222", None, None, NOW - 60_000),
            agent("cccc3333", None, None, NOW - 1_000),
        ];
        let entries = merge(None, &agents, &|_| None, NOW);
        let statuses: Vec<_> = entries.iter().map(|e| (e.key.as_str(), e.status)).collect();
        assert_eq!(
            statuses,
            [
                ("session:aaaa1111", Status::Working),
                ("session:bbbb2222", Status::Stopped),
                ("session:cccc3333", Status::Starting),
            ]
        );
    }

    #[test]
    fn merge_ignores_interactive_sessions() {
        let mut interactive = agent("x", Some(1), Some("idle"), NOW);
        interactive.kind = "interactive".to_string();
        assert!(merge(None, &[interactive], &|_| None, NOW).is_empty());
    }

    #[test]
    fn merge_reads_shell_pending_and_unknown_workspaces() {
        let list = ws_list(
            r#"{"workspaces":[
            {"id":"sh","path":"/w/sh","created_at":null,"session":{"kind":"shell","id":null,"state":"running","pid":null,"stale":null},"repos":[]},
            {"id":"this-is-a-test-d8efb24f","name":"This is a Test","path":"/w/this-is-a-test-d8efb24f","created_at":null,"session":{"kind":"shell","id":null,"state":"running","pid":null,"stale":null},"repos":[]},
            {"id":"new","path":"/w/new","created_at":null,"session":null,"repos":[]},
            {"id":null,"path":"/w/stray","created_at":null,"session":null,"repos":[]}]}"#,
        );
        let entries = merge(Some(list), &[], &|_| None, NOW);
        let got: Vec<_> = entries
            .iter()
            .map(|e| (e.key.as_str(), e.name.as_str(), e.kind, e.status))
            .collect();
        assert_eq!(
            got,
            [
                ("sh", "sh", Kind::Shell, Status::Running),
                (
                    "this-is-a-test-d8efb24f",
                    "This is a Test",
                    Kind::Shell,
                    Status::Running
                ),
                ("new", "new", Kind::Pending, Status::Pending),
                ("path:/w/stray", "stray", Kind::Unknown, Status::Unknown),
            ]
        );
    }

    #[test]
    fn warning_text_reads_strings_and_objects() {
        let warnings: Vec<Value> =
            serde_json::from_str(r#"["plain", {"code":"x","message":"from object"}, 3]"#).unwrap();
        let texts: Vec<_> = warnings.iter().map(warning_text).collect();
        assert_eq!(texts, ["plain", "from object", "3"]);
    }
}
