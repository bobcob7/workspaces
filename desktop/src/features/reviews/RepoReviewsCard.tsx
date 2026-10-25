import { Button, Switch } from "@mui/material";
import type { JSX } from "react";
import { age } from "../../format";
import { useAppDispatch, useAppSelector } from "../../store";
import { sectionChosen } from "../ui/uiSlice";
import { repoName } from "./groups";
import { filterChanged, setWatch } from "./reviewsSlice";
import styles from "./RepoReviewsCard.module.css";

export interface RepoReviewsCardProps {
  readonly repoId: string;
}

/**
 * The Pull request reviews card in a repo's detail in the Repos section:
 * Watch and Auto-review switches, open count, last check, and Show in
 * Reviews. Renders nothing for a repo `gh` can't check.
 */
export const RepoReviewsCard = ({ repoId }: RepoReviewsCardProps): JSX.Element | null => {
  const dispatch = useAppDispatch();
  const repo = useAppSelector((s) => s.reviews.view?.repos.find((r) => r.id === repoId));
  const interval = useAppSelector((s) => s.settings.settings?.reviews.checkIntervalMinutes ?? 5);
  if (repo === undefined || !repo.github) {
    return null;
  }
  const status = [
    repo.open === null ? null : `${repo.open} open`,
    repo.checkedAtMs === null ? null : `checked ${age(new Date(repo.checkedAtMs).toISOString())}`,
  ].filter((part) => part !== null);
  const showInReviews = (): void => {
    dispatch(filterChanged(repoName(repoId)));
    dispatch(sectionChosen("reviews"));
  };
  return (
    <section className={styles.root} aria-label="Pull request reviews">
      <header className={styles.header}>
        <h2>Pull request reviews</h2>
        <span className={styles.spacer} />
        {repo.watch && status.length > 0 && <span className={styles.muted}>{status.join(" · ")}</span>}
        {repo.watch && (
          <Button size="small" onClick={showInReviews}>
            Show in Reviews
          </Button>
        )}
      </header>
      {repo.error !== null && repo.watch && <p className={styles.error}>{repo.error}</p>}
      <label className={styles.row}>
        <span className={styles.text}>
          <span className={styles.label}>Watch pull requests</span>
          <span className={styles.muted}>
            Check this repo's open PRs every {interval === 60 ? "hour" : `${interval} minute${interval === 1 ? "" : "s"}`} and
            list them in Reviews.
          </span>
        </span>
        <Switch
          checked={repo.watch}
          onChange={(e) => void dispatch(setWatch({ repo: repoId, watch: e.target.checked, autoReview: repo.autoReview }))}
        />
      </label>
      <label className={styles.row}>
        <span className={styles.text}>
          <span className={styles.label}>Review new pull requests automatically</span>
          <span className={styles.muted}>Start a review agent for each new PR that matches your review rules in Settings.</span>
        </span>
        <Switch
          checked={repo.autoReview}
          disabled={!repo.watch}
          onChange={(e) => void dispatch(setWatch({ repo: repoId, watch: repo.watch, autoReview: e.target.checked }))}
        />
      </label>
    </section>
  );
};
