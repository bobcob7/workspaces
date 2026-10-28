import { Alert, Button, CircularProgress, IconButton, Menu, MenuItem } from "@mui/material";
import { useEffect, useMemo, useState, type JSX, type KeyboardEvent } from "react";
import type { DecisionKind, PullRequest, ReportComment, ReportProfile, ReviewReport, ReviewRun } from "../../api";
import { useAppDispatch, useAppSelector } from "../../store";
import { conversationClosed, fetchConversation } from "../conversation/conversationSlice";
import { sectionChosen, selected } from "../ui/uiSlice";
import { FindingRow } from "./FindingRow";
import {
  bandCount,
  decidedEarlier,
  groupFindings,
  keyAction,
  profilesIn,
  severityText,
  tally,
  tallyParts,
  verdictLabels,
  visibleFindings,
  type Finding,
  type ReportFilter,
} from "./report";
import {
  decideFindings,
  defaultTriage,
  fetchReport,
  filterChanged,
  findingFocused,
  findingToggled,
  noteDrafted,
  openFullReport,
  optionalToggled,
  reportKey,
  selectionCleared,
  selectionToggled,
} from "./reportsSlice";
import { startReview } from "./reviewsSlice";
import styles from "./Report.module.css";

const plural = (count: number, word: string): string => `${count} ${word}${count === 1 ? "" : "s"}`;

/** The review agent's last reply, quoted when a profile never submitted. */
const useLastReply = (sessionId: string | null, wanted: boolean): string | null => {
  const dispatch = useAppDispatch();
  const conversation = useAppSelector((s) => (sessionId === null ? undefined : s.conversations.bySession[sessionId]));
  useEffect(() => {
    if (sessionId === null || !wanted) {
      return undefined;
    }
    void dispatch(fetchConversation(sessionId));
    return () => {
      dispatch(conversationClosed(sessionId));
    };
  }, [dispatch, sessionId, wanted]);
  const reply = conversation?.items.findLast((item) => item.kind === "assistant");
  return reply !== undefined && "text" in reply ? reply.text.trim() : null;
};

interface TileProps {
  readonly profile: ReportProfile;
  readonly working: boolean;
  readonly on: boolean;
  readonly onClick: () => void;
}

const ProfileTile = ({ profile, working, on, onClick }: TileProps): JSX.Element => {
  const state = profile.missing ? (working ? "pending" : "missing") : "in";
  return (
    <button type="button" className={styles.profile} data-state={state} aria-pressed={on} onClick={onClick}>
      {state === "pending" && <span className={styles.pendingDot} aria-hidden="true" />}
      <b>{profile.name}</b>
      {profile.model !== "" && <span className={styles.modelChip}>{profile.model}</span>}
      {state === "in" && profile.verdict !== null && (
        <span className={styles.verdict} data-verdict={profile.verdict}>
          {verdictLabels[profile.verdict]}
        </span>
      )}
      <span className={styles.muted}>{state === "in" ? severityText(profile) : state === "pending" ? "not in yet" : "missing"}</span>
    </button>
  );
};

interface FilterChipProps {
  readonly label: string;
  readonly on: boolean;
  readonly onClick: () => void;
}

const FilterChip = ({ label, on, onClick }: FilterChipProps): JSX.Element => (
  <button type="button" className={styles.filterChip} aria-pressed={on} onClick={onClick}>
    {label}
  </button>
);

interface FiltersProps {
  readonly report: ReviewReport;
  readonly filter: ReportFilter;
  readonly onChange: (change: Partial<ReportFilter>) => void;
}

/** Profile chips, category chips, Hide decided, and Show superseded in the overflow. */
const Filters = ({ report, filter, onChange }: FiltersProps): JSX.Element => {
  const [menu, setMenu] = useState<HTMLElement | null>(null);
  const profiles = [...new Set(report.comments.map((c) => c.profile))];
  const categories = [...new Set(report.comments.map((c) => c.category))].toSorted();
  return (
    <div className={styles.filters} role="toolbar" aria-label="Filters">
      <FilterChip label="All profiles" on={filter.profile === null} onClick={() => onChange({ profile: null })} />
      {profiles.map((p) => (
        <FilterChip key={p} label={p} on={filter.profile === p} onClick={() => onChange({ profile: filter.profile === p ? null : p })} />
      ))}
      <span className={styles.gap} />
      <FilterChip label="Any category" on={filter.category === null} onClick={() => onChange({ category: null })} />
      {categories.map((c) => (
        <FilterChip key={c} label={c} on={filter.category === c} onClick={() => onChange({ category: filter.category === c ? null : c })} />
      ))}
      <span className={styles.spacer} />
      <FilterChip label="Hide decided" on={filter.hideDecided} onClick={() => onChange({ hideDecided: !filter.hideDecided })} />
      {report.superseded.length > 0 && (
        <>
          <IconButton size="small" aria-label="More filters" onClick={(e) => setMenu(e.currentTarget)}>
            ⋯
          </IconButton>
          <Menu anchorEl={menu} open={menu !== null} onClose={() => setMenu(null)}>
            <MenuItem
              selected={filter.showSuperseded}
              onClick={() => {
                setMenu(null);
                onChange({ showSuperseded: !filter.showSuperseded });
              }}
            >
              {filter.showSuperseded ? "✓ " : ""}Show superseded
            </MenuItem>
          </Menu>
        </>
      )}
    </div>
  );
};

export interface ReportViewProps {
  readonly pr: PullRequest;
  /** The review shown; the newest unless an older one was picked. */
  readonly review: ReviewRun;
  /** Whether its agent is still working. */
  readonly working: boolean;
}

/** The Findings tab: profiles, filters, findings by band, and the action bar (report.md). */
export const ReportView = ({ pr, review, working }: ReportViewProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const key = reportKey(pr.repo, review.sha);
  const entry = useAppSelector((s) => s.reports.byKey[key]);
  const triage = useAppSelector((s) => s.reports.triage[pr.key]) ?? defaultTriage;
  const deciding = useAppSelector((s) => s.reports.deciding);
  const older = pr.reviews.slice(pr.reviews.indexOf(review) + 1).filter((r) => r.recorded);
  const byKey = useAppSelector((s) => s.reports.byKey);
  const olderReports = older.map((r) => byKey[reportKey(pr.repo, r.sha)]?.report ?? null);
  const report = entry?.report ?? null;

  useEffect(() => {
    for (const r of older) {
      void dispatch(fetchReport({ repo: pr.repo, ref: r.sha, refresh: false }));
    }
  }, [dispatch, pr.repo, older.map((r) => r.sha).join(",")]);

  const missing = report?.profiles.filter((p) => p.missing) ?? [];
  const lastReply = useLastReply(review.sessionId, !working && missing.length > 0);
  const groups = useMemo(() => (report === null ? [] : groupFindings(report, triage.filter)), [report, triage.filter]);
  const order = visibleFindings(groups, triage.optionalOpen);
  const earlier = decidedEarlier(olderReports.filter((r) => r !== null));

  if (report === null) {
    if (entry?.error != null) {
      return (
        <Alert severity="error" className={styles.alert}>
          <code>{entry.error.code}</code>: {entry.error.message}
        </Alert>
      );
    }
    return (
      <div className={styles.loading}>
        <CircularProgress size={18} aria-label="Loading the report" />
      </div>
    );
  }

  const openAgent = (id: string): void => {
    dispatch(sectionChosen("agents"));
    dispatch(selected(id));
  };
  const reviewAgain = (): void => void dispatch(startReview({ pr }));
  const setFilter = (change: Partial<ReportFilter>): void => void dispatch(filterChanged({ pr: pr.key, value: change }));
  const busy = (c: ReportComment): boolean => deciding.includes(c.id);
  const noteOf = (c: ReportComment): string => triage.notes[c.id] ?? c.decision?.note ?? "";

  /** The findings a decision on `finding` applies to: the selection, when it's in it. */
  const targets = (finding: Finding): readonly ReportComment[] => {
    if (!triage.selected.includes(finding.comment.id)) {
      return [finding.comment];
    }
    return order
      .filter((f) => !f.readOnly && f.comment.decision?.done == null && triage.selected.includes(f.comment.id))
      .map((f) => f.comment);
  };
  const decide = (finding: Finding, decision: DecisionKind | "clear", suggestion?: number): void => {
    if (finding.readOnly) {
      return;
    }
    const comments = suggestion === undefined ? targets(finding) : [finding.comment];
    void dispatch(
      decideFindings({
        repo: pr.repo,
        ref: review.sha,
        findings: comments.map((c) => ({ id: c.id, note: noteOf(c) })),
        decision,
        suggestion: suggestion ?? null,
      }),
    );
  };
  const saveNote = (comment: ReportComment, text: string): void => {
    if (comment.decision === null) {
      dispatch(noteDrafted({ pr: pr.key, value: { id: comment.id, text } }));
      return;
    }
    void dispatch(
      decideFindings({
        repo: pr.repo,
        ref: review.sha,
        findings: [{ id: comment.id, note: text }],
        decision: comment.decision.decision,
        suggestion: comment.decision.suggestion,
      }),
    );
  };

  const onKeyDown = (event: KeyboardEvent): void => {
    if (event.target instanceof HTMLElement && event.target.closest("input, textarea, [role=menu]") !== null) {
      return;
    }
    if (event.metaKey || event.ctrlKey || event.altKey) {
      return;
    }
    if (event.key === "Escape" && triage.selected.length > 0) {
      dispatch(selectionCleared(pr.key));
      return;
    }
    const action = keyAction(event.key);
    if (action === null || order.length === 0) {
      return;
    }
    const at = Math.max(
      order.findIndex((f) => f.comment.id === triage.focused),
      0,
    );
    const focused = order[at];
    if (action.kind === "move") {
      event.preventDefault();
      const next = order[Math.min(Math.max(at + action.by, 0), order.length - 1)];
      if (next !== undefined) {
        dispatch(findingFocused({ pr: pr.key, value: next.comment.id }));
        document.getElementById(`toggle-${next.comment.id}`)?.focus();
      }
      return;
    }
    if (focused === undefined) {
      return;
    }
    event.preventDefault();
    const current = focused.comment.decision;
    const actionable = !focused.readOnly && current?.done == null;
    switch (action.kind) {
      case "toggle":
        dispatch(findingToggled({ pr: pr.key, value: focused.comment.id }));
        return;
      case "decide":
        if (actionable) {
          decide(focused, action.decision);
        }
        return;
      case "clear":
        if (actionable && current !== null) {
          decide(focused, "clear");
        }
        return;
      case "suggest":
        if (actionable && triage.expanded.includes(focused.comment.id) && action.index < focused.comment.suggestions.length) {
          decide(focused, "fix", action.index);
        }
        return;
    }
  };

  const { submitted, total } = profilesIn(report);
  const finishedEmpty = !working && submitted === 0;
  const counts = tally(report, groups, triage.optionalOpen);
  const parts = tallyParts(counts);
  const toComment = counts.comment;
  const toFix = counts.fix;

  return (
    <div className={styles.root}>
      <div className={styles.scroll}>
        {working && total > 0 && (
          <Alert severity="info" className={styles.alert}>
            {submitted} of {plural(total, "profile")} {submitted === 1 ? "has" : "have"} submitted. Findings appear as each one
            does; you can start deciding now.
          </Alert>
        )}
        {finishedEmpty ? (
          <Alert
            severity="warning"
            className={styles.alert}
            action={
              <>
                <Button size="small" onClick={() => openAgent(review.workspaceId)}>
                  Open agent
                </Button>
                <Button size="small" onClick={reviewAgain}>
                  Review again
                </Button>
              </>
            }
          >
            The review agent finished without submitting a review.
          </Alert>
        ) : (
          !working &&
          missing.length > 0 && (
            <Alert severity="warning" className={styles.alert}>
              <b>{missing.map((p) => p.name).join(", ")}</b> never submitted: the review agent finished without{" "}
              {missing.length === 1 ? "it" : "them"}.{lastReply !== null && ` Its last message: "${lastReply}".`}{" "}
              <button type="button" className={styles.linkButton} onClick={reviewAgain}>
                Review again
              </button>{" "}
              or{" "}
              <button type="button" className={styles.linkButton} onClick={() => openAgent(review.workspaceId)}>
                Open agent
              </button>
              .
            </Alert>
          )
        )}
        {report.profiles.length > 0 && (
          <div className={styles.profiles} aria-label="Profiles">
            {report.profiles.map((p) => (
              <ProfileTile
                key={p.name}
                profile={p}
                working={working}
                on={triage.filter.profile === p.name}
                onClick={() => setFilter({ profile: triage.filter.profile === p.name ? null : p.name })}
              />
            ))}
          </div>
        )}
        {!finishedEmpty && report.comments.length === 0 && report.superseded.length === 0 && !working && (
          <Alert severity="success" className={styles.alert}>
            No findings from {plural(submitted, "profile")}.
          </Alert>
        )}
        {(report.comments.length > 0 || report.superseded.length > 0) && (
          <Filters report={report} filter={triage.filter} onChange={setFilter} />
        )}
        <div className={styles.findings} aria-label="Findings" onKeyDown={onKeyDown}>
          {groups.map((group) => {
            const collapsed = group.band.id === "optional" && !triage.optionalOpen;
            const heading = (
              <>
                <span className={styles.bandBar} data-band={group.band.id} />
                {group.band.title}
                <span className={styles.bandCount}>{bandCount(group, triage.filter)}</span>
              </>
            );
            return (
              <section key={group.band.id} aria-label={group.band.title}>
                {group.band.id === "optional" ? (
                  <button
                    type="button"
                    className={`${styles.band} ${styles.bandToggle}`}
                    aria-expanded={!collapsed}
                    onClick={() => dispatch(optionalToggled(pr.key))}
                  >
                    {heading}
                    <span aria-hidden="true">{collapsed ? "▸" : "▾"}</span>
                  </button>
                ) : (
                  <h3 className={styles.band}>{heading}</h3>
                )}
                {!collapsed &&
                  group.findings.map((f) => (
                    <FindingRow
                      key={f.comment.id}
                      finding={f}
                      repo={pr.repo}
                      gitRef={review.sha}
                      expanded={triage.expanded.includes(f.comment.id)}
                      focused={triage.focused === f.comment.id || (triage.focused === null && order[0] === f)}
                      selected={triage.selected.includes(f.comment.id)}
                      busy={busy(f.comment)}
                      decidedAt={f.comment.decision === null ? earlier.get(f.comment.id) : undefined}
                      draft={triage.notes[f.comment.id]}
                      onToggle={() => dispatch(findingToggled({ pr: pr.key, value: f.comment.id }))}
                      onFocus={() => {
                        if (triage.focused !== f.comment.id) {
                          dispatch(findingFocused({ pr: pr.key, value: f.comment.id }));
                        }
                      }}
                      onSelect={() => dispatch(selectionToggled({ pr: pr.key, value: f.comment.id }))}
                      onDecide={(d, s) => decide(f, d, s)}
                      onNote={(text) => saveNote(f.comment, text)}
                      onOpenAgent={openAgent}
                    />
                  ))}
              </section>
            );
          })}
        </div>
      </div>
      <footer className={styles.actionBar} aria-label="Actions">
        <span className={styles.tally} aria-label="Tally">
          {parts.map((part) => (
            <span key={part.key} data-kind={part.key}>
              <b>{part.count}</b> {part.label}
            </span>
          ))}
        </span>
        <span className={styles.spacer} />
        <Button size="small" onClick={() => void dispatch(openFullReport({ repo: pr.repo, ref: review.sha }))}>
          Open full report
        </Button>
        {/* Comment and Fix act on the decisions; they arrive with ADR 0041's step. */}
        <Button size="small" variant="outlined" disabled>
          {toComment > 0 && !working ? `Comment on ${toComment}…` : "Comment…"}
        </Button>
        <Button size="small" variant="contained" disabled>
          {toFix > 0 && !working ? `Fix ${toFix}…` : "Fix…"}
        </Button>
      </footer>
    </div>
  );
};
