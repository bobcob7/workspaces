import { FormControlLabel, Switch } from "@mui/material";
import type { JSX } from "react";
import type { ReviewsView } from "../../api";
import { useAppSelector } from "../../store";
import styles from "./RepoSlots.module.css";

/**
 * Whether gh can check a repo: one the last check marked as GitHub, or, for
 * a repo not cached yet, one on github.com (ADR 0035).
 */
export const isGitHubRepo = (repoId: string, view: ReviewsView | null): boolean => {
  const known = view?.repos.find((r) => r.id === repoId);
  if (known !== undefined) {
    return known.github;
  }
  const parts = repoId.split("/");
  return parts.length === 3 && (parts[0] === "github.com" || (view?.repos.some((r) => r.github && r.id.startsWith(`${parts[0] ?? ""}/`)) ?? false));
};

interface RepoIdProps {
  readonly repoId: string;
}

/** The eye (watched) and bolt (auto-review) after a repo's name in Repos. */
export const RepoReviewsIcons = ({ repoId }: RepoIdProps): JSX.Element | null => {
  const repo = useAppSelector((s) => s.reviews.view?.repos.find((r) => r.id === repoId));
  if (repo === undefined || !repo.watch) {
    return null;
  }
  return (
    <span className={styles.icons}>
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" role="img" aria-label="Watched in Reviews">
        <path d="M2 12s3.6-7 10-7 10 7 10 7-3.6 7-10 7S2 12 2 12Z" />
        <circle cx="12" cy="12" r="3" />
      </svg>
      {repo.autoReview && (
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" role="img" aria-label="Auto-review on">
          <path d="M13 2 4 14h7l-1 8 9-12h-7l1-8Z" />
        </svg>
      )}
    </span>
  );
};

interface AddRepoWatchProps {
  readonly repoId: string | null;
  readonly checked: boolean;
  readonly onChange: (checked: boolean) => void;
}

/** The Add repo dialog's Watch switch, shown once the spec resolves to a GitHub repo. */
export const AddRepoWatchSwitch = ({ repoId, checked, onChange }: AddRepoWatchProps): JSX.Element | null => {
  const view = useAppSelector((s) => s.reviews.view);
  if (repoId === null || !isGitHubRepo(repoId, view)) {
    return null;
  }
  return (
    <FormControlLabel
      control={<Switch checked={checked} onChange={(e) => onChange(e.target.checked)} />}
      label="Watch pull requests in Reviews"
    />
  );
};

/** What removing a repo means for Reviews, for the Remove dialog. */
export const RemoveRepoReviewsNote = ({ repoId }: RepoIdProps): JSX.Element | null => {
  const watched = useAppSelector((s) => s.reviews.view?.repos.find((r) => r.id === repoId)?.watch === true);
  if (!watched) {
    return null;
  }
  return (
    <>
      Its pull requests stop being checked and leave Reviews. Review workspaces stay in Agents until you remove them.
    </>
  );
};
