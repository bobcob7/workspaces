//! Ask about a pull request (ADR 0051): a read-only Claude Code agent in a
//! new workspace at the PR's head, primed with the PR, that waits for
//! questions.

use super::github::split_repo;
use super::names::{ask_name, short_sha};
use crate::commands::Created;
use crate::model::Entry;
use crate::runner::{AppError, Runner};
use serde::Serialize;

/// Pre-approved, so reading the PR never waits for permission.
pub const ALLOWED_TOOLS: &[&str] = &[
    "Read",
    "Grep",
    "Glob",
    "Bash(gh pr view *)",
    "Bash(gh pr diff *)",
    "Bash(gh pr checks *)",
    "Bash(git log *)",
    "Bash(git show *)",
    "Bash(git diff *)",
    "Bash(git blame *)",
    "Bash(git grep *)",
];

/// Denied for the whole session: no edits, commits, pushes, or posts.
pub const DISALLOWED_TOOLS: &[&str] = &[
    "Edit",
    "Write",
    "NotebookEdit",
    "Bash(git commit *)",
    "Bash(git push *)",
    "Bash(gh pr comment *)",
    "Bash(gh pr review *)",
    "Bash(gh pr merge *)",
    "Bash(gh pr close *)",
    "Bash(gh pr edit *)",
    "Bash(gh pr ready *)",
    "Bash(gh issue *)",
    "Bash(gh api *)",
];

/// The pull request the agent is about.
pub struct About<'a> {
    /// The cache id, such as `github.com/bobcob7/buff-bear`.
    pub repo: &'a str,
    /// The pull request's number.
    pub number: u64,
    /// The pull request's title.
    pub title: &'a str,
    /// The pull request's web address.
    pub url: &'a str,
    /// The full head commit sha.
    pub head: &'a str,
    /// The head branch's name.
    pub head_branch: &'a str,
    /// The base branch's name.
    pub base_branch: &'a str,
}

/// The agent's first message (exact text in ADR 0051's design).
pub fn prompt(about: &About<'_>) -> String {
    let pr = match split_repo(about.repo) {
        Some((_, owner, name)) => format!("{owner}/{name}#{}", about.number),
        None => format!("{}#{}", about.repo, about.number),
    };
    let sha7 = short_sha(about.head);
    format!(
        r#"You answer questions about pull request {pr}, "{title}" ({url}). This workspace has {repo} checked out at the pull request's head commit, {sha7}: branch {head_branch} into {base_branch}.

First get to know it, without changing anything:
1. Run `gh pr view {number} --repo {repo} --comments` for the description and the discussion.
2. Run `gh pr diff {number} --repo {repo}` for the changes.
3. Read the changed files, and the code around them that calls them or that they call, until you can explain how the change fits in. Use `git log`, `git show` and `git blame` for history when it helps.

Rules: you are read-only. Don't edit, create or delete files, don't commit or push, and don't post anything to GitHub: no comments, reviews, approvals or labels. If I ask for a change, describe it with file and line, and say that a fix agent can make it.

When you've read it, reply with a short summary: what the pull request changes and why, the files that matter most, and anything that looks risky or unclear. Then wait for my questions, and answer them with file and line references."#,
        title = about.title,
        url = about.url,
        repo = about.repo,
        head_branch = about.head_branch,
        base_branch = about.base_branch,
        number = about.number,
    )
}

/// `ws claude` arguments without `--json` (the runner adds it):
/// the prompt comes right after `--`, because both tool flags take several
/// values and would take a prompt that followed them as a tool.
pub fn ask_args(about: &About<'_>, model: &str) -> Vec<String> {
    let mut args = vec![
        "claude".to_string(),
        "--name".to_string(),
        ask_name(about.repo, about.number, about.head),
        format!("{}@{}", about.repo, about.head),
        "--".to_string(),
        prompt(about),
    ];
    let model = model.trim();
    if !model.is_empty() {
        args.push("--model".to_string());
        args.push(model.to_string());
    }
    args.push("--permission-mode".to_string());
    args.push("default".to_string());
    args.push("--allowedTools".to_string());
    args.extend(ALLOWED_TOOLS.iter().map(|tool| tool.to_string()));
    args.push("--disallowedTools".to_string());
    args.extend(DISALLOWED_TOOLS.iter().map(|tool| tool.to_string()));
    args
}

/// An agent already asking about this pull request at this head: a
/// listed entry whose workspace id starts with `ask_name(..) + "-"`.
pub fn existing<'e>(
    entries: &'e [Entry],
    repo: &str,
    number: u64,
    head: &str,
) -> Option<&'e Entry> {
    let prefix = format!("{}-", ask_name(repo, number, head));
    entries.iter().find(|entry| {
        entry
            .workspace_id
            .as_deref()
            .is_some_and(|id| id.starts_with(&prefix))
    })
}

/// The agent that answers questions about a pull request.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct AskStarted {
    /// The workspace id.
    pub id: String,
    /// The Claude session, if one is known.
    pub session: Option<String>,
    /// The workspace's folder.
    pub path: String,
    /// It was already running; nothing new was started.
    pub already: bool,
}

/// Returns the existing agent, or runs `ws claude` and returns the new one.
pub async fn start(
    runner: &Runner,
    about: &About<'_>,
    model: &str,
    entries: &[Entry],
) -> Result<AskStarted, AppError> {
    if let Some(entry) = existing(entries, about.repo, about.number, about.head) {
        return Ok(AskStarted {
            id: entry.workspace_id.clone().unwrap_or_default(),
            session: entry.session_id.clone(),
            path: entry.path.clone(),
            already: true,
        });
    }
    let created: Created = runner.ws_json(&ask_args(about, model)).await?;
    Ok(AskStarted {
        id: created.id,
        session: Some(created.session),
        path: created.path,
        already: false,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::model::{Kind, Status};
    use std::os::unix::fs::PermissionsExt;

    const REPO: &str = "github.com/bobcob7/buff-bear";
    const HEAD: &str = "3f2a9c1e0b6d4a2f9e8c7b6a5d4c3b2a1f0e9d8c";

    fn about<'a>(repo: &'a str, head: &'a str) -> About<'a> {
        About {
            repo,
            number: 42,
            title: "Add rate limiting",
            url: "https://github.com/bobcob7/buff-bear/pull/42",
            head,
            head_branch: "feat/rate-limit",
            base_branch: "main",
        }
    }

    fn agent(id: &str, session: Option<&str>) -> Entry {
        Entry {
            key: id.to_string(),
            kind: Kind::Agent,
            name: id.to_string(),
            workspace_id: Some(id.to_string()),
            session_id: session.map(str::to_string),
            path: format!("/w/{id}"),
            status: Status::Idle,
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
            parent_id: None,
        }
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

    #[test]
    fn prompt_matches_the_design_text() {
        let expected = [
            r#"You answer questions about pull request bobcob7/buff-bear#42, "Add rate limiting" (https://github.com/bobcob7/buff-bear/pull/42). This workspace has github.com/bobcob7/buff-bear checked out at the pull request's head commit, 3f2a9c1: branch feat/rate-limit into main."#,
            "",
            "First get to know it, without changing anything:",
            "1. Run `gh pr view 42 --repo github.com/bobcob7/buff-bear --comments` for the description and the discussion.",
            "2. Run `gh pr diff 42 --repo github.com/bobcob7/buff-bear` for the changes.",
            "3. Read the changed files, and the code around them that calls them or that they call, until you can explain how the change fits in. Use `git log`, `git show` and `git blame` for history when it helps.",
            "",
            "Rules: you are read-only. Don't edit, create or delete files, don't commit or push, and don't post anything to GitHub: no comments, reviews, approvals or labels. If I ask for a change, describe it with file and line, and say that a fix agent can make it.",
            "",
            "When you've read it, reply with a short summary: what the pull request changes and why, the files that matter most, and anything that looks risky or unclear. Then wait for my questions, and answer them with file and line references.",
        ]
        .join("\n");
        assert_eq!(prompt(&about(REPO, HEAD)), expected);
    }

    #[test]
    fn prompt_falls_back_to_the_repo_id_without_an_owner() {
        let text = prompt(&about("weird", HEAD));
        assert!(text.starts_with("You answer questions about pull request weird#42, "));
    }

    #[test]
    fn ask_args_put_the_prompt_before_the_tool_flags() {
        let about = about(REPO, HEAD);
        let mut expected = vec![
            "claude".to_string(),
            "--name".to_string(),
            "ask-buff-bear-42-3f2a9c1".to_string(),
            format!("{REPO}@{HEAD}"),
            "--".to_string(),
            prompt(&about),
            "--permission-mode".to_string(),
            "default".to_string(),
            "--allowedTools".to_string(),
        ];
        expected.extend(ALLOWED_TOOLS.iter().map(|t| t.to_string()));
        expected.push("--disallowedTools".to_string());
        expected.extend(DISALLOWED_TOOLS.iter().map(|t| t.to_string()));
        assert_eq!(ask_args(&about, ""), expected);
        assert_eq!(ALLOWED_TOOLS.len(), 11);
        assert_eq!(DISALLOWED_TOOLS.len(), 13);
    }

    #[test]
    fn ask_args_pass_a_model() {
        let about = about(REPO, HEAD);
        let args = ask_args(&about, "opus");
        assert_eq!(args[6], "--model");
        assert_eq!(args[7], "opus");
        assert_eq!(args[8], "--permission-mode");
        assert!(!ask_args(&about, "  ").contains(&"--model".to_string()));
    }

    #[test]
    fn ask_args_check_out_the_head_commit() {
        let args = ask_args(&about(REPO, HEAD), "");
        assert_eq!(args[3], format!("{REPO}@{HEAD}"));
    }

    #[test]
    fn disallowed_tools_cover_edits_and_github_posts() {
        for tool in [
            "Edit",
            "Write",
            "NotebookEdit",
            "Bash(git push *)",
            "Bash(gh pr comment *)",
            "Bash(gh pr review *)",
            "Bash(gh api *)",
        ] {
            assert!(DISALLOWED_TOOLS.contains(&tool), "missing {tool}");
        }
    }

    #[test]
    fn existing_finds_an_agent_for_the_same_head_only() {
        let entries = [
            agent("ask-buff-bear-42-3f2a9c1-0a1b2c3d", None),
            agent("ask-buff-bear-42-9999999-0a1b2c3d", None),
            agent("ask-buff-bear-421-3f2a9c1-0a1b2c3d", None),
            agent("review-buff-bear-42-3f2a9c1-0a1b2c3d", None),
        ];
        let found = existing(&entries, REPO, 42, HEAD).unwrap();
        assert_eq!(found.key, "ask-buff-bear-42-3f2a9c1-0a1b2c3d");
        assert!(existing(&entries[1..2], REPO, 42, HEAD).is_none());
        assert!(existing(&entries[2..3], REPO, 42, HEAD).is_none());
        assert!(existing(&entries[3..4], REPO, 42, HEAD).is_none());
    }

    #[tokio::test]
    async fn start_returns_an_existing_agent_without_running_ws() {
        let (dir, runner) = fake_ws("exit 1");
        let entries = [agent("ask-buff-bear-42-3f2a9c1-0a1b2c3d", Some("5f6e7d8c"))];
        let started = start(&runner, &about(REPO, HEAD), "", &entries)
            .await
            .unwrap();
        assert_eq!(
            started,
            AskStarted {
                id: "ask-buff-bear-42-3f2a9c1-0a1b2c3d".to_string(),
                session: Some("5f6e7d8c".to_string()),
                path: "/w/ask-buff-bear-42-3f2a9c1-0a1b2c3d".to_string(),
                already: true,
            }
        );
        assert!(calls(&dir).is_empty());
    }

    #[tokio::test]
    async fn start_runs_ws_claude_and_returns_the_new_agent() {
        let script = r#"echo '{"id":"ask-buff-bear-42-3f2a9c1-0a1b2c3d","session":"5f6e7d8c","path":"/w/a"}'"#;
        let (dir, runner) = fake_ws(script);
        let started = start(&runner, &about(REPO, HEAD), "", &[]).await.unwrap();
        assert!(!started.already);
        assert_eq!(started.session.as_deref(), Some("5f6e7d8c"));
        assert_eq!(started.id, "ask-buff-bear-42-3f2a9c1-0a1b2c3d");
        let log = calls(&dir);
        assert!(log[0].starts_with(&format!(
            "claude --json --name ask-buff-bear-42-3f2a9c1 {REPO}@{HEAD} -- You answer questions about pull request"
        )));
    }

    #[tokio::test]
    async fn start_returns_the_ws_error() {
        let script =
            r#"echo '{"error":{"code":"claude_untrusted","message":"not trusted"}}' >&2; exit 1"#;
        let (_dir, runner) = fake_ws(script);
        let err = start(&runner, &about(REPO, HEAD), "", &[])
            .await
            .unwrap_err();
        assert_eq!(err.code, "claude_untrusted");
    }
}
