import type { JSX, KeyboardEvent } from "react";
import type { PullRequest, ReviewReport } from "../../api";
import { age } from "../../format";
import { useAppDispatch, useAppSelector } from "../../store";
import { groupPrs, repoName, summary, visibleOrder } from "./groups";
import { PrDot } from "./PrDot";
import { reportKey } from "./reportsSlice";
import { dismissedToggled, filterChanged, reviewSelected } from "./reviewsSlice";
import { useReports } from "./useReports";
import styles from "./ReviewsList.module.css";

/** Pull requests in watched repos, grouped by review status. */
export const ReviewsList = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const view = useAppSelector((s) => s.reviews.view);
  const filter = useAppSelector((s) => s.reviews.filter);
  const selectedKey = useAppSelector((s) => s.reviews.selectedKey);
  const dismissedOpen = useAppSelector((s) => s.reviews.dismissedOpen);
  const reports = useAppSelector((s) => s.reports.byKey);
  useReports(view?.prs ?? []);
  const reportOf = (pr: PullRequest): ReviewReport | null => {
    const review = pr.reviews[0];
    return review?.recorded === true ? reports[reportKey(pr.repo, review.sha)]?.report ?? null : null;
  };
  const groups = groupPrs(view?.prs ?? [], filter);
  const failing = view?.repos.filter((r) => r.watch && r.github && r.error !== null) ?? [];
  const autoWarnings = view?.warnings.filter((w) => w.startsWith("auto-review")) ?? [];
  const onKeyDown = (event: KeyboardEvent): void => {
    if (event.key !== "ArrowDown" && event.key !== "ArrowUp") {
      return;
    }
    const order = visibleOrder(groups, dismissedOpen);
    if (order.length === 0) {
      return;
    }
    event.preventDefault();
    const at = selectedKey === null ? -1 : order.indexOf(selectedKey);
    const next = event.key === "ArrowDown" ? Math.min(at + 1, order.length - 1) : Math.max(at - 1, 0);
    const key = order[next] ?? null;
    dispatch(reviewSelected(key));
    document.getElementById(`pr-${key ?? ""}`)?.focus();
  };
  return (
    <div className={styles.root}>
      <div className={styles.search}>
        <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true">
          <circle cx="11" cy="11" r="7" />
          <path d="m20 20-3.5-3.5" />
        </svg>
        <input
          type="search"
          placeholder="Filter pull requests"
          aria-label="Filter pull requests"
          value={filter}
          onChange={(e) => dispatch(filterChanged(e.target.value))}
        />
      </div>
      <nav className={styles.groups} aria-label="Pull requests" onKeyDown={onKeyDown}>
        {[...failing.map((r) => `${repoName(r.id)}: ${r.error ?? ""}`), ...autoWarnings].map((warning) => (
          <p key={warning} className={styles.warning} role="status">
            {warning}
          </p>
        ))}
        {groups.length === 0 && <p className={styles.empty}>No pull requests to show.</p>}
        {groups.map((group) => {
          const collapsed = group.id === "dismissed" && !dismissedOpen;
          return (
            <section key={group.id} className={styles.group} aria-label={group.title}>
              {group.id === "dismissed" ? (
                <button
                  type="button"
                  className={`${styles.heading} ${styles.toggle}`}
                  aria-expanded={!collapsed}
                  onClick={() => dispatch(dismissedToggled())}
                >
                  {group.title}
                  <span className={styles.count}>{group.prs.length}</span>
                  <span className={styles.chevron} aria-hidden="true">
                    {collapsed ? "▸" : "▾"}
                  </span>
                </button>
              ) : (
                <h2 className={styles.heading}>
                  {group.title}
                  <span className={styles.count}>{group.prs.length}</span>
                </h2>
              )}
              {!collapsed && (
                <ul className={styles.list}>
                  {group.prs.map((pr) => (
                    <li key={pr.key}>
                      <button
                        type="button"
                        id={`pr-${pr.key}`}
                        className={styles.item}
                        aria-current={pr.key === selectedKey ? "true" : undefined}
                        onClick={() => dispatch(reviewSelected(pr.key))}
                      >
                        <span className={styles.row}>
                          <PrDot pr={pr} />
                          <span className={styles.name}>
                            #{pr.number} {pr.title}
                          </span>
                          <span className={styles.age}>{age(pr.updatedAt)}</span>
                        </span>
                        <span className={styles.summary}>{summary(pr, reportOf(pr))}</span>
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </section>
          );
        })}
      </nav>
    </div>
  );
};
