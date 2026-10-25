//! The commands behind the Repos section: `ws cache` and its subcommands.

use crate::commands::AppState;
use crate::runner::{self, AppError, Runner};
use serde::de::DeserializeOwned;
use serde::{Deserialize, Serialize};
use serde_json::Value;
use tauri::State;

type CmdResult<T> = Result<T, AppError>;

fn strings(args: &[&str]) -> Vec<String> {
    args.iter().map(|s| (*s).to_string()).collect()
}

/// A cached repo, from `ws cache list --json`.
#[derive(Debug, Deserialize, Serialize, PartialEq)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct CachedRepo {
    pub id: String,
    pub path: String,
    pub upstream: String,
    pub default_branch: String,
    pub last_fetched_at: Option<String>,
    pub size_bytes: u64,
    #[serde(default)]
    pub branches: u32,
    #[serde(default)]
    pub tags: u32,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct CacheListing {
    pub repos: Vec<CachedRepo>,
    #[serde(default)]
    pub warnings: Vec<String>,
}

/// Every cached repo, with its size, fetch time, and ref counts.
#[tauri::command]
pub async fn cache_list(state: State<'_, AppState>) -> CmdResult<CacheListing> {
    state.runner.ws_json(&strings(&["cache", "list"])).await
}

/// A branch or tag and the commit it points at, from `ws cache refs`.
#[derive(Debug, Deserialize, Serialize, PartialEq)]
pub struct RepoRef {
    pub name: String,
    pub kind: String,
    pub commit: String,
    pub subject: String,
    pub author: String,
    pub date: Option<String>,
}

#[derive(Debug, Deserialize, Serialize)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct RefListing {
    pub id: String,
    pub default_branch: String,
    pub refs: Vec<RepoRef>,
}

/// A cached repo's branches and tags (ADR 0033).
#[tauri::command]
pub async fn cache_refs(state: State<'_, AppState>, id: String) -> CmdResult<RefListing> {
    state.runner.ws_json(&strings(&["cache", "refs", &id])).await
}

/// What a repo spec resolves to, from `ws cache resolve`.
#[derive(Debug, Deserialize, Serialize, PartialEq)]
pub struct Resolution {
    pub id: String,
    #[serde(rename = "ref")]
    pub reference: Option<String>,
    pub upstream: String,
    pub cached: bool,
    pub source: String,
}

#[derive(Deserialize)]
struct Resolutions {
    repos: Vec<Resolution>,
}

/// Resolves a spec without cloning, for the Add repo preview. An ambiguous
/// name fails with `repo_ambiguous` and its candidates in the details.
#[tauri::command]
pub async fn cache_resolve(state: State<'_, AppState>, spec: String) -> CmdResult<Resolution> {
    resolve(&state.runner, &spec).await
}

async fn resolve(runner: &Runner, spec: &str) -> CmdResult<Resolution> {
    let mut resolved: Resolutions = runner
        .ws_json(&strings(&["cache", "resolve", spec]))
        .await?;
    resolved
        .repos
        .pop()
        .ok_or_else(|| AppError::new("unexpected_output", "ws cache resolve printed no repo"))
}

/// One repo's outcome in a multi-repo `ws cache` command.
#[derive(Debug, Serialize, PartialEq)]
pub struct RepoResult {
    pub id: String,
    pub error: Option<AppError>,
}

#[derive(Deserialize)]
struct RepoItems<T> {
    repos: Vec<T>,
}

#[derive(Deserialize)]
struct ItemError {
    code: String,
    message: String,
}

impl From<ItemError> for AppError {
    fn from(e: ItemError) -> Self {
        AppError::new(&e.code, e.message)
    }
}

/// Runs a `ws cache` command that prints `{"repos": [...]}` even when some
/// repos fail, which it does with a non-zero exit (ADR 0016). Anything else
/// on stdout means the whole command failed, with its error on stderr.
async fn repo_items<T: DeserializeOwned>(runner: &Runner, args: &[&str]) -> CmdResult<Vec<T>> {
    let mut args = strings(args);
    args.insert(1, "--json".to_string());
    let outcome = runner.run("ws", &args).await?;
    match serde_json::from_slice::<RepoItems<T>>(&outcome.stdout) {
        Ok(items) => Ok(items.repos),
        Err(_) if !outcome.success => Err(runner::ws_error(&outcome.stderr)),
        Err(_) => runner::parse_json::<RepoItems<T>>(&outcome.stdout, "ws").map(|i| i.repos),
    }
}

#[derive(Deserialize)]
struct UpdateItem {
    id: String,
    error: Option<ItemError>,
}

/// Fetches the given cached repos, or every one with `all`: `ws cache
/// update`. Returns each repo's outcome.
#[tauri::command]
pub async fn cache_fetch(
    state: State<'_, AppState>,
    ids: Vec<String>,
    all: bool,
) -> CmdResult<Vec<RepoResult>> {
    fetch(&state.runner, &ids, all).await
}

async fn fetch(runner: &Runner, ids: &[String], all: bool) -> CmdResult<Vec<RepoResult>> {
    let mut args = vec!["cache", "update"];
    if all {
        args.push("--all");
    } else {
        args.extend(ids.iter().map(String::as_str));
    }
    let items: Vec<UpdateItem> = repo_items(runner, &args).await?;
    Ok(items
        .into_iter()
        .map(|i| RepoResult {
            id: i.id,
            error: i.error.map(AppError::from),
        })
        .collect())
}

/// A repo added to the cache, from `ws cache add`.
#[derive(Debug, Deserialize, Serialize, PartialEq)]
pub struct AddedRepo {
    pub id: String,
    pub path: String,
    pub upstream: String,
    pub added: bool,
}

#[derive(Deserialize)]
struct AddItem {
    #[serde(flatten)]
    repo: AddedRepo,
    error: Option<ItemError>,
}

/// Clones a repo into the cache: `ws cache add`. A repo that's already
/// cached comes back with `added: false`.
#[tauri::command]
pub async fn cache_add(state: State<'_, AppState>, spec: String) -> CmdResult<AddedRepo> {
    add(&state.runner, &spec).await
}

async fn add(runner: &Runner, spec: &str) -> CmdResult<AddedRepo> {
    let item = repo_items::<AddItem>(runner, &["cache", "add", spec])
        .await?
        .pop()
        .ok_or_else(|| AppError::new("unexpected_output", "ws cache add printed no repo"))?;
    match item.error {
        Some(error) => Err(error.into()),
        None => Ok(item.repo),
    }
}

#[derive(Deserialize)]
struct RemoveItem {
    error: Option<Value>,
}

/// Deletes a repo from the cache: `ws cache remove`.
#[tauri::command]
pub async fn cache_remove(state: State<'_, AppState>, id: String) -> CmdResult<()> {
    remove(&state.runner, &id).await
}

async fn remove(runner: &Runner, id: &str) -> CmdResult<()> {
    let items: Vec<RemoveItem> = repo_items(runner, &["cache", "remove", id]).await?;
    match items.into_iter().find_map(|i| i.error) {
        Some(error) => Err(match serde_json::from_value::<ItemError>(error) {
            Ok(e) => e.into(),
            Err(_) => AppError::new("ws_failed", "ws cache remove failed"),
        }),
        None => Ok(()),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::PermissionsExt;

    /// A directory holding a fake `ws` script, for PATH. The script also
    /// writes its arguments to `args` in the same directory.
    fn fake_ws(body: &str) -> (tempfile::TempDir, Runner) {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("ws");
        let script = format!(
            "#!/bin/sh\necho \"$@\" > \"{}\"\n{body}\n",
            dir.path().join("args").display()
        );
        std::fs::write(&path, script).unwrap();
        std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o755)).unwrap();
        let runner = Runner::new(dir.path().display().to_string());
        (dir, runner)
    }

    fn args(dir: &tempfile::TempDir) -> String {
        std::fs::read_to_string(dir.path().join("args"))
            .unwrap()
            .trim()
            .to_string()
    }

    #[test]
    fn cached_repo_serializes_in_camel_case() {
        let repo: CachedRepo = serde_json::from_str(
            r#"{"id":"github.com/o/n","path":"/c/n.git","upstream":"https://github.com/o/n.git","default_branch":"main","last_fetched_at":"2026-10-24T09:12:00Z","size_bytes":18432000,"branches":14,"tags":6}"#,
        )
        .unwrap();
        let json = serde_json::to_value(&repo).unwrap();
        assert_eq!(json["defaultBranch"], "main");
        assert_eq!(json["lastFetchedAt"], "2026-10-24T09:12:00Z");
        assert_eq!(json["sizeBytes"], 18_432_000);
        assert_eq!(json["branches"], 14);
    }

    #[test]
    fn cached_repo_from_older_ws_has_no_counts() {
        let repo: CachedRepo = serde_json::from_str(
            r#"{"id":"github.com/o/n","path":"/c","upstream":"u","default_branch":"main","last_fetched_at":null,"size_bytes":1}"#,
        )
        .unwrap();
        assert_eq!((repo.branches, repo.tags), (0, 0));
    }

    #[tokio::test]
    async fn resolve_returns_the_one_repo() {
        let (_dir, runner) = fake_ws(
            r#"echo '{"repos":[{"id":"github.com/o/n","ref":null,"upstream":"https://github.com/o/n.git","cached":false,"source":"probe"}],"warnings":[]}'"#,
        );
        let resolution = resolve(&runner, "n").await.unwrap();
        assert_eq!(resolution.id, "github.com/o/n");
        assert_eq!(resolution.source, "probe");
        assert!(!resolution.cached);
    }

    #[tokio::test]
    async fn resolve_keeps_ambiguous_candidates() {
        let (_dir, runner) = fake_ws(
            r#"echo '{"error":{"code":"repo_ambiguous","message":"several","details":{"candidates":["github.com/a/n","github.com/b/n"]}}}' >&2; exit 3"#,
        );
        let err = resolve(&runner, "n").await.unwrap_err();
        assert_eq!(err.code, "repo_ambiguous");
        assert_eq!(err.details.unwrap()["candidates"][1], "github.com/b/n");
    }

    #[tokio::test]
    async fn fetch_all_passes_all_and_reports_each_repo() {
        let (dir, runner) = fake_ws(
            r#"echo '{"repos":[{"id":"github.com/o/a","error":null},{"id":"github.com/o/b","error":{"code":"fetch_failed","message":"offline"}}],"warnings":[]}'; exit 1"#,
        );
        let results = fetch(&runner, &[], true).await.unwrap();
        assert_eq!(args(&dir), "cache --json update --all");
        assert_eq!(
            results,
            [
                RepoResult {
                    id: "github.com/o/a".into(),
                    error: None
                },
                RepoResult {
                    id: "github.com/o/b".into(),
                    error: Some(AppError::new("fetch_failed", "offline"))
                },
            ]
        );
    }

    #[tokio::test]
    async fn fetch_passes_ids() {
        let (dir, runner) = fake_ws(r#"echo '{"repos":[],"warnings":[]}'"#);
        fetch(&runner, &["github.com/o/a".to_string()], false)
            .await
            .unwrap();
        assert_eq!(args(&dir), "cache --json update github.com/o/a");
    }

    #[tokio::test]
    async fn fetch_reports_a_whole_command_failure() {
        let (_dir, runner) = fake_ws(
            r#"echo '{"error":{"code":"repo_not_found","message":"not cached"}}' >&2; exit 3"#,
        );
        let err = fetch(&runner, &["x".to_string()], false)
            .await
            .unwrap_err();
        assert_eq!(err.code, "repo_not_found");
    }

    #[tokio::test]
    async fn add_returns_the_added_repo() {
        let (_dir, runner) = fake_ws(
            r#"echo '{"repos":[{"id":"github.com/o/n","path":"/c/n.git","upstream":"https://github.com/o/n.git","added":true,"error":null}],"warnings":[]}'"#,
        );
        let added = add(&runner, "o/n").await.unwrap();
        assert_eq!(added.id, "github.com/o/n");
        assert!(added.added);
    }

    #[tokio::test]
    async fn add_reports_a_failed_clone() {
        let (_dir, runner) = fake_ws(
            r#"echo '{"repos":[{"id":"github.com/o/n","path":"/c/n.git","upstream":"u","added":false,"error":{"code":"clone_failed","message":"denied"}}],"warnings":[]}'; exit 1"#,
        );
        let err = add(&runner, "o/n").await.unwrap_err();
        assert_eq!(err, AppError::new("clone_failed", "denied"));
    }

    #[tokio::test]
    async fn remove_reports_the_item_error() {
        let (dir, runner) = fake_ws(
            r#"echo '{"repos":[{"id":"github.com/o/n","path":"/c","error":{"code":"git_failed","message":"busy"}}],"warnings":[]}'; exit 1"#,
        );
        let err = remove(&runner, "github.com/o/n").await.unwrap_err();
        assert_eq!(args(&dir), "cache --json remove github.com/o/n");
        assert_eq!(err.code, "git_failed");
    }

    #[tokio::test]
    async fn remove_succeeds() {
        let (_dir, runner) = fake_ws(
            r#"echo '{"repos":[{"id":"github.com/o/n","path":"/c","error":null}],"warnings":[]}'"#,
        );
        assert!(remove(&runner, "github.com/o/n").await.is_ok());
    }

    /// Lists the real cache and the first repo's refs: `cargo test -- --ignored`.
    #[tokio::test]
    #[ignore = "runs the installed ws"]
    async fn cache_live() {
        let runner = Runner::new(crate::env::resolve_path());
        let listing: CacheListing = runner.ws_json(&strings(&["cache", "list"])).await.unwrap();
        for repo in &listing.repos {
            println!(
                "{:<40} {} branches {} tags",
                repo.id, repo.branches, repo.tags
            );
        }
        if let Some(repo) = listing.repos.first() {
            let refs: RefListing = runner
                .ws_json(&strings(&["cache", "refs", &repo.id]))
                .await
                .unwrap();
            for r in refs.refs.iter().take(10) {
                println!("{:<6} {:<30} {}", r.kind, r.name, r.subject);
            }
        }
    }
}
