//! Workspaces desktop: a window onto `ws` workspaces and their Claude Code
//! agents. It runs `ws` and `claude` for everything it does, and reads
//! Claude Code's job state for detail they don't print.

mod cache;
mod commands;
mod env;
mod jobs;
mod model;
mod pty;
mod runner;
mod transcript;

use commands::AppState;
use tauri::Manager;

pub fn run() {
    let state = AppState {
        runner: runner::Runner::new(env::resolve_path()),
        terminals: pty::Terminals::default(),
        jobs_dir: jobs::jobs_dir(),
    };
    tauri::Builder::default()
        .plugin(tauri_plugin_opener::init())
        .manage(state)
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
            commands::stop_agent,
            commands::restore_agents,
            commands::open_external,
            commands::read_transcript,
            commands::terminal_open,
            commands::terminal_write,
            commands::terminal_resize,
            commands::terminal_close,
        ])
        .build(tauri::generate_context!())
        .expect("error while building the app")
        .run(|app, event| {
            if let tauri::RunEvent::Exit = event {
                app.state::<AppState>().terminals.close_all();
            }
        });
}
