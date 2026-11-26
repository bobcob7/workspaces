import { Button, Dialog, DialogActions, DialogContent, DialogContentText, DialogTitle } from "@mui/material";
import { useState, type JSX } from "react";
import type { Artifact, ArtifactContent, NewAnchor, Thread } from "../../api";
import { age } from "../../format";
import { useAppDispatch, useAppSelector } from "../../store";
import { noticeShown, narrowBack } from "../ui/uiSlice";
import { removeArtifact } from "./artifactsSlice";
import { CommentComposer } from "./CommentComposer";
import { CommentPopover } from "./CommentPopover";
import { decodeText } from "./content";
import { ImageViewer } from "./ImageViewer";
import { MarkdownViewer } from "./MarkdownViewer";
import { TextViewer } from "./TextViewer";
import { ThreadList } from "./ThreadList";
import { useArtifactDetail } from "./useArtifactDetail";
import styles from "./ArtifactsPane.module.css";

const bylineOf = (artifact: Artifact): string => {
  const who = artifact.createdBy.kind === "person" ? "you" : (artifact.createdBy.workspace ?? "");
  return `added by ${who}, ${age(artifact.createdAt)}`;
};

const threadsText = (artifact: Artifact): string =>
  `${artifact.threads.open} open · ${artifact.threads.resolved} resolved · ${artifact.threads.detached} detached`;

/** A comment being written: anchored to a selection, or to the whole artifact when `anchor` is null. The popover opens beside `anchorEl`. */
interface Draft {
  readonly anchor: NewAnchor | null;
  readonly wholeBlock: boolean;
  readonly anchorEl: HTMLElement | null;
}

interface ArtifactBodyProps {
  readonly artifact: Artifact;
  readonly content: ArtifactContent;
  readonly shownRev: number;
  readonly threads: readonly Thread[];
  readonly onLatest: boolean;
  readonly activeThread: string | null;
  readonly onActivate: (thread: string) => void;
  readonly onChanged: () => void;
  readonly onGoToLatest: () => void;
}

/** The viewer, the margin of threads and the comment popover for the shown revision. */
const ArtifactBody = ({
  artifact,
  content,
  shownRev,
  threads,
  onLatest,
  activeThread,
  onActivate,
  onChanged,
  onGoToLatest,
}: ArtifactBodyProps): JSX.Element => {
  const [draft, setDraft] = useState<Draft | null>(null);
  const source = decodeText(content.dataBase64);
  const dismiss = (): void => setDraft(null);
  const popover = draft !== null && (
    <CommentPopover anchorEl={draft.anchorEl} onDismiss={dismiss}>
      <CommentComposer
        artifactId={artifact.id}
        anchor={draft.anchor}
        wholeBlock={draft.wholeBlock}
        onPosted={(thread) => {
          setDraft(null);
          onChanged();
          onActivate(thread);
        }}
        onDismiss={dismiss}
      />
    </CommentPopover>
  );
  const wholeComment = onLatest && (
    <Button
      size="small"
      className={styles.wholeComment}
      onClick={(event) => setDraft({ anchor: null, wholeBlock: false, anchorEl: event.currentTarget })}
    >
      Comment on the whole artifact
    </Button>
  );
  const threadList = (
    <ThreadList
      artifactId={artifact.id}
      threads={threads}
      activeThread={activeThread}
      readOnly={!onLatest}
      onActivate={onActivate}
      onChanged={onChanged}
    />
  );

  const notice = !onLatest && (
    <p className={styles.notice} role="status">
      {`Viewing r${shownRev}, not the latest. Comments are read-only.`}
      <Button size="small" onClick={onGoToLatest}>
        Go to latest
      </Button>
    </p>
  );

  if (artifact.kind === "markdown") {
    return (
      <>
        {notice}
        <div className={styles.split}>
          <MarkdownViewer
            source={source}
            highlights={onLatest ? threads : []}
            threads={threads}
            revision={shownRev}
            onComment={onLatest ? (anchor, wholeBlock, anchorEl) => setDraft({ anchor, wholeBlock, anchorEl }) : null}
            activeThread={activeThread}
            onThreadClick={onActivate}
          />
          <aside className={styles.margin}>
            {wholeComment}
            {threadList}
          </aside>
        </div>
        {popover}
      </>
    );
  }

  return (
    <>
      {notice}
      {artifact.kind === "image" ? (
        <ImageViewer
          name={artifact.name}
          contentType={content.contentType}
          size={content.size}
          dataBase64={content.dataBase64}
          byline={bylineOf(artifact)}
        />
      ) : (
        <TextViewer text={source} />
      )}
      {wholeComment}
      {threadList}
      {popover}
    </>
  );
};

interface ArtifactDetailProps {
  readonly id: string;
}

/** One artifact: its header, revision picker, viewer and threads. Remounted for each selection. */
const ArtifactDetail = ({ id }: ArtifactDetailProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const { artifact, content, comments, error, shownRev, chooseRev, reload, retry } = useArtifactDetail(id);
  const [confirming, setConfirming] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [active, setActive] = useState<{ readonly thread: string; readonly rev: number | null } | null>(null);
  const activeThread = active !== null && active.rev === shownRev ? active.thread : null;
  const setActiveThread = (thread: string): void => setActive({ thread, rev: shownRev });
  const onLatest = artifact !== null && shownRev === artifact.latest;

  const copyReference = async (): Promise<void> => {
    if (artifact === null || shownRev === null) {
      return;
    }
    const reference = `${artifact.reference}${onLatest ? "" : `/r${shownRev}`}`;
    try {
      await navigator.clipboard.writeText(reference);
      dispatch(noticeShown({ severity: "info", message: `Copied ${reference}` }));
    } catch {
      dispatch(noticeShown({ severity: "error", message: "Couldn't copy the reference" }));
    }
  };

  const remove = async (): Promise<void> => {
    setDeleting(true);
    const result = await dispatch(removeArtifact(id));
    if (removeArtifact.fulfilled.match(result)) {
      dispatch(noticeShown({ severity: "success", message: `Deleted ${artifact?.name ?? "the artifact"}` }));
      dispatch(narrowBack());
    }
    setDeleting(false);
    setConfirming(false);
  };

  return (
    <div className={styles.root}>
      {artifact !== null && (
        <header className={styles.header}>
          <div className={styles.titleRow}>
            <h2 className={styles.title}>{artifact.title ?? artifact.name}</h2>
            {artifact.title !== null && <span className={styles.file}>{artifact.name}</span>}
          </div>
          <div className={styles.actions}>
            <Button size="small" onClick={() => void copyReference()}>
              Copy reference
            </Button>
            <select
              className={styles.revision}
              aria-label="Revision"
              value={shownRev ?? ""}
              onChange={(event) => chooseRev(Number(event.target.value))}
            >
              {artifact.revisions.map((revision) => (
                <option key={revision.rev} value={revision.rev}>
                  {`r${revision.rev}${revision.rev === artifact.latest ? " (latest)" : ""}`}
                </option>
              ))}
            </select>
            <span className={styles.threads}>{threadsText(artifact)}</span>
            <Button size="small" color="error" onClick={() => setConfirming(true)}>
              Delete
            </Button>
          </div>
        </header>
      )}
      {error !== null && (
        <div className={styles.error} role="alert">
          {error.message}
          <Button size="small" onClick={retry}>
            Retry
          </Button>
        </div>
      )}
      {error === null && (artifact === null || content === null) && (
        <p className={styles.loading} role="status">
          {artifact === null ? "Loading artifact..." : `Loading r${shownRev ?? artifact.latest}...`}
        </p>
      )}
      {artifact !== null && content !== null && comments !== null && shownRev !== null && (
        <ArtifactBody
          key={shownRev}
          artifact={artifact}
          content={content}
          shownRev={shownRev}
          threads={comments.threads}
          onLatest={onLatest}
          activeThread={activeThread}
          onActivate={setActiveThread}
          onChanged={reload}
          onGoToLatest={() => chooseRev(artifact.latest)}
        />
      )}
      <Dialog open={confirming} onClose={() => !deleting && setConfirming(false)}>
        <DialogTitle>{`Delete ${artifact?.name ?? "this artifact"}?`}</DialogTitle>
        <DialogContent>
          <DialogContentText>This removes the artifact with all its revisions and comments.</DialogContentText>
        </DialogContent>
        <DialogActions>
          <Button disabled={deleting} onClick={() => setConfirming(false)}>
            Cancel
          </Button>
          <Button color="error" disabled={deleting} onClick={() => void remove()}>
            {deleting ? "Deleting..." : "Delete"}
          </Button>
        </DialogActions>
      </Dialog>
    </div>
  );
};

/** The Artifacts section's detail pane: the selected artifact's header, viewer and comment threads. */
export const ArtifactsPane = (): JSX.Element => {
  const selected = useAppSelector((s) => s.artifacts.selected);
  return (
    <section className={styles.pane} role="region" aria-label="Artifact">
      {selected === null ? <p className={styles.empty}>Select an artifact</p> : <ArtifactDetail key={selected} id={selected} />}
    </section>
  );
};
