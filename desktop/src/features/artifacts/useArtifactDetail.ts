import { useEffect, useState } from "react";
import { api, toAppError, type AppError, type Artifact, type ArtifactContent, type CommentList } from "../../api";
import { useAppSelector } from "../../store";

export interface ArtifactDetailData {
  readonly artifact: Artifact | null;
  readonly content: ArtifactContent | null;
  readonly comments: CommentList | null;
  readonly error: AppError | null;
  /** The revision on screen: the chosen one, else the latest. */
  readonly shownRev: number | null;
  readonly chooseRev: (rev: number) => void;
  /** Loads the artifact and its comments again, after a comment changed. */
  readonly reload: () => void;
}

/** One artifact's shown data: its summary and revisions, the chosen revision's bytes, and its comment threads. */
export const useArtifactDetail = (id: string): ArtifactDetailData => {
  const updatedAt = useAppSelector((s) => s.artifacts.items.find((item) => item.id === id)?.updatedAt ?? null);
  const [reloads, setReloads] = useState(0);
  const [artifact, setArtifact] = useState<Artifact | null>(null);
  const [chosenRev, setChosenRev] = useState<number | null>(null);
  const [content, setContent] = useState<ArtifactContent | null>(null);
  const [comments, setComments] = useState<CommentList | null>(null);
  const [error, setError] = useState<AppError | null>(null);
  const shownRev = chosenRev ?? artifact?.latest ?? null;
  const shownContent = content !== null && content.rev === shownRev ? content : null;

  useEffect(() => {
    let active = true;
    const load = async (): Promise<void> => {
      try {
        const [shown, threads] = await Promise.all([api.artifactShow(id), api.artifactComments(id)]);
        if (active) {
          setArtifact(shown);
          setComments(threads);
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
  }, [id, updatedAt, reloads]);

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

  return {
    artifact,
    content: shownContent,
    comments,
    error,
    shownRev,
    chooseRev: setChosenRev,
    reload: () => setReloads((count) => count + 1),
  };
};
