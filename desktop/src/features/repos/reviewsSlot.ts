import type { ComponentType } from "react";

export interface RepoIdProps {
  readonly repoId: string;
}

export interface AddRepoWatchProps {
  /** The resolved repo, or null while the spec doesn't resolve. */
  readonly repoId: string | null;
  readonly checked: boolean;
  readonly onChange: (checked: boolean) => void;
}

/**
 * What the Reviews section puts into Repos once it's built
 * (docs/design/desktop-sections/repos.md, "Slots for Reviews"). Any module
 * in features/reviews/ may export these by name; Repos renders each one it
 * finds, and nothing in its place until then.
 */
export interface ReviewsSlot {
  /** The Pull request reviews card at the top of a repo's detail. */
  readonly RepoReviewsCard: ComponentType<RepoIdProps>;
  /** Icons after a repo's name in the list: watched, auto-review. */
  readonly RepoReviewsIcons: ComponentType<RepoIdProps>;
  /** The Add repo dialog's Watch switch. Its value goes out as addRepo's `watch`. */
  readonly AddRepoWatchSwitch: ComponentType<AddRepoWatchProps>;
  /** A sentence for the Remove dialog about what Reviews stops doing. */
  readonly RemoveRepoReviewsNote: ComponentType<RepoIdProps>;
}

const modules = import.meta.glob<Partial<ReviewsSlot>>(["../reviews/*.tsx", "!../reviews/*.test.tsx"], {
  eager: true,
});

/** The Reviews exports found, merged; empty until Reviews ships. */
export const reviewsSlot: Partial<ReviewsSlot> = Object.values(modules).reduce<Partial<ReviewsSlot>>(
  (found, module) => ({
    RepoReviewsCard: found.RepoReviewsCard ?? module.RepoReviewsCard,
    RepoReviewsIcons: found.RepoReviewsIcons ?? module.RepoReviewsIcons,
    AddRepoWatchSwitch: found.AddRepoWatchSwitch ?? module.AddRepoWatchSwitch,
    RemoveRepoReviewsNote: found.RemoveRepoReviewsNote ?? module.RemoveRepoReviewsNote,
  }),
  {},
);
