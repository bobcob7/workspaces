//! Artifacts: what `ws artifact` stores, for the Artifacts section, the
//! viewers and prompt attachments (ADR 0078). The app never reads or
//! writes the store itself.

use crate::commands::AppState;
use crate::ipc::State;
use crate::runner::{ws_error, AppError, Runner};
use base64ct::{Base64, Encoding};
use serde::{Deserialize, Serialize};

type CmdResult<T> = Result<T, AppError>;

/// The largest file `artifact_add` accepts, before `ws` runs (ADR 0078).
pub const MAX_UPLOAD: usize = 10 * 1024 * 1024;

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct Actor {
    pub kind: String,
    pub workspace: Option<String>,
    pub name: Option<String>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct ThreadCounts {
    pub open: u32,
    pub resolved: u32,
    pub detached: u32,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct ArtifactSummary {
    pub id: String,
    pub reference: String,
    pub name: String,
    pub title: Option<String>,
    pub kind: String,
    pub content_type: String,
    pub latest: u32,
    pub size: u64,
    pub created_at: String,
    pub updated_at: String,
    pub created_by: Actor,
    pub threads: ThreadCounts,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct Revision {
    pub rev: u32,
    pub content_type: String,
    pub size: u64,
    pub sha256: String,
    pub created_at: String,
    pub created_by: Actor,
    pub note: Option<String>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct Artifact {
    #[serde(flatten)]
    pub summary: ArtifactSummary,
    pub revisions: Vec<Revision>,
}

#[derive(Debug, Clone, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ArtifactContent {
    pub rev: u32,
    pub content_type: String,
    pub size: u64,
    pub data_base64: String,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct Anchor {
    pub rev: u32,
    pub start: u64,
    pub end: u64,
    pub line_start: u32,
    pub line_end: u32,
    pub quote: String,
    pub prefix: String,
    pub suffix: String,
    pub state: String,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct ThreadComment {
    pub id: String,
    pub author: Actor,
    pub body: String,
    pub created_at: String,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct Thread {
    pub id: String,
    pub anchor: Option<Anchor>,
    pub resolved: bool,
    pub resolved_at: Option<String>,
    pub resolved_by: Option<Actor>,
    pub comments: Vec<ThreadComment>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct CommentList {
    pub artifact_id: String,
    pub latest: u32,
    pub threads: Vec<Thread>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct CommentAdded {
    pub thread: Thread,
    pub comment: ThreadComment,
}

/// The anchor a new comment quotes: the frontend's `selectionAnchor`.
#[derive(Debug, Clone, Default, PartialEq, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct NewAnchor {
    pub quote: String,
    pub line: u32,
    pub prefix: String,
    pub suffix: String,
}

#[derive(Deserialize)]
struct ArtifactList {
    artifacts: Vec<ArtifactSummary>,
}

#[derive(Deserialize)]
struct ArtifactResult {
    artifact: Artifact,
}

#[derive(Deserialize)]
struct ThreadResult {
    thread: Thread,
}

#[derive(Deserialize)]
struct RemoveList {
    artifacts: Vec<RemoveItem>,
}

#[derive(Deserialize)]
struct RemoveItem {
    error: Option<ItemError>,
}

#[derive(Deserialize)]
struct ItemError {
    code: String,
    message: String,
}

/// Every artifact `ws artifact list` lists, latest first.
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn artifact_list(
    state: State<'_, AppState>,
    kind: Option<String>,
    workspace: Option<String>,
) -> CmdResult<Vec<ArtifactSummary>> {
    list(&state.runner, kind.as_deref(), workspace.as_deref()).await
}

async fn list(
    runner: &Runner,
    kind: Option<&str>,
    workspace: Option<&str>,
) -> CmdResult<Vec<ArtifactSummary>> {
    let mut args = strings(&["artifact", "list", "--json"]);
    args.extend(flag("--kind", kind));
    args.extend(flag("--workspace", workspace));
    args.extend(strings(&["--limit", "1000"]));
    let listing: ArtifactList = runner.ws_json_as_given(&args).await?;
    Ok(listing.artifacts)
}

/// One artifact with its revisions, for the viewer's header.
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn artifact_show(state: State<'_, AppState>, id: String) -> CmdResult<Artifact> {
    show(&state.runner, &id).await
}

async fn show(runner: &Runner, id: &str) -> CmdResult<Artifact> {
    check_id(id)?;
    let args = strings(&["artifact", "show", "--json", "--", id]);
    let result: ArtifactResult = runner.ws_json_as_given(&args).await?;
    Ok(result.artifact)
}

/// One revision's bytes, base64 encoded (the latest by default).
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn artifact_read(
    state: State<'_, AppState>,
    id: String,
    rev: Option<u32>,
) -> CmdResult<ArtifactContent> {
    read(&state.runner, &id, rev).await
}

async fn read(runner: &Runner, id: &str, rev: Option<u32>) -> CmdResult<ArtifactContent> {
    check_id(id)?;
    let mut args = strings(&["artifact", "cat"]);
    args.extend(flag("--rev", rev.map(|n| n.to_string()).as_deref()));
    args.extend(strings(&["--", id]));
    let outcome = runner.run("ws", &args).await?;
    if !outcome.success {
        return Err(ws_error(&outcome.stderr));
    }
    let artifact = show(runner, id).await?;
    let target = rev.unwrap_or(artifact.summary.latest);
    Ok(ArtifactContent {
        rev: target,
        content_type: content_type_of(&artifact, target)?,
        size: outcome.stdout.len() as u64,
        data_base64: Base64::encode_string(&outcome.stdout),
    })
}

fn content_type_of(artifact: &Artifact, rev: u32) -> CmdResult<String> {
    artifact
        .revisions
        .iter()
        .find(|revision| revision.rev == rev)
        .map(|revision| revision.content_type.clone())
        .ok_or_else(|| {
            AppError::new(
                "artifact_unknown",
                format!("{} has no revision {rev}", artifact.summary.id),
            )
        })
}

/// Stores a new artifact from base64 bytes the frontend read from a file or paste.
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn artifact_add(
    state: State<'_, AppState>,
    name: String,
    title: Option<String>,
    data_base64: String,
) -> CmdResult<Artifact> {
    add(&state.runner, &name, title.as_deref(), &data_base64).await
}

async fn add(
    runner: &Runner,
    name: &str,
    title: Option<&str>,
    data_base64: &str,
) -> CmdResult<Artifact> {
    let bytes = decode_upload(name, data_base64)?;
    let mut args = strings(&["artifact", "add", "-", "--json", "--name", name]);
    args.extend(flag("--title", title));
    let result: ArtifactResult = runner.ws_json_with_input(&args, Some(&bytes)).await?;
    Ok(result.artifact)
}

/// The decoded upload, or the error that refuses it before `ws` runs.
fn decode_upload(name: &str, data_base64: &str) -> CmdResult<Vec<u8>> {
    let bytes = Base64::decode_vec(data_base64)
        .map_err(|_| AppError::new("bad_args", format!("{name} isn't base64")))?;
    if bytes.len() > MAX_UPLOAD {
        return Err(AppError::new(
            "artifact_invalid",
            format!("{name} is over the 10 MiB limit"),
        ));
    }
    Ok(bytes)
}

/// Removes one artifact; `ws` reports a refusal per item.
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn artifact_remove(state: State<'_, AppState>, id: String) -> CmdResult<()> {
    remove(&state.runner, &id).await
}

async fn remove(runner: &Runner, id: &str) -> CmdResult<()> {
    check_id(id)?;
    let args = strings(&["artifact", "rm", "--json", "--", id]);
    let listing: RemoveList = runner.ws_json_as_given(&args).await?;
    let failed = listing.artifacts.into_iter().find_map(|item| item.error);
    match failed {
        Some(error) => Err(AppError::new(&error.code, error.message)),
        None => Ok(()),
    }
}

/// Every thread on an artifact, with its comments.
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn artifact_comments(state: State<'_, AppState>, id: String) -> CmdResult<CommentList> {
    comments(&state.runner, &id).await
}

async fn comments(runner: &Runner, id: &str) -> CmdResult<CommentList> {
    check_id(id)?;
    let args = strings(&["artifact", "comment", "list", "--json", "--", id]);
    runner.ws_json_as_given(&args).await
}

/// Starts a thread on the artifact (`anchor`), or replies to one (`reply`).
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn artifact_comment_add(
    state: State<'_, AppState>,
    id: String,
    body: String,
    reply: Option<String>,
    anchor: Option<NewAnchor>,
) -> CmdResult<CommentAdded> {
    add_comment(&state.runner, &id, &body, reply.as_deref(), anchor.as_ref()).await
}

async fn add_comment(
    runner: &Runner,
    id: &str,
    body: &str,
    reply: Option<&str>,
    anchor: Option<&NewAnchor>,
) -> CmdResult<CommentAdded> {
    check_id(id)?;
    if let Some(reply) = reply {
        check_thread(reply)?;
    }
    let mut args = strings(&["artifact", "comment", "add", "--json", "--body-file", "-"]);
    args.extend(flag("--reply", reply));
    if let Some(anchor) = anchor {
        args.extend(anchor_flags(anchor));
    }
    args.extend(strings(&["--", id]));
    runner
        .ws_json_with_input(&args, Some(body.as_bytes()))
        .await
}

fn anchor_flags(anchor: &NewAnchor) -> Vec<String> {
    let line = anchor.line.to_string();
    strings(&[
        "--quote",
        &anchor.quote,
        "--line",
        &line,
        "--prefix",
        &anchor.prefix,
        "--suffix",
        &anchor.suffix,
    ])
}

/// Resolves a thread, or opens it again with `reopen`.
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn artifact_comment_resolve(
    state: State<'_, AppState>,
    id: String,
    thread: String,
    reopen: bool,
) -> CmdResult<Thread> {
    resolve(&state.runner, &id, &thread, reopen).await
}

async fn resolve(runner: &Runner, id: &str, thread: &str, reopen: bool) -> CmdResult<Thread> {
    check_id(id)?;
    check_thread(thread)?;
    let mut args = strings(&["artifact", "comment", "resolve", "--json"]);
    if reopen {
        args.push("--reopen".to_string());
    }
    args.extend(strings(&["--", id, thread]));
    let result: ThreadResult = runner.ws_json_as_given(&args).await?;
    Ok(result.thread)
}

/// An artifact id, `art-` and 8 lowercase hex digits, with an optional `/r<n>`
/// revision (1 to 999): what `internal/artifact` accepts as a reference.
fn valid_id(id: &str) -> bool {
    let (base, revision) = match id.split_once('/') {
        Some((base, revision)) => (base, Some(revision)),
        None => (id, None),
    };
    let hex = base.strip_prefix("art-").unwrap_or("");
    let id_ok = hex.len() == 8
        && hex
            .bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b));
    id_ok && revision.is_none_or(valid_revision)
}

fn valid_revision(revision: &str) -> bool {
    let Some(digits) = revision.strip_prefix('r') else {
        return false;
    };
    digits.len() <= 3
        && !digits.starts_with('0')
        && !digits.is_empty()
        && digits.bytes().all(|b| b.is_ascii_digit())
}

/// A thread id: `t` and a positive number, as `ws artifact comment` names them.
fn valid_thread(thread: &str) -> bool {
    let Some(digits) = thread.strip_prefix('t') else {
        return false;
    };
    !digits.is_empty() && !digits.starts_with('0') && digits.bytes().all(|b| b.is_ascii_digit())
}

fn check_id(id: &str) -> CmdResult<()> {
    if valid_id(id) {
        return Ok(());
    }
    Err(AppError::new(
        "bad_args",
        format!("{id:?} is not an artifact id"),
    ))
}

fn check_thread(thread: &str) -> CmdResult<()> {
    if valid_thread(thread) {
        return Ok(());
    }
    Err(AppError::new(
        "bad_args",
        format!("{thread:?} is not a thread id"),
    ))
}

fn strings(parts: &[&str]) -> Vec<String> {
    parts.iter().map(|part| part.to_string()).collect()
}

/// `name value` when the value is set, nothing otherwise.
fn flag(name: &str, value: Option<&str>) -> Vec<String> {
    value.map_or_else(Vec::new, |value| strings(&[name, value]))
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::PermissionsExt;

    const ACTOR: &str = r#"{"kind":"person","workspace":null,"name":null}"#;
    const ARTIFACT: &str = r#"{"artifact":{"id":"art-1a2b3c4d","reference":"@artifact:art-1a2b3c4d","name":"a.txt","title":null,"kind":"text","content_type":"text/plain","latest":1,"size":2,"created_at":"2026-11-23T18:00:00Z","updated_at":"2026-11-23T18:00:00Z","created_by":{"kind":"person","workspace":null,"name":null},"threads":{"open":0,"resolved":0,"detached":0},"revisions":[{"rev":1,"content_type":"text/plain","size":2,"sha256":"abc","created_at":"2026-11-23T18:00:00Z","created_by":{"kind":"person","workspace":null,"name":null},"note":null}]},"warnings":[]}"#;
    const THREAD: &str = r#"{"id":"t1","anchor":null,"resolved":false,"resolved_at":null,"resolved_by":null,"comments":[]}"#;

    /// A directory holding a fake `ws` script, for PATH. The script writes
    /// its arguments, one per line, to `args` and its stdin to `stdin`.
    fn fake_ws(body: &str) -> (tempfile::TempDir, Runner) {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("ws");
        let script = format!(
            "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"{args}\"\ncat > \"{stdin}\"\n{body}\n",
            args = dir.path().join("args").display(),
            stdin = dir.path().join("stdin").display(),
        );
        std::fs::write(&path, script).unwrap();
        std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o755)).unwrap();
        let runner = Runner::new(format!("{}:/bin:/usr/bin", dir.path().display()));
        (dir, runner)
    }

    fn read_file(dir: &tempfile::TempDir, file: &str) -> String {
        std::fs::read_to_string(dir.path().join(file)).unwrap()
    }

    fn args_of(dir: &tempfile::TempDir) -> Vec<String> {
        read_file(dir, "args").lines().map(str::to_string).collect()
    }

    #[tokio::test]
    async fn list_passes_kind_workspace_and_the_limit() {
        let (dir, runner) = fake_ws(r#"echo '{"artifacts":[],"warnings":[]}'"#);
        let listed = list(&runner, Some("image"), Some("pitch-ab12cd34"))
            .await
            .unwrap();
        assert!(listed.is_empty());
        assert_eq!(
            args_of(&dir),
            strings(&[
                "artifact",
                "list",
                "--json",
                "--kind",
                "image",
                "--workspace",
                "pitch-ab12cd34",
                "--limit",
                "1000",
            ])
        );
    }

    #[tokio::test]
    async fn list_leaves_out_unset_filters() {
        let (dir, runner) = fake_ws(r#"echo '{"artifacts":[],"warnings":[]}'"#);
        list(&runner, None, None).await.unwrap();
        assert_eq!(
            args_of(&dir),
            strings(&["artifact", "list", "--json", "--limit", "1000"])
        );
    }

    #[tokio::test]
    async fn show_returns_the_artifact_with_its_revisions() {
        let (dir, runner) = fake_ws(&format!("echo '{ARTIFACT}'"));
        let shown = show(&runner, "art-1a2b3c4d").await.unwrap();
        assert_eq!(shown.summary.name, "a.txt");
        assert_eq!(shown.revisions.len(), 1);
        assert_eq!(
            args_of(&dir),
            strings(&["artifact", "show", "--json", "--", "art-1a2b3c4d"])
        );
    }

    #[tokio::test]
    async fn read_returns_base64_of_the_revision_bytes() {
        let body = format!(
            "case \"$2\" in cat) printf hi; cp \"$(dirname \"$0\")/args\" \"$(dirname \"$0\")/cat_args\" ;; show) echo '{ARTIFACT}' ;; esac"
        );
        let (dir, runner) = fake_ws(&body);
        let content = read(&runner, "art-1a2b3c4d", Some(1)).await.unwrap();
        assert_eq!(content.rev, 1);
        assert_eq!(content.content_type, "text/plain");
        assert_eq!(content.size, 2);
        assert_eq!(content.data_base64, "aGk=");
        assert_eq!(
            read_file(&dir, "cat_args")
                .lines()
                .map(str::to_string)
                .collect::<Vec<_>>(),
            strings(&["artifact", "cat", "--rev", "1", "--", "art-1a2b3c4d"])
        );
    }

    #[tokio::test]
    async fn read_reports_ws_error_codes() {
        let body = r#"echo '{"error":{"code":"artifact_unknown","message":"no such artifact"}}' >&2; exit 3"#;
        let (_dir, runner) = fake_ws(body);
        let err = read(&runner, "art-0badc0de", None).await.unwrap_err();
        assert_eq!(err.code, "artifact_unknown");
    }

    #[tokio::test]
    async fn add_sends_the_decoded_bytes_on_stdin() {
        let (dir, runner) = fake_ws(&format!("echo '{ARTIFACT}'"));
        let added = add(&runner, "a.txt", Some("Notes"), "aGVsbG8=")
            .await
            .unwrap();
        assert_eq!(added.summary.id, "art-1a2b3c4d");
        assert_eq!(read_file(&dir, "stdin"), "hello");
        assert_eq!(
            args_of(&dir),
            strings(&["artifact", "add", "-", "--json", "--name", "a.txt", "--title", "Notes",])
        );
    }

    #[tokio::test]
    async fn add_refuses_a_file_over_the_limit_without_running_ws() {
        let (dir, runner) = fake_ws("exit 1");
        let data = Base64::encode_string(&vec![0u8; MAX_UPLOAD + 1]);
        let err = add(&runner, "big.bin", None, &data).await.unwrap_err();
        assert_eq!(err.code, "artifact_invalid");
        assert_eq!(err.message, "big.bin is over the 10 MiB limit");
        assert!(!dir.path().join("args").exists());
    }

    #[tokio::test]
    async fn add_refuses_bad_base64_without_running_ws() {
        let (dir, runner) = fake_ws("exit 1");
        let err = add(&runner, "a.txt", None, "not base64!")
            .await
            .unwrap_err();
        assert_eq!(err.code, "bad_args");
        assert!(!dir.path().join("args").exists());
    }

    #[tokio::test]
    async fn remove_returns_the_item_error() {
        let body = r#"echo '{"artifacts":[{"id":"art-0000000a","removed":false,"error":{"code":"artifact_forbidden","message":"not yours"}}],"warnings":[]}'"#;
        let (dir, runner) = fake_ws(body);
        let err = remove(&runner, "art-0000000a").await.unwrap_err();
        assert_eq!(err.code, "artifact_forbidden");
        assert_eq!(
            args_of(&dir),
            strings(&["artifact", "rm", "--json", "--", "art-0000000a"])
        );
    }

    #[tokio::test]
    async fn remove_succeeds_when_no_item_has_an_error() {
        let body = r#"echo '{"artifacts":[{"id":"art-0000000a","removed":true,"error":null}],"warnings":[]}'"#;
        let (_dir, runner) = fake_ws(body);
        remove(&runner, "art-0000000a").await.unwrap();
    }

    #[tokio::test]
    async fn comments_lists_the_threads() {
        let body = format!(
            r#"echo '{{"artifact_id":"art-0000000a","latest":1,"threads":[{THREAD}],"warnings":[]}}'"#
        );
        let (dir, runner) = fake_ws(&body);
        let list = comments(&runner, "art-0000000a").await.unwrap();
        assert_eq!(list.threads[0].id, "t1");
        assert_eq!(
            args_of(&dir),
            strings(&[
                "artifact",
                "comment",
                "list",
                "--json",
                "--",
                "art-0000000a"
            ])
        );
    }

    #[tokio::test]
    async fn comment_add_replies_with_the_body_on_stdin() {
        let body = format!(
            r#"echo '{{"artifact_id":"art-0000000a","thread":{THREAD},"comment":{{"id":"c1","author":{ACTOR},"body":"Thanks","created_at":"2026-11-23T18:20:00Z"}},"warnings":[]}}'"#
        );
        let (dir, runner) = fake_ws(&body);
        let added = add_comment(&runner, "art-0000000a", "Thanks", Some("t1"), None)
            .await
            .unwrap();
        assert_eq!(added.comment.body, "Thanks");
        assert_eq!(read_file(&dir, "stdin"), "Thanks");
        assert_eq!(
            args_of(&dir),
            strings(&[
                "artifact",
                "comment",
                "add",
                "--json",
                "--body-file",
                "-",
                "--reply",
                "t1",
                "--",
                "art-0000000a",
            ])
        );
    }

    #[tokio::test]
    async fn comment_add_anchors_a_quote_with_its_context() {
        let body = format!(
            r#"echo '{{"artifact_id":"art-0000000a","thread":{THREAD},"comment":{{"id":"c1","author":{ACTOR},"body":"Why?","created_at":"2026-11-23T18:20:00Z"}},"warnings":[]}}'"#
        );
        let (dir, runner) = fake_ws(&body);
        let anchor = NewAnchor {
            quote: "the cache is shared".to_string(),
            line: 7,
            prefix: "Today ".to_string(),
            suffix: " across".to_string(),
        };
        add_comment(&runner, "art-0000000a", "Why?", None, Some(&anchor))
            .await
            .unwrap();
        assert_eq!(
            args_of(&dir),
            strings(&[
                "artifact",
                "comment",
                "add",
                "--json",
                "--body-file",
                "-",
                "--quote",
                "the cache is shared",
                "--line",
                "7",
                "--prefix",
                "Today ",
                "--suffix",
                " across",
                "--",
                "art-0000000a",
            ])
        );
    }

    #[tokio::test]
    async fn comment_resolve_passes_reopen() {
        let body =
            format!(r#"echo '{{"artifact_id":"art-0000000a","thread":{THREAD},"warnings":[]}}'"#);
        let (dir, runner) = fake_ws(&body);
        let thread = resolve(&runner, "art-0000000a", "t1", true).await.unwrap();
        assert_eq!(thread.id, "t1");
        assert_eq!(
            args_of(&dir),
            strings(&[
                "artifact",
                "comment",
                "resolve",
                "--json",
                "--reopen",
                "--",
                "art-0000000a",
                "t1"
            ])
        );
    }

    const GOOD: &str = "art-1a2b3c4d";

    fn assert_refused_without_running_ws(dir: &tempfile::TempDir, err: AppError) {
        assert_eq!(err.code, "bad_args");
        assert!(!dir.path().join("args").exists(), "ws must not run");
    }

    #[test]
    fn valid_id_matches_the_go_form_with_an_optional_revision() {
        for good in ["art-00ff12ab", "art-1a2b3c4d/r2", "art-1a2b3c4d/r999"] {
            assert!(valid_id(good), "{good} should be valid");
        }
        for bad in [
            "",
            "--help",
            "-x",
            "art-1",
            "art-00FF12AB",
            "art-1a2b3c4d/r0",
            "art-1a2b3c4d/r1000",
            "art-1a2b3c4d/r",
            "art-1a2b3c4d ",
            "art-1a2b3c4d\n",
            "@artifact:art-1a2b3c4d",
            "art-1a2b3c4dd",
        ] {
            assert!(!valid_id(bad), "{bad:?} should be invalid");
        }
    }

    #[test]
    fn valid_thread_is_t_and_a_positive_number() {
        assert!(valid_thread("t1") && valid_thread("t12"));
        for bad in ["", "--help", "t", "t0", "t01", "c1", "t1 "] {
            assert!(!valid_thread(bad), "{bad:?} should be invalid");
        }
    }

    #[tokio::test]
    async fn every_id_taking_command_refuses_a_flag_as_the_id() {
        let (dir, runner) = fake_ws("exit 1");
        assert_refused_without_running_ws(&dir, show(&runner, "--help").await.unwrap_err());
        assert_refused_without_running_ws(&dir, read(&runner, "--help", None).await.unwrap_err());
        assert_refused_without_running_ws(&dir, remove(&runner, "--help").await.unwrap_err());
        assert_refused_without_running_ws(&dir, comments(&runner, "--help").await.unwrap_err());
        let added = add_comment(&runner, "--help", "hi", None, None).await;
        assert_refused_without_running_ws(&dir, added.unwrap_err());
        let resolved = resolve(&runner, "--help", "t1", false).await;
        assert_refused_without_running_ws(&dir, resolved.unwrap_err());
    }

    #[tokio::test]
    async fn comment_commands_refuse_a_flag_as_a_thread_or_reply() {
        let (dir, runner) = fake_ws("exit 1");
        let reply = add_comment(&runner, GOOD, "hi", Some("--help"), None).await;
        assert_refused_without_running_ws(&dir, reply.unwrap_err());
        let resolved = resolve(&runner, GOOD, "--help", false).await;
        assert_refused_without_running_ws(&dir, resolved.unwrap_err());
    }

    #[tokio::test]
    async fn positional_ids_follow_a_double_dash() {
        let (dir, runner) = fake_ws(&format!("echo '{ARTIFACT}'"));
        show(&runner, "art-1a2b3c4d/r2").await.unwrap();
        let args = args_of(&dir);
        let dashes = args.iter().position(|arg| arg == "--").unwrap();
        assert_eq!(args[dashes + 1..], ["art-1a2b3c4d/r2"]);
    }
}
