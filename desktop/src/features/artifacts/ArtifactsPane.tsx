import { Button, Dialog, DialogActions, DialogContent, DialogContentText, DialogTitle } from "@mui/material";
import { useEffect, useState, type JSX } from "react";
import { api, toAppError, type AppError, type Artifact, type ArtifactContent } from "../../api";
import { age } from "../../format";
import { useAppDispatch, useAppSelector } from "../../store";
import { removeArtifact } from "./artifactsSlice";
import { decodeText } from "./content";
import { ImageViewer } from "./ImageViewer";
import { TextViewer } from "./TextViewer";
import styles from "./ArtifactsPane.module.css";

const bylineOf = (artifact: Artifact): string => {
  const who = artifact.createdBy.kind === "person" ? "you" : (artifact.createdBy.workspace ?? "");
  return `added by ${who}, ${age(artifact.createdAt)}`;
};

const threadsText = (artifact: Artifact): string =>
  `${artifact.threads.open} open · ${artifact.threads.resolved} resolved · ${artifact.threads.detached} detached`;

interface ArtifactViewProps {
  readonly artifact: Artifact;
  readonly content: ArtifactContent;
}

/** The viewer for the artifact's kind. Markdown shows as text until its viewer lands. */
const ArtifactView = ({ artifact, content }: ArtifactViewProps): JSX.Element =>
  artifact.kind === "image" ? (
    <ImageViewer
      name={artifact.name}
      contentType={content.contentType}
      size={content.size}
      dataBase64={content.dataBase64}
      byline={bylineOf(artifact)}
    />
  ) : (
    <TextViewer text={decodeText(content.dataBase64)} />
  );

interface ArtifactDetailProps {
  readonly id: string;
}

/** One artifact: its header, revision picker and viewer. Remounted for each selection. */
const ArtifactDetail = ({ id }: ArtifactDetailProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const updatedAt = useAppSelector((s) => s.artifacts.items.find((item) => item.id === id)?.updatedAt ?? null);
  const [artifact, setArtifact] = useState<Artifact | null>(null);
  const [chosenRev, setChosenRev] = useState<number | null>(null);
  const [content, setContent] = useState<ArtifactContent | null>(null);
  const [error, setError] = useState<AppError | null>(null);
  const [confirming, setConfirming] = useState(false);
  const shownRev = chosenRev ?? artifact?.latest ?? null;

  useEffect(() => {
    let active = true;
    const load = async (): Promise<void> => {
      try {
        const shown = await api.artifactShow(id);
        if (active) {
          setArtifact(shown);
        }
      } catch (err: unknown) {
        if (active) {
          setError(toAppError(err));
        }
      }
    };
    void load();
    return () => {
      active = false;
    };
  }, [id, updatedAt]);

  useEffect(() => {
    if (shownRev === null) {
      return;
    }
    let active = true;
    const load = async (): Promise<void> => {
      try {
        const read = await api.artifactRead(id, shownRev);
        if (active) {
          setContent(read);
          setError(null);
        }
      } catch (err: unknown) {
        if (active) {
          setError(toAppError(err));
        }
      }
    };
    void load();
    return () => {
      active = false;
    };
  }, [id, shownRev]);

  const copyReference = (): void => {
    if (artifact === null || shownRev === null) {
      return;
    }
    const suffix = shownRev === artifact.latest ? "" : `/r${shownRev}`;
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
              onChange={(event) => setChosenRev(Number(event.target.value))}
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
      {artifact !== null && content !== null && <ArtifactView artifact={artifact} content={content} />}
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

/** The Artifacts section's detail pane: the selected artifact's header and viewer. */
export const ArtifactsPane = (): JSX.Element => {
  const selected = useAppSelector((s) => s.artifacts.selected);
  return (
    <section className={styles.pane} role="region" aria-label="Artifact">
      {selected === null ? <p className={styles.empty}>Select an artifact</p> : <ArtifactDetail key={selected} id={selected} />}
    </section>
  );
};
