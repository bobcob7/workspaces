//! Workspaces desktop: a window onto `ws` workspaces and their Claude Code
//! agents. It runs `ws` and `claude` for everything it does, and reads
//! Claude Code's job state for detail they don't print.

mod about;
mod cache;
mod commands;
mod env;
mod jobs;
mod model;
mod pty;
mod reviews;
mod runner;
mod transcript;

use commands::AppState;
use reviews::checker::{Checker, Deps};
use reviews::store::{self, Store};
use std::sync::Arc;
use tauri::Manager;

pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_opener::init())
        .setup(|app| {
            let path = app
                .path()
                .app_config_dir()
                .ok()
                .map(|d| d.join(store::FILE_NAME));
            let state = AppState {
                runner: runner::Runner::new(env::resolve_path()),
                terminals: pty::Terminals::default(),
                jobs_dir: jobs::jobs_dir(),
                store: Arc::new(Store::open(path)),
                reviews: Arc::new(Checker::default()),
                reports: reviews::report::Cache::default(),
            };
            let deps = Deps {
                runner: state.runner.clone(),
                store: state.store.clone(),
                jobs_dir: state.jobs_dir.clone(),
            };
            tauri::async_runtime::spawn(state.reviews.clone().run(deps));
            app.manage(state);
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            commands::list_entries,
            commands::cache_repos,
            cache::cache_list,
            cache::cache_refs,
            cache::cache_resolve,
            cache::cache_fetch,
            cache::cache_add,
            cache::cache_remove,
            commands::create_agent,
            commands::remove_entry,
            commands::rename_workspace,
            commands::versions,
            commands::set_workspace_hidden,
            commands::stop_agent,
            commands::restore_agents,
            commands::open_external,
            commands::read_transcript,
            commands::terminal_open,
            commands::terminal_write,
            commands::terminal_resize,
            commands::terminal_close,
            reviews::reviews_view,
            reviews::reviews_check_now,
            reviews::reviews_load_repos,
            reviews::reviews_set_watch,
            reviews::reviews_dismiss,
            reviews::reviews_restore,
            reviews::reviews_plan,
            reviews::reviews_profiles,
            reviews::reviews_profile_show,
            reviews::reviews_profile_add,
            reviews::reviews_profile_edit,
            reviews::reviews_profile_remove,
            reviews::reviews_start,
            reviews::review_report,
            reviews::review_decide,
            reviews::review_open_report,
            reviews::review_comment_plan,
            reviews::review_comment_post,
            reviews::review_fix_plan,
            reviews::review_fix_start,
            reviews::review_fix_sent,
            reviews::settings_get,
            reviews::settings_set_reviews,
        ])
        .build(tauri::generate_context!())
        .expect("error while building the app")
        .run(|app, event| {
            if let tauri::RunEvent::Exit = event {
                app.state::<AppState>().terminals.close_all();
            }
        });
}
