import { Button, Dialog, DialogActions, DialogContent, DialogContentText, DialogTitle } from "@mui/material";
import { useEffect, useState, type JSX } from "react";
import type { PullRequest, ReviewRun } from "../../api";
import { age } from "../../format";
import { useAppDispatch, useAppSelector } from "../../store";
import { removeEntry } from "../agents/agentsSlice";
import { StatusDot } from "../agents/StatusDot";
import { conversationClosed, fetchConversation } from "../conversation/conversationSlice";
import { MarkdownText } from "../conversation/ConversationView";
import { sectionChosen, selected } from "../ui/uiSlice";
import { isReviewing, shortSha } from "./groups";
import styles from "./ReviewCard.module.css";

/** How often a running review's reply is re-read. */
const replyInterval = 4000;

/** The agent's latest reply, read from its transcript. */
const Reply = ({ sessionId, live }: { readonly sessionId: string; readonly live: boolean }): JSX.Element | null => {
  const dispatch = useAppDispatch();
  const conversation = useAppSelector((s) => s.conversations.bySession[sessionId]);
  useEffect(() => {
    void dispatch(fetchConversation(sessionId));
    const timer = live ? window.setInterval(() => void dispatch(fetchConversation(sessionId)), replyInterval) : null;
    return () => {
      if (timer !== null) {
        window.clearInterval(timer);
      }
      dispatch(conversationClosed(sessionId));
    };
  }, [dispatch, sessionId, live]);
  const reply = conversation?.items.findLast((item) => item.kind === "assistant");
  if (reply === undefined || !("text" in reply)) {
    return null;
  }
  return (
    <div className={styles.reply}>
      <MarkdownText text={reply.text} />
    </div>
  );
};

export interface ReviewCardProps {
  readonly review: ReviewRun;
  readonly pr: PullRequest;
  /** Older reviews start collapsed. */
  readonly collapsed?: boolean;
}

/** One review agent of a pull request: its status, result, and reply. */
export const ReviewCard = ({ review, pr, collapsed = false }: ReviewCardProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const entry = useAppSelector((s) => s.agents.entries.find((e) => e.workspaceId === review.workspaceId));
  const busy = useAppSelector((s) => entry !== undefined && s.agents.busy.includes(entry.key));
  const [open, setOpen] = useState(!collapsed);
  const [confirming, setConfirming] = useState(false);
  const atHead = review.sha !== "" && pr.headRefOid.startsWith(review.sha.slice(0, 7));
  const openAgent = (): void => {
    dispatch(sectionChosen("agents"));
    dispatch(selected(review.workspaceId));
  };
  return (
    <section className={styles.root} aria-label={review.workspaceId}>
      <header className={styles.header}>
        <button type="button" className={styles.toggle} aria-expanded={open} onClick={() => setOpen(!open)}>
          <StatusDot status={review.status} />
          <span className={styles.name}>{review.workspaceId}</span>
        </button>
        {review.auto && <span className={styles.auto}>auto</span>}
        <span className={styles.muted}>
          {isReviewing(review.status) ? "reviewing" : "reviewed"}{" "}
          {shortSha(review.sha)}
          {review.createdAt !== null && ` · ${age(review.createdAt)}`}
          {!atHead && " · older commit"}
        </span>
        <span className={styles.spacer} />
        {review.removed ? (
          <span className={styles.muted} title="Removed once every reviewer had reported; the findings are kept">
            agent removed after finishing
          </span>
        ) : (
          <>
            <Button size="small" onClick={openAgent}>
              Open agent
            </Button>
            <Button size="small" color="error" disabled={entry === undefined || busy} onClick={() => setConfirming(true)}>
              Remove
            </Button>
          </>
        )}
      </header>
      {open && (
        <div className={styles.body}>
          {review.detail !== null && review.status === "working" && <p className={styles.detail}>{review.detail}</p>}
          {review.result !== null && <p className={styles.result}>{review.result}</p>}
          {review.sessionId !== null && (
            <Reply sessionId={review.sessionId} live={review.status === "working" || review.status === "starting"} />
          )}
          {review.result === null && review.sessionId === null && <p className={styles.muted}>No reply yet.</p>}
        </div>
      )}
      <Dialog open={confirming} onClose={() => setConfirming(false)}>
        <DialogTitle>Remove {review.workspaceId}?</DialogTitle>
        <DialogContent>
          <DialogContentText>
            This stops the review agent and deletes its workspace, including anything it changed there. The pull request
            itself isn't touched; it goes back to To review unless another review is at its head.
          </DialogContentText>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setConfirming(false)}>Cancel</Button>
          <Button
            color="error"
            variant="contained"
            onClick={() => {
              setConfirming(false);
              if (entry !== undefined) {
                void dispatch(removeEntry(entry));
              }
            }}
          >
            Remove
          </Button>
        </DialogActions>
      </Dialog>
    </section>
  );
};
