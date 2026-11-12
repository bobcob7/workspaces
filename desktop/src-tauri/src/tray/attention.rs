//! Which agents the tray tracks, and when one starts needing the user
//! (ADR 0050). Pure: no I/O, no tauri.

use crate::model::{Entry, Kind, Status};
use std::collections::{BTreeMap, BTreeSet};

/// How long a state must hold before it's announced, so a state seen in
/// only one listing (4 s apart) never notifies.
pub const DWELL_MS: i64 = 3_000;

/// Longest snippet, in characters, before it's cut with `…`.
pub const SNIPPET_MAX: usize = 80;

/// What a tracked agent asks of the user.
// `RunNeedsAttention` is the name the ADR 0057 contract gives the variant.
#[allow(clippy::enum_variant_names)]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Attention {
    NeedsInput,
    Done,
    Busy,
    /// A loop run is blocked and waits for an answer (ADR 0057).
    Blocked,
    /// A loop run stopped without a verdict (ADR 0057).
    RunNeedsAttention,
}

/// `needsInput` and `idle` need input (the Agents list's Needs input
/// group), `done` is done, everything else is busy.
pub fn attention_of(status: Status) -> Attention {
    match status {
        Status::NeedsInput | Status::Idle => Attention::NeedsInput,
        Status::Done => Attention::Done,
        Status::Working
        | Status::Starting
        | Status::Stopped
        | Status::Running
        | Status::Pending
        | Status::Unknown => Attention::Busy,
    }
}

/// An agent in a workspace, with no parent, not hidden (ADRs 0043, 0044).
pub fn is_tracked(entry: &Entry) -> bool {
    entry.kind == Kind::Agent
        && entry.workspace_id.is_some()
        && entry.parent_id.is_none()
        && !entry.hidden
}

/// `s` if it has at most `max` chars, else its first `max - 1` chars
/// followed by `…`. Counts chars, not bytes.
pub fn truncate(s: &str, max: usize) -> String {
    if s.chars().count() <= max {
        return s.to_string();
    }
    let mut out: String = s.chars().take(max.saturating_sub(1)).collect();
    out.push('…');
    out
}

/// The first non-empty line, trimmed, of: for NeedsInput and Busy,
/// `detail` then `intent`; for Done, `result` then `detail` then
/// `intent`. Cut to SNIPPET_MAX. "" when none has text.
pub fn snippet(entry: &Entry, attention: Attention) -> String {
    let sources: [Option<&String>; 3] = match attention {
        Attention::Done => [
            entry.result.as_ref(),
            entry.detail.as_ref(),
            entry.intent.as_ref(),
        ],
        Attention::NeedsInput
        | Attention::Busy
        | Attention::Blocked
        | Attention::RunNeedsAttention => [entry.detail.as_ref(), entry.intent.as_ref(), None],
    };
    sources
        .into_iter()
        .flatten()
        .find_map(|text| first_line(text))
        .map(|line| truncate(line, SNIPPET_MAX))
        .unwrap_or_default()
}

fn first_line(text: &str) -> Option<&str> {
    text.lines().map(str::trim).find(|line| !line.is_empty())
}

/// A tracked agent as the tray and notifications see it.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Candidate {
    pub key: String,
    pub name: String,
    pub snippet: String,
    pub attention: Attention,
    /// `updated_at`, else `created_at`, else "" (RFC 3339, so it sorts).
    pub active_at: String,
    /// A loop run that is blocked or needs attention; `loop_step` notifies it.
    pub loop_raised: bool,
}

/// The tracked entries, in listing order.
pub fn candidates(entries: &[Entry]) -> Vec<Candidate> {
    entries
        .iter()
        .filter(|entry| is_tracked(entry))
        .map(|entry| {
            let attention = attention_of(entry.status);
            Candidate {
                key: entry.key.clone(),
                name: entry.name.clone(),
                snippet: snippet(entry, attention),
                attention,
                active_at: entry
                    .updated_at
                    .clone()
                    .or_else(|| entry.created_at.clone())
                    .unwrap_or_default(),
                loop_raised: loop_attention(entry).is_some(),
            }
        })
        .collect()
}

/// What the watcher remembers of one agent.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Tracked {
    pub attention: Attention,
    /// When it entered `attention` (ms since the epoch).
    pub since_ms: i64,
    /// A notification for this stretch was sent, or it was first seen in it.
    pub announced: bool,
}

/// Every tracked agent's record, by `Candidate::key`.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Snapshot {
    pub tracked: BTreeMap<String, Tracked>,
}

/// An agent that started needing input (`NeedsInput`) or finished (`Done`),
/// or a loop run that is `Blocked` or `RunNeedsAttention`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Event {
    pub key: String,
    pub name: String,
    pub snippet: String,
    pub attention: Attention,
}

/// The next snapshot and the events for one listing (README "Behavior").
pub fn step(prev: &Snapshot, current: &[Candidate], now_ms: i64) -> (Snapshot, Vec<Event>) {
    let mut next = Snapshot::default();
    let mut events = Vec::new();
    for candidate in current {
        let record = match prev.tracked.get(&candidate.key) {
            None => Tracked {
                attention: candidate.attention,
                since_ms: now_ms,
                announced: true,
            },
            Some(old) if old.attention != candidate.attention => Tracked {
                attention: candidate.attention,
                since_ms: now_ms,
                announced: false,
            },
            Some(old) => {
                let due = candidate.attention != Attention::Busy
                    && !candidate.loop_raised
                    && !old.announced
                    && now_ms - old.since_ms >= DWELL_MS;
                if due {
                    events.push(Event {
                        key: candidate.key.clone(),
                        name: candidate.name.clone(),
                        snippet: candidate.snippet.clone(),
                        attention: candidate.attention,
                    });
                }
                Tracked {
                    announced: old.announced || due,
                    ..*old
                }
            }
        };
        next.tracked.insert(candidate.key.clone(), record);
    }
    (next, events)
}

/// What loop_step remembers of loop runs (ADR 0057).
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct LoopSnapshot {
    /// A listing has been stepped (the first one notifies nothing).
    pub seeded: bool,
    /// Raised runs not yet notified: key → when first seen raised (ms).
    pub pending: BTreeMap<String, i64>,
    /// Runs already notified, or raised in the first listing; never pruned.
    pub notified: BTreeSet<String>,
}

/// The loop attention of entry: Blocked for "blocked", RunNeedsAttention for
/// "needs_attention", None otherwise or without a workspace.
pub fn loop_attention(entry: &Entry) -> Option<Attention> {
    entry.workspace_id.as_ref()?;
    match entry.loop_run.as_ref()?.attention.as_deref()? {
        "blocked" => Some(Attention::Blocked),
        "needs_attention" => Some(Attention::RunNeedsAttention),
        _ => None,
    }
}

/// One notification per raised loop run (ADR 0057): a run raised for
/// DWELL_MS is announced once, and never again.
pub fn loop_step(
    prev: &LoopSnapshot,
    entries: &[Entry],
    now_ms: i64,
) -> (LoopSnapshot, Vec<Event>) {
    let mut next = LoopSnapshot {
        seeded: true,
        pending: BTreeMap::new(),
        notified: prev.notified.clone(),
    };
    let mut events = Vec::new();
    for entry in entries {
        let Some(attention) = loop_attention(entry) else {
            continue;
        };
        if next.notified.contains(&entry.key) {
            continue;
        }
        if !prev.seeded {
            next.notified.insert(entry.key.clone());
            continue;
        }
        let since = prev.pending.get(&entry.key).copied().unwrap_or(now_ms);
        if now_ms - since < DWELL_MS {
            next.pending.insert(entry.key.clone(), since);
            continue;
        }
        events.push(Event {
            key: entry.key.clone(),
            name: entry.name.clone(),
            snippet: snippet(entry, Attention::NeedsInput),
            attention,
        });
        next.notified.insert(entry.key.clone());
    }
    (next, events)
}

#[cfg(test)]
pub(crate) mod testing {
    use crate::model::{Entry, Kind, Status};

    /// A tracked agent `key` with `status`; tests change fields as needed.
    pub fn entry(key: &str, status: Status) -> Entry {
        Entry {
            key: key.to_string(),
            kind: Kind::Agent,
            name: key.to_string(),
            workspace_id: Some(key.to_string()),
            session_id: Some(format!("s-{key}")),
            path: format!("/w/{key}"),
            status,
            stale: Some(false),
            created_at: Some("2026-11-06T10:00:00Z".to_string()),
            updated_at: None,
            detail: None,
            intent: None,
            result: None,
            links: Vec::new(),
            repos: Vec::new(),
            review: None,
            hidden: false,
            parent_id: None,
            loop_run: None,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::testing::entry;
    use super::*;
    use crate::model::WsLoopRun;

    fn c(key: &str, attention: Attention) -> Candidate {
        Candidate {
            key: key.to_string(),
            name: key.to_string(),
            snippet: String::new(),
            attention,
            active_at: String::new(),
            loop_raised: false,
        }
    }

    fn loop_entry(key: &str, attention: Option<&str>) -> Entry {
        let mut e = entry(key, Status::NeedsInput);
        e.loop_run = Some(WsLoopRun {
            loop_name: "deps-bump".to_string(),
            attention: attention.map(str::to_string),
        });
        e
    }

    fn seeded() -> LoopSnapshot {
        LoopSnapshot {
            seeded: true,
            ..LoopSnapshot::default()
        }
    }

    #[test]
    fn needs_input_and_idle_statuses_need_input() {
        assert_eq!(attention_of(Status::NeedsInput), Attention::NeedsInput);
        assert_eq!(attention_of(Status::Idle), Attention::NeedsInput);
    }

    #[test]
    fn done_status_is_done() {
        assert_eq!(attention_of(Status::Done), Attention::Done);
    }

    #[test]
    fn other_statuses_are_busy() {
        for status in [
            Status::Working,
            Status::Starting,
            Status::Stopped,
            Status::Running,
            Status::Pending,
            Status::Unknown,
        ] {
            assert_eq!(attention_of(status), Attention::Busy);
        }
    }

    #[test]
    fn top_level_workspace_agent_is_tracked() {
        assert!(is_tracked(&entry("a", Status::Done)));
    }

    #[test]
    fn hidden_child_or_workspaceless_or_shell_is_not_tracked() {
        let mut hidden = entry("a", Status::Done);
        hidden.hidden = true;
        assert!(!is_tracked(&hidden));

        let mut child = entry("a", Status::Done);
        child.parent_id = Some("p".to_string());
        assert!(!is_tracked(&child));

        let mut no_workspace = entry("a", Status::Done);
        no_workspace.workspace_id = None;
        assert!(!is_tracked(&no_workspace));

        let mut shell = entry("a", Status::Done);
        shell.kind = Kind::Shell;
        assert!(!is_tracked(&shell));
    }

    #[test]
    fn truncate_keeps_short_text() {
        assert_eq!(truncate("abc", 3), "abc");
    }

    #[test]
    fn truncate_cuts_long_text_with_ellipsis() {
        assert_eq!(truncate("abcd", 3), "ab…");
    }

    #[test]
    fn truncate_counts_chars_not_bytes() {
        assert_eq!(truncate("ééééé", 4), "ééé…");
    }

    #[test]
    fn done_snippet_uses_first_line_of_result() {
        let mut e = entry("a", Status::Done);
        e.result = Some("\n  shipped PR #4  \nmore".to_string());
        assert_eq!(snippet(&e, Attention::Done), "shipped PR #4");
    }

    #[test]
    fn done_snippet_falls_back_to_detail() {
        let mut e = entry("a", Status::Done);
        e.detail = Some("d".to_string());
        assert_eq!(snippet(&e, Attention::Done), "d");
    }

    #[test]
    fn needs_input_snippet_prefers_detail_over_result() {
        let mut e = entry("a", Status::NeedsInput);
        e.detail = Some("Allow Bash?".to_string());
        e.result = Some("r".to_string());
        assert_eq!(snippet(&e, Attention::NeedsInput), "Allow Bash?");
    }

    #[test]
    fn needs_input_snippet_falls_back_to_intent() {
        let mut e = entry("a", Status::NeedsInput);
        e.intent = Some("i".to_string());
        assert_eq!(snippet(&e, Attention::NeedsInput), "i");
    }

    #[test]
    fn snippet_is_empty_without_text() {
        let e = entry("a", Status::Done);
        assert_eq!(snippet(&e, Attention::Done), "");
    }

    #[test]
    fn snippet_is_cut_to_snippet_max() {
        let mut e = entry("a", Status::Working);
        e.detail = Some("x".repeat(100));
        let text = snippet(&e, Attention::Busy);
        assert_eq!(text.chars().count(), SNIPPET_MAX);
        assert!(text.ends_with('…'));
    }

    #[test]
    fn candidates_keep_tracked_entries_in_order() {
        let mut hidden = entry("b", Status::Done);
        hidden.hidden = true;
        let mut child = entry("c", Status::Done);
        child.parent_id = Some("p".to_string());
        let entries = [
            entry("a", Status::Done),
            hidden,
            child,
            entry("d", Status::Working),
        ];
        let found = candidates(&entries);
        let keys: Vec<&str> = found.iter().map(|c| c.key.as_str()).collect();
        let attentions: Vec<Attention> = found.iter().map(|c| c.attention).collect();
        assert_eq!(keys, ["a", "d"]);
        assert_eq!(attentions, [Attention::Done, Attention::Busy]);
    }

    #[test]
    fn candidate_active_at_prefers_updated_at() {
        let mut e = entry("a", Status::Done);
        assert_eq!(
            candidates(&[e.clone()])[0].active_at,
            "2026-11-06T10:00:00Z"
        );
        e.updated_at = Some("2026-11-06T11:00:00Z".to_string());
        assert_eq!(candidates(&[e])[0].active_at, "2026-11-06T11:00:00Z");
    }

    #[test]
    fn first_seen_needs_input_is_announced_without_event() {
        let (next, events) = step(&Snapshot::default(), &[c("a", Attention::NeedsInput)], 0);
        assert!(events.is_empty());
        assert!(next.tracked["a"].announced);
    }

    #[test]
    fn needs_input_notifies_once_after_dwell() {
        let first = [c("a", Attention::Busy)];
        let (s0, e0) = step(&Snapshot::default(), &first, 0);
        assert!(e0.is_empty());

        let waiting = [c("a", Attention::NeedsInput)];
        let (s1, e1) = step(&s0, &waiting, 4_000);
        assert!(e1.is_empty());

        let (s2, e2) = step(&s1, &waiting, 8_000);
        assert_eq!(
            e2,
            [Event {
                key: "a".to_string(),
                name: "a".to_string(),
                snippet: String::new(),
                attention: Attention::NeedsInput,
            }]
        );

        let (_, e3) = step(&s2, &waiting, 12_000);
        assert!(e3.is_empty());
    }

    #[test]
    fn done_notifies_once_after_dwell() {
        let (s0, _) = step(&Snapshot::default(), &[c("a", Attention::Busy)], 0);
        let (s1, e1) = step(&s0, &[c("a", Attention::Done)], 4_000);
        assert!(e1.is_empty());
        let (s2, e2) = step(&s1, &[c("a", Attention::Done)], 8_000);
        assert_eq!(e2.len(), 1);
        assert_eq!(e2[0].attention, Attention::Done);
        let (_, e3) = step(&s2, &[c("a", Attention::Done)], 12_000);
        assert!(e3.is_empty());
    }

    #[test]
    fn done_again_after_work_notifies_again() {
        let (s0, _) = step(&Snapshot::default(), &[c("a", Attention::Done)], 0);
        let (s1, e1) = step(&s0, &[c("a", Attention::Busy)], 4_000);
        assert!(e1.is_empty());
        let (s2, e2) = step(&s1, &[c("a", Attention::Done)], 8_000);
        assert!(e2.is_empty());
        let (_, e3) = step(&s2, &[c("a", Attention::Done)], 12_000);
        assert_eq!(e3.len(), 1);
        assert_eq!(e3[0].attention, Attention::Done);
    }

    #[test]
    fn brief_needs_input_flicker_is_not_notified() {
        let (s0, _) = step(&Snapshot::default(), &[c("a", Attention::Busy)], 0);
        let (s1, _) = step(&s0, &[c("a", Attention::NeedsInput)], 4_000);
        let (s2, _) = step(&s1, &[c("a", Attention::Done)], 8_000);
        let (_, events) = step(&s2, &[c("a", Attention::Done)], 12_000);
        assert_eq!(events.len(), 1);
        assert_eq!(events[0].attention, Attention::Done);
    }

    #[test]
    fn dwell_is_inclusive_at_exactly_dwell_ms() {
        let (s0, _) = step(&Snapshot::default(), &[c("a", Attention::Busy)], 0);
        let (s1, e1) = step(&s0, &[c("a", Attention::NeedsInput)], 1_000);
        assert!(e1.is_empty());
        let (s2, e2) = step(&s1, &[c("a", Attention::NeedsInput)], 3_999);
        assert!(e2.is_empty());
        let (_, e3) = step(&s2, &[c("a", Attention::NeedsInput)], 4_000);
        assert_eq!(e3.len(), 1);
    }

    #[test]
    fn busy_is_never_an_event() {
        let (s0, _) = step(&Snapshot::default(), &[c("a", Attention::NeedsInput)], 0);
        let (s1, e1) = step(&s0, &[c("a", Attention::Busy)], 4_000);
        let (_, e2) = step(&s1, &[c("a", Attention::Busy)], 100_000);
        assert!(e1.is_empty());
        assert!(e2.is_empty());
    }

    #[test]
    fn dropped_key_seen_again_in_done_has_no_event() {
        let (s0, _) = step(&Snapshot::default(), &[c("a", Attention::Busy)], 0);
        let (s1, _) = step(&s0, &[], 4_000);
        assert!(!s1.tracked.contains_key("a"));
        let (_, events) = step(&s1, &[c("a", Attention::Done)], 8_000);
        assert!(events.is_empty());
    }

    #[test]
    fn event_carries_current_name() {
        let (s0, _) = step(&Snapshot::default(), &[c("a", Attention::Busy)], 0);
        let (s1, _) = step(&s0, &[c("a", Attention::NeedsInput)], 4_000);
        let mut renamed = c("a", Attention::NeedsInput);
        renamed.name = "renamed".to_string();
        let (_, events) = step(&s1, &[renamed], 8_000);
        assert_eq!(events[0].name, "renamed");
    }

    #[test]
    fn loop_attention_maps_blocked_and_needs_attention() {
        assert_eq!(
            loop_attention(&loop_entry("a", Some("blocked"))),
            Some(Attention::Blocked)
        );
        assert_eq!(
            loop_attention(&loop_entry("a", Some("needs_attention"))),
            Some(Attention::RunNeedsAttention)
        );
        assert_eq!(loop_attention(&loop_entry("a", Some("other"))), None);
        assert_eq!(loop_attention(&loop_entry("a", None)), None);
    }

    #[test]
    fn loop_attention_is_none_without_workspace_or_loop_run() {
        let mut no_workspace = loop_entry("a", Some("blocked"));
        no_workspace.workspace_id = None;
        assert_eq!(loop_attention(&no_workspace), None);
        assert_eq!(loop_attention(&entry("a", Status::NeedsInput)), None);
    }

    #[test]
    fn loop_run_raised_in_first_listing_is_not_notified() {
        let (next, events) = loop_step(
            &LoopSnapshot::default(),
            &[loop_entry("a", Some("blocked"))],
            0,
        );
        assert!(events.is_empty());
        assert!(next.seeded);
        assert!(next.notified.contains("a"));
    }

    #[test]
    fn loop_run_raised_later_notifies_once_after_dwell() {
        let (s0, _) = loop_step(&LoopSnapshot::default(), &[entry("a", Status::Done)], 0);
        let raised = [loop_entry("a", Some("blocked"))];
        let (s1, e1) = loop_step(&s0, &raised, 0);
        assert!(e1.is_empty());
        let (s2, e2) = loop_step(&s1, &raised, DWELL_MS - 1);
        assert!(e2.is_empty());
        let (s3, e3) = loop_step(&s2, &raised, DWELL_MS);
        assert_eq!(
            e3,
            [Event {
                key: "a".to_string(),
                name: "a".to_string(),
                snippet: String::new(),
                attention: Attention::Blocked,
            }]
        );
        let (_, e4) = loop_step(&s3, &raised, 100_000);
        assert!(e4.is_empty());
    }

    #[test]
    fn loop_run_moving_to_blocked_after_notice_is_not_renotified() {
        let (s0, _) = loop_step(&seeded(), &[loop_entry("a", Some("needs_attention"))], 0);
        let (s1, e1) = loop_step(&s0, &[loop_entry("a", Some("needs_attention"))], DWELL_MS);
        assert_eq!(e1.len(), 1);
        assert_eq!(e1[0].attention, Attention::RunNeedsAttention);
        let (_, e2) = loop_step(&s1, &[loop_entry("a", Some("blocked"))], DWELL_MS * 2);
        assert!(e2.is_empty());
    }

    #[test]
    fn loop_run_raised_again_after_resolving_is_not_renotified() {
        let (s0, _) = loop_step(&seeded(), &[loop_entry("a", Some("blocked"))], 0);
        let (s1, e1) = loop_step(&s0, &[loop_entry("a", Some("blocked"))], DWELL_MS);
        assert_eq!(e1.len(), 1);
        let (s2, _) = loop_step(&s1, &[loop_entry("a", None)], DWELL_MS * 2);
        let (_, e3) = loop_step(&s2, &[loop_entry("a", Some("blocked"))], DWELL_MS * 3);
        assert!(e3.is_empty());
    }

    #[test]
    fn loop_run_needs_attention_gives_run_needs_attention() {
        let (s0, _) = loop_step(&seeded(), &[], 0);
        let raised = [loop_entry("a", Some("needs_attention"))];
        let (s1, _) = loop_step(&s0, &raised, 0);
        let (_, events) = loop_step(&s1, &raised, DWELL_MS);
        assert_eq!(events[0].attention, Attention::RunNeedsAttention);
    }

    #[test]
    fn entries_without_workspace_or_loop_run_give_no_loop_event() {
        let mut no_workspace = loop_entry("b", Some("blocked"));
        no_workspace.workspace_id = None;
        let entries = [no_workspace, entry("c", Status::NeedsInput)];
        let (s0, _) = loop_step(&seeded(), &entries, 0);
        let (s1, events) = loop_step(&s0, &entries, DWELL_MS);
        assert!(events.is_empty());
        assert!(s1.pending.is_empty());
    }

    #[test]
    fn loop_raised_candidate_gets_no_regular_event() {
        let raised = Candidate {
            loop_raised: true,
            ..c("a", Attention::NeedsInput)
        };
        let (s0, _) = step(&Snapshot::default(), &[c("a", Attention::Busy)], 0);
        let (s1, _) = step(&s0, std::slice::from_ref(&raised), 4_000);
        let (_, events) = step(&s1, &[raised], 8_000);
        assert!(events.is_empty());
    }

    #[test]
    fn candidates_mark_loop_raised_runs() {
        let found = candidates(&[loop_entry("a", Some("blocked")), entry("b", Status::Done)]);
        assert!(found[0].loop_raised);
        assert!(!found[1].loop_raised);
    }
}
