import { useEffect, type JSX } from "react";
import { useAppDispatch, useAppSelector } from "../../store";
import { artifactLinkOpened, loadArtifacts } from "./artifactsSlice";
import styles from "./ArtifactRef.module.css";

export interface ArtifactRefProps {
  readonly id: string;
  readonly rev: number | null;
  /** What the text said, shown until the artifact's title is known. */
  readonly raw: string;
}

/** Loads the artifact list once, so references can show titles and know which ids exist. */
export const useArtifactsLoaded = (): boolean => {
  const dispatch = useAppDispatch();
  const loaded = useAppSelector((s) => s.artifacts.loaded);
  const loading = useAppSelector((s) => s.artifacts.loading);
  useEffect(() => {
    if (!loaded && !loading) {
      void dispatch(loadArtifacts());
    }
  }, [dispatch, loaded, loading]);
  return loaded;
};

/**
 * A reference to an artifact, as a link that opens it in the app at the
 * revision it names (ADR 0084). It shows the title or name once the list
 * has loaded, and an id the store doesn't have is struck through.
 */
export const ArtifactRef = ({ id, rev, raw }: ArtifactRefProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const loaded = useArtifactsLoaded();
  const artifact = useAppSelector((s) => s.artifacts.items.find((item) => item.id === id));
  if (loaded && artifact === undefined) {
    return (
      <span className={styles.missing} title="not in the store">
        {raw}
      </span>
    );
  }
  const label = artifact === undefined ? raw : `${artifact.title ?? artifact.name}${rev === null ? "" : ` (r${rev})`}`;
  return (
    <a
      href={`#${id}`}
      className={styles.link}
      title={raw}
      onClick={(event) => {
        event.preventDefault();
        dispatch(artifactLinkOpened({ id, rev }));
      }}
    >
      {label}
    </a>
  );
};
