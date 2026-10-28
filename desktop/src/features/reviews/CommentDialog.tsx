import {
  Alert,
  Button,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  MenuItem,
  TextField,
} from "@mui/material";
import { useState, type JSX } from "react";
import { api, type CommentPlan, type DraftComment, type ReviewEvent } from "../../api";
import { useAppDispatch, useAppSelector } from "../../store";
import { MarkdownText } from "../conversation/ConversationView";
import { commentDialogClosed, postReview } from "./actionsSlice";
import { ownerRepo, shortSha } from "./groups";
import styles from "./ActionDialogs.module.css";

/** Comment is the default; the others are never preselected (ADR 0041). */
const events: readonly { readonly value: ReviewEvent; readonly label: string }[] = [
  { value: "COMMENT", label: "Comment" },
  { value: "REQUEST_CHANGES", label: "Request changes" },
  { value: "APPROVE", label: "Approve" },
];

/** `line 58`, `lines 12–15`, or `file`. */
export const placeLabel = (comment: DraftComment): string => {
  if (comment.kind === "file" || comment.line === null) {
    return "file";
  }
  return comment.startLine === null ? `line ${comment.line}` : `lines ${comment.startLine}–${comment.line}`;
};

const plural = (count: number, word: string): string => `${count} ${word}${count === 1 ? "" : "s"}`;

/** `2 line comments · 0 file comments · 0 in summary`. */
export const commentCounts = (comments: readonly DraftComment[], inBody: number): string => {
  const lines = comments.filter((c) => c.kind === "line").length;
  return [plural(lines, "line comment"), plural(comments.length - lines, "file comment"), `${inBody} in summary`].join(" · ");
};

interface CardProps {
  readonly comment: DraftComment;
  readonly onChange: (body: string) => void;
}

const CommentCard = ({ comment, onChange }: CardProps): JSX.Element => {
  const [editing, setEditing] = useState(false);
  return (
    <section className={styles.card} aria-label={`Comment on ${comment.path}`}>
      <div className={styles.cardHead}>
        <span className={styles.path}>{comment.path}</span>
        <span className={styles.where}>{placeLabel(comment)}</span>
        <span className={styles.spacer} />
        <span className={styles.kind}>{comment.kind === "line" ? "line comment" : "file comment"}</span>
        <Button size="small" onClick={() => setEditing(!editing)}>
          {editing ? "Done" : "Edit"}
        </Button>
      </div>
      <div className={styles.cardBody}>
        {editing ? (
          <TextField
            fullWidth
            multiline
            minRows={3}
            value={comment.body}
            onChange={(e) => onChange(e.target.value)}
            slotProps={{ htmlInput: { "aria-label": `Text of the comment on ${comment.path}`, className: styles.mono } }}
          />
        ) : (
          <MarkdownText text={comment.body} />
        )}
      </div>
    </section>
  );
};

const Preview = ({ plan }: { readonly plan: CommentPlan }): JSX.Element => {
  const dispatch = useAppDispatch();
  const dialog = useAppSelector((s) => s.actions.comment);
  const [body, setBody] = useState(plan.body);
  const [event, setEvent] = useState<ReviewEvent>("COMMENT");
  const [comments, setComments] = useState(plan.comments);
  const posting = dialog?.posting ?? false;
  const error = dialog?.error ?? null;
  const pending = error?.code === "review_pending" ? (plan.pendingReview ?? plan.prUrl) : plan.pendingReview;
  const empty = body.trim() === "" && comments.length === 0;
  const post = (): void => {
    if (dialog === null) {
      return;
    }
    void dispatch(
      postReview({
        prKey: dialog.prKey,
        repo: dialog.repo,
        ref: dialog.ref,
        request: { number: plan.number, commitId: plan.commitId, body, event, comments, findings: plan.findings },
      }),
    );
  };
  return (
    <>
      <DialogContent className={styles.content}>
        <p className={styles.lead}>
          One review, {plan.login === null ? "as you" : <>as <b>{plan.login}</b></>}, at <code className={styles.mono}>{shortSha(plan.commitId)}</code>
          {plan.outdated ? "" : " (the PR's current head)"}. Nothing is posted until you choose Post review.
        </p>
        {plan.outdated && (
          <Alert severity="warning">
            The pull request&apos;s head is now <code className={styles.mono}>{shortSha(plan.head)}</code>. These comments are
            pinned to <code className={styles.mono}>{shortSha(plan.commitId)}</code>, the commit that was reviewed, so GitHub
            may show them as outdated.
          </Alert>
        )}
        <div className={styles.row}>
          <TextField
            label="Review summary"
            className={styles.summary}
            multiline
            minRows={1}
            maxRows={8}
            value={body}
            onChange={(e) => setBody(e.target.value)}
          />
          <TextField select label="Event" className={styles.event} value={event} onChange={(e) => setEvent(e.target.value as ReviewEvent)}>
            {events.map((e) => (
              <MenuItem key={e.value} value={e.value}>
                {e.label}
              </MenuItem>
            ))}
          </TextField>
        </div>
        {comments.map((c, i) => (
          <CommentCard
            key={c.finding}
            comment={c}
            onChange={(text) => setComments(comments.map((other, j) => (j === i ? { ...other, body: text } : other)))}
          />
        ))}
        <Alert severity="info" icon={false}>
          @mentions in agent-written text are shown as code, so nobody is notified by a finding.
        </Alert>
        {pending !== null && (
          <Alert
            severity="warning"
            action={
              <Button color="inherit" size="small" onClick={() => void api.openExternal(pending)}>
                Open on GitHub
              </Button>
            }
          >
            You have an unsubmitted review on this pull request. Finish or discard it on GitHub first.
          </Alert>
        )}
        {error !== null && error.code !== "review_pending" && <Alert severity="error">{error.message}</Alert>}
      </DialogContent>
      <DialogActions>
        <span className={styles.counts}>{commentCounts(comments, plan.inBody)}</span>
        <Button onClick={() => dispatch(commentDialogClosed())} disabled={posting}>
          Cancel
        </Button>
        {pending === null && (
          <Button
            variant="contained"
            onClick={post}
            disabled={posting || empty}
            startIcon={posting ? <CircularProgress size={14} color="inherit" /> : undefined}
          >
            Post review
          </Button>
        )}
      </DialogActions>
    </>
  );
};

/** Comment on N…: exactly what will be posted, editable, before anything is (report.md). */
export const CommentDialog = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const dialog = useAppSelector((s) => s.actions.comment);
  const pr = useAppSelector((s) => s.reviews.view?.prs.find((p) => p.key === dialog?.prKey));
  const title = pr === undefined ? "Post a review" : `Post a review on ${ownerRepo(pr.repo)}#${pr.number}`;
  const close = (): void => {
    if (dialog?.posting !== true) {
      dispatch(commentDialogClosed());
    }
  };
  return (
    <Dialog open={dialog !== null} onClose={close} fullWidth maxWidth="md">
      <DialogTitle>{title}</DialogTitle>
      {dialog !== null && dialog.plan !== null ? (
        <Preview key={`${dialog.prKey}@${dialog.ref}`} plan={dialog.plan} />
      ) : (
        <DialogContent className={styles.loading}>
          {dialog?.error != null ? (
            <Alert severity="error">{dialog.error.message}</Alert>
          ) : (
            <CircularProgress size={20} aria-label="Reading the pull request" />
          )}
        </DialogContent>
      )}
    </Dialog>
  );
};
