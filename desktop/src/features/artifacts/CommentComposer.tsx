import { Button, TextField } from "@mui/material";
import { useState, type FormEvent, type JSX } from "react";
import { api, toAppError, type NewAnchor } from "../../api";
import { useAppDispatch } from "../../store";
import { noticeShown } from "../ui/uiSlice";
import styles from "./ThreadList.module.css";

export interface CommentComposerProps {
  readonly artifactId: string;
  /** What the comment anchors to; null for the whole artifact. */
  readonly anchor: NewAnchor | null;
  /** The selection wasn't in the block's source, so the block is anchored instead. */
  readonly wholeBlock: boolean;
  /** Called with the id of the new thread. */
  readonly onPosted: (thread: string) => void;
  readonly onDismiss: (() => void) | null;
}

/** A new comment: the quote it anchors to, then the text. Post sends it as a new thread. */
export const CommentComposer = ({
  artifactId,
  anchor,
  wholeBlock,
  onPosted,
  onDismiss,
}: CommentComposerProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const [body, setBody] = useState("");
  const [posting, setPosting] = useState(false);
  const canPost = body.trim() !== "" && !posting;

  const post = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    if (!canPost) {
      return;
    }
    setPosting(true);
    try {
      const added = await api.artifactCommentAdd(artifactId, body.trim(), null, anchor);
      setBody("");
      dispatch(noticeShown({ severity: "success", message: "Comment posted" }));
      onPosted(added.thread.id);
    } catch (err: unknown) {
      dispatch(noticeShown({ severity: "error", message: toAppError(err).message }));
    } finally {
      setPosting(false);
    }
  };

  return (
    <form className={styles.composer} onSubmit={(event) => void post(event)}>
      {anchor !== null && (
        <blockquote className={styles.quote}>
          {wholeBlock ? "Comments on the whole paragraph" : anchor.quote}
        </blockquote>
      )}
      <TextField
        size="small"
        fullWidth
        multiline
        minRows={2}
        placeholder="Add a comment…"
        value={body}
        onChange={(event) => setBody(event.target.value)}
        slotProps={{ htmlInput: { "aria-label": "Comment" } }}
      />
      <div className={styles.actions}>
        {onDismiss !== null && (
          <Button size="small" onClick={onDismiss}>
            Cancel
          </Button>
        )}
        <Button type="submit" size="small" variant="contained" disabled={!canPost}>
          {posting ? "Posting..." : "Post"}
        </Button>
      </div>
    </form>
  );
};
