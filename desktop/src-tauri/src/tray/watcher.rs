//! Feeds each listing to the attention step, and lists by itself when the
//! frontend hasn't for a while, as when the window is closed (ADR 0050).

use super::attention::{self, Attention, Candidate, Event, Snapshot};
use super::view::TrayView;
use crate::commands::AppState;
use crate::model::Entry;
use crate::reviews::store::NotificationSettings;
use std::sync::{Mutex, PoisonError};
use std::time::Duration;
use tauri::{AppHandle, Manager};

/// How often the watcher wakes, the same as the frontend's poll.
pub const WATCH_INTERVAL: Duration = Duration::from_secs(4);
const WATCH_INTERVAL_MS: i64 = 4_000;

/// One listing's tracked agents and the events it produced.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Observed {
    /// The tracked agents in the listing.
    pub candidates: Vec<Candidate>,
    /// The agents that started needing input or finished in this listing.
    pub events: Vec<Event>,
}

/// The snapshot and when the last listing arrived, shared by
/// `list_entries` and `watch_forever`.
#[derive(Debug, Default)]
pub struct Watcher {
    inner: Mutex<Inner>,
}

#[derive(Debug, Default)]
struct Inner {
    snapshot: Snapshot,
    last_observed_ms: Option<i64>,
    last_view: Option<TrayView>,
}

impl Watcher {
    /// Steps the snapshot with `entries` and records `now_ms` as the last listing.
    pub fn observe(&self, entries: &[Entry], now_ms: i64) -> Observed {
        let candidates = attention::candidates(entries);
        let mut inner = self.inner.lock().unwrap_or_else(PoisonError::into_inner);
        let (snapshot, events) = attention::step(&inner.snapshot, &candidates, now_ms);
        inner.snapshot = snapshot;
        inner.last_observed_ms = Some(now_ms);
        Observed { candidates, events }
    }

    /// True, and remembers `view`, when it differs from the last one.
    pub fn view_changed(&self, view: &TrayView) -> bool {
        let mut inner = self.inner.lock().unwrap_or_else(PoisonError::into_inner);
        if inner.last_view.as_ref() == Some(view) {
            return false;
        }
        inner.last_view = Some(view.clone());
        true
    }

    /// No listing yet, or none in the last WATCH_INTERVAL.
    pub fn is_stale(&self, now_ms: i64) -> bool {
        let inner = self.inner.lock().unwrap_or_else(PoisonError::into_inner);
        inner
            .last_observed_ms
            .is_none_or(|t| now_ms - t >= WATCH_INTERVAL_MS)
    }
}

/// The events the settings allow: NeedsInput when `enabled && needs_input`,
/// Done when `enabled && done`, in order.
pub fn wanted(events: &[Event], settings: &NotificationSettings) -> Vec<Event> {
    if !settings.enabled {
        return Vec::new();
    }
    events
        .iter()
        .filter(|event| match event.attention {
            Attention::NeedsInput => settings.needs_input,
            Attention::Done => settings.done,
            Attention::Busy => false,
        })
        .cloned()
        .collect()
}

/// Every WATCH_INTERVAL, when the watcher is stale, runs `commands::list`
/// and publishes what it observed. Listing errors are logged with
/// `eprintln!("tray: listing failed: {}: {}", err.code, err.message)` and skipped.
pub async fn watch_forever(app: AppHandle) {
    let mut interval = tokio::time::interval(WATCH_INTERVAL);
    interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
    loop {
        interval.tick().await;
        let state = app.state::<AppState>();
        if !state.watcher.is_stale(crate::commands::now_ms()) {
            continue;
        }
        match crate::commands::list(&state.runner, state.jobs_dir.as_deref()).await {
            Ok(listing) => {
                let observed = state
                    .watcher
                    .observe(&listing.entries, crate::commands::now_ms());
                super::publish(&app, &observed);
            }
            Err(err) => eprintln!("tray: listing failed: {}: {}", err.code, err.message),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::model::Status;
    use crate::tray::attention::testing::entry;
    use crate::tray::view::tray_view;

    fn settings(enabled: bool, needs_input: bool, done: bool) -> NotificationSettings {
        NotificationSettings {
            enabled,
            needs_input,
            done,
        }
    }

    fn event(key: &str, attention: Attention) -> Event {
        Event {
            key: key.to_string(),
            name: key.to_string(),
            snippet: String::new(),
            attention,
        }
    }

    #[test]
    fn new_watcher_is_stale() {
        let watcher = Watcher::default();
        assert!(watcher.is_stale(0));
        assert!(watcher.is_stale(i64::MAX));
    }

    #[test]
    fn observed_listing_is_fresh_for_one_interval() {
        let watcher = Watcher::default();
        watcher.observe(&[], 10_000);
        assert!(!watcher.is_stale(13_999));
        assert!(watcher.is_stale(14_000));
    }

    #[test]
    fn view_changed_tracks_the_last_view() {
        let watcher = Watcher::default();
        let empty = tray_view(&[]);
        let waiting = Candidate {
            key: "a".to_string(),
            name: "a".to_string(),
            snippet: String::new(),
            attention: Attention::NeedsInput,
            active_at: String::new(),
        };
        let busy = tray_view(&[waiting]);

        assert!(watcher.view_changed(&empty));
        assert!(!watcher.view_changed(&empty));
        assert!(watcher.view_changed(&busy));
    }

    #[test]
    fn done_after_working_notifies_once_after_dwell() {
        let watcher = Watcher::default();
        let working = [entry("a", Status::Working)];
        let done = [entry("a", Status::Done)];

        assert!(watcher.observe(&working, 0).events.is_empty());
        assert!(watcher.observe(&done, 4_000).events.is_empty());

        let third = watcher.observe(&done, 8_000);
        assert_eq!(third.events, vec![event("a", Attention::Done)]);
        assert_eq!(third.candidates.len(), 1);
        assert_eq!(third.candidates[0].key, "a");
        assert!(watcher.observe(&done, 12_000).events.is_empty());
    }

    #[test]
    fn first_seen_needs_input_does_not_notify() {
        let watcher = Watcher::default();
        let observed = watcher.observe(&[entry("a", Status::NeedsInput)], 0);
        assert!(observed.events.is_empty());
        assert_eq!(observed.candidates.len(), 1);
    }

    #[test]
    fn wanted_keeps_events_the_settings_allow() {
        let events = [
            event("a", Attention::NeedsInput),
            event("b", Attention::Done),
        ];

        assert_eq!(
            wanted(&events, &settings(true, true, true)),
            events.to_vec()
        );
        assert_eq!(
            wanted(&events, &settings(true, false, true)),
            vec![event("b", Attention::Done)]
        );
        assert_eq!(
            wanted(&events, &settings(true, true, false)),
            vec![event("a", Attention::NeedsInput)]
        );
        assert!(wanted(&events, &settings(false, true, true)).is_empty());
    }
}
