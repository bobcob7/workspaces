import type { PullRequest, ReviewGroup, ReviewsView } from "../../api";

export interface PrGroup {
  readonly id: ReviewGroup;
  readonly title: string;
  readonly prs: readonly PullRequest[];
}

const groupOrder: readonly ReviewGroup[] = ["toReview", "reviewing", "reviewed", "dismissed"];

export const groupTitles: Readonly<Record<ReviewGroup, string>> = {
  toReview: "To review",
  reviewing: "Reviewing",
  reviewed: "Reviewed",
  dismissed: "Dismissed",
};

/** The status a pull request's dot and label show. */
export type PrStatus = ReviewGroup | "outOfDate";

export const prStatus = (pr: PullRequest): PrStatus =>
  pr.group === "reviewed" && pr.outOfDate ? "outOfDate" : pr.group;

/** `buff-bear` from `github.com/bobcob7/buff-bear`. */
export const repoName = (repo: string): string => repo.split("/").at(-1) ?? repo;

/** `bobcob7/buff-bear` from `github.com/bobcob7/buff-bear`. */
export const ownerRepo = (repo: string): string => repo.split("/").slice(1).join("/");

export const shortSha = (sha: string): string => sha.slice(0, 7);

/** Matches number (`42` or `#42`), title, author, and repo, case-insensitively. */
export const matchesFilter = (pr: PullRequest, filter: string): boolean => {
  const query = filter.trim().toLowerCase();
  if (query === "") {
    return true;
  }
  if (/^#?\d+$/.test(query)) {
    return String(pr.number) === query.replace("#", "");
  }
  return [pr.title, pr.author.login, pr.repo].some((field) => field.toLowerCase().includes(query));
};

/** Pull requests by group, in group order, newest first within each. */
export const groupPrs = (prs: readonly PullRequest[], filter: string): readonly PrGroup[] =>
  groupOrder
    .map((id) => ({
      id,
      title: groupTitles[id],
      prs: prs
        .filter((pr) => pr.group === id && matchesFilter(pr, filter))
        .toSorted((a, b) => b.updatedAt.localeCompare(a.updatedAt) || a.key.localeCompare(b.key)),
    }))
    .filter((g) => g.prs.length > 0);

const plural = (count: number, word: string): string => `${count} ${word}${count === 1 ? "" : "s"}`;

const firstLine = (text: string): string => text.split("\n").find((line) => line.trim() !== "")?.trim() ?? "";

/** The part of a row's summary after repo and author. */
export const summaryExtra = (pr: PullRequest): string | null => {
  const review = pr.reviews[0];
  switch (pr.group) {
    case "toReview":
      if (pr.requested) {
        return "review requested";
      }
      return pr.isDraft ? "draft" : null;
    case "reviewing":
      return review?.status === "needsInput" ? "needs input" : "agent working";
    case "reviewed":
      if (pr.outOfDate) {
        return pr.newCommits === null ? "new commits" : plural(pr.newCommits.count, "new commit");
      }
      return review !== undefined && review.result !== null ? firstLine(review.result) : "reviewed";
    case "dismissed":
      return pr.isDraft ? "draft" : null;
  }
};

export const summary = (pr: PullRequest): string =>
  [repoName(pr.repo), pr.author.login, summaryExtra(pr)].filter((part) => part !== null && part !== "").join(" · ");

/** The rail badge: pull requests to review, plus reviews out of date. */
export const badgeCount = (view: ReviewsView | null): number =>
  view?.prs.filter((pr) => pr.group === "toReview" || prStatus(pr) === "outOfDate").length ?? 0;

export const watchedCount = (view: ReviewsView | null): number => view?.repos.filter((r) => r.watch && r.github).length ?? 0;

/** The order rows appear in the list, for ↑ and ↓. */
export const visibleOrder = (groups: readonly PrGroup[], dismissedOpen: boolean): readonly string[] =>
  groups.filter((g) => g.id !== "dismissed" || dismissedOpen).flatMap((g) => g.prs.map((pr) => pr.key));

/** `ws claude …` as the Review will run card shows it, shortened. */
export const commandPreview = (plan: {
  readonly name: string;
  readonly spec: string;
  readonly prompt: string;
  readonly model: string;
  readonly permissionMode: string;
}): string => {
  const [repo, sha = ""] = plan.spec.split("@");
  const options = [
    plan.model === "" ? null : `--model ${plan.model}`,
    plan.permissionMode === "" ? null : `--permission-mode ${plan.permissionMode}`,
  ].filter((o) => o !== null);
  const prompt = firstLine(plan.prompt);
  const shortPrompt = prompt.length > 48 ? `${prompt.slice(0, 48)}…` : prompt;
  return [
    "ws claude --name",
    plan.name,
    `${repo ?? ""}@${sha.slice(0, 8)}…`,
    "--",
    ...options,
    `"${shortPrompt}"`,
  ].join(" ");
};
