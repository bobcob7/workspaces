//! A pull request's merge status and every reason for it, whichever forge
//! it's on (ADR 0060).

use serde::{Deserialize, Serialize};

/// Declaration order is precedence: earlier wins.
#[derive(
    Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash, Default, Serialize, Deserialize,
)]
#[serde(rename_all = "camelCase")]
pub enum MergeStatus {
    Draft,
    Conflicting,
    Failing,
    Behind,
    ChangesRequested,
    AwaitingApproval,
    Blocked,
    Pending,
    #[default]
    Unknown,
    Ready,
}

/// The review decision, as the forge reports it.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum ReviewState {
    #[default]
    NotRequired,
    Approved,
    Required,
    ChangesRequested,
}

/// How many of a pull request's checks passed, failed or are still pending.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Checks {
    pub total: u32,
    pub passed: u32,
    pub failed: u32,
    pub pending: u32,
}

/// What a forge says about a pull request's mergeability, before any reason is chosen.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct MergeSignals {
    pub draft: bool,
    /// `None`: the forge hasn't computed it yet.
    pub conflicting: Option<bool>,
    pub checks: Checks,
    /// The forge requires the branch to be updated from its base.
    pub behind: bool,
    pub review: ReviewState,
    /// GitHub's `mergeStateStatus` is `BLOCKED`.
    pub blocked: bool,
}

/// A pull request's merge status: the `merge` field in its JSON.
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct MergeInfo {
    pub status: MergeStatus,
    pub reasons: Vec<MergeStatus>,
    pub checks: Checks,
}

/// The merge status, every reason, and the check counts (ADR 0060).
pub fn merge_info(signals: &MergeSignals) -> MergeInfo {
    let mut reasons = Vec::new();
    if signals.draft {
        reasons.push(MergeStatus::Draft);
    }
    if signals.conflicting == Some(true) {
        reasons.push(MergeStatus::Conflicting);
    }
    if signals.checks.failed > 0 {
        reasons.push(MergeStatus::Failing);
    }
    if signals.behind {
        reasons.push(MergeStatus::Behind);
    }
    match signals.review {
        ReviewState::ChangesRequested => reasons.push(MergeStatus::ChangesRequested),
        ReviewState::Required => reasons.push(MergeStatus::AwaitingApproval),
        ReviewState::NotRequired | ReviewState::Approved => {}
    }
    if blocks_alone(signals) {
        reasons.push(MergeStatus::Blocked);
    }
    if signals.checks.pending > 0 {
        reasons.push(MergeStatus::Pending);
    }
    if signals.conflicting.is_none() {
        reasons.push(MergeStatus::Unknown);
    }
    let status = reasons.first().copied().unwrap_or(MergeStatus::Ready);
    MergeInfo {
        status,
        reasons,
        checks: signals.checks,
    }
}

/// A block is only worth reporting when no other reason explains it.
fn blocks_alone(signals: &MergeSignals) -> bool {
    signals.blocked
        && signals.checks.failed == 0
        && signals.checks.pending == 0
        && matches!(
            signals.review,
            ReviewState::NotRequired | ReviewState::Approved
        )
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn signals() -> MergeSignals {
        MergeSignals {
            conflicting: Some(false),
            ..MergeSignals::default()
        }
    }

    fn failing() -> Checks {
        Checks {
            total: 1,
            failed: 1,
            ..Checks::default()
        }
    }

    fn pending() -> Checks {
        Checks {
            total: 1,
            pending: 1,
            ..Checks::default()
        }
    }

    #[test]
    fn ready_when_nothing_applies() {
        let info = merge_info(&signals());
        assert_eq!(info.status, MergeStatus::Ready);
        assert!(info.reasons.is_empty());
    }

    #[test]
    fn unknown_when_mergeability_is_not_computed() {
        let info = merge_info(&MergeSignals {
            conflicting: None,
            ..signals()
        });
        assert_eq!(info.status, MergeStatus::Unknown);
    }

    #[test]
    fn draft_wins_over_conflicts_and_failures() {
        let info = merge_info(&MergeSignals {
            draft: true,
            conflicting: Some(true),
            checks: failing(),
            ..signals()
        });
        assert_eq!(info.status, MergeStatus::Draft);
        assert_eq!(
            info.reasons,
            [
                MergeStatus::Draft,
                MergeStatus::Conflicting,
                MergeStatus::Failing
            ]
        );
    }

    #[test]
    fn conflicting_wins_over_failing() {
        let info = merge_info(&MergeSignals {
            conflicting: Some(true),
            checks: failing(),
            ..signals()
        });
        assert_eq!(info.status, MergeStatus::Conflicting);
    }

    #[test]
    fn failing_wins_over_behind() {
        let info = merge_info(&MergeSignals {
            behind: true,
            checks: failing(),
            ..signals()
        });
        assert_eq!(info.status, MergeStatus::Failing);
    }

    #[test]
    fn behind_wins_over_awaiting_approval() {
        let info = merge_info(&MergeSignals {
            behind: true,
            review: ReviewState::Required,
            ..signals()
        });
        assert_eq!(info.status, MergeStatus::Behind);
    }

    #[test]
    fn changes_requested_wins_over_pending_checks() {
        let info = merge_info(&MergeSignals {
            review: ReviewState::ChangesRequested,
            checks: pending(),
            ..signals()
        });
        assert_eq!(info.status, MergeStatus::ChangesRequested);
        assert_eq!(
            info.reasons,
            [MergeStatus::ChangesRequested, MergeStatus::Pending]
        );
    }

    #[test]
    fn blocked_needs_no_other_explanation() {
        let info = merge_info(&MergeSignals {
            blocked: true,
            ..signals()
        });
        assert_eq!(info.reasons, [MergeStatus::Blocked]);
    }

    #[test]
    fn blocked_is_left_to_failing_checks() {
        let info = merge_info(&MergeSignals {
            blocked: true,
            checks: failing(),
            ..signals()
        });
        assert_eq!(info.reasons, [MergeStatus::Failing]);
    }

    #[test]
    fn blocked_is_left_to_pending_checks() {
        let info = merge_info(&MergeSignals {
            blocked: true,
            checks: pending(),
            ..signals()
        });
        assert_eq!(info.reasons, [MergeStatus::Pending]);
    }

    #[test]
    fn blocked_is_left_to_awaiting_approval() {
        let info = merge_info(&MergeSignals {
            blocked: true,
            review: ReviewState::Required,
            ..signals()
        });
        assert_eq!(info.reasons, [MergeStatus::AwaitingApproval]);
    }

    #[test]
    fn reasons_never_include_ready_and_are_in_precedence_order() {
        let info = merge_info(&MergeSignals {
            draft: true,
            behind: true,
            checks: pending(),
            conflicting: None,
            ..signals()
        });
        assert_eq!(
            info.reasons,
            [
                MergeStatus::Draft,
                MergeStatus::Behind,
                MergeStatus::Pending,
                MergeStatus::Unknown
            ]
        );
    }

    #[test]
    fn checks_are_copied() {
        let checks = Checks {
            total: 4,
            passed: 1,
            failed: 2,
            pending: 1,
        };
        let info = merge_info(&MergeSignals {
            checks,
            ..signals()
        });
        assert_eq!(info.checks, checks);
    }

    #[test]
    fn merge_info_serializes_camel_case() {
        let info = MergeInfo {
            status: MergeStatus::AwaitingApproval,
            reasons: vec![MergeStatus::AwaitingApproval],
            checks: Checks {
                total: 2,
                passed: 2,
                failed: 0,
                pending: 0,
            },
        };
        assert_eq!(
            serde_json::to_value(&info).unwrap(),
            json!({
                "status": "awaitingApproval",
                "reasons": ["awaitingApproval"],
                "checks": {"total": 2, "passed": 2, "failed": 0, "pending": 0}
            })
        );
    }
}
