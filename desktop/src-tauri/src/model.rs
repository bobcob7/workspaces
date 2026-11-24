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
    /// The review record of a workspace `ws review start` made (ADR 0039).
    #[serde(default)]
    pub review: Option<ReviewRecord>,
    /// Left out of the default views (ADR 0043).
    #[serde(default)]
    pub hidden: bool,
    /// The workspace this one was started from (ADR 0044).
    #[serde(default)]
    pub parent: Option<String>,
    /// The loop run this workspace is, with what it asks of a person (ADR 0057).
    #[serde(default)]
    pub loop_run: Option<WsLoopRun>,
    /// The workspace's own step record, null without one (ADR 0077).
    #[serde(default)]
    pub progress: Option<WsProgress>,
    /// Its steps plus its descendants', null when there are none (ADR 0077).
    #[serde(default)]
    pub rollup: Option<WsRollup>,
    /// The environment the workspace runs in: "local" or a name.
    #[serde(default)]
    pub environment: Option<String>,
    /// "online" or "offline" for the environment.
    #[serde(default)]
    pub environment_status: Option<String>,
    /// The environment's snapshot is old (top level, not the session's).
    #[serde(default)]
    pub stale: Option<bool>,
    /// The state of the remote agent that runs the session.
    #[serde(default)]
    pub agent_state: Option<String>,
}

/// A workspace's `progress` from `ws list` (ADR 0077).
#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct WsProgress {
    #[serde(default)]
    pub name: Option<String>,
    pub total: u64,
    pub done: u64,
    #[serde(default)]
    pub note: Option<String>,
    #[serde(default)]
    pub updated_at: Option<String>,
}

/// A workspace's `rollup` from `ws list`: its steps and its descendants' (ADR 0077).
#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct WsRollup {
    pub total: u64,
    pub done: u64,
    pub fraction: f64,
}

/// A loop run's `loop_run` from `ws list` (ADR 0057).
#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
pub struct WsLoopRun {
    #[serde(rename = "loop")]
    pub loop_name: String,
    /// "blocked" or "needs_attention"; null when nothing is asked.
    #[serde(default)]
    pub attention: Option<String>,
}

/// What a review workspace reviews, from its `review.toml`.
#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct ReviewRecord {
    pub repo: String,
    /// The full commit reviewed.
    #[serde(rename = "ref")]
    pub git_ref: String,
    #[serde(default)]
    pub requester: String,
    #[serde(default)]
    pub profiles: Vec<ReviewProfile>,
    #[serde(default)]
    pub pr: Option<ReviewPr>,
}

#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
pub struct ReviewProfile {
    pub name: String,
    #[serde(default)]
    pub model: String,
    #[serde(default)]
    pub auto: bool,
}

#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct ReviewPr {
    pub number: u64,
    #[serde(default)]
    pub url: String,
}

#[derive(Debug, Deserialize)]
pub struct WsSession {
    pub kind: String,
    pub id: Option<String>,
    pub state: String,
    pub stale: Option<bool>,
    /// What runs the session: "claude", "ws", or null for a shell.
    #[serde(default)]
    pub runner: Option<String>,
    /// The session's model id, as `ws list` reports it.
    #[serde(default)]
    pub model: Option<String>,
    /// A `ws` runner's progress and endpoint, null for a Claude agent.
    #[serde(default)]
    pub ws_runner: Option<WsRunner>,
}

/// A `ws` runner's `ws_runner` from `ws list` (reference.md).
#[derive(Debug, Clone, Default, Deserialize, Serialize, PartialEq)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct WsRunner {
    #[serde(default)]
    pub model_alias: Option<String>,
    #[serde(default)]
    pub endpoint: Option<String>,
    #[serde(default)]
    pub url: Option<String>,
    #[serde(default)]
    pub api: Option<String>,
    #[serde(default)]
    pub context_tokens: Option<u64>,
    #[serde(default)]
    pub step: Option<u64>,
    #[serde(default)]
    pub max_steps: Option<u64>,
    #[serde(default)]
    pub restarts: Option<u64>,
    #[serde(default)]
    pub usage: Option<WsRunnerUsage>,
    #[serde(default)]
    pub mcp: Vec<WsRunnerMcp>,
    #[serde(default)]
    pub exit: Option<String>,
    #[serde(default)]
    pub endpoint_changes: Option<u64>,
}

#[derive(Debug, Clone, Default, Deserialize, Serialize, PartialEq)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct WsRunnerUsage {
    pub requests: u64,
    pub prompt_tokens: u64,
    pub completion_tokens: u64,
}

#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct WsRunnerMcp {
    pub server: String,
    pub state: String,
    #[serde(default)]
    pub tools: u64,
    #[serde(default)]
    pub loaded: u64,
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
    /// A finished review whose agent and workspace `ws` removed (ADR
    /// 0045); only in the Reviews view, never the Agents list.
    Removed,
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
    pub review: Option<ReviewRecord>,
    /// The workspace is hidden: shown only in the Agents list's Hidden
    /// group, and not counted as waiting (ADR 0043). A raised loop run is
    /// never hidden (ADR 0057).
    pub hidden: bool,
    /// The workspace this one was started from, removed with it (ADR 0044).
    pub parent_id: Option<String>,
    /// Set for a loop run's workspace (ADR 0057).
    pub loop_run: Option<WsLoopRun>,
    /// "claude" or "ws" for an agent, "" for anything else.
    pub runner: String,
    /// The model for display: a `claude-` prefix dropped, any other id whole.
    pub model: Option<String>,
    /// A `ws` runner's progress and endpoint.
    pub ws_runner: Option<WsRunner>,
    /// The workspace's own step record (ADR 0077).
    pub progress: Option<WsProgress>,
    /// The steps of the workspace and its descendants (ADR 0077).
    pub rollup: Option<WsRollup>,
    /// "local" or the name of the environment the entry runs in.
    pub environment: String,
    /// The entry's environment is connected (always true for local).
    pub environment_online: bool,
    /// The entry's snapshot of a remote environment is old.
    pub snapshot_stale: bool,
}

/// The model id shown for an agent: `claude-sonnet-5-5` reads `sonnet-5-5`.
fn display_model(id: &str) -> String {
    id.strip_prefix("claude-").unwrap_or(id).to_string()
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

/// The status of a remote agent, from the snapshot: the controller can't
/// read the remote's job files, so `agent_state` stands in for them.
fn remote_agent_status(ws_state: &str, agent_state: Option<&str>) -> Status {
    match (ws_state, agent_state) {
        ("working", _) => Status::Working,
        ("idle", Some("blocked")) => Status::NeedsInput,
        ("idle", Some("done")) => Status::Done,
        _ => agent_status(ws_state, None),
    }
}

/// The key of a remote entry, so a remote and a local id never collide.
fn remote_key(environment: &str, id: &str) -> String {
    format!("env:{environment}:{id}")
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
    let environment = ws
        .environment
        .clone()
        .unwrap_or_else(|| "local".to_string());
    let remote = environment != "local";
    let key = match (&ws.id, remote) {
        (Some(id), true) => remote_key(&environment, id),
        (Some(id), false) => id.clone(),
        (None, _) => format!("path:{}", ws.path),
    };
    let environment_online = !remote || ws.environment_status.as_deref() == Some("online");
    let snapshot_stale = remote && ws.stale.unwrap_or(false);
    let (kind, status, session_id, stale) = match &ws.session {
        None if ws.id.is_none() => (Kind::Unknown, Status::Unknown, None, None),
        None => (Kind::Pending, Status::Pending, None, None),
        Some(s) if s.kind == "shell" => (Kind::Shell, agent_status(&s.state, None), None, None),
        Some(s) => (Kind::Agent, Status::Unknown, s.id.clone(), s.stale),
    };
    let runner = agent_runner(kind, ws.session.as_ref());
    let job = session_id
        .as_deref()
        .filter(|_| runner != "ws" && !remote)
        .and_then(job_for);
    let status = match (&ws.session, kind) {
        (Some(s), Kind::Agent) if remote => {
            remote_agent_status(&s.state, ws.agent_state.as_deref())
        }
        (Some(s), Kind::Agent) => agent_status(&s.state, job.as_ref()),
        _ => status,
    };
    let raised = ws.loop_run.as_ref().is_some_and(|r| r.attention.is_some());
    let status = match status {
        Status::Idle | Status::Done if raised => Status::NeedsInput,
        other => other,
    };
    let model = ws
        .session
        .as_ref()
        .and_then(|s| s.model.as_deref())
        .map(display_model);
    let ws_runner = ws
        .session
        .as_ref()
        .and_then(|s| s.ws_runner.clone())
        .filter(|_| runner == "ws");
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
        review: ws.review,
        hidden: ws.hidden && !raised,
        parent_id: ws.parent,
        loop_run: ws.loop_run,
        runner,
        model,
        ws_runner,
        progress: ws.progress,
        rollup: ws.rollup,
        environment,
        environment_online,
        snapshot_stale,
    }
}

/// What runs an agent. `ws` says so; a Claude agent that doesn't say is
/// `claude`. Shells and workspaces without a session have none.
fn agent_runner(kind: Kind, session: Option<&WsSession>) -> String {
    match (kind, session.and_then(|s| s.runner.as_deref())) {
        (Kind::Agent, Some(runner)) => runner.to_string(),
        (Kind::Agent, None) => "claude".to_string(),
        _ => String::new(),
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
        review: None,
        hidden: false,
        parent_id: None,
        loop_run: None,
        runner: "claude".to_string(),
        model: None,
        ws_runner: None,
        progress: None,
        rollup: None,
        environment: "local".to_string(),
        environment_online: true,
        snapshot_stale: false,
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
    fn merge_keeps_whether_a_workspace_is_hidden() {
        let entries = merge(Some(ws_list(ONE_AGENT)), &[], &|_| None, NOW);
        assert!(!entries[0].hidden, "hidden defaults to false");
        let hidden = ONE_AGENT.replace(
            r#""path":"/w/fix-login","#,
            r#""path":"/w/fix-login","hidden":true,"#,
        );
        let entries = merge(Some(ws_list(&hidden)), &[], &|_| None, NOW);
        assert!(entries[0].hidden);
    }

    fn with_loop_run(json: &str, loop_run: &str) -> String {
        json.replace(
            r#""path":"/w/fix-login","#,
            &format!(r#""path":"/w/fix-login","hidden":true,"loop_run":{loop_run},"#),
        )
    }

    #[test]
    fn merge_reads_loop_run() {
        let raised = with_loop_run(ONE_AGENT, r#"{"loop":"deps-bump","attention":"blocked"}"#);
        let entries = merge(Some(ws_list(&raised)), &[], &|_| None, NOW);
        let e = &entries[0];
        assert!(!e.hidden, "a raised loop run is not hidden");
        assert_eq!(e.status, Status::NeedsInput);
        assert_eq!(
            e.loop_run,
            Some(WsLoopRun {
                loop_name: "deps-bump".into(),
                attention: Some("blocked".into()),
            })
        );
    }

    #[test]
    fn merge_keeps_a_loop_run_without_attention_hidden() {
        let resolved = with_loop_run(ONE_AGENT, r#"{"loop":"deps-bump","attention":null}"#);
        let entries = merge(Some(ws_list(&resolved)), &[], &|_| None, NOW);
        let e = &entries[0];
        assert!(e.hidden);
        assert_eq!(e.status, Status::Idle);
        assert_eq!(
            e.loop_run.as_ref().map(|r| r.loop_name.as_str()),
            Some("deps-bump")
        );
    }

    #[test]
    fn merge_has_no_loop_run_when_ws_sends_none() {
        let entries = merge(Some(ws_list(ONE_AGENT)), &[], &|_| None, NOW);
        assert!(entries[0].loop_run.is_none());
    }

    #[test]
    fn entry_serializes_loop_run_as_camel_case() {
        let raised = with_loop_run(ONE_AGENT, r#"{"loop":"deps-bump","attention":"blocked"}"#);
        let entries = merge(Some(ws_list(&raised)), &[], &|_| None, NOW);
        let json = serde_json::to_value(&entries[0]).unwrap();
        assert_eq!(
            json["loopRun"],
            serde_json::json!({"loop": "deps-bump", "attention": "blocked"})
        );
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
    fn merge_reads_review_record() {
        let list = ws_list(
            r#"{"workspaces":[{"id":"review-buff-bear-42-3f2a9c1-d8efb24f","path":"/w/r","created_at":null,
            "session":{"kind":"agent","id":"7be1d0c2","state":"working","pid":1,"stale":false},
            "repos":[{"id":"github.com/bobcob7/buff-bear","path":"buff-bear","ref":"3f2a9c1e","ref_kind":"commit"}],
            "review":{"version":1,"repo":"github.com/bobcob7/buff-bear","ref":"3f2a9c1e","requester":"",
              "started_at":"2026-10-26T18:00:00Z","profiles":[{"name":"go","model":"","auto":true}],
              "pr":{"number":42,"url":"https://github.com/bobcob7/buff-bear/pull/42","base":"main","head_branch":"feat","cross_repository":false}}}]}"#,
        );
        let entries = merge(Some(list), &[], &|_| None, NOW);
        let review = entries[0].review.as_ref().unwrap();
        assert_eq!(review.pr.as_ref().map(|p| p.number), Some(42));
        assert_eq!(review.git_ref, "3f2a9c1e");
        assert!(review.profiles[0].auto);
        assert!(merge(Some(ws_list(ONE_AGENT)), &[], &|_| None, NOW)[0]
            .review
            .is_none());
    }

    const WS_RUNNER: &str = r#"{"workspaces":[{"id":"t1-impl-d8efb24f","path":"/w/t1",
        "session":{"kind":"agent","id":"rn-3f2a9c1e","state":"idle","runner":"ws",
        "model":"qwen/qwen3.5-9b",
        "ws_runner":{"model_alias":"qwen9b","endpoint":"gpu","step":12,"max_steps":80,
        "restarts":0,"usage":{"requests":12,"prompt_tokens":210344,"completion_tokens":9120},
        "mcp":[{"server":"metrics","state":"connected","tools":6,"loaded":3}],
        "exit":null,"endpoint_changes":0}}}],"warnings":[]}"#;

    #[test]
    fn merge_reads_ws_runner_without_a_claude_job() {
        let job_for = |id: &str| -> Option<JobState> { panic!("no Claude job lookup for {id}") };
        let entries = merge(Some(ws_list(WS_RUNNER)), &[], &job_for, NOW);
        let e = &entries[0];
        assert_eq!(e.runner, "ws");
        assert_eq!(e.status, Status::Idle);
        assert_eq!(e.model.as_deref(), Some("qwen/qwen3.5-9b"));
        let runner = e.ws_runner.as_ref().expect("a ws runner");
        assert_eq!(runner.model_alias.as_deref(), Some("qwen9b"));
        assert_eq!((runner.step, runner.max_steps), (Some(12), Some(80)));
        assert_eq!(runner.mcp[0].loaded, 3);
    }

    #[test]
    fn merge_reads_progress_and_rollup() {
        let list = ws_list(
            r#"{"workspaces":[{"id":"orch-d8efb24f","path":"/w/orch",
            "progress":{"name":"build auth","total":4,"done":1,"note":"tests","updated_at":"2026-11-22T20:00:00Z"},
            "rollup":{"total":7,"done":3,"fraction":0.4286}}],"warnings":[]}"#,
        );
        let entries = merge(Some(list), &[], &|_| None, NOW);
        let e = &entries[0];
        let progress = e.progress.as_ref().expect("a progress record");
        assert_eq!(progress.name.as_deref(), Some("build auth"));
        assert_eq!((progress.total, progress.done), (4, 1));
        assert_eq!(progress.note.as_deref(), Some("tests"));
        assert_eq!(progress.updated_at.as_deref(), Some("2026-11-22T20:00:00Z"));
        let rollup = e.rollup.as_ref().expect("a rollup");
        assert_eq!((rollup.total, rollup.done), (7, 3));
        assert!((rollup.fraction - 0.4286).abs() < 1e-9);
    }

    #[test]
    fn merge_reads_missing_progress_and_rollup_as_none() {
        let entries = merge(Some(ws_list(ONE_AGENT)), &[], &|_| None, NOW);
        assert_eq!(entries[0].progress, None);
        assert_eq!(entries[0].rollup, None);
    }

    #[test]
    fn merge_reads_a_null_progress_name_and_note() {
        let list = ws_list(
            r#"{"workspaces":[{"id":"t1-d8efb24f","path":"/w/t1",
            "progress":{"name":null,"total":2,"done":0,"note":null,"updated_at":null},
            "rollup":null}],"warnings":[]}"#,
        );
        let entries = merge(Some(list), &[], &|_| None, NOW);
        let progress = entries[0].progress.as_ref().expect("a progress record");
        assert_eq!(
            (progress.name.as_deref(), progress.note.as_deref()),
            (None, None)
        );
        assert_eq!(entries[0].rollup, None);
    }

    #[test]
    fn merge_drops_the_claude_prefix_from_a_claude_model() {
        let claude = ONE_AGENT.replace(
            r#""state":"idle","pid":1,"stale":false}"#,
            r#""state":"idle","pid":1,"stale":false,"runner":"claude","model":"claude-sonnet-5-5"}"#,
        );
        let entries = merge(Some(ws_list(&claude)), &[], &|_| None, NOW);
        let e = &entries[0];
        assert_eq!(e.runner, "claude");
        assert_eq!(e.model.as_deref(), Some("sonnet-5-5"));
        assert_eq!(e.ws_runner, None);
    }

    #[test]
    fn merge_treats_a_session_without_a_runner_as_claude() {
        let entries = merge(Some(ws_list(ONE_AGENT)), &[], &|_| None, NOW);
        assert_eq!(entries[0].runner, "claude");
        assert_eq!(entries[0].model, None);
    }

    fn remote_list(state: &str, agent_state: &str) -> WsList {
        ws_list(&format!(
            r#"{{"workspaces":[{{"id":"fix","path":"/r/fix","environment":"linux-server",
            "environment_status":"online","stale":false,"agent_state":"{agent_state}",
            "session":{{"kind":"agent","id":"s1","state":"{state}"}}}}]}}"#
        ))
    }

    #[test]
    fn remote_entry_key_has_environment() {
        let entries = merge(Some(remote_list("idle", "")), &[], &|_| None, NOW);
        assert_eq!(entries[0].key, "env:linux-server:fix");
        assert_eq!(entries[0].environment, "linux-server");
        assert!(entries[0].environment_online);
        assert!(!entries[0].snapshot_stale);
    }

    #[test]
    fn local_entry_key_is_unchanged() {
        let entries = merge(Some(ws_list(ONE_AGENT)), &[], &|_| None, NOW);
        assert_eq!(entries[0].key, "fix-login");
        assert_eq!(entries[0].environment, "local");
        assert!(entries[0].environment_online);
    }

    #[test]
    fn remote_blocked_agent_needs_input() {
        assert_eq!(
            remote_agent_status("idle", Some("blocked")),
            Status::NeedsInput
        );
        assert_eq!(remote_agent_status("idle", Some("done")), Status::Done);
        assert_eq!(remote_agent_status("working", None), Status::Working);
        assert_eq!(remote_agent_status("idle", None), Status::Idle);
        let entries = merge(Some(remote_list("idle", "blocked")), &[], &|_| None, NOW);
        assert_eq!(entries[0].status, Status::NeedsInput);
    }

    #[test]
    fn remote_entry_reads_no_job() {
        let entries = merge(
            Some(remote_list("idle", "")),
            &[],
            &|_| panic!("a remote entry must not read a job file"),
            NOW,
        );
        assert_eq!(entries[0].status, Status::Idle);
    }

    fn remote_list_with(environment: &str, status_field: &str, stale: &str) -> WsList {
        ws_list(&format!(
            r#"{{"workspaces":[{{"id":"fix","path":"/r/fix","environment":"{environment}"{status_field}{stale},
            "session":{{"kind":"agent","id":"s1","state":"idle"}}}}]}}"#
        ))
    }

    #[test]
    fn remote_entry_is_offline_unless_the_status_is_online() {
        for status_field in [
            r#","environment_status":"offline""#,
            r#","environment_status":"unknown""#,
            "",
        ] {
            let list = remote_list_with("linux-server", status_field, "");
            let entries = merge(Some(list), &[], &|_| None, NOW);
            assert!(!entries[0].environment_online, "{status_field}");
        }
    }

    #[test]
    fn remote_entry_reads_a_stale_snapshot() {
        let list = remote_list_with("linux-server", "", r#","stale":true"#);
        let entries = merge(Some(list), &[], &|_| None, NOW);
        assert!(entries[0].snapshot_stale);
    }

    #[test]
    fn local_entry_ignores_a_stray_stale_flag() {
        let list = remote_list_with("local", "", r#","stale":true"#);
        let entries = merge(Some(list), &[], &|_| None, NOW);
        assert_eq!(entries[0].environment, "local");
        assert!(!entries[0].snapshot_stale);
        assert!(entries[0].environment_online);
    }

    #[test]
    fn missing_environment_is_local() {
        let entries = merge(Some(ws_list(ONE_AGENT)), &[], &|_| None, NOW);
        assert_eq!(entries[0].environment, "local");
        assert!(!entries[0].snapshot_stale);
    }

    #[test]
    fn warning_text_reads_strings_and_objects() {
        let warnings: Vec<Value> =
            serde_json::from_str(r#"["plain", {"code":"x","message":"from object"}, 3]"#).unwrap();
        let texts: Vec<_> = warnings.iter().map(warning_text).collect();
        assert_eq!(texts, ["plain", "from object", "3"]);
    }
}
