import { Button } from "@mui/material";
import { useState, type JSX } from "react";
import { formatSize } from "../../format";
import { dataSource } from "./content";
import styles from "./ImageViewer.module.css";

export interface ImageViewerProps {
  readonly name: string;
  readonly contentType: string;
  readonly size: number;
  readonly dataBase64: string;
  /** Who added the shown revision and when, as the caption ends. */
  readonly byline: string;
}

interface NaturalSize {
  readonly width: number;
  readonly height: number;
}

/** An image artifact, fitted to the pane or shown at 100%. Never inlined as SVG or HTML. */
export const ImageViewer = ({ name, contentType, size, dataBase64, byline }: ImageViewerProps): JSX.Element => {
  const [fit, setFit] = useState(true);
  const [natural, setNatural] = useState<NaturalSize | null>(null);
  const caption = [contentType, formatSize(size), natural === null ? "" : `${natural.width}×${natural.height}`, byline]
    .filter((part) => part !== "")
    .join(" · ");
  return (
    <figure className={styles.root}>
      <div className={styles.controls} role="group" aria-label="Zoom">
        <Button size="small" aria-pressed={fit} onClick={() => setFit(true)}>
          Fit
        </Button>
        <Button size="small" aria-pressed={!fit} onClick={() => setFit(false)}>
          100%
        </Button>
      </div>
      <div className={fit ? styles.frameFit : styles.frameNatural}>
        <img
          className={fit ? styles.imageFit : styles.imageNatural}
          src={dataSource(contentType, dataBase64)}
          alt={name}
          onLoad={(event) =>
            setNatural({ width: event.currentTarget.naturalWidth, height: event.currentTarget.naturalHeight })
          }
        />
      </div>
      <figcaption className={styles.caption}>{caption}</figcaption>
    </figure>
  );
};
