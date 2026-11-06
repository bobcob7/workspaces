//! What the tray shows and what notifications say (ADR 0050). Pure.

use super::attention::{truncate, Attention, Candidate, Event};

/// Most agents listed per menu group; the rest are "and N more".
pub const GROUP_MAX: usize = 10;
/// Longest menu label, in characters.
pub const LABEL_MAX: usize = 60;
/// More events than this in one listing become one summary notification.
pub const SUMMARY_OVER: usize = 3;

/// One agent row in the tray menu.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct TrayItem {
    /// The agent's key, used to select it when clicked.
    pub key: String,
    /// The menu text, from `label`.
    pub label: String,
}

/// A menu group: at most GROUP_MAX items, and how many were left out.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct TrayGroup {
    /// The listed agents, most recently active first.
    pub items: Vec<TrayItem>,
    /// Agents in the group that aren't in `items`.
    pub more: usize,
}

/// Everything the tray shows.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct TrayView {
    /// The needs-input count, or None at 0.
    pub title: Option<String>,
    /// The hover text, counting every needs-input agent.
    pub tooltip: String,
    /// Agents that need input.
    pub needs_input: TrayGroup,
    /// Agents that are done.
    pub done: TrayGroup,
}

/// One native notification.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Notice {
    /// The notification title.
    pub title: String,
    /// The notification body.
    pub body: String,
}

/// `name — snippet`, or `name` when the snippet is empty, cut to LABEL_MAX.
pub fn label(candidate: &Candidate) -> String {
    let text = if candidate.snippet.is_empty() {
        candidate.name.clone()
    } else {
        format!("{} — {}", candidate.name, candidate.snippet)
    };
    truncate(&text, LABEL_MAX)
}

/// The tray for these candidates (README "Tray menu").
pub fn tray_view(candidates: &[Candidate]) -> TrayView {
    let needs_input = group(candidates, Attention::NeedsInput);
    let done = group(candidates, Attention::Done);
    let count = needs_input.items.len() + needs_input.more;
    TrayView {
        title: (count > 0).then(|| count.to_string()),
        tooltip: tooltip(count),
        needs_input,
        done,
    }
}

fn group(candidates: &[Candidate], attention: Attention) -> TrayGroup {
    let mut matching: Vec<&Candidate> = candidates
        .iter()
        .filter(|candidate| candidate.attention == attention)
        .collect();
    matching.sort_by(|a, b| {
        b.active_at
            .cmp(&a.active_at)
            .then_with(|| a.name.cmp(&b.name))
    });
    let more = matching.len().saturating_sub(GROUP_MAX);
    let items = matching
        .into_iter()
        .take(GROUP_MAX)
        .map(|candidate| TrayItem {
            key: candidate.key.clone(),
            label: label(candidate),
        })
        .collect();
    TrayGroup { items, more }
}

fn tooltip(count: usize) -> String {
    match count {
        0 => "Workspaces: no agents need input".to_string(),
        1 => "Workspaces: 1 agent needs input".to_string(),
        n => format!("Workspaces: {n} agents need input"),
    }
}

/// The notifications for one listing's events (README "Notification text").
pub fn notices(events: &[Event]) -> Vec<Notice> {
    let shown: Vec<&Event> = events
        .iter()
        .filter(|event| event.attention != Attention::Busy)
        .collect();
    if shown.len() > SUMMARY_OVER {
        return vec![summary(&shown)];
    }
    shown.into_iter().map(notice).collect()
}

fn notice(event: &Event) -> Notice {
    if event.attention == Attention::Done {
        return Notice {
            title: format!("{} is done", event.name),
            body: or_default(&event.snippet, "Finished its turn"),
        };
    }
    Notice {
        title: format!("{} needs input", event.name),
        body: or_default(&event.snippet, "Waiting for your answer"),
    }
}

fn summary(events: &[&Event]) -> Notice {
    let count = events.len();
    let names: Vec<&str> = events
        .iter()
        .take(SUMMARY_OVER)
        .map(|event| event.name.as_str())
        .collect();
    Notice {
        title: format!("{count} agents need you"),
        body: format!("{} and {} more", names.join(", "), count - SUMMARY_OVER),
    }
}

fn or_default(snippet: &str, fallback: &str) -> String {
    if snippet.is_empty() {
        fallback.to_string()
    } else {
        snippet.to_string()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn candidate(key: &str, attention: Attention, active_at: &str) -> Candidate {
        Candidate {
            key: key.to_string(),
            name: key.to_string(),
            snippet: String::new(),
            attention,
            active_at: active_at.to_string(),
        }
    }

    fn event(name: &str, attention: Attention, snippet: &str) -> Event {
        Event {
            key: name.to_string(),
            name: name.to_string(),
            snippet: snippet.to_string(),
            attention,
        }
    }

    #[test]
    fn label_joins_name_and_snippet() {
        let mut c = candidate("fix-login", Attention::NeedsInput, "");
        c.snippet = "Allow Bash?".to_string();
        assert_eq!(label(&c), "fix-login — Allow Bash?");
    }

    #[test]
    fn label_is_name_without_snippet() {
        let c = candidate("fix-login", Attention::Done, "");
        assert_eq!(label(&c), "fix-login");
    }

    #[test]
    fn label_is_cut_to_label_max() {
        let mut c = candidate("a", Attention::Done, "");
        c.snippet = "x".repeat(100);
        let text = label(&c);
        assert_eq!(text.chars().count(), LABEL_MAX);
        assert!(text.ends_with('…'));
    }

    #[test]
    fn empty_tray_has_no_title_and_empty_groups() {
        let view = tray_view(&[]);
        assert_eq!(view.title, None);
        assert_eq!(view.tooltip, "Workspaces: no agents need input");
        assert!(view.needs_input.items.is_empty());
        assert_eq!(view.needs_input.more, 0);
        assert!(view.done.items.is_empty());
        assert_eq!(view.done.more, 0);
    }

    #[test]
    fn tray_counts_needs_input_and_lists_each_group() {
        let view = tray_view(&[
            candidate("n", Attention::NeedsInput, ""),
            candidate("d1", Attention::Done, ""),
            candidate("d2", Attention::Done, ""),
            candidate("b", Attention::Busy, ""),
        ]);
        assert_eq!(view.title, Some("1".to_string()));
        assert_eq!(view.tooltip, "Workspaces: 1 agent needs input");
        assert_eq!(view.needs_input.items.len(), 1);
        assert_eq!(view.done.items.len(), 2);
        let keys: Vec<&str> = view
            .needs_input
            .items
            .iter()
            .chain(&view.done.items)
            .map(|item| item.key.as_str())
            .collect();
        assert!(!keys.contains(&"b"));
    }

    #[test]
    fn done_group_sorts_by_active_at_then_name() {
        let view = tray_view(&[
            candidate("b", Attention::Done, "2026-11-06T10:00:00Z"),
            candidate("a", Attention::Done, "2026-11-06T11:00:00Z"),
            candidate("a2", Attention::Done, "2026-11-06T10:00:00Z"),
        ]);
        let keys: Vec<&str> = view
            .done
            .items
            .iter()
            .map(|item| item.key.as_str())
            .collect();
        assert_eq!(keys, ["a", "a2", "b"]);
    }

    #[test]
    fn group_lists_at_most_group_max_and_counts_the_rest() {
        let candidates: Vec<Candidate> = (0..12)
            .map(|i| candidate(&format!("a{i:02}"), Attention::NeedsInput, ""))
            .collect();
        let view = tray_view(&candidates);
        assert_eq!(view.title, Some("12".to_string()));
        assert_eq!(view.tooltip, "Workspaces: 12 agents need input");
        assert_eq!(view.needs_input.items.len(), GROUP_MAX);
        assert_eq!(view.needs_input.more, 2);
    }

    #[test]
    fn tooltip_pluralizes_needs_input_count() {
        assert_eq!(tooltip(0), "Workspaces: no agents need input");
        assert_eq!(tooltip(1), "Workspaces: 1 agent needs input");
        assert_eq!(tooltip(3), "Workspaces: 3 agents need input");
    }

    #[test]
    fn needs_input_notice_falls_back_to_waiting_text() {
        assert_eq!(
            notices(&[event("fix-login", Attention::NeedsInput, "")]),
            [Notice {
                title: "fix-login needs input".to_string(),
                body: "Waiting for your answer".to_string(),
            }]
        );
    }

    #[test]
    fn done_notice_uses_snippet() {
        assert_eq!(
            notices(&[event("fix-login", Attention::Done, "PR #4")]),
            [Notice {
                title: "fix-login is done".to_string(),
                body: "PR #4".to_string(),
            }]
        );
    }

    #[test]
    fn done_notice_falls_back_to_finished_text() {
        let found = notices(&[event("a", Attention::Done, "")]);
        assert_eq!(found[0].body, "Finished its turn");
    }

    #[test]
    fn three_events_give_three_notices_in_order() {
        let found = notices(&[
            event("a", Attention::NeedsInput, ""),
            event("b", Attention::Done, ""),
            event("c", Attention::NeedsInput, ""),
        ]);
        let titles: Vec<&str> = found.iter().map(|n| n.title.as_str()).collect();
        assert_eq!(titles, ["a needs input", "b is done", "c needs input"]);
    }

    #[test]
    fn more_than_three_events_give_one_summary() {
        let events: Vec<Event> = ["a", "b", "c", "d", "e"]
            .iter()
            .map(|name| event(name, Attention::NeedsInput, ""))
            .collect();
        assert_eq!(
            notices(&events),
            [Notice {
                title: "5 agents need you".to_string(),
                body: "a, b, c and 2 more".to_string(),
            }]
        );
    }

    #[test]
    fn busy_event_alone_gives_no_notice() {
        assert!(notices(&[event("a", Attention::Busy, "")]).is_empty());
    }
}
