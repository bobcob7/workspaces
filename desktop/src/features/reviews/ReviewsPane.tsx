import { Alert, Button, Chip, CircularProgress } from "@mui/material";
import { useEffect, type JSX } from "react";
import { api, type AppError, type PullRequest } from "../../api";
import { age } from "../../format";
import { useAppDispatch, useAppSelector } from "../../store";
import { MarkdownText } from "../conversation/ConversationView";
import { commandPreview, ownerRepo, shortSha, watchedCount } from "./groups";
import { PrDot } from "./PrDot";
import { ReviewCard } from "./ReviewCard";
import { dismissPr, fetchPlan, openStartDialog, restorePr, startReview, watchedOpened } from "./reviewsSlice";
import styles from "./ReviewsPane.module.css";

const commands = /(brew install gh|gh auth login)/;

/** `gh` missing or signed out, with the command to run shown as code. */
export const GhAlert = ({ error }: { readonly error: AppError }): JSX.Element => (
  <Alert severity="warning" className={styles.ghAlert}>
    {error.message.split(commands).map((part, i) => (commands.test(part) ? <code key={i}>{part}</code> : part))}
  </Alert>
);

const plural = (count: number, word: string): string => `${count} ${word}${count === 1 ? "" : "s"}`;

const quoteList = (subjects: readonly string[]): string => {
  const quoted = subjects.map((s) => `"${s}"`);
  if (quoted.length <= 1) {
    return quoted.join("");
  }
  return `${quoted.slice(0, -1).join(", ")} and ${quoted.at(-1) ?? ""}`;
};

const ReviewWillRun = ({ pr }: { readonly pr: PullRequest }): JSX.Element => {
  const dispatch = useAppDispatch();
  const plan = useAppSelector((s) => s.reviews.plans[pr.key]);
  useEffect(() => {
    void dispatch(fetchPlan(pr));
  }, [pr.key, pr.headRefOid]);
  return (
    <section className={styles.card} aria-label="Review will run">
      <header className={styles.cardHeader}>
        <h2>Review will run</h2>
        <span className={styles.spacer} />
        <Button size="small" onClick={() => void dispatch(openStartDialog(pr))}>
          Edit before starting
        </Button>
      </header>
      <div className={styles.cardBody}>
        <pre className={styles.command}>{plan === undefined ? "…" : commandPreview(plan)}</pre>
        <p className={styles.caption}>
          A new workspace with the PR's head commit. The agent reports its findings here and in Agents; it doesn't
          comment on GitHub unless your prompt asks it to.
        </p>
      </div>
    </section>
  );
};

const PrHeader = ({ pr }: { readonly pr: PullRequest }): JSX.Element => {
  const dispatch = useAppDispatch();
  const busy = useAppSelector((s) => s.reviews.busy.includes(pr.key));
  const reviewedAtHead = pr.group === "reviewed" && !pr.outOfDate;
  const primary = pr.group === "reviewing" || reviewedAtHead ? null : pr.outOfDate ? "Review again" : "Review";
  const updatedLater = age(pr.updatedAt) !== age(pr.createdAt);
  return (
    <header className={styles.header}>
      <div className={styles.titleRow}>
        <h1 className={styles.title}>{pr.title}</h1>
        <PrDot pr={pr} withLabel />
        {pr.outOfDate && pr.group === "reviewed" && (
          <span className={styles.warnChip}>
            {pr.newCommits === null ? "new commits since review" : `${plural(pr.newCommits.count, "new commit")} since review`}
          </span>
        )}
        <span className={styles.spacer} />
        <Button size="small" onClick={() => void api.openExternal(pr.url)}>
          Open on GitHub
        </Button>
        {pr.group === "dismissed" ? (
          <Button size="small" onClick={() => void dispatch(restorePr(pr))}>
            Restore
          </Button>
        ) : (
          <Button size="small" onClick={() => void dispatch(dismissPr(pr))}>
            Dismiss
          </Button>
        )}
        {primary !== null && (
          <Button size="small" variant="contained" disabled={busy} onClick={() => void dispatch(startReview({ pr }))}>
            {busy ? "Starting…" : primary}
          </Button>
        )}
      </div>
      <div className={styles.meta}>
        <Chip
          size="small"
          color="primary"
          variant="outlined"
          clickable
          label={`${ownerRepo(pr.repo)}#${pr.number}`}
          title={pr.url}
          onClick={() => void api.openExternal(pr.url)}
        />
        <span>
          {pr.author.login} opened {age(pr.createdAt)}
          {updatedLater && `, updated ${age(pr.updatedAt)}`}
        </span>
        <span className={styles.sep}>·</span>
        <span className={styles.mono}>
          {pr.headRefName} → {pr.baseRefName}
        </span>
        <span className={styles.sep}>·</span>
        <span className={styles.mono}>{shortSha(pr.headRefOid)}</span>
        <span className={styles.sep}>·</span>
        <span>
          <span className={styles.adds}>+{pr.additions}</span> <span className={styles.dels}>−{pr.deletions}</span> in{" "}
          {plural(pr.changedFiles, "file")}
        </span>
        {pr.isDraft && <span className={styles.chip}>Draft</span>}
        {pr.requested && <span className={styles.warnChip}>Review requested from you</span>}
      </div>
    </header>
  );
};

const PrDetail = ({ pr }: { readonly pr: PullRequest }): JSX.Element => {
  const [newest, ...older] = pr.reviews;
  return (
    <section className={styles.root} aria-label={`${ownerRepo(pr.repo)}#${pr.number}`}>
      <PrHeader pr={pr} />
      <div className={styles.body}>
        {newest === undefined ? (
          <>
            <section className={styles.card} aria-label="Description">
              <header className={styles.cardHeader}>
                <h2>Description</h2>
              </header>
              <div className={styles.cardBody}>
                {pr.body.trim() === "" ? (
                  <p className={styles.caption}>No description provided.</p>
                ) : (
                  <MarkdownText text={pr.body} />
                )}
              </div>
            </section>
            <ReviewWillRun pr={pr} />
          </>
        ) : (
          <>
            <ReviewCard review={newest} pr={pr} />
            {older.map((review) => (
              <ReviewCard key={review.workspaceId} review={review} pr={pr} collapsed />
            ))}
            {pr.outOfDate && pr.group === "reviewed" && (
              <p className={styles.outOfDate} role="status">
                {pr.newCommits === null || pr.newCommits.subjects.length === 0
                  ? "New commits were pushed after this review."
                  : `${plural(pr.newCommits.count, "commit")} pushed after this review: ${quoteList(pr.newCommits.subjects)}.`}{" "}
                Review again to check them.
              </p>
            )}
          </>
        )}
      </div>
    </section>
  );
};

const EmptyWatch = ({ error }: { readonly error: AppError | null }): JSX.Element => {
  const dispatch = useAppDispatch();
  return (
    <div className={styles.empty}>
      <div className={styles.emptyBox}>
        <h2>Watch a repo to see its pull requests here</h2>
        <p>
          Reviews checks your watched GitHub repos for open pull requests and starts a review agent for any of them in
          one click.
        </p>
        <Button variant="contained" onClick={() => dispatch(watchedOpened())}>
          Choose repos to watch
        </Button>
        {error !== null && <GhAlert error={error} />}
      </div>
    </div>
  );
};

/** The selected pull request, or what Reviews is for when there's none. */
export const ReviewsPane = (): JSX.Element => {
  const view = useAppSelector((s) => s.reviews.view);
  const selectedKey = useAppSelector((s) => s.reviews.selectedKey);
  const fetchError = useAppSelector((s) => s.reviews.error);
  const pr = view?.prs.find((p) => p.key === selectedKey);
  if (view === null) {
    return (
      <div className={styles.empty}>
        {fetchError === null ? <CircularProgress size={20} aria-label="Loading" /> : <Alert severity="error">{fetchError.message}</Alert>}
      </div>
    );
  }
  const watched = watchedCount(view);
  if (watched === 0) {
    return <EmptyWatch error={view.error} />;
  }
  if (pr !== undefined) {
    return (
      <>
        {view.error !== null && <GhAlert error={view.error} />}
        <PrDetail key={pr.key} pr={pr} />
      </>
    );
  }
  const checked = view.checkedAtMs === null ? "" : ` Checked ${age(new Date(view.checkedAtMs).toISOString())}.`;
  return (
    <div className={styles.empty}>
      <div className={styles.emptyBox}>
        {view.prs.length === 0 ? (
          <p>
            No open pull requests in {watched} watched repo{watched === 1 ? "" : "s"}.{checked}
          </p>
        ) : (
          <p>Select a pull request.</p>
        )}
        {view.error !== null && <GhAlert error={view.error} />}
      </div>
    </div>
  );
};
