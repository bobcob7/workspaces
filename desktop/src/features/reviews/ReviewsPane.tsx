import { Alert, Button, Chip, CircularProgress, Menu, MenuItem } from "@mui/material";
import { useEffect, useState, type JSX } from "react";
import { api, type AppError, type PullRequest, type ReviewRun } from "../../api";
import { age } from "../../format";
import { useAppDispatch, useAppSelector } from "../../store";
import { MarkdownText } from "../conversation/ConversationView";
import { sectionChosen, selected } from "../ui/uiSlice";
import { commandPreview, isReviewing, ownerRepo, shortSha, watchedCount } from "./groups";
import { PrDot } from "./PrDot";
import { ReportView } from "./ReportView";
import { defaultTriage, fetchReport, reportKey, reviewViewed, tabChosen, type DetailTab } from "./reportsSlice";
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
          A new workspace with the PR's head commit. The review agent runs one reviewer per profile, and each submits
          its findings with ws review; nothing is posted to GitHub.
        </p>
      </div>
    </section>
  );
};

const openAgent = (dispatch: ReturnType<typeof useAppDispatch>, id: string): void => {
  dispatch(sectionChosen("agents"));
  dispatch(selected(id));
};

interface PrHeaderProps {
  readonly pr: PullRequest;
  /** The review shown, if any. */
  readonly review: ReviewRun | undefined;
}

const PrHeader = ({ pr, review }: PrHeaderProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const requester = useAppSelector((s) =>
    review === undefined || !review.recorded
      ? null
      : review.requester ?? s.reports.byKey[reportKey(pr.repo, review.sha)]?.report?.review?.requester ?? null,
  );
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
        {pr.group === "reviewing" && review !== undefined && (
          <Button size="small" onClick={() => openAgent(dispatch, review.workspaceId)}>
            Open agent
          </Button>
        )}
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
        {requester !== null && requester !== "" && (
          <Chip
            size="small"
            color="primary"
            variant="outlined"
            clickable
            label={`Requested by ${requester}`}
            title={`Open ${requester} in Agents`}
            onClick={() => openAgent(dispatch, requester)}
          />
        )}
      </div>
    </header>
  );
};

const Description = ({ pr }: { readonly pr: PullRequest }): JSX.Element => (
  <section className={styles.card} aria-label="Description">
    <header className={styles.cardHeader}>
      <h2>Description</h2>
    </header>
    <div className={styles.cardBody}>
      {pr.body.trim() === "" ? <p className={styles.caption}>No description provided.</p> : <MarkdownText text={pr.body} />}
    </div>
  </section>
);

/** The amber strip over a review of an older commit: what was pushed since, and where its decisions stay. */
const OutOfDate = ({ pr, review }: { readonly pr: PullRequest; readonly review: ReviewRun }): JSX.Element => {
  const newest = pr.reviews[0] === review;
  const pushed =
    !newest || pr.newCommits === null
      ? `This review is of ${shortSha(review.sha)}; the pull request's head is now ${shortSha(pr.headRefOid)}.`
      : pr.newCommits.subjects.length === 0
        ? `${pr.author.login} pushed ${plural(pr.newCommits.count, "commit")} after this review.`
        : `${pr.author.login} pushed ${plural(pr.newCommits.count, "commit")} after this review: ${quoteList(pr.newCommits.subjects)}.`;
  return (
    <p className={styles.outOfDate} role="status">
      {pushed} These decisions stay with {shortSha(review.sha)}
      {pr.outOfDate ? (
        <>
          ; <b>Review again</b> reviews {shortSha(pr.headRefOid)} from scratch.
        </>
      ) : (
        "."
      )}
    </p>
  );
};

interface TabRowProps {
  readonly pr: PullRequest;
  readonly review: ReviewRun;
  readonly tabs: readonly DetailTab[];
  readonly tab: DetailTab;
  readonly findings: number | null;
}

const tabLabels: Readonly<Record<DetailTab, string>> = {
  findings: "Findings",
  description: "Description",
  agent: "Review agent",
};

/** Findings / Description / Review agent, with the review shown and links to the others. */
const TabRow = ({ pr, review, tabs, tab, findings }: TabRowProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const [menu, setMenu] = useState<HTMLElement | null>(null);
  const others = pr.reviews.filter((r) => r !== review);
  const working = isReviewing(review.status);
  return (
    <div className={styles.tabRow}>
      <div role="tablist" className={styles.tabs}>
        {tabs.map((t) => (
          <button
            key={t}
            type="button"
            role="tab"
            className={styles.tab}
            aria-selected={t === tab}
            onClick={() => dispatch(tabChosen({ pr: pr.key, value: t }))}
          >
            {tabLabels[t]}
            {t === "findings" && findings !== null && <span className={styles.tabCount}>{findings}</span>}
          </button>
        ))}
      </div>
      <span className={styles.spacer} />
      <span className={styles.caption}>
        {review.workspaceId} · {working ? "started" : `reviewed ${shortSha(review.sha)} ·`} {age(review.createdAt)}
        {others.length > 0 && (
          <>
            {" · "}
            <button type="button" className={styles.linkButton} onClick={(e) => setMenu(e.currentTarget)}>
              {pr.reviews[0] === review ? `older reviews (${others.length})` : "other reviews"}
            </button>
          </>
        )}
      </span>
      <Menu anchorEl={menu} open={menu !== null} onClose={() => setMenu(null)}>
        {others.map((r) => (
          <MenuItem
            key={r.workspaceId}
            onClick={() => {
              setMenu(null);
              dispatch(reviewViewed({ pr: pr.key, value: r === pr.reviews[0] ? null : r.workspaceId }));
            }}
          >
            {shortSha(r.sha)} · {age(r.createdAt)} · {r.workspaceId}
          </MenuItem>
        ))}
      </Menu>
    </div>
  );
};

const ReviewedDetail = ({ pr, review }: { readonly pr: PullRequest; readonly review: ReviewRun }): JSX.Element => {
  const dispatch = useAppDispatch();
  const triage = useAppSelector((s) => s.reports.triage[pr.key]) ?? defaultTriage;
  const entry = useAppSelector((s) => s.reports.byKey[reportKey(pr.repo, review.sha)]);
  const working = isReviewing(review.status);
  useEffect(() => {
    if (review.recorded) {
      void dispatch(fetchReport({ repo: pr.repo, ref: review.sha, refresh: true }));
    }
  }, [dispatch, pr.repo, review.sha, review.recorded]);
  const report = entry?.report ?? null;
  const hasFindings =
    review.recorded &&
    (entry?.error != null || (report !== null && (!working || report.profiles.some((p) => !p.missing))));
  const tabs: readonly DetailTab[] = hasFindings ? ["findings", "description", "agent"] : ["description", "agent"];
  const chosen = triage.tab ?? (hasFindings ? "findings" : "agent");
  const tab = tabs.includes(chosen) ? chosen : "agent";
  const atHead = review.sha !== "" && pr.headRefOid.startsWith(review.sha.slice(0, 7));
  return (
    <>
      <TabRow pr={pr} review={review} tabs={tabs} tab={tab} findings={report?.comments.length ?? null} />
      {!atHead && !working && (
        <div className={styles.strip}>
          <OutOfDate pr={pr} review={review} />
        </div>
      )}
      {tab === "findings" ? (
        <ReportView pr={pr} review={review} working={working} />
      ) : (
        <div className={styles.body}>
          {tab === "description" ? (
            <Description pr={pr} />
          ) : (
            pr.reviews.map((r) => <ReviewCard key={r.workspaceId} review={r} pr={pr} collapsed={r !== review} />)
          )}
        </div>
      )}
    </>
  );
};

const PrDetail = ({ pr }: { readonly pr: PullRequest }): JSX.Element => {
  const viewed = useAppSelector((s) => s.reports.triage[pr.key]?.viewed ?? null);
  const review = pr.reviews.find((r) => r.workspaceId === viewed) ?? pr.reviews[0];
  return (
    <section className={styles.root} aria-label={`${ownerRepo(pr.repo)}#${pr.number}`}>
      <PrHeader pr={pr} review={review} />
      {review === undefined ? (
        <div className={styles.body}>
          <Description pr={pr} />
          <ReviewWillRun pr={pr} />
        </div>
      ) : (
        <ReviewedDetail pr={pr} review={review} />
      )}
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
