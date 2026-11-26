import { Button, Dialog, DialogActions, DialogContent, DialogContentText, DialogTitle } from "@mui/material";
import { useState, type JSX } from "react";
import type { Artifact, ArtifactContent, NewAnchor, Thread } from "../../api";
import { age } from "../../format";
import { useAppDispatch, useAppSelector } from "../../store";
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
        onPosted={() => {
          setDraft(null);
          onChanged();
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

  if (artifact.kind === "markdown") {
    return (
      <>
        <div className={styles.split}>
          <MarkdownViewer
            source={source}
            highlights={onLatest ? threads : []}
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
  const { artifact, content, comments, error, shownRev, chooseRev, reload } = useArtifactDetail(id);
  const [confirming, setConfirming] = useState(false);
  const [activeThread, setActiveThread] = useState<string | null>(null);
  const onLatest = artifact !== null && shownRev === artifact.latest;

  const copyReference = (): void => {
    if (artifact === null || shownRev === null) {
      return;
    }
    const suffix = onLatest ? "" : `/r${shownRev}`;
    void navigator.clipboard.writeText(`${artifact.reference}${suffix}`).catch(() => undefined);
  };

  const remove = (): void => {
    setConfirming(false);
    void dispatch(removeArtifact(id));
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
            <Button size="small" onClick={copyReference}>
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
        <p className={styles.error} role="alert">
          {error.message}
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
        />
      )}
      <Dialog open={confirming} onClose={() => setConfirming(false)}>
        <DialogTitle>{`Delete ${artifact?.name ?? "this artifact"}?`}</DialogTitle>
        <DialogContent>
          <DialogContentText>This removes the artifact with all its revisions and comments.</DialogContentText>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setConfirming(false)}>Cancel</Button>
          <Button color="error" onClick={remove}>
            Delete
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
