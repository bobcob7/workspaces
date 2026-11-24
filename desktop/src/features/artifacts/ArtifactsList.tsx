import type { DragEvent, JSX } from "react";
import { useEffect, useState } from "react";
import type { ArtifactSummary } from "../../api";
import { age, formatSize } from "../../format";
import { useAppDispatch, useAppSelector } from "../../store";
import {
  artifactFilterChosen,
  artifactSearchChanged,
  artifactSelected,
  loadArtifacts,
  uploadArtifacts,
  type ArtifactFilter,
} from "./artifactsSlice";
import styles from "./ArtifactsList.module.css";
import { imagesFromClipboard } from "./upload";

const artifactsPollInterval = 10_000;

const filters: readonly { readonly value: ArtifactFilter; readonly label: string }[] = [
  { value: "all", label: "All" },
  { value: "image", label: "Images" },
  { value: "markdown", label: "Documents" },
  { value: "text", label: "Text" },
];

const matchesSearch = (item: ArtifactSummary, needle: string): boolean =>
  needle === "" || item.name.toLowerCase().includes(needle) || (item.title ?? "").toLowerCase().includes(needle);

/** The artifacts the kind filter and the search keep, in the order ws lists them. */
export const visibleArtifacts = (
  items: readonly ArtifactSummary[],
  filter: ArtifactFilter,
  search: string,
): readonly ArtifactSummary[] => {
  const needle = search.trim().toLowerCase();
  return items.filter((item) => (filter === "all" || item.kind === filter) && matchesSearch(item, needle));
};

const creatorText = (item: ArtifactSummary): string => (item.createdBy.kind === "person" ? "you" : (item.createdBy.workspace ?? ""));

const threadText = (item: ArtifactSummary): string => {
  const parts = [`${item.threads.open} open`, `${item.threads.detached} det.`];
  return item.threads.open + item.threads.detached === 0 ? "" : parts.join(" · ");
};

const metaText = (item: ArtifactSummary): string =>
  [item.kind, formatSize(item.size), item.latest > 1 ? `r${item.latest}` : "", threadText(item), age(item.updatedAt)]
    .filter((part) => part !== "")
    .join(" · ");

const isFileDrag = (event: DragEvent): boolean => Array.from(event.dataTransfer.types).includes("Files");

/** Artifacts in the section: kind filters, search, uploads by drop or paste, and one row each. */
export const ArtifactsList = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const items = useAppSelector((s) => s.artifacts.items);
  const filter = useAppSelector((s) => s.artifacts.filter);
  const search = useAppSelector((s) => s.artifacts.search);
  const selected = useAppSelector((s) => s.artifacts.selected);
  const uploads = useAppSelector((s) => s.artifacts.uploads);
  const error = useAppSelector((s) => s.artifacts.error);
  const [dragging, setDragging] = useState(false);
  const shown = visibleArtifacts(items, filter, search);

  useEffect(() => {
    void dispatch(loadArtifacts());
    const timer = window.setInterval(() => void dispatch(loadArtifacts()), artifactsPollInterval);
    return () => window.clearInterval(timer);
  }, [dispatch]);

  useEffect(() => {
    const onPaste = (event: ClipboardEvent): void => {
      const images = imagesFromClipboard(event);
      if (images.length === 0) {
        return;
      }
      event.preventDefault();
      void dispatch(uploadArtifacts(images));
    };
    window.addEventListener("paste", onPaste);
    return () => window.removeEventListener("paste", onPaste);
  }, [dispatch]);

  const onDrop = (event: DragEvent): void => {
    event.preventDefault();
    setDragging(false);
    const files = Array.from(event.dataTransfer.files);
    if (files.length > 0) {
      void dispatch(uploadArtifacts(files));
    }
  };

  return (
    <div
      className={styles.root}
      onDragEnter={(event) => isFileDrag(event) && setDragging(true)}
      onDragOver={(event) => isFileDrag(event) && event.preventDefault()}
      onDragLeave={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
          setDragging(false);
        }
      }}
      onDrop={onDrop}
    >
      <div className={styles.controls}>
        <div className={styles.chips} role="group" aria-label="Kind">
          {filters.map((chip) => (
            <button
              key={chip.value}
              type="button"
              className={styles.chip}
              aria-pressed={chip.value === filter}
              onClick={() => dispatch(artifactFilterChosen(chip.value))}
            >
              {chip.label}
            </button>
          ))}
        </div>
        <input
          type="search"
          className={styles.search}
          aria-label="Search artifacts"
          placeholder="Search…"
          value={search}
          onChange={(event) => dispatch(artifactSearchChanged(event.target.value))}
        />
      </div>
      {dragging && <div className={styles.dropZone}>Drop files here, or paste an image</div>}
      {error !== null && (
        <p className={styles.error} role="status">
          {error.message}
        </p>
      )}
      {uploads.map((upload, index) => (
        <p key={`${upload.name}-${index}`} className={styles.upload} role="status">
          Uploading {upload.name}…
        </p>
      ))}
      {shown.length === 0 ? (
        <p className={styles.empty}>{items.length === 0 ? "No artifacts yet." : "No artifacts match."}</p>
      ) : (
        <ul className={styles.list} aria-label="Artifacts">
          {shown.map((item) => (
            <li key={item.id}>
              <button
                type="button"
                className={styles.item}
                aria-current={item.id === selected ? "true" : undefined}
                onClick={() => dispatch(artifactSelected(item.id))}
              >
                <span className={styles.name}>{item.title ?? item.name}</span>
                <span className={styles.meta}>{metaText(item)}</span>
                <span className={styles.meta}>{creatorText(item)}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
};
