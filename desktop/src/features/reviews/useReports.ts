import { useEffect, useRef } from "react";
import type { PullRequest } from "../../api";
import { useAppDispatch, useAppStore } from "../../store";
import { isReviewing } from "./groups";
import { fetchReport, reportKey } from "./reportsSlice";

/** How often a working review's report is read again (ADR 0040). */
export const reportInterval = 30_000;

interface Newest {
  readonly repo: string;
  readonly ref: string;
  readonly working: boolean;
}

const newestReviews = (prs: readonly PullRequest[]): readonly Newest[] =>
  prs.flatMap((pr) => {
    const review = pr.reviews[0];
    return review?.recorded === true ? [{ repo: pr.repo, ref: review.sha, working: isReviewing(review.status) }] : [];
  });

/**
 * Keeps the newest review's report of each pull request: read once from the
 * session's cache, again every 30 s while its agent works, and once more when
 * the agent goes idle.
 */
export const useReports = (prs: readonly PullRequest[]): void => {
  const dispatch = useAppDispatch();
  const store = useAppStore();
  const wasWorking = useRef(new Map<string, boolean>());
  const newest = newestReviews(prs);
  const signature = newest.map((n) => `${reportKey(n.repo, n.ref)}:${String(n.working)}`).join(",");

  useEffect(() => {
    const loaded = store.getState().reports.byKey;
    for (const n of newest) {
      const key = reportKey(n.repo, n.ref);
      const stopped = wasWorking.current.get(key) === true && !n.working;
      if (loaded[key] === undefined || stopped) {
        void dispatch(fetchReport({ repo: n.repo, ref: n.ref, refresh: stopped }));
      }
      wasWorking.current.set(key, n.working);
    }
  }, [dispatch, store, signature]);

  useEffect(() => {
    const working = newest.filter((n) => n.working);
    if (working.length === 0) {
      return undefined;
    }
    const timer = window.setInterval(() => {
      for (const n of working) {
        void dispatch(fetchReport({ repo: n.repo, ref: n.ref, refresh: true }));
      }
    }, reportInterval);
    return () => window.clearInterval(timer);
  }, [dispatch, signature]);
};
