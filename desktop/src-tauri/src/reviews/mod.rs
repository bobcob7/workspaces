//! The Reviews section: open pull requests in watched repos, and review
//! agents for them (ADRs 0032, 0034, 0035).

pub mod auto;
pub mod checker;
pub mod github;
pub mod names;
pub mod plan;
pub mod store;
pub mod view;

use crate::commands::AppState;
use crate::runner::AppError;
use checker::Started;
use plan::ReviewPlan;
use store::{PrHead, RepoWatch, ReviewSettings, Settings};
use tauri::State;
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
pub fn reviews_plan(
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
    Ok(plan::plan(&repo, &pr, &state.store.get().settings.reviews))
}

/// Starts a review by hand (`ws claude`). Starting one that already exists
/// isn't an error.
#[tauri::command]
pub async fn reviews_start(state: State<'_, AppState>, plan: ReviewPlan) -> CmdResult<Started> {
    state.reviews.start_review(&state.runner, &plan).await
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
