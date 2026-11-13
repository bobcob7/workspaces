import { Button, MenuItem, TextField } from "@mui/material";
import type { JSX, KeyboardEvent } from "react";
import type { MergeStatus, PullRequest, ReviewReport, ReviewsView } from "../../api";
import { age } from "../../format";
import { useAppDispatch, useAppSelector } from "../../store";
import { activeFilterCount, authorsOf, groupPrs, mergeLabels, repoName, summary, visibleOrder, type Involves } from "./groups";
import { MergeBadge } from "./MergeBadge";
import { PrDot } from "./PrDot";
import { reportKey } from "./reportsSlice";
import { authorFiltered, dismissedToggled, filterChanged, filtersCleared, involvesFiltered, mergeFiltered, reviewSelected } from "./reviewsSlice";
import { useReports } from "./useReports";
import styles from "./ReviewsList.module.css";

const mergeStatuses = Object.keys(mergeLabels) as MergeStatus[];

const involvesOptions: readonly { readonly value: Involves; readonly label: string }[] = [
  { value: "anyone", label: "Anyone" },
  { value: "meAny", label: "Me: mine or requested" },
  { value: "mine", label: "Opened by me" },
  { value: "requested", label: "Review requested from me" },
];

const hostOf = (repo: string): string => repo.split("/")[0] ?? repo;

/** Hosts with pull requests that have no login, so "mine" and "requested" can't match them. */
const hostsWithoutLogin = (view: ReviewsView): readonly string[] => {
  const withPrs = new Set(view.prs.map((pr) => hostOf(pr.repo)));
  return view.hosts.filter((host) => host.login === null && withPrs.has(host.host)).map((host) => host.host);
};

/** Pull requests in watched repos, grouped by review status. */
export const ReviewsList = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const view = useAppSelector((s) => s.reviews.view);
  const filter = useAppSelector((s) => s.reviews.filter);
  const filters = useAppSelector((s) => s.reviews.filters);
  const selectedKey = useAppSelector((s) => s.reviews.selectedKey);
  const dismissedOpen = useAppSelector((s) => s.reviews.dismissedOpen);
  const reports = useAppSelector((s) => s.reports.byKey);
  useReports(view?.prs ?? []);
  const reportOf = (pr: PullRequest): ReviewReport | null => {
    const review = pr.reviews[0];
    return review?.recorded === true ? reports[reportKey(pr.repo, review.sha)]?.report ?? null : null;
  };
  const prs = view?.prs ?? [];
  const groups = groupPrs(prs, filter, filters);
  const loginless = view === null || filters.involves === "anyone" ? [] : hostsWithoutLogin(view);
  const failing = view?.repos.filter((r) => r.watch && r.forge !== null && r.error !== null) ?? [];
  const hostErrors = view?.hosts.flatMap((host) => (host.error === null ? [] : [`${host.host}: ${host.error.message}`])) ?? [];
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
      <div className={styles.filters}>
        <TextField
          select
          size="small"
          label="Author"
          value={filters.author ?? ""}
          onChange={(e) => dispatch(authorFiltered(e.target.value === "" ? null : e.target.value))}
        >
          <MenuItem value="">All authors</MenuItem>
          {authorsOf(prs).map((login) => (
            <MenuItem key={login} value={login}>
              {login}
            </MenuItem>
          ))}
        </TextField>
        <TextField
          select
          size="small"
          label="Involves"
          value={filters.involves}
          onChange={(e) => dispatch(involvesFiltered(e.target.value as Involves))}
        >
          {involvesOptions.map((option) => (
            <MenuItem key={option.value} value={option.value}>
              {option.label}
            </MenuItem>
          ))}
        </TextField>
        <TextField
          select
          size="small"
          label="Merge status"
          value={filters.merge ?? ""}
          onChange={(e) => dispatch(mergeFiltered(e.target.value === "" ? null : (e.target.value as MergeStatus)))}
        >
          <MenuItem value="">Any status</MenuItem>
          {mergeStatuses.map((status) => (
            <MenuItem key={status} value={status}>
              {mergeLabels[status]}
            </MenuItem>
          ))}
        </TextField>
        {activeFilterCount(filters) > 0 && (
          <Button size="small" onClick={() => dispatch(filtersCleared())}>
            Clear filters
          </Button>
        )}
      </div>
      <nav className={styles.groups} aria-label="Pull requests" onKeyDown={onKeyDown}>
        {loginless.length > 0 && (
          <p className={styles.warning} role="status">
            Mine and requested don&apos;t include {loginless.join(", ")}: no login there.
          </p>
        )}
        {[...hostErrors, ...failing.map((r) => `${repoName(r.id)}: ${r.error ?? ""}`), ...autoWarnings].map((warning) => (
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
                          <MergeBadge merge={pr.merge} compact />
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
