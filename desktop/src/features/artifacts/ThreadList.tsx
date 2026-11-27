import { Button, TextField } from "@mui/material";
import { useEffect, useRef, useState, type FormEvent, type JSX } from "react";
import { api, toAppError, type Actor, type Thread } from "../../api";
import { useAppDispatch } from "../../store";
import { noticeShown } from "../ui/uiSlice";
import { LinkedText } from "./LinkedText";
import styles from "./ThreadList.module.css";

export const actorName = (actor: Actor): string => (actor.kind === "person" ? "you" : (actor.workspace ?? actor.name ?? ""));

const isDetached = (thread: Thread): boolean => !thread.resolved && thread.anchor?.state === "detached";

const isOpen = (thread: Thread): boolean => !thread.resolved && !isDetached(thread);

const lineOf = (thread: Thread): number => thread.anchor?.lineStart ?? Number.MAX_SAFE_INTEGER;

interface ThreadGroups {
  readonly open: readonly Thread[];
  readonly detached: readonly Thread[];
  readonly resolved: readonly Thread[];
}

/** The threads in the order the margin shows them: open by line, then detached, then resolved. */
export const groupThreads = (threads: readonly Thread[]): ThreadGroups => ({
  open: threads.filter(isOpen).sort((a, b) => lineOf(a) - lineOf(b)),
  detached: threads.filter(isDetached),
  resolved: threads.filter((thread) => thread.resolved),
});

const headline = (thread: Thread): string => {
  if (thread.anchor === null) {
    return "Whole artifact";
  }
  const quote = `"${thread.anchor.quote}"`;
  return thread.anchor.state === "detached" ? `${quote} on r${thread.anchor.rev}` : quote;
};

interface ThreadItemProps {
  readonly artifactId: string;
  readonly thread: Thread;
  readonly active: boolean;
  readonly readOnly: boolean;
  /** How many threads the list shows as resolved now, before this item's action. */
  readonly resolvedCount: number;
  readonly onActivate: (thread: string) => void;
  readonly onChanged: () => void;
}

const ThreadItem = ({
  artifactId,
  thread,
  active,
  readOnly,
  resolvedCount,
  onActivate,
  onChanged,
}: ThreadItemProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const itemRef = useRef<HTMLLIElement>(null);
  const [reply, setReply] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (active) {
      itemRef.current?.scrollIntoView?.({ block: "nearest" });
    }
  }, [active]);

  const resolveNotice = thread.resolved ? "Thread reopened" : `Thread resolved - see Resolved (${resolvedCount + 1})`;

  const perform = async (action: () => Promise<unknown>, success?: string): Promise<void> => {
    setBusy(true);
    try {
      await action();
      if (success !== undefined) {
        dispatch(noticeShown({ severity: "success", message: success }));
      }
      onChanged();
    } catch (err: unknown) {
      dispatch(noticeShown({ severity: "error", message: toAppError(err).message }));
    } finally {
      setBusy(false);
    }
  };

  const sendReply = (event: FormEvent): void => {
    event.preventDefault();
    const body = reply.trim();
    if (busy || body === "") {
      return;
    }
    void perform(async () => {
      await api.artifactCommentAdd(artifactId, body, thread.id, null);
      setReply("");
    });
  };

  return (
    <li ref={itemRef} data-thread-item={thread.id} className={active ? styles.itemActive : styles.item}>
      <button type="button" className={styles.headline} onClick={() => onActivate(thread.id)}>
        {headline(thread)}
      </button>
      <ul className={styles.comments}>
        {thread.comments.map((comment) => (
          <li key={comment.id}>
            <span className={styles.author}>{actorName(comment.author)}:</span> <LinkedText text={comment.body} />
          </li>
        ))}
      </ul>
      {!readOnly && (
        <>
          <form className={styles.reply} onSubmit={sendReply}>
            <TextField
              size="small"
              fullWidth
              placeholder="Reply…"
              value={reply}
              onChange={(event) => setReply(event.target.value)}
              slotProps={{ htmlInput: { "aria-label": `Reply to ${thread.id}` } }}
            />
            <Button type="submit" size="small" disabled={busy || reply.trim() === ""}>
              {busy ? "Sending..." : "Reply"}
            </Button>
          </form>
          <Button
            size="small"
            disabled={busy}
            onClick={() =>
              void perform(() => api.artifactCommentResolve(artifactId, thread.id, thread.resolved), resolveNotice)
            }
          >
            {thread.resolved ? "Reopen" : "Resolve"}
          </Button>
        </>
      )}
    </li>
  );
};

export interface ThreadListProps {
  readonly artifactId: string;
  readonly threads: readonly Thread[];
  readonly activeThread: string | null;
  /** Read-only on an older revision: no reply, resolve or reopen. */
  readonly readOnly: boolean;
  readonly onActivate: (thread: string) => void;
  readonly onChanged: () => void;
}

/** The artifact's comment threads: open, then detached, then resolved (collapsed). */
export const ThreadList = ({
  artifactId,
  threads,
  activeThread,
  readOnly,
  onActivate,
  onChanged,
}: ThreadListProps): JSX.Element => {
  const groups = groupThreads(threads);
  const itemFor = (thread: Thread): JSX.Element => (
    <ThreadItem
      key={thread.id}
      artifactId={artifactId}
      thread={thread}
      active={thread.id === activeThread}
      readOnly={readOnly}
      resolvedCount={groups.resolved.length}
      onActivate={onActivate}
      onChanged={onChanged}
    />
  );
  return (
    <div className={styles.root}>
      {threads.length === 0 && <p className={styles.empty}>No comments yet.</p>}
      <ul className={styles.list} aria-label="Comment threads">
        {groups.open.map(itemFor)}
      </ul>
      {groups.detached.length > 0 && (
        <section aria-label="Detached threads">
          <h3 className={styles.heading}>Detached ({groups.detached.length})</h3>
          <ul className={styles.list}>{groups.detached.map(itemFor)}</ul>
        </section>
      )}
      {groups.resolved.length > 0 && (
        <details className={styles.resolved}>
          <summary className={styles.summary}>Resolved ({groups.resolved.length})</summary>
          <ul className={styles.list}>{groups.resolved.map(itemFor)}</ul>
        </details>
      )}
    </div>
  );
};
