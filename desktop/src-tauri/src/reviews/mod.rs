//! The Reviews section: open pull requests in watched repos, and review
//! agents for them (ADRs 0032, 0034, 0035, 0038, 0039).

pub mod auto;
pub mod checker;
pub mod comment;
pub mod fix;
pub mod github;
pub mod names;
pub mod plan;
pub mod profiles;
pub mod report;
pub mod store;
pub mod view;

use crate::commands::AppState;
use crate::runner::AppError;
use checker::Started;
use comment::{CommentPlan, PostRequest, Posted};
use fix::{FixFinding, FixPlan, FixStarted, StartFix};
use plan::ReviewPlan;
use profiles::{ListedProfile, ProfileDetail, ProfileDraft};
use report::DecideRequest;
use serde::Serialize;
use serde_json::Value;
use store::{PrHead, RepoWatch, ReviewSettings, Settings};
use tauri::{Manager, State};
use tauri_plugin_opener::OpenerExt;
use view::ReviewsView;

type CmdResult<T> = Result<T, AppError>;

fn view(state: &AppState) -> ReviewsView {
    state.reviews.view(&state.store)
}

#[tauri::command]
pub fn reviews_view(state: State<'_, AppState>) -> ReviewsView {
    view(&state)
}

#[tauri::command]
pub fn reviews_check_now(state: State<'_, AppState>) -> ReviewsView {
    state.reviews.check_now();
    view(&state)
}

/// Re-reads the cached repos for the Watched repos dialog.
#[tauri::command]
pub async fn reviews_load_repos(state: State<'_, AppState>) -> CmdResult<ReviewsView> {
    state.reviews.load_cached(&state.runner).await?;
    Ok(view(&state))
}

/// Sets a repo's Watch and Auto-review switches. Newly watching a repo
/// checks it at once.
#[tauri::command]
pub fn reviews_set_watch(
    state: State<'_, AppState>,
    repo: String,
    watch: bool,
    auto_review: bool,
) -> CmdResult<ReviewsView> {
    let was = state.store.get().reviews.watch(&repo);
    state
        .store
        .update(|d| d.reviews.set_watch(&repo, RepoWatch { watch, auto_review }))?;
    if watch && (!was.watch || (auto_review && !was.auto_review)) {
        state.reviews.check_now();
    }
    Ok(view(&state))
}

#[tauri::command]
pub fn reviews_dismiss(
    state: State<'_, AppState>,
    repo: String,
    number: u64,
    head: String,
) -> CmdResult<ReviewsView> {
    state
        .store
        .update(|d| d.reviews.dismiss(PrHead { repo, number, head }))?;
    Ok(view(&state))
}

#[tauri::command]
pub fn reviews_restore(
    state: State<'_, AppState>,
    repo: String,
    number: u64,
) -> CmdResult<ReviewsView> {
    state.store.update(|d| d.reviews.restore(&repo, number))?;
    Ok(view(&state))
}

/// What reviewing the pull request would run, with the Settings defaults.
#[tauri::command]
pub async fn reviews_plan(
    state: State<'_, AppState>,
    repo: String,
    number: u64,
) -> CmdResult<ReviewPlan> {
    let pr = state.reviews.pull_request(&repo, number).ok_or_else(|| {
        AppError::new(
            "pr_not_found",
            format!("{repo}#{number} isn't among the open pull requests"),
        )
    })?;
    let settings = state.store.get().settings.reviews;
    plan::build(&state.runner, &repo, &pr, &settings).await
}

/// Every reviewer profile `ws review profiles` lists, for Settings.
#[tauri::command]
pub async fn reviews_profiles(state: State<'_, AppState>) -> CmdResult<Vec<ListedProfile>> {
    profiles::list(&state.runner).await
}

/// One profile with its guide (`ws review profiles show`), for the editor.
#[tauri::command]
pub async fn reviews_profile_show(
    state: State<'_, AppState>,
    name: String,
) -> CmdResult<ProfileDetail> {
    profiles::show(&state.runner, &name).await
}

/// Adds a profile (`ws review profiles add`); the draft's Runs is its
/// `--when` (ADR 0042).
#[tauri::command]
pub async fn reviews_profile_add(
    state: State<'_, AppState>,
    name: String,
    draft: ProfileDraft,
) -> CmdResult<ProfileDetail> {
    profiles::add(&state.runner, &name, &draft).await
}

/// Changes the draft's fields of a profile (`ws review profiles edit`). A
/// built-in becomes an override.
#[tauri::command]
pub async fn reviews_profile_edit(
    state: State<'_, AppState>,
    name: String,
    draft: ProfileDraft,
) -> CmdResult<ProfileDetail> {
    profiles::edit(&state.runner, &name, &draft).await
}

/// What removing a profile did, with the settings it left.
#[derive(Debug, Serialize)]
pub struct ProfileRemoved {
    pub restored: bool,
    pub settings: Settings,
}

/// Removes a profile's file (`ws review profiles rm`). A profile that's
/// gone for good loses its Settings row; a restored built-in keeps it.
#[tauri::command]
pub async fn reviews_profile_remove(
    state: State<'_, AppState>,
    name: String,
) -> CmdResult<ProfileRemoved> {
    let removed = profiles::remove(&state.runner, &name).await?;
    let mut settings = state.store.get().settings;
    if profiles::forget_removed(&mut settings.reviews.profiles, &removed) {
        settings = state.store.update(|d| {
            profiles::forget_removed(&mut d.settings.reviews.profiles, &removed);
            d.settings.clone()
        })?;
    }
    Ok(ProfileRemoved {
        restored: removed.restored,
        settings,
    })
}

/// Starts a review by hand (`ws review start`). Starting one that already
/// exists isn't an error.
#[tauri::command]
pub async fn reviews_start(state: State<'_, AppState>, plan: ReviewPlan) -> CmdResult<Started> {
    state.reviews.start_review(&state.runner, &plan).await
}

/// A review's report (`ws review report --json`), from the session's cache
/// unless `refresh` asks to read it again (ADR 0040).
#[tauri::command]
pub async fn review_report(
    state: State<'_, AppState>,
    repo: String,
    git_ref: String,
    refresh: bool,
) -> CmdResult<Value> {
    report::read(&state.runner, &state.reports, &repo, &git_ref, refresh).await
}

/// Decides one or more findings in one `ws review decide`, and returns the
/// report with the new decisions.
#[tauri::command]
pub async fn review_decide(
    state: State<'_, AppState>,
    repo: String,
    git_ref: String,
    request: DecideRequest,
) -> CmdResult<Value> {
    report::decide(&state.runner, &state.reports, &repo, &git_ref, &request).await
}

/// Writes `ws review report --format html` to the app's cache directory and
/// opens it in the browser.
#[tauri::command]
pub async fn review_open_report(
    app: tauri::AppHandle,
    state: State<'_, AppState>,
    repo: String,
    git_ref: String,
) -> CmdResult<String> {
    let dir = app
        .path()
        .app_cache_dir()
        .map_err(|e| AppError::new("internal", format!("no cache directory: {e}")))?;
    let path = report::write_html(&state.runner, &dir, &repo, &git_ref).await?;
    app.opener()
        .open_path(path.display().to_string(), None::<&str>)
        .map_err(|e| AppError::new("open_failed", e.to_string()))?;
    Ok(path.display().to_string())
}

/// What posting the findings decided `comment` would post, for the preview
/// (ADR 0041). Nothing is posted.
#[tauri::command]
pub async fn review_comment_plan(
    state: State<'_, AppState>,
    repo: String,
    git_ref: String,
    number: u64,
) -> CmdResult<CommentPlan> {
    let pr = state.reviews.pull_request(&repo, number);
    let host = github::split_repo(&repo).map_or("", |(h, _, _)| h);
    let url = pr.as_ref().map_or_else(
        || format!("https://{repo}/pull/{number}"),
        |p| p.url.clone(),
    );
    let subject = comment::Subject {
        repo: &repo,
        git_ref: &git_ref,
        number,
        head: pr
            .as_ref()
            .map_or(git_ref.as_str(), |p| p.head_ref_oid.as_str()),
        pr_url: &url,
        login: state.reviews.login(host),
    };
    comment::plan(&state.runner, &state.reports, subject).await
}

/// Posts the review the person confirmed in the preview, as one GitHub
/// review, and records the findings as commented.
#[tauri::command]
pub async fn review_comment_post(
    state: State<'_, AppState>,
    repo: String,
    git_ref: String,
    request: PostRequest,
) -> CmdResult<Posted> {
    comment::post(&state.runner, &state.reports, &repo, &git_ref, &request).await
}

/// Whom the findings decided `fix` would go to, and the message.
#[tauri::command]
pub async fn review_fix_plan(
    state: State<'_, AppState>,
    repo: String,
    git_ref: String,
    review: String,
    findings: Vec<FixFinding>,
) -> CmdResult<FixPlan> {
    let report = report::read(&state.runner, &state.reports, &repo, &git_ref, false).await?;
    let settings = state.store.get().settings.reviews;
    let agent = fix::AgentSettings {
        model: &settings.model,
        permission_mode: &settings.permission_mode,
    };
    fix::plan(
        &report,
        &review,
        &findings,
        &state.reviews.entries(),
        &agent,
    )
}

/// Starts a fix agent with the message, and records the findings as sent
/// to it.
#[tauri::command]
pub async fn review_fix_start(
    state: State<'_, AppState>,
    repo: String,
    git_ref: String,
    request: StartFix,
) -> CmdResult<FixStarted> {
    let settings = state.store.get().settings.reviews;
    let agent = fix::AgentSettings {
        model: &settings.model,
        permission_mode: &settings.permission_mode,
    };
    fix::start_agent(
        &state.runner,
        &state.reports,
        &repo,
        &git_ref,
        &request,
        &agent,
    )
    .await
}

/// Records findings as sent to an agent to fix, once the app has typed the
/// message into its terminal.
#[tauri::command]
pub async fn review_fix_sent(
    state: State<'_, AppState>,
    repo: String,
    git_ref: String,
    findings: Vec<String>,
    agent: String,
) -> CmdResult<Value> {
    report::acted(
        &state.runner,
        &state.reports,
        &repo,
        &git_ref,
        &findings,
        report::Acted::FixSent(&agent),
    )
    .await
}

#[tauri::command]
pub fn settings_get(state: State<'_, AppState>) -> Settings {
    state.store.get().settings
}

#[tauri::command]
pub fn settings_set_reviews(
    state: State<'_, AppState>,
    reviews: ReviewSettings,
) -> CmdResult<Settings> {
    reviews.validate()?;
    let settings = state.store.update(|d| {
        d.settings.reviews = reviews;
        d.settings.clone()
    })?;
    Ok(settings)
}
