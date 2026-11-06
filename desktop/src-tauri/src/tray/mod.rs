//! The menu-bar tray and native notifications for top-level agents that
//! need input or are done (ADR 0050).

pub mod attention;
pub mod view;
pub mod watcher;

use crate::commands::AppState;
use tauri::{AppHandle, Manager};
use tauri_plugin_notification::NotificationExt;
use watcher::Observed;

/// Sends the notifications the settings allow for one listing. Native and
/// thin: verified by cargo build and clippy, and by hand.
pub fn publish(app: &AppHandle, observed: &Observed) {
    let settings = app.state::<AppState>().store.get().settings.notifications;
    for notice in view::notices(&watcher::wanted(&observed.events, &settings)) {
        if let Err(err) = app
            .notification()
            .builder()
            .title(&notice.title)
            .body(&notice.body)
            .show()
        {
            eprintln!("tray: notification failed: {err}");
        }
    }
}
